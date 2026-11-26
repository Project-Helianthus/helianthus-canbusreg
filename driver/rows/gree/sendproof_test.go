package gree

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/runtime/sources"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

type recordingSender struct {
	frames []reg.GreeVRFCommand
}

func (s *recordingSender) send(f reg.GreeVRFCommand) error {
	s.frames = append(s.frames, reg.GreeVRFCommand{Identifier: f.Identifier, Data: bytes.Clone(f.Data)})
	return nil
}

func (s *recordingSender) requireFrame(t *testing.T, id uint32, payload string, native reg.GreeVRFCommand, ok bool, err error) {
	t.Helper()
	if err != nil || !ok || len(s.frames) != 1 {
		t.Fatalf("send error %v, native encodable %t, frames %+v; want one frame", err, ok, s.frames)
	}
	f := s.frames[0]
	if f.Identifier != id || hex.EncodeToString(f.Data) != payload || f.Identifier != native.Identifier || !bytes.Equal(f.Data, native.Data) {
		t.Fatalf("recorded %08x %x; want literal %08x %s and encoder %08x %x", f.Identifier, f.Data, id, payload, native.Identifier, native.Data)
	}
}

func (s *recordingSender) requireRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil || len(s.frames) != 0 {
		t.Fatalf("refused call: got error %v and frames %+v, want error and no frames", err, s.frames)
	}
}

type sendVector struct {
	Name, Payload        string
	Identifier, Seed     uint32
	Unit, Type, Property uint8
	Start, Value         uint16
	Kind                 reg.GreeVRFDictionaryKind
}

func TestEverySettingAndPropertyRowSendsExactlyOneFrameThroughTheRecordingSender(t *testing.T) {
	pending := make(map[string]sendVector)
	for _, v := range vectors[[]sendVector](t, "commands") {
		if _, exists := pending[v.Name]; exists {
			t.Fatalf("duplicate vector for %s", v.Name)
		}
		pending[v.Name] = v
	}
	for _, row := range CommandRows() {
		if row.ClockID != 0 {
			continue
		}
		v, ok := pending[row.Name]
		if !ok {
			t.Errorf("missing vector for row (or duplicate row) %s", row.Name)
			continue
		}
		delete(pending, row.Name)
		t.Run(row.Name, func(t *testing.T) { proveCommandSend(t, row, v) })
	}
	for name := range pending {
		t.Errorf("row missing for vector %s", name)
	}
}

func proveCommandSend(t *testing.T, row CommandRow, v sendVector) {
	t.Helper()
	var sender recordingSender
	if v.Kind != 0 {
		if row.Setting == nil || *row.Setting != (Coordinate{v.Type, v.Start, v.Kind}) || row.Property != nil {
			t.Fatalf("setting descriptor: %+v", row)
		}
		key := reg.GreeVRFRegisterKey{Interface: bus(t, "synthetic", 1), Family: reg.GreeVRFData, Unit: v.Unit, Type: row.Setting.Type}
		setting := reg.GreeVRFAddressedSetting{Key: key, Class: indoor, Coordinate: row.Setting.Start, RawValue: v.Value}
		want, _, ok := reg.EncodeGreeVRFAddressedWrite(setting)
		err := SendSetting(SettingCall{Setting: setting, Deadline: time.Now().Add(time.Hour)}, sender.send)
		sender.requireFrame(t, v.Identifier, v.Payload, want, ok, err)
		return
	}
	if row.Property == nil || *row.Property != v.Property || row.Setting != nil {
		t.Fatalf("property descriptor: %+v", row)
	}
	want, ok := reg.EncodeGreeVRFCommand(v.Seed, v.Unit, v.Property, v.Value)
	err := SendProperty(*row.Property, v.Seed, v.Unit, v.Value, sender.send)
	sender.requireFrame(t, v.Identifier, v.Payload, want, ok, err)
}

