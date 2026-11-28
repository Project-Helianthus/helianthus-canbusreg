package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

func TestProjectProfilesPlanOneInstance(t *testing.T) {
	f := NewFactory()
	pcsPlan := Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &PCSSelection{
		Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
		Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
	}}
	linkPlan := Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: append(twoSources(), coordinatorEntry())}
	for _, tc := range []struct {
		key     string
		options Options
	}{
		{"pcs-one", pcsPlan},
		{"link-one", linkPlan},
	} {
		raw, err := json.Marshal(tc.options)
		if err != nil {
			t.Fatal(err)
		}
		plans, err := f.Plan(config(tc.key, `{"interface":"can0"}`, string(raw)))
		if err != nil || len(plans) != 1 || string(plans[0].Instance) != tc.key {
			t.Fatalf("%s: %v %v", tc.key, plans, err)
		}
	}
	stored := f.planned["link-one"].Options.Sources
	if len(stored) != 3 {
		t.Fatalf("sources %d", len(stored))
	}
	route := stored[2]
	if route.Source != 0xF4 || route.Destination != 0xF5 || route.Endpoint != "F4" || route.Direction != coordinatorDirection || route.Scope != coordinatorScope || route.Group != "" || route.Slot != "" {
		t.Fatalf("coordinator route %+v", route)
	}
	if f.planned["pcs-one"].Options.PCS == nil || f.planned["pcs-one"].Options.CacheAge != time.Second {
		t.Fatal("pcs selection was not kept")
	}
}

func TestProjectOptionsNameTheMember(t *testing.T) {
	f := NewFactory()
	pcs := Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &PCSSelection{
		Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
		Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
	}}
	body, err := json.Marshal(pcs)
	if err != nil {
		t.Fatal(err)
	}
	dup := twoSources()
	dup[1] = moduleEntry(1, "01", "QAAa", "9011", "release11")
	duplicated, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: dup})
	if err != nil {
		t.Fatal(err)
	}
	missing := pcs
	missing.CacheAge = 0
	missingRaw, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, options, member string }{
		{"unknown key", strings.TrimSuffix(string(body), "}") + `,"nope":1}`, "nope"},
		{"selection on pcs", strings.TrimSuffix(string(body), "}") + `,"selection":{"scope":"local"}}`, "selection"},
		{"duplicate source", string(duplicated), "source"},
		{"missing cache age", string(missingRaw), "cache_age"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Plan(config("x", `{"interface":"can0"}`, tc.options))
			if err == nil || !strings.Contains(err.Error(), tc.member) {
				t.Fatalf("err = %v, want member %q", err, tc.member)
			}
		})
	}
}

func TestLinkSourcesKeepTheirLayoutValues(t *testing.T) {
	// Each source differs in source, endpoint, project code, release and choice.
	// source 01 / QAAA / 0005 / legacy19 is layout C, so payload A stays on checkpoint_0.
	// source 02 / QAAa / 9011 / release11 is layout W, so payload B stays on full_capacity.
	// Using one selection for both writes payload B over checkpoint_0 or leaves full_capacity absent.
	// A source with no entry must not replace either value.
	payloadA := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	payloadB := []byte{0x0A, 0x00, 0x14, 0x00, 0x1E, 0x28, 0x32, 0x3C}
	payloadC := []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	sources := twoSources()
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x180BF401, Extended: true, Data: payloadA, Monotonic: time.Millisecond},
		{ID: 0x180BF402, Extended: true, Data: payloadB, Monotonic: 2 * time.Millisecond},
		{ID: 0x180BF403, Extended: true, Data: payloadC, Monotonic: 3 * time.Millisecond},
	}}
	pub := &recordingPublisher{}
	inst, reg := runRecorded(t, "link-layouts", "can0", string(raw), script, pub)
	waitReceived(t, inst, 3)
	wantA := linkField(t, sources[0], 1, 0x180BF401, payloadA, "checkpoint_0")
	wantB := linkField(t, sources[1], 1, 0x180BF402, payloadB, "full_capacity")
	deadline := time.Now().Add(5 * time.Second)
	var gotA, gotB readingBody
	for {
		gotA = readReading(t, reg, "growatt.bms.link.checkpoint_0")
		gotB = readReading(t, reg, "growatt.bms.link.full_capacity")
		if gotA.Value != nil && gotB.Value != nil && *gotA.Value == wantA && *gotB.Value == wantB && len(pub.snapshot()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			a, b := "", ""
			if gotA.Value != nil {
				a = *gotA.Value
			}
			if gotB.Value != nil {
				b = *gotB.Value
			}
			t.Fatalf("checkpoint %q full %q batches %d, want %s and %s and no batch", a, b, len(pub.snapshot()), wantA, wantB)
		}
		time.Sleep(time.Millisecond)
	}
	if again := readReading(t, reg, "growatt.bms.link.checkpoint_0"); again.Value == nil || *again.Value != wantA {
		t.Fatalf("unselected source changed checkpoint_0 to %v", again.Value)
	}
	cycles := linkField(t, sources[1], 1, 0x180BF402, payloadB, "fine_cycles")
	state := linkField(t, sources[1], 1, 0x180BF402, payloadB, "algorithm_state")
	if got := readReading(t, reg, "growatt.bms.link.fine_cycles"); got.Value == nil || *got.Value != cycles {
		t.Fatalf("fine_cycles %v, want %s", got.Value, cycles)
	}
	if got := readReading(t, reg, "growatt.bms.link.algorithm_state"); got.Value == nil || *got.Value != state {
		t.Fatalf("algorithm_state %v, want %s", got.Value, state)
	}
}

