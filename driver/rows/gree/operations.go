package gree

import (
	"encoding/binary"
	"fmt"
	"slices"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const setTemperatureOperation = "thermal.operation.set_temperature"

func temperatureOperation() roweval.BindingRow {
	id := setTemperature.String()
	min, max, step := number(0), number(65535), number(1)
	return roweval.BindingRow{NativeID: id, SubjectRole: zone, SpecRef: id, Service: thermal("thermal.service.zone"), Capability: thermal("thermal.capability.set_temperature"), Write: &roweval.WriteRow{
		Operation: thermal(setTemperatureOperation), Argument: setpoint, TargetRow: id, ReadbackRow: id,
		Inverse:      roweval.Convert{Numeric: &roweval.Affine{Num: 10, Den: 1, Add: semreg.Decimal{Coefficient: "-27315", Exponent10: -1}}},
		NativeBounds: roweval.Bounds{Min: &min, Max: &max, Step: &step},
	}}
}

// SettingTarget associates a configured indoor unit with one supplied zone.
type SettingTarget struct {
	Equipment Equipment
	Zone      driver.NodeRef
	ZoneBasis []semreg.EvidenceRef
}

// TemperatureTarget resolves the explicit owner independently of read ranking.
// An unresolved or multiply resolved owner declares no route.
func TemperatureTarget(zoneRef driver.NodeRef, owner driver.AnchorKey, units []SettingTarget) (SettingTarget, bool) {
	var found SettingTarget
	if zoneRef.Validate() != nil || owner.Slot == "" || owner.Validate() != nil {
		return found, false
	}
	for _, unit := range units {
		if unit.Equipment.Anchor != owner || unit.Zone != zoneRef || unit.Equipment.Class != indoor || !validBasis(unit.ZoneBasis) {
			continue
		}
		if found.Equipment.Anchor.Slot != "" {
			return SettingTarget{}, false
		}
		found = unit
	}
	if found.Equipment.Anchor.Slot == "" {
		return found, false
	}
	if _, _, ok := reg.EncodeGreeVRFAddressedWrite(found.setting(0)); !ok {
		return SettingTarget{}, false
	}
	return found, true
}

func (t SettingTarget) setting(raw uint16) reg.GreeVRFAddressedSetting {
	key := t.Equipment.Key
	key.Type = setTemperature.Type
	return reg.GreeVRFAddressedSetting{Key: key, Class: t.Equipment.Class, Coordinate: setTemperature.Start, RawValue: raw}
}

// EncodeTemperature inverts the kelvin argument for the uniquely resolved owner.
func EncodeTemperature(zoneRef driver.NodeRef, owner driver.AnchorKey, units []SettingTarget, argument semreg.Quantity, limits *driver.ValueLimits) (reg.GreeVRFCommand, error) {
	target, ok := TemperatureTarget(zoneRef, owner, units)
	if !ok || argument.Unit != kelvin || argument.Validate() != nil {
		return reg.GreeVRFCommand{}, fmt.Errorf("no unique temperature target or invalid kelvin argument")
	}
	b := temperatureOperation()
	t := roweval.Table[Coordinate]{Native: []roweval.NativeRow[Coordinate]{{ID: b.NativeID, At: setTemperature, Unit: "native.gree.tenth_celsius", SpecRef: b.SpecRef, Wire: wire(2)}}, Bindings: []roweval.BindingRow{b}}
	d := descriptors[Coordinate](roweval.Codec{Decode: func(raw uint64, _ uint16) (semreg.Decimal, error) { return number(raw), nil }, Encode: func(n semreg.Decimal, _ uint16) (uint64, error) {
		raw, err := uintNumber(n)
		if err != nil || raw > 65535 {
			return 0, fmt.Errorf("not a representable set temperature")
		}
		return raw, nil
	}})
	out, err := roweval.Invert(t, roweval.Writing[Coordinate]{NativeID: b.NativeID, SubjectRole: zone, Operation: b.Write.Operation, Argument: semreg.TypedField{ID: setpoint, Value: quantity(argument.Number, argument.Unit)}, Limits: limits, Descriptors: d})
	if err != nil {
		return reg.GreeVRFCommand{}, err
	}
	if out.Reason != "" {
		return reg.GreeVRFCommand{}, fmt.Errorf("%s: %s", out.Reason, out.Detail)
	}
	return EncodeSetting(target.setting(binary.LittleEndian.Uint16(out.Payload)))
}

// SubjectRole selects a resource kind, not an identity or a bus destination.
func SubjectRole(class reg.GreeVRFDeviceClass) string {
	switch class {
	case indoor:
		return terminal
	case outdoor:
		return generator
	case reg.GreeVRFHotWaterUnit, reg.GreeVRFWaterTank:
		return "dhw"
	default:
		return ""
	}
}

func validBasis(basis []semreg.EvidenceRef) bool {
	if len(basis) == 0 {
		return false
	}
	for _, ref := range basis {
		if ref.Validate() != nil {
			return false
		}
	}
	return true
}

// Relation binds the explicitly supplied endpoints only when the relation's
// required evidence basis is present. A missing basis asserts no absence.
func Relation(name string, from, to driver.NodeRef, class semreg.RelationBasis, basis []semreg.EvidenceRef) (driver.LinkClaim, bool) {
	var allowed bool
	switch name {
	case "air_supply":
		allowed = class == semreg.RelationConfiguration
	case "system_member":
		allowed = class == semreg.RelationConfiguration || class == semreg.RelationGateRule
	case "refrigerant_circuit_member":
		allowed = class == semreg.RelationNativeObservation
	case "generator_compressor":
		allowed = class == semreg.RelationGateRule
	}
	claim := driver.LinkClaim{Key: driver.LinkKey{Type: thermal(semreg.DefinitionID("thermal.relation." + name)), From: from, To: to}, Basis: slices.Clone(basis)}
	if !allowed || claim.Validate() != nil {
		return driver.LinkClaim{}, false
	}
	return claim, true
}