func TestRefusedArgumentsSendNothing(t *testing.T) {
	cases := vectors[struct {
		Settings []struct {
			Name, Deadline string
			Unit           uint8
			Start, Value   uint16
		}
		Properties []struct {
			Name           string
			Property, Unit uint8
			Value          uint16
		}
		Temperature []struct {
			Name string
			Unit semreg.DefinitionID
		}
	}](t, "send_denials")
	for _, c := range cases.Settings {
		t.Run(c.Name, func(t *testing.T) {
			var deadline time.Time
			switch c.Deadline {
			case "future":
				deadline = time.Now().Add(time.Hour)
			case "elapsed":
				deadline = time.Now().Add(-time.Hour)
			case "missing":
			default:
				t.Fatalf("unknown deadline fixture %q", c.Deadline)
			}
			key := reg.GreeVRFRegisterKey{Interface: bus(t, "synthetic", 1), Family: reg.GreeVRFData, Unit: c.Unit, Type: 0x6f}
			setting := reg.GreeVRFAddressedSetting{Key: key, Class: indoor, Coordinate: c.Start, RawValue: c.Value}
			var sender recordingSender
			sender.requireRefusal(t, SendSetting(SettingCall{Setting: setting, Deadline: deadline}, sender.send))
		})
	}
	for _, c := range cases.Properties {
		t.Run(c.Name, func(t *testing.T) {
			var sender recordingSender
			sender.requireRefusal(t, SendProperty(c.Property, 0x1c800000, c.Unit, c.Value, sender.send))
		})
	}
	for _, c := range cases.Temperature {
		t.Run(c.Name, func(t *testing.T) {
			target := temperatureFixture(t)
			arg := temperatureKelvin("29615", -2)
			arg.Unit = c.Unit
			var sender recordingSender
			sender.requireRefusal(t, SendTemperature(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, arg, nil, sender.send))
		})
	}
}

func TestSetTemperatureSendsOnceToTheDeclaredOwnerNotTheReadWinner(t *testing.T) {
	for owner, id := range []uint32{0x1cbfd073, 0x1cbfd0f3} {
		t.Run(fmt.Sprintf("owner-%d", owner), func(t *testing.T) {
			const high, low = int32(11), int32(9)
			scores := [2]int32{high, high}
			scores[owner] = low
			book, err := sources.NewTable(func() semreg.MonotonicPoint {
				return semreg.MonotonicPoint{ClockEpochID: "clock:synthetic", Nanoseconds: "1"}
			})
			if err != nil {
				t.Fatal(err)
			}
			var units []SettingTarget
			var element driver.Element
			values := make(map[sources.SourceKey]sources.MemberEvaluation)
			for i, score := range scores {
				_, p := imagePoint(t, bus(t, "synthetic", 1), 0x20+uint8(i), 0x73, 0, false, 210+10*i)
				in := readInput(p, indoor, zone)
				in.Equipment.Anchor.Slot = fmt.Sprintf("equipment-%d", i)
				in.Equipment.Producer.ID = semreg.ResourceID(fmt.Sprintf("resource.equipment.%d", i))
				in.Template.CandidateID = semreg.CandidateID(fmt.Sprintf("candidate.synthetic.%d", i))
				in.Priority = &score
				units = append(units, SettingTarget{Equipment: in.Equipment, Zone: in.Subject, ZoneBasis: in.ZoneBasis})
				element = admitTemperatureRead(t, book, values, i, p, in)
			}
			read := book.Element(element, values)
			winner := units[1-owner]
			readWant := []semreg.Quantity{temperatureKelvin("29415", -2), temperatureKelvin("29515", -2)}
			if read.Resolution.Winner == nil || read.Resolution.Winner.Producer != winner.Equipment.Producer || read.Value == nil || read.Value.Quantity == nil || *read.Value.Quantity != readWant[1-owner] {
				t.Fatalf("read winner: %+v; want %v and %v", read, winner.Equipment.Producer, readWant[1-owner])
			}
			target := units[owner]
			var sender recordingSender
			want, _, ok := reg.EncodeGreeVRFAddressedWrite(target.setting(230))
			err = SendTemperature(target.Zone, target.Equipment.Anchor, units, temperatureKelvin("29615", -2), nil, sender.send)
			sender.requireFrame(t, id, "0003e600", want, ok, err)
		})
	}
}

