package driver

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

const testBridge semdriver.BridgeID = "bridge.synthetic"

func slotOf(v uint8) *uint8 { return &v }

func ref(t *testing.T, k IdentityKey) semdriver.NodeRef {
	t.Helper()
	r, err := k.Ref()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func proof(raw string) []semreg.EvidenceRef {
	return []semreg.EvidenceRef{{Owner: "driver.canbusreg", Kind: "evidence.synthetic.frame",
		Digest:   semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw)))),
		Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
}

// Each field of the key moves the node: bridge, instance, kind and every
// anchor part. A production Ref that dropped any one of them would make a pair
// below equal.
func TestIdentityKeyEveryFieldMovesTheNode(t *testing.T) {
	base := ModuleKey(testBridge, "inst-a", 0x01, slotOf(3))
	want := "can/inst-a/module/01/slot3"
	if got := ref(t, base); got.ID != semreg.ResourceID(want) || got.Bridge != testBridge {
		t.Fatalf("base %+v", got)
	}
	variants := map[string]IdentityKey{
		"bridge":      ModuleKey("bridge.other", "inst-a", 0x01, slotOf(3)),
		"instance":    ModuleKey(testBridge, "inst-b", 0x01, slotOf(3)),
		"source":      ModuleKey(testBridge, "inst-a", 0x02, slotOf(3)),
		"slot value":  ModuleKey(testBridge, "inst-a", 0x01, slotOf(4)),
		"slot absent": ModuleKey(testBridge, "inst-a", 0x01, nil),
		"slot zero":   ModuleKey(testBridge, "inst-a", 0x01, slotOf(0)),
		"kind":        {testBridge, "inst-a", IdentityPack, []string{"01", "slot3"}},
	}
	seen := map[semdriver.NodeRef]string{ref(t, base): "base"}
	for name, k := range variants {
		r := ref(t, k)
		if other, dup := seen[r]; dup {
			t.Fatalf("%s equals %s: %+v", name, other, r)
		}
		seen[r] = name
	}
}

func TestIdentityKeyKindsCoverBatteryPackModuleCellInterfaceGreeAndZone(t *testing.T) {
	module := ModuleKey(testBridge, "inst-a", 0x0A, nil)
	keys := []IdentityKey{
		InterfaceKey(testBridge, "inst-a", "can-synthetic"), BatteryKey(testBridge, "inst-a"),
		PackKey(testBridge, "inst-a", "bank-ordinary"), module,
		CellKey(module, "cell-synthetic"), GreeEquipmentKey(testBridge, "inst-a", "unit-synthetic"),
		ZoneKey(testBridge, "inst-a", "zone-synthetic"),
	}
	want := []string{
		"can/inst-a/interface/can-synthetic", "can/inst-a/battery", "can/inst-a/pack/bank-ordinary",
		"can/inst-a/module/0A", "can/inst-a/cell/0A/cell-synthetic",
		"can/inst-a/gree-equipment/unit-synthetic", "can/inst-a/thermal-system/zone-synthetic",
	}
	for i, k := range keys {
		if r := ref(t, k); string(r.ID) != want[i] {
			t.Fatalf("%d: %s, want %s", i, r.ID, want[i])
		}
	}
}

func TestIdentityKeyRejectsSeparatorAndEmptyParts(t *testing.T) {
	for _, k := range []IdentityKey{
		PackKey(testBridge, "inst-a", ""), PackKey(testBridge, "inst-a", "a/b"),
		BatteryKey(testBridge, "inst/a"), BatteryKey("", "inst-a"),
	} {
		if _, err := k.Ref(); err == nil {
			t.Fatalf("accepted %+v", k)
		}
	}
}

