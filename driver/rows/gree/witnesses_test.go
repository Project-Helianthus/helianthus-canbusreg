package gree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	pack "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/packs/thermal"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func vectors[T any](t *testing.T, name string) T {
	t.Helper()
	var out T
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func bus(t *testing.T, name string, index int) canbus.InterfaceIdentity {
	t.Helper()
	id, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNativeCommandInventoryMatchesWireVectors(t *testing.T) {
	type witness struct {
		Name, Payload        string
		Identifier, Seed     uint32
		Unit, Type, Property uint8
		Start, Value         uint16
		Kind                 reg.GreeVRFDictionaryKind
		LabelOnly, Conflict  bool
	}
	rows := make(map[string]CommandRow)
	for _, r := range CommandRows() {
		if _, ok := rows[r.Name]; ok {
			t.Fatalf("duplicate %s", r.Name)
		}
		if r.ClockID == 0 {
			rows[r.Name] = r
		}
	}
	iface := bus(t, "synthetic", 1)
	for _, w := range vectors[[]witness](t, "commands") {
		t.Run(w.Name, func(t *testing.T) {
			r, ok := rows[w.Name]
			if !ok {
				t.Fatal("missing command")
			}
			delete(rows, w.Name)
			var f, want reg.GreeVRFCommand
			var err error
			if w.Kind != 0 {
				c := Coordinate{w.Type, w.Start, w.Kind}
				if r.Setting == nil || *r.Setting != c || !r.SynchronousReadback || r.LabelOnly != w.LabelOnly || r.Conflict != w.Conflict {
					t.Fatalf("descriptor: %+v", r)
				}
				arg := reg.GreeVRFAddressedSetting{Key: reg.GreeVRFRegisterKey{Interface: iface, Family: reg.GreeVRFData, Unit: w.Unit, Type: w.Type}, Class: indoor, Coordinate: w.Start, RawValue: w.Value}
				f, err = EncodeSetting(arg)
				want, _, ok = reg.EncodeGreeVRFAddressedWrite(arg)
			} else {
				if r.Property == nil || *r.Property != w.Property || r.SynchronousReadback {
					t.Fatalf("descriptor: %+v", r)
				}
				f, err = EncodeProperty(w.Property, w.Seed, w.Unit, w.Value)
				want, ok = reg.EncodeGreeVRFCommand(w.Seed, w.Unit, w.Property, w.Value)
			}
			var frames []reg.GreeVRFCommand
			if err == nil {
				frames = append(frames, f)
			}
			if !ok || len(frames) != 1 || err != nil || f.Identifier != w.Identifier || hex.EncodeToString(f.Data) != w.Payload || r.Width != len(f.Data) || f.Identifier != want.Identifier || !bytes.Equal(f.Data, want.Data) {
				t.Fatalf("got %08x %x, want %08x %s (%v)", f.Identifier, f.Data, w.Identifier, w.Payload, err)
			}
		})
	}
	if len(rows) != 0 {
		t.Fatalf("no vector: %+v", rows)
	}
}

func evidence(raw []byte) []semreg.EvidenceRef {
	return []semreg.EvidenceRef{{Owner: "driver.canbusreg", Kind: "evidence.synthetic.frame", Digest: semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(raw))), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
}

func template(p reg.GreeVRFTypedPoint) semreg.FactCandidate {
	source, epoch, binding := semreg.SourceID("source.synthetic"), semreg.SourceEpochID("epoch.synthetic"), semreg.NativeBindingID("binding.synthetic")
	generation := semreg.Uint64("2")
	refs := evidence(p.Value.Raw)
	mono := semreg.MonotonicPoint{ClockEpochID: "clock:synthetic", Nanoseconds: semreg.Uint64(fmt.Sprint(p.Received.Nanoseconds()))}
	wall := semreg.TimePoint{UnixNanoseconds: "100", ClockID: "clock.synthetic", UncertaintyNS: "0"}
	return semreg.FactCandidate{CandidateID: "candidate.synthetic", Revision: "1", Quality: semreg.Quality{Assertion: semreg.AssertionObserved, Qualification: semreg.QualificationQualified, Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood, Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessFresh, Reasons: []semreg.DefinitionID{}}, Times: semreg.Times{ReceivedAt: wall, EvaluatedAt: wall, ReceiptMonotonic: mono, EvaluateMonotonic: mono}, FreshnessPolicy: semreg.FreshnessPolicy{PolicyID: "policy.synthetic", Version: "1.0.0", FreshForNS: "1000", RetainForNS: "2000", MaxWallUncertaintyNS: "0"}, BindingID: &binding, SourceEpochID: &epoch, DriverGeneration: &generation, Evidence: refs, Origin: semreg.OriginRef{OriginID: "origin.synthetic", Kind: semreg.OriginNativeObservation, SourceID: &source, SourceEpochID: &epoch, BindingID: &binding, Evidence: refs}}
}

func imagePoint(t *testing.T, iface canbus.InterfaceIdentity, unit, typ uint8, start uint16, bits bool, raw int) (*reg.GreeVRFRegisterImage, reg.GreeVRFTypedPoint) {
	t.Helper()
	key := reg.GreeVRFRegisterKey{Interface: iface, Family: reg.GreeVRFData, Unit: unit, Type: typ}
	img := reg.NewGreeVRFRegisterImage(key)
	kind := bytePoint
	if bits {
		kind = bitPoint
	}
	r, ok := reg.LookupGreeVRFDictionary(typ, start, kind)
	if !ok {
		t.Fatalf("no dictionary point %02x:%02x", typ, start)
	}
	first := start
	if bits {
		first -= first % 8
	}
	payload := []byte{byte(first), byte(raw)}
	if r.Width == 2 {
		payload = append(payload, byte(raw>>8))
	}
	if !img.Apply(fragment(t, key, payload, time.Second), bits) {
		t.Fatal("fragment rejected")
	}
	return img, pointAt(t, img, start, kind)
}

func pointAt(t *testing.T, img *reg.GreeVRFRegisterImage, start uint16, kind reg.GreeVRFDictionaryKind) reg.GreeVRFTypedPoint {
	t.Helper()
	for _, p := range img.TypedPoints() {
		if p.Row.Coordinate == start && p.Row.Kind == kind {
			return p
		}
	}
	t.Fatal("point not found")
	return reg.GreeVRFTypedPoint{}
}

func fragment(t *testing.T, key reg.GreeVRFRegisterKey, payload []byte, at time.Duration) reg.Evidence {
	t.Helper()
	id, err := canbus.NewExtendedID(0x1effc000 | uint32(key.Unit)<<7 | uint32(key.Type))
	if err != nil {
		t.Fatal(err)
	}
	f, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return reg.Evidence{Interface: key.Interface, Frame: f, Monotonic: at}
}

func readInput(p reg.GreeVRFTypedPoint, class reg.GreeVRFDeviceClass, role string) ReadInput {
	m := pack.Metadata()
	idx := semreg.DefinitionIndex{Pack: m.Pack}
	for _, x := range m.Services {
		idx.Services = append(idx.Services, x.Ref)
	}
	for _, x := range m.Capabilities {
		idx.Capabilities = append(idx.Capabilities, x.Ref)
	}
	for _, x := range m.Fields {
		idx.Fields = append(idx.Fields, x.Ref)
	}
	priority := int32(-12)
	refs := evidence(p.Value.Raw)
	return ReadInput{Equipment: Equipment{Anchor: driver.AnchorKey{Slot: "synthetic-equipment"}, Class: class, Key: p.Key, Producer: driver.NodeRef{Bridge: "bridge.synthetic", ID: "resource.synthetic.equipment"}}, Subject: driver.NodeRef{Bridge: "bridge.synthetic", ID: "resource.synthetic.subject"}, Dimension: "synthetic-subject", Roles: roweval.Roles{Subjects: map[string]roweval.RoleBinding{role: {ServiceInstance: "service.synthetic"}}, Catalog: []semreg.DefinitionIndex{idx}}, Template: template(p), ZoneBasis: refs, CircuitBasis: refs, MeasuredZone: map[Coordinate][]semreg.EvidenceRef{}, Priority: &priority}
}

func TestReadBindingsPreserveNativeAndCandidateMetadata(t *testing.T) {
	rows := vectors[[][]any](t, "reads")
	iface := bus(t, "synthetic", 1)
	seen := make(map[Coordinate]bool)
	for _, v := range rows {
		typ, start, bits := uint8(v[0].(float64)), uint16(v[1].(float64)), v[2].(bool)
		unit, class, role := uint8(v[4].(float64)), reg.GreeVRFDeviceClass(v[5].(float64)), v[6].(string)
		field, want := semreg.DefinitionID(v[7].(string)), v[8].(string)
		t.Run(fmt.Sprintf("%s-%02x", field, start), func(t *testing.T) {
			_, p := imagePoint(t, iface, unit, typ, start, bits, int(v[3].(float64)))
			seen[coordinate(p.Row)] = true
			in := readInput(p, class, role)
			if field == "thermal.measurement.humidity" {
				in.MeasuredZone[coordinate(p.Row)] = in.ZoneBasis
			}
			if field == "thermal.mode.terminal_unit" {
				_, power := imagePoint(t, iface, unit, 0x1f, 0, true, 1)
				in.Power = &power
			}
			got, err := Evaluate(p, in)
			cand := got.Evaluation.Candidate
			if err != nil || cand == nil || got.Source == nil || !reflect.DeepEqual(got.Point, p) {
				t.Fatalf("read: %+v, %v", got, err)
			}
			if cand.Key.FactID != field || len(cand.Key.Dimensions) != 1 || cand.Key.Dimensions[0].ID != semreg.DefinitionID("thermal.dimension."+role) || *cand.Key.Dimensions[0].Value.Text != in.Dimension || !reflect.DeepEqual(cand.Quality, in.Template.Quality) || !reflect.DeepEqual(cand.Times, in.Template.Times) || got.Source.For.Subject != in.Subject || got.Source.Priority != -12 {
				t.Fatalf("metadata %+v %+v", cand, got.Source)
			}
			if cand.Value.Quantity != nil {
				if cand.Value.Quantity.Number != (semreg.Decimal{Coefficient: want, Exponent10: int32(v[9].(float64))}) {
					t.Fatalf("quantity: %+v", cand.Value.Quantity)
				}
			} else if cand.Value.Symbol == nil || cand.Value.Symbol.Token != want || !cand.Value.Symbol.Known {
				t.Fatalf("symbol: %+v", cand.Value.Symbol)
			}
		})
	}
	for _, r := range ReadRows() {
		if !seen[r.Point] {
			t.Fatalf("no read witness for %+v", r)
		}
	}
}

func TestUnassociatedAndQualifiedNativePointsAreNotPromoted(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	for _, v := range []struct {
		typ     uint8
		start   uint16
		raw     int
		missing string
	}{
		{0x20, 0x09, 151, "point-specific zone association"}, {0x20, 0x18, 141, "point-specific zone association"},
		{0x20, 0x0e, 122, "exact water sensor to DHW subject association"}, {0x20, 0x0f, 123, "exact water sensor to DHW subject association"}, {0x20, 0x10, 124, "exact water sensor to DHW subject association"}, {0x20, 0x11, 125, "exact water sensor to DHW subject association"},
		{0x78, 0x59, 0x1234, "terminal-unit valve subject and ratio scale"}, {0x78, 0x5b, 0x2356, "terminal-unit valve subject and ratio scale"}, {0x78, 0x5d, 0x3456, "terminal-unit valve subject and ratio scale"},
		{0x71, 0x15, 55, "unambiguous dictionary meaning"}, {0x73, 4, 0x123, "unambiguous dictionary meaning"},
	} {
		_, p := imagePoint(t, iface, 0x20, v.typ, v.start, false, v.raw)
		got, err := Evaluate(p, readInput(p, indoor, zone))
		if err != nil || !p.Present || !reflect.DeepEqual(got.Point, p) || got.Evaluation.Candidate != nil || got.Source != nil || got.Missing != v.missing {
			t.Fatalf("%02x:%02x got %+v %v", v.typ, v.start, got, err)
		}
	}
}

func TestSettingOwnerIsIndependentOfSourceAndPartialReadback(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	old, p := imagePoint(t, iface, 0x21, 0x73, 0, false, 210)
	in := readInput(p, indoor, zone)
	left := SettingTarget{Equipment: in.Equipment, Zone: in.Subject, ZoneBasis: in.ZoneBasis}
	right := left
	right.Equipment.Anchor.Slot = "other-equipment"
	right.Equipment.Key.Unit = 0x20
	units := []SettingTarget{right, left}
	argument := semreg.Quantity{Number: semreg.Decimal{Coefficient: "29615", Exponent10: -2}, Unit: "unit.kelvin"}
	f, err := EncodeTemperature(in.Subject, left.Equipment.Anchor, units, argument, nil)
	if err != nil || f.Identifier != 0x1cbfd0f3 || hex.EncodeToString(f.Data) != "0003e600" {
		t.Fatalf("frame = %+v, %v", f, err)
	}
	if !old.Apply(fragment(t, p.Key, []byte{0, 230}, 2*time.Second), false) || !reflect.DeepEqual(pointAt(t, old, 0, bytePoint), p) {
		t.Fatal("partial report changed previous setpoint")
	}
	got := pointAt(t, old, 0, bytePoint)
	if got.Value.Numerator != 210 {
		t.Fatal(got)
	}
	if !old.Apply(fragment(t, p.Key, []byte{0, 230, 0}, 3*time.Second), false) {
		t.Fatal("complete report rejected")
	}
	got = pointAt(t, old, 0, bytePoint)
	if got.Value.Numerator != 230 || got.Received != 3*time.Second {
		t.Fatal(got)
	}
	for _, test := range []struct {
		owner driver.AnchorKey
		units []SettingTarget
		arg   semreg.Quantity
	}{
		{driver.AnchorKey{}, units, argument}, {left.Equipment.Anchor, []SettingTarget{left, left}, argument},
		{left.Equipment.Anchor, units, semreg.Quantity{Number: argument.Number, Unit: "unit.pascal"}},
		{left.Equipment.Anchor, units, semreg.Quantity{Number: semreg.Decimal{Coefficient: "1"}, Unit: "unit.kelvin"}},
	} {
		if f, e := EncodeTemperature(in.Subject, test.owner, test.units, test.arg, nil); e == nil || len(f.Data) != 0 {
			t.Fatalf("refused call produced %+v %v", f, e)
		}
	}
}
