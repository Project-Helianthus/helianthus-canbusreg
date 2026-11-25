package gree

import (
	"testing"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

func TestTemperatureOwnerMustMatchConfiguredTarget(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	_, p := imagePoint(t, iface, 0x20, 0x73, 0, false, 230)
	in := readInput(p, indoor, zone)
	in.Equipment.Anchor.Parent = "resource.synthetic.parent"
	original := SettingTarget{Equipment: in.Equipment, Zone: in.Subject, ZoneBasis: in.ZoneBasis}
	if _, ok := TemperatureTarget(in.Subject, in.Equipment.Anchor, []SettingTarget{original}); !ok {
		t.Fatal("configured owner not found")
	}
	for _, v := range []struct {
		name   string
		change func(*SettingTarget)
	}{
		{"anchor slot", func(x *SettingTarget) { x.Equipment.Anchor.Slot = "other" }},
		{"anchor parent", func(x *SettingTarget) { x.Equipment.Anchor.Parent = "resource.synthetic.other" }},
		{"anchor scheme", func(x *SettingTarget) { x.Equipment.Anchor.Scheme = "native.synthetic" }},
		{"anchor issuer", func(x *SettingTarget) { x.Equipment.Anchor.Issuer = "synthetic" }},
		{"anchor digest", func(x *SettingTarget) { x.Equipment.Anchor.Key = evidence([]byte{9})[0].Digest }},
		{"zone id", func(x *SettingTarget) { x.Zone.ID = "resource.synthetic.other" }},
		{"zone bridge", func(x *SettingTarget) { x.Zone.Bridge = "bridge.synthetic.other" }},
		{"zone basis", func(x *SettingTarget) { x.ZoneBasis = nil }},
		{"device class", func(x *SettingTarget) { x.Equipment.Class = reg.GreeVRFHotWaterUnit }},
		{"destination", func(x *SettingTarget) { x.Equipment.Key.Unit = 0x08 }},
		{"interface", func(x *SettingTarget) { x.Equipment.Key.Interface = reg.GreeVRFRegisterKey{}.Interface }},
		{"family", func(x *SettingTarget) { x.Equipment.Key.Family = reg.GreeVRFAddressedWrite }},
	} {
		t.Run(v.name, func(t *testing.T) {
			target := original
			v.change(&target)
			if _, ok := TemperatureTarget(in.Subject, in.Equipment.Anchor, []SettingTarget{target}); ok {
				t.Fatal("accepted a different owner or invalid target")
			}
		})
	}
}

func TestIndoorModeKeepsUnmappedEnumerationNative(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	_, point := imagePoint(t, iface, 0x20, 0x21, 0, false, 7)
	_, power := imagePoint(t, iface, 0x20, 0x1f, 0, true, 1)
	in := readInput(point, indoor, terminal)
	in.Power = &power
	got, err := Evaluate(point, in)
	if err != nil || got.Point.EnumerationLabel == "" || got.Source == nil || got.Evaluation.Candidate != nil || got.Evaluation.Reason == "" {
		t.Fatalf("unmapped enumeration: %+v %v", got, err)
	}
}

func TestReadRejectsAnotherNativeSourceAndKeepsAbsentDeclaration(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	_, p := imagePoint(t, iface, 0x20, 0x20, 0, false, 123)
	in := readInput(p, indoor, zone)
	for _, v := range []struct {
		name   string
		change func(*reg.GreeVRFTypedPoint)
	}{
		{"interface name", func(x *reg.GreeVRFTypedPoint) { x.Key.Interface = bus(t, "other", 1) }},
		{"interface index", func(x *reg.GreeVRFTypedPoint) { x.Key.Interface = bus(t, "synthetic", 2) }},
		{"unit", func(x *reg.GreeVRFTypedPoint) { x.Key.Unit++ }},
		{"family", func(x *reg.GreeVRFTypedPoint) { x.Key.Family = reg.GreeVRFAddressedWrite }},
	} {
		t.Run(v.name, func(t *testing.T) {
			other := p
			v.change(&other)
			got, err := Evaluate(other, in)
			if err != nil || got.Missing != "matching native target" || got.Source != nil {
				t.Fatalf("wrong native source: %+v %v", got, err)
			}
		})
	}
	absent := pointAt(t, reg.NewGreeVRFRegisterImage(p.Key), 0, bytePoint)
	got, err := Evaluate(absent, in)
	if err != nil || got.Source == nil || got.Evaluation.Candidate != nil || got.Missing != "native point absent" {
		t.Fatalf("absent source: %+v %v", got, err)
	}
	noAnchor := in
	noAnchor.Equipment.Anchor = driver.AnchorKey{}
	got, err = Evaluate(p, noAnchor)
	if err != nil || got.Source != nil || got.Missing != "configured equipment anchor" {
		t.Fatalf("unanchored source: %+v %v", got, err)
	}
}