func lifecycle(t *testing.T, units GreeUnits, reverse bool) []string {
	t.Helper()
	order := []int{0, 1, 2}
	if reverse {
		order = []int{2, 1, 0}
	}
	var ids []string
	instances := []semdriver.InstanceKey{"inst-bms-a", "inst-bms-b", "inst-gree"}
	for _, i := range order {
		switch i {
		case 0, 1:
			ids = append(ids, string(ref(t, BatteryKey(testBridge, instances[i])).ID),
				string(ref(t, ModuleKey(testBridge, instances[i], 0x01, nil)).ID))
		case 2:
			links, err := units.Relations(testBridge, instances[i])
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range links {
				ids = append(ids, string(l.Key.From.ID)+">"+string(l.Key.To.ID)+"@"+string(l.Key.Type.ID))
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func twoUnits() GreeUnits {
	tied := int32(1)
	return GreeUnits{
		{Anchor: "equip-one", Unit: 0x20, Zones: []GreeZoneRelation{{Zone: "zone-main", Basis: "configured", Priority: &tied}}},
		{Anchor: "equip-two", Unit: 0x21, Zones: []GreeZoneRelation{{Zone: "zone-main", Basis: "configured", Priority: &tied, SettingOwner: true}}},
	}
}

func TestIdentityTwoLifecyclesWithReversedFactoryOrderMatchAnIndependentGraph(t *testing.T) {
	want := []string{
		"can/inst-bms-a/battery", "can/inst-bms-a/module/01",
		"can/inst-bms-b/battery", "can/inst-bms-b/module/01",
		"can/inst-gree/gree-equipment/equip-one>can/inst-gree/thermal-system/zone-main@thermal.relation.air_supply",
		"can/inst-gree/gree-equipment/equip-two>can/inst-gree/thermal-system/zone-main@thermal.relation.air_supply",
	}
	sort.Strings(want)
	forward, reversed := lifecycle(t, twoUnits(), false), lifecycle(t, twoUnits(), true)
	if !reflect.DeepEqual(forward, want) || !reflect.DeepEqual(reversed, want) {
		t.Fatalf("forward %v\nreversed %v\nwant %v", forward, reversed, want)
	}
}

func TestSerialClaimChangedSerialIsANewUnboundClaimOnTheSameNode(t *testing.T) {
	node := ref(t, ModuleKey(testBridge, "inst-a", 0x01, nil))
	first, err := SerialClaim(node, "serial-synthetic-1", proof("a"))
	if err != nil {
		t.Fatal(err)
	}
	same, _ := SerialClaim(node, "serial-synthetic-1", proof("a"))
	changed, err := SerialClaim(node, "serial-synthetic-2", proof("a"))
	if err != nil {
		t.Fatal(err)
	}
	wantKey := semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("serial-synthetic-1"))))
	if first.Key != wantKey || !reflect.DeepEqual(first, same) || first.Key == changed.Key {
		t.Fatalf("claims: %+v %+v", first, changed)
	}
	if again := ref(t, ModuleKey(testBridge, "inst-a", 0x01, nil)); again != node || changed.Member != node {
		t.Fatal("a changed serial moved the node")
	}
	if _, err := SerialClaim(node, "", proof("a")); err == nil {
		t.Fatal("empty serial accepted")
	}
}

func TestGreeUnitsRejectRepeatedAnchorCoordinateAndIncompleteRelation(t *testing.T) {
	for name, units := range map[string]GreeUnits{
		"anchor":     {{Anchor: "a", Unit: 1}, {Anchor: "a", Unit: 2}},
		"coordinate": {{Anchor: "a", Unit: 1}, {Anchor: "b", Unit: 1}},
		"empty":      {{Anchor: "", Unit: 1}},
		"basis":      {{Anchor: "a", Unit: 1, Zones: []GreeZoneRelation{{Zone: "z"}}}},
		"zone":       {{Anchor: "a", Unit: 1, Zones: []GreeZoneRelation{{Zone: "z", Basis: "b"}, {Zone: "z", Basis: "b"}}}},
	} {
		if units.Validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if err := twoUnits().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGreeEquipmentKeepsAnchorAndCoordinateSeparateFromClass(t *testing.T) {
	iface, err := canbus.NewInterfaceIdentity("can-synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	units := twoUnits()
	one, err := units.Equipment(testBridge, "inst-gree", iface, "equip-one", canbusreg.GreeVRFIndoorUnit)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := units.Equipment(testBridge, "inst-gree", iface, "equip-two", canbusreg.GreeVRFIndoorUnit)
	if one.Key.Unit != 0x20 || two.Key.Unit != 0x21 || one.Key.Family != canbusreg.GreeVRFData ||
		one.Anchor.Slot != "equip-one" || one.Producer == two.Producer || one.Class != two.Class {
		t.Fatalf("%+v %+v", one, two)
	}
	if _, err := units.Equipment(testBridge, "inst-gree", iface, "absent", canbusreg.GreeVRFIndoorUnit); err == nil {
		t.Fatal("unconfigured anchor resolved")
	}
}

func TestGreeSettingRouteGoesOnlyToTheDeclaredOwnerAndVanishesWithoutIt(t *testing.T) {
	iface, _ := canbus.NewInterfaceIdentity("can-synthetic", 1)
	classes := map[string]canbusreg.GreeVRFDeviceClass{"equip-one": canbusreg.GreeVRFIndoorUnit, "equip-two": canbusreg.GreeVRFIndoorUnit}
	zone := ref(t, ZoneKey(testBridge, "inst-gree", "zone-main"))
	argument := semreg.Quantity{Number: semreg.Decimal{Coefficient: "29615", Exponent10: -2}, Unit: "unit.kelvin"}
	send := func(units GreeUnits) (canbusreg.GreeVRFCommand, error) {
		owner, ok := units.SettingOwner("zone-main")
		if !ok {
			return canbusreg.GreeVRFCommand{}, fmt.Errorf("no owner")
		}
		targets, err := units.SettingTargets(testBridge, "inst-gree", iface, classes)
		if err != nil {
			t.Fatal(err)
		}
		return gree.EncodeTemperature(zone, owner, targets, argument, nil)
	}
	frame, err := send(twoUnits())
	if err != nil || frame.Identifier != 0x1cbfd0f3 {
		t.Fatalf("owner frame %08x %v", frame.Identifier, err)
	}
	units := twoUnits()
	units[1].Zones[0].SettingOwner = false
	if f, err := send(units); err == nil {
		t.Fatalf("route without owner: %+v", f)
	}
	both := twoUnits()
	both[0].Zones[0].SettingOwner = true
	if _, ok := both.SettingOwner("zone-main"); ok {
		t.Fatal("two owners resolved")
	}
}

func TestGreePriorityStaysKeyedToItsSource(t *testing.T) {
	a, b := int32(5), int32(7)
	units := GreeUnits{
		{Anchor: "equip-one", Unit: 1, Zones: []GreeZoneRelation{{Zone: "zone-x", Basis: "c", Priority: &a}}},
		{Anchor: "equip-two", Unit: 2, Zones: []GreeZoneRelation{{Zone: "zone-x", Basis: "c", Priority: &b}, {Zone: "zone-y", Basis: "c"}}},
	}
	if p, ok := units.Priority("equip-one", "zone-x"); !ok || *p != 5 {
		t.Fatal("first priority")
	}
	if p, ok := units.Priority("equip-two", "zone-x"); !ok || *p != 7 {
		t.Fatal("second priority")
	}
	if _, ok := units.Priority("equip-two", "zone-y"); ok {
		t.Fatal("undeclared priority invented")
	}
	if _, ok := units.Priority("equip-one", "zone-y"); ok {
		t.Fatal("priority crossed zones")
	}
}
