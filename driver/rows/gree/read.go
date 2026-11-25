package gree

import (
	"fmt"
	"strconv"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const (
	indoor    = reg.GreeVRFIndoorUnit
	outdoor   = reg.GreeVRFOutdoorUnit
	bytePoint = reg.GreeVRFDictionaryByte
	bitPoint  = reg.GreeVRFDictionaryBit
	kelvin    = "unit.kelvin"
	zone      = "zone"
	terminal  = "terminal_unit"
	system    = "system"
	circuit   = "refrigerant_circuit"
	generator = "heat_generator"
	setpoint  = "thermal.setpoint.temperature_setpoint"
)

var thermalPack = semreg.PackRef{ID: "helianthus.pack.thermal", Version: "1.0.0"}

func thermal(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: thermalPack, ID: id, Version: thermalPack.Version}
}

var indoorPower = Coordinate{0x1f, 0, bitPoint}
var setTemperature = Coordinate{0x73, 0, bytePoint}

// ReadBinding associates one typed point with one thermal field and subject role.
type ReadBinding struct {
	Point   Coordinate
	Class   reg.GreeVRFDeviceClass
	Subject string
	Field   semreg.DefinitionID
	Unit    semreg.DefinitionID
	Master  bool
	WayBack string
	convert roweval.Convert
}

func temperature() roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: 1, Add: semreg.Decimal{Coefficient: "27315", Exponent10: -2}}}
}

func numeric(num, den int64) roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: num, Den: den}}
}

func symbols(field semreg.DefinitionID, names ...string) roweval.Convert {
	m := make(map[string]string)
	for i, s := range names {
		if s != "" {
			m[strconv.Itoa(i)] = string(field) + "." + s
		}
	}
	return roweval.Convert{Symbols: m}
}

func enum(c Coordinate, class reg.GreeVRFDeviceClass, subject string, field semreg.DefinitionID, master bool, wayBack string, names ...string) ReadBinding {
	return ReadBinding{c, class, subject, field, field, master, wayBack, symbols(field, names...)}
}