func TestPCSPublishesBoundElementsAndRows(t *testing.T) {
	sel := PCSSelection{
		Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
		Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
	}
	raw, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: 2500000000, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString("14BEFFF6FFF65563")
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x313, Data: payload, Monotonic: time.Millisecond},
	}}
	pub := &recordingPublisher{}
	inst, reg := runRecorded(t, "pcs-bound", "can0", string(raw), script, pub)
	batches := waitBatches(t, pub, 1)
	facts := batches[0].FactUpserts
	assertQuantity(t, facts, "storage.state.soc", "unit.ratio", "0.85")
	assertQuantity(t, facts, "storage.state.soh", "unit.ratio", "0.99")
	assertQuantity(t, facts, "storage.pack.current", "unit.ampere", "-1")
	assertQuantity(t, facts, "storage.pack.voltage", "unit.volt", "53.1")
	for _, fact := range facts {
		if fact.FreshnessPolicy.FreshForNS != "2500000000" || fact.FreshnessPolicy.RetainForNS != "2500000001" {
			t.Fatalf("cache age window %+v", fact.FreshnessPolicy)
		}
		if text := subjectText(t, fact); text != "pack" {
			t.Fatalf("dimension %s", text)
		}
	}
	want := pcsMemberIDs(t, sel)
	got := map[string]bool{}
	for _, member := range reg.members {
		local := string(member.spec.Local)
		if strings.HasPrefix(local, "diagnostic.") {
			continue
		}
		got[local] = true
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("missing native row %s", id)
		}
	}
	for id := range got {
		if !want[id] {
			t.Fatalf("row %s is not a row of this selection", id)
		}
	}
	if _, ok := got["growatt.bms.pcs.standard.313.soc"]; !ok {
		t.Fatal("soc row is not a member")
	}
	if got["growatt.bms.pcs.extended19.1902.soc"] {
		t.Fatal("extended row is a member of a standard selection")
	}
	methods := map[string]bool{}
	for _, method := range reg.methods {
		methods[string(method.Local)] = true
	}
	for _, row := range pcs.Commands() {
		if !methods[row.ID] {
			t.Fatalf("command %s is not a member", row.ID)
		}
	}
	for _, row := range pcs.ExtendedCommands() {
		if !methods[row.ID] {
			t.Fatalf("command %s is not a member", row.ID)
		}
	}
	native := readReading(t, reg, "growatt.bms.pcs.standard.313.soc")
	if native.Value == nil || native.Unit != "percent" {
		t.Fatalf("soc native %+v", native)
	}
	_ = inst
}

func runRecorded(t *testing.T, key, iface, options string, script replay.Script, pub *recordingPublisher) (*instance, *memRegistry) {
	t.Helper()
	factory, err := NewFactoryWithSource(func(name string) (receiver.Receiver, error) {
		if name != iface {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		return replay.Open(script)
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := factory.Plan(config(key, `{"interface":"`+iface+`"}`, options))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	built, err := factory.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	inst := built.(*instance)
	reg := &memRegistry{}
	if err := inst.registerNamespace(reg); err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(context.Background(), feedSession{pub: pub}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Stop(context.Background()) })
	return inst, reg
}

func linkField(t *testing.T, entry LINKSourceSelection, index int, id uint32, payload []byte, name string) string {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity("can0", index)
	if err != nil {
		t.Fatal(err)
	}
	sels, err := NewLINKSelections(identity, []LINKSourceSelection{entry})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := sels.Lookup(entry.Source)
	if !ok {
		t.Fatalf("source %d missing", entry.Source)
	}
	ident, err := canbus.NewExtendedID(id)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(ident, payload)
	if err != nil {
		t.Fatal(err)
	}
	evidence := canbusreg.Evidence{Frame: frame, Interface: identity}
	decoded := canbusreg.DecodeLINKTelemetry(got.Selection, linkContext(got.Selection.SelectionSpecValue(), evidence))
	field, present := decoded.Fields[name]
	if !present || decoded.Status != "accepted" || field.Denominator != 1 {
		t.Fatalf("%s status %s field %+v", name, decoded.Status, field)
	}
	return spelledNumerator(field.Numerator)
}

// spelledNumerator is the namespace spelling of a decoded whole numerator:
// trailing zeros move into the exponent, so 10 is 1e1 and 513 stays 513.
func spelledNumerator(n int64) string {
	exp := 0
	for n != 0 && n%10 == 0 {
		n /= 10
		exp++
	}
	if exp == 0 {
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10) + "e" + strconv.Itoa(exp)
}

func pcsMemberIDs(t *testing.T, sel PCSSelection) map[string]bool {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := NewPCSSelection(identity, sel)
	if err != nil {
		t.Fatal(err)
	}
	q := pcs.Qualifiers(selection, pcsInput(selection, canbusreg.Evidence{}))
	keys := map[string]struct{}{
		"flavor": {}, "bank": {}, "contract-group": {}, "layout": {},
		"scope": {}, "direction": {}, "endpoint": {}, "group": {}, "slot": {},
		"destination": {}, "source": {}, "release-11": {}, "local": {}, "gateway": {},
		"local-units": {}, "group-two": {}, "group-eight": {}, "group-present": {},
		"fine-group": {}, "configuration-word": {},
	}
	out := map[string]bool{}
	for _, row := range pcs.CombinedTable().Native {
		if row.ID == "" {
			continue
		}
		admit := true
		for key, want := range row.When.Equal {
			if _, keep := keys[key]; !keep {
				continue
			}
			if q.Values[key] != want {
				admit = false
				break
			}
		}
		if admit {
			out[row.ID] = true
		}
	}
	return out
}