func admitTemperatureRead(t *testing.T, book *sources.Table, values map[sources.SourceKey]sources.MemberEvaluation, index int, p reg.GreeVRFTypedPoint, in ReadInput) driver.Element {
	t.Helper()
	r, err := Evaluate(p, in)
	if err != nil || r.Source == nil || r.Evaluation.Candidate == nil {
		t.Fatalf("temperature source: %+v, %v", r, err)
	}
	src, c := *r.Source, *r.Evaluation.Candidate
	if err := book.Declare(src); err != nil {
		t.Fatal(err)
	}
	asset := semreg.AssetID(fmt.Sprintf("asset:unit%d", index))
	member := semreg.Snapshot{AssetID: asset, Facts: []semreg.FactEnvelope{{AssetID: asset, Key: c.Key, Candidates: []semreg.FactCandidate{c}}}}
	if err := book.Admit(driver.Sample{From: src, Candidate: c}, member); err != nil {
		t.Fatal(err)
	}
	ref := driver.SourceRef{For: src.For, Producer: src.Producer, PathKey: src.PathKey}
	values[sources.KeyOf(ref)] = sources.MemberEvaluation{Member: member, View: semreg.EvaluationView{Facts: []semreg.EvaluatedFact{{CandidateID: c.CandidateID, CandidateRevision: c.Revision, Freshness: semreg.FreshnessFresh, EffectiveAvailability: semreg.AvailabilityAvailable}}}}
	return src.For
}

func TestNoUniqueOwnerDeclaresNoRouteAndSendsNothing(t *testing.T) {
	target := temperatureFixture(t)
	other := target
	other.Equipment.Anchor.Slot = "other-equipment"
	other.Equipment.Key.Unit++
	ambiguous := other
	ambiguous.Equipment.Anchor = target.Equipment.Anchor
	for _, test := range []struct {
		name  string
		owner driver.AnchorKey
		units []SettingTarget
	}{
		{"missing owner", driver.AnchorKey{}, []SettingTarget{target, other}},
		{"unresolved owner", other.Equipment.Anchor, []SettingTarget{target}},
		{"ambiguous owner", target.Equipment.Anchor, []SettingTarget{target, ambiguous}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := TemperatureTarget(target.Zone, test.owner, test.units); ok {
				t.Fatal("declared a route without a unique owner")
			}
			var sender recordingSender
			sender.requireRefusal(t, SendTemperature(target.Zone, test.owner, test.units, temperatureKelvin("29615", -2), nil, sender.send))
		})
	}
}

func TestReadbackShowsOldValueUntilTheReportArrives(t *testing.T) {
	image, old := imagePoint(t, bus(t, "synthetic", 1), 0x20, 0x73, 0, false, 210)
	in := readInput(old, indoor, zone)
	target := SettingTarget{Equipment: in.Equipment, Zone: in.Subject, ZoneBasis: in.ZoneBasis}
	var sender recordingSender
	want, _, ok := reg.EncodeGreeVRFAddressedWrite(target.setting(230))
	err := SendTemperature(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, temperatureKelvin("29615", -2), nil, sender.send)
	sender.requireFrame(t, 0x1cbfd073, "0003e600", want, ok, err)
	for _, stage := range []struct {
		name, kelvin     string
		report, raw      []byte
		at, pointReceipt time.Duration
	}{
		{"after send", "29415", nil, []byte{210, 0}, 0, time.Second},
		{"partial report", "29415", []byte{0, 230}, []byte{210, 0}, 2 * time.Second, time.Second},
		{"complete report", "29615", []byte{0, 230, 0}, []byte{230, 0}, 3 * time.Second, 3 * time.Second},
	} {
		t.Run(stage.name, func(t *testing.T) {
			if stage.report != nil && !image.Apply(fragment(t, old.Key, stage.report, stage.at), false) {
				t.Fatal("synthetic report rejected")
			}
			p := pointAt(t, image, 0, bytePoint)
			in.Template = template(p)
			got, err := Evaluate(p, in)
			c := got.Evaluation.Candidate
			if err != nil || !p.Present || !bytes.Equal(p.Value.Raw, stage.raw) || p.Received != stage.pointReceipt || c == nil || c.Value == nil || c.Value.Quantity == nil || *c.Value.Quantity != temperatureKelvin(stage.kelvin, -2) {
				t.Fatalf("readback: native %+v, candidate %+v, error %v; want %x at %v and %s hundredths of kelvin", p, c, err, stage.raw, stage.pointReceipt, stage.kelvin)
			}
		})
	}
}