// ReadRows returns detached bindings. An empty WayBack means no defined write.
func ReadRows() []ReadBinding {
	return []ReadBinding{
		enum(Coordinate{0x21, 0, bytePoint}, indoor, terminal, "thermal.mode.terminal_unit", false, "setting.71:00", "", "cool", "dry", "fan_only", "heat"),
		enum(Coordinate{0x1e, 0, bitPoint}, indoor, terminal, "thermal.status.terminal_unit_fault", false, "", "clear", "present"),
		enum(Coordinate{0x1f, 4, bitPoint}, indoor, terminal, "thermal.status.terminal_unit_operation", false, "", "idle", "active"),
		{Coordinate{0x20, 0, bytePoint}, indoor, zone, "thermal.measurement.air_temperature", kelvin, false, "", temperature()},
		{setTemperature, indoor, zone, setpoint, kelvin, false, "setting.73:00", temperature()},
		{Coordinate{0x20, 9, bytePoint}, 0, zone, "thermal.measurement.humidity", "unit.ratio", false, "", numeric(1, 100)},
		{Coordinate{0x20, 0x18, bytePoint}, 0, zone, "thermal.measurement.humidity", "unit.ratio", false, "", numeric(1, 100)},
		enum(Coordinate{0x21, 0x51, bytePoint}, outdoor, system, "thermal.mode.system", true, "", "", "off", "cool", "dry", "fan_only", "heat"),
		enum(Coordinate{0x1f, 0x51, bitPoint}, outdoor, system, "thermal.status.operation", true, "", "idle", "active"),
		enum(Coordinate{0x1e, 0x51, bitPoint}, outdoor, system, "thermal.status.fault", true, "", "clear", "present"),
		enum(Coordinate{0x1e, 0x8a, bitPoint}, outdoor, generator, "thermal.status.heat_generator_fault", false, "", "clear", "present"),
		{Coordinate{0x31, 0x91, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_high_side_pressure", "unit.pascal", false, "", numeric(1000, 1)},
		{Coordinate{0x31, 0x93, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_low_side_pressure", "unit.pascal", false, "", numeric(1000, 1)},
		{Coordinate{0x20, 0x50, bytePoint}, outdoor, system, "thermal.measurement.outdoor_air_temperature", kelvin, true, "", temperature()},
		{Coordinate{0x20, 0x51, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_condensing_temperature", kelvin, true, "", temperature()},
		{Coordinate{0x20, 0x52, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_evaporating_temperature", kelvin, true, "", temperature()},
		{Coordinate{0x20, 0x69, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_condensing_temperature", kelvin, false, "", temperature()},
		{Coordinate{0x20, 0x6a, bytePoint}, outdoor, circuit, "thermal.measurement.refrigerant_evaporating_temperature", kelvin, false, "", temperature()},
	}
}

// Equipment keeps the configured anchor, decoded class and routing key separate.
type Equipment struct {
	Anchor   driver.AnchorKey
	Class    reg.GreeVRFDeviceClass
	Key      reg.GreeVRFRegisterKey
	Producer driver.NodeRef
}

func (e Equipment) configured() bool { return e.Anchor.Slot != "" && e.Anchor.Validate() == nil }

func (e Equipment) matches(k reg.GreeVRFRegisterKey) bool {
	return e.Key.Interface == k.Interface && e.Key.Unit == k.Unit &&
		(k.Family == reg.GreeVRFData || k.Family == reg.GreeVRFAddressedData)
}

// ReadInput is resolved for one equipment source and one possible subject.
// Humidity requires a separately evidenced association for its exact point.
type ReadInput struct {
	Equipment    Equipment
	Subject      driver.NodeRef
	Dimension    string
	Roles        roweval.Roles
	Template     semreg.FactCandidate
	ZoneBasis    []semreg.EvidenceRef
	CircuitBasis []semreg.EvidenceRef
	MeasuredZone map[Coordinate][]semreg.EvidenceRef
	Priority     *int32
	Power        *reg.GreeVRFTypedPoint
	NonHaltFault *reg.GreeVRFTypedPoint
}

// ReadResult preserves the native point even when a semantic result is unavailable.
type ReadResult struct {
	Point      reg.GreeVRFTypedPoint
	Evaluation roweval.Evaluation
	Source     *driver.Source
	Missing    string
}

func bindingFor(p reg.GreeVRFTypedPoint, in ReadInput) (ReadBinding, string) {
	if !in.Equipment.configured() {
		return ReadBinding{}, "configured equipment anchor"
	}
	if !in.Equipment.matches(p.Key) {
		return ReadBinding{}, "matching native target"
	}
	if p.Row.LabelOnly || p.Row.Conflict != "" {
		return ReadBinding{}, "unambiguous dictionary meaning"
	}
	c := coordinate(p.Row)
	for _, row := range ReadRows() {
		if row.Point != c {
			continue
		}
		if in.Equipment.Class == 0 || row.Class != 0 && row.Class != in.Equipment.Class {
			return ReadBinding{}, "decoded device class"
		}
		if row.Master {
			master, ok := (reg.GreeVRFDeviceIdentity{Class: in.Equipment.Class, Unit: p.Key.Unit}).OutdoorRegisterUnit(true)
			if !ok || p.Key.Unit != master {
				return ReadBinding{}, "master outdoor system source"
			}
		}
		if row.Subject == circuit && !validBasis(in.CircuitBasis) {
			return ReadBinding{}, "module-to-refrigerant-circuit association"
		}
		if row.Subject == zone {
			basis := in.ZoneBasis
			if row.Field == "thermal.measurement.humidity" {
				basis = in.MeasuredZone[c]
			}
			if len(basis) == 0 {
				return ReadBinding{}, "point-specific zone association"
			}
			for _, ref := range basis {
				if ref.Validate() != nil {
					return ReadBinding{}, "valid zone association evidence"
				}
			}
		}
		if in.Priority == nil {
			return ReadBinding{}, "declared source priority"
		}
		if in.Dimension == "" || in.Subject.Validate() != nil || in.Equipment.Producer.Validate() != nil {
			return ReadBinding{}, "resolved subject and producer"
		}
		return row, ""
	}
	switch c {
	case Coordinate{0x20, 0x0e, bytePoint}, Coordinate{0x20, 0x0f, bytePoint}, Coordinate{0x20, 0x10, bytePoint}, Coordinate{0x20, 0x11, bytePoint}:
		return ReadBinding{}, "exact water sensor to DHW subject association"
	case Coordinate{0x78, 0x59, bytePoint}, Coordinate{0x78, 0x5b, bytePoint}, Coordinate{0x78, 0x5d, bytePoint}:
		return ReadBinding{}, "terminal-unit valve subject and ratio scale"
	default:
		return ReadBinding{}, "no established semantic binding"
	}
}

func pointNumber(p reg.GreeVRFTypedPoint) (semreg.Decimal, error) {
	if b, ok := p.Value.Bits[0]; p.Row.Kind == bitPoint && ok {
		if b {
			return number(1), nil
		}
		return number(0), nil
	}
	n, d := p.Value.Numerator, p.Value.Denominator
	if d <= 0 || p.Value.Status != reg.GreeVRFValueDecoded {
		return semreg.Decimal{}, fmt.Errorf("no decoded native number")
	}
	var exp int32
	for d > 1 && d%10 == 0 {
		exp--
		d /= 10
	}
	if d != 1 {
		return semreg.Decimal{}, fmt.Errorf("no exact decimal representation")
	}
	for n != 0 && n%10 == 0 {
		n /= 10
		exp++
	}
	if n == 0 {
		exp = 0
	}
	return semreg.Decimal{Coefficient: strconv.FormatInt(n, 10), Exponent10: exp}, nil
}

// Evaluate passes the retained native point unchanged and evaluates only its
// semantic binding; it does not run a presentation converter or keep state.
func Evaluate(p reg.GreeVRFTypedPoint, in ReadInput) (ReadResult, error) {
	out := ReadResult{Point: p}
	row, missing := bindingFor(p, in)
	if missing != "" {
		out.Missing = missing
		return out, nil
	}
	dimension := semreg.DefinitionID("thermal.dimension." + row.Subject)
	text := in.Dimension
	key := semreg.FactKey{PackID: thermalPack.ID, PackVersion: thermalPack.Version, FactID: row.Field, Dimensions: []semreg.Dimension{{ID: dimension, Value: semreg.Value{Kind: semreg.ValueText, Text: &text}}}}
	out.Source = &driver.Source{For: driver.Element{Subject: in.Subject, Key: key}, Producer: in.Equipment.Producer, PathKey: row.Point.String(), Priority: *in.Priority}
	if err := out.Source.Validate(); err != nil {
		out.Source = nil
		return out, err
	}
	if !p.Present {
		out.Missing = "native point absent"
		return out, nil
	}
	if row.Field == "thermal.mode.terminal_unit" {
		power := in.Power
		if power == nil || !power.Present || coordinate(power.Row) != indoorPower || !in.Equipment.matches(power.Key) || power.Key.Family != p.Key.Family {
			out.Missing = "indoor power state"
			return out, nil
		}
		powered, ok := power.Value.Bits[0]
		if !ok {
			out.Missing = "indoor power state"
			return out, nil
		}
		if !powered {
			row.convert = roweval.Convert{Hook: "power-off"}
		}
	}
	if row.Field == "thermal.status.fault" || row.Field == "thermal.status.heat_generator_fault" {
		if !p.Value.Bits[0] {
			other := in.NonHaltFault
			if other == nil || !other.Present || other.Key != p.Key || coordinate(other.Row) != (Coordinate{0x1e, p.Row.Coordinate + 1, bitPoint}) {
				out.Missing = "non-halt fault state"
				return out, nil
			}
			active, ok := other.Value.Bits[0]
			if !ok {
				out.Missing = "non-halt fault state"
				return out, nil
			}
			if active {
				row.convert = symbols(row.Field, "present", "present")
			}
		}
	}
	if len(row.convert.Symbols) != 0 && p.EnumerationLabel == "" {
		out.Missing = "documented enumeration symbol"
		return out, nil
	}
	b := roweval.BindingRow{NativeID: row.Point.String(), SubjectRole: row.Subject, SpecRef: row.Point.String(), Service: thermal(semreg.DefinitionID("thermal.service." + row.Subject)), Capability: thermal(semreg.DefinitionID("thermal.capability.read." + row.Subject)), Read: &roweval.ReadRow{Fact: thermal(row.Field), Unit: row.Unit, Policy: in.Template.FreshnessPolicy.PolicyID, Dimensions: map[semreg.DefinitionID]string{semreg.DefinitionID("thermal.dimension." + row.Subject): "subject_anchor"}, ToRegistry: row.convert}}
	t := roweval.Table[Coordinate]{Native: []roweval.NativeRow[Coordinate]{{ID: b.NativeID, At: row.Point, SpecRef: b.SpecRef, Unit: "native.gree", Wire: wire(p.Row.Width)}}, Bindings: []roweval.BindingRow{b}, Hooks: map[string]roweval.RowHook{"power-off": func(_ roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, _ roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		return roweval.DerivedValue{Value: semreg.Value{Kind: semreg.ValueSymbol, Symbol: &semreg.Symbol{Namespace: row.Field, Token: string(row.Field) + ".off", Known: true}}}, ""
	}}}
	d := descriptors[Coordinate](roweval.Codec{Decode: func(_ uint64, _ uint16) (semreg.Decimal, error) { return pointNumber(p) }, Encode: func(n semreg.Decimal, _ uint16) (uint64, error) { return uintNumber(n) }})
	ev, err := roweval.Evaluate(t, roweval.Acquired[Coordinate]{Selector: row.Point, Payload: p.Value.Raw, Roles: in.Roles, Template: in.Template, Coordinates: map[string]string{"subject_anchor": in.Dimension}, Descriptors: d})
	out.Evaluation = ev
	if err != nil {
		return out, err
	}
	return out, nil
}
