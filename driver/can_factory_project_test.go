package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/periodic"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
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
		gotA = readReading(t, reg, "growatt.bms.link.checkpoint_0.01")
		gotB = readReading(t, reg, "growatt.bms.link.full_capacity.02")
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
	if again := readReading(t, reg, "growatt.bms.link.checkpoint_0.01"); again.Value == nil || *again.Value != wantA {
		t.Fatalf("unselected source changed checkpoint_0 to %v", again.Value)
	}
	cycles := linkField(t, sources[1], 1, 0x180BF402, payloadB, "fine_cycles")
	state := linkField(t, sources[1], 1, 0x180BF402, payloadB, "algorithm_state")
	if got := readReading(t, reg, "growatt.bms.link.fine_cycles.02"); got.Value == nil || *got.Value != cycles {
		t.Fatalf("fine_cycles %v, want %s", got.Value, cycles)
	}
	if got := readReading(t, reg, "growatt.bms.link.algorithm_state.02"); got.Value == nil || *got.Value != state {
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
	if n := countFacts(facts, "storage.pack.voltage"); n != 1 {
		t.Fatalf("pack voltage candidates %d", n)
	}
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

func standardPCS() PCSSelection {
	return PCSSelection{
		Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
		Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
	}
}

func TestPCSNativeFamiliesReceiveValues(t *testing.T) {
	sel := standardPCS()
	raw, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	extrema, identity, gauge := mustHex(t, "5A0D2F0D20030B01"), mustHex(t, "1122330102030400"), mustHex(t, "000A0014001E012C")
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x319, Data: extrema, Monotonic: time.Millisecond},
		{ID: 0x320, Data: identity, Monotonic: 2 * time.Millisecond},
		{ID: 0x315, Data: gauge, Monotonic: 3 * time.Millisecond},
	}}
	inst, reg := runRecorded(t, "pcs-families", "can0", string(raw), script, &recordingPublisher{})
	waitReceived(t, inst, 3)
	assertNative(t, reg, "growatt.bms.pcs.standard.319.maximum_cell", pcsNative(t, sel, 0x319, extrema))
	assertNative(t, reg, "growatt.bms.pcs.standard.320.hardware", pcsNative(t, sel, 0x320, identity))
	assertNative(t, reg, "growatt.bms.pcs.standard.315.capacity", pcsNative(t, sel, 0x315, gauge))
}

func TestLinkFineCapacityPublishesCharge(t *testing.T) {
	entry := moduleEntry(2, "02", "QAAA", "0006", "")
	payload := mustHex(t, "e803190007095802")
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: []LINKSourceSelection{entry}})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x180BF402, Extended: true, Data: payload, Monotonic: time.Millisecond},
	}}
	pub := &recordingPublisher{}
	_, reg := runRecorded(t, "link-capacity", "can0", string(raw), script, pub)
	batches := waitBatches(t, pub, 1)
	if factByID(batches[0].FactUpserts, "storage.capacity.charge") == nil {
		t.Fatal("storage.capacity.charge was not published")
	}
	assertNative(t, reg, "growatt.bms.link.full_capacity.02", linkFineNative(t, entry, 0x180BF402, payload, "growatt.bms.link.full_capacity"))
}

func TestLinkSerialFragmentFillsIdentityRows(t *testing.T) {
	entry := moduleEntry(2, "02", "QAAA", "0006", "")
	payload := mustHex(t, "0f31323334353637")
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: []LINKSourceSelection{entry}})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x18FBF402, Extended: true, Data: payload, Monotonic: time.Millisecond},
	}}
	inst, reg := runRecorded(t, "link-serial", "can0", string(raw), script, &recordingPublisher{})
	waitReceived(t, inst, 1)
	assertNative(t, reg, "growatt.bms.link.serial_header.02", linkIdentityNative(t, entry, 0x18FBF402, payload, "growatt.bms.link.serial_header"))
}

func TestLinkDiagnosticFrameFillsRegisteredRow(t *testing.T) {
	entry := moduleEntry(2, "02", "QAAA", "0006", "")
	payload := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: []LINKSourceSelection{entry}})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x1850F402, Extended: true, Data: payload, Monotonic: time.Millisecond},
	}}
	inst, reg := runRecorded(t, "link-diagnostic", "can0", string(raw), script, &recordingPublisher{})
	waitReceived(t, inst, 1)
	local, want := linkDiagnosticNative(t, entry, 0x1850F402, payload)
	assertNative(t, reg, local+".02", want)
}

func TestLinkSourcesKeepSeparateRemainingCapacity(t *testing.T) {
	left := moduleEntry(1, "01", "QAAA", "0006", "")
	right := moduleEntry(2, "02", "QAAA", "0006", "")
	payloadA := []byte{0, 0, 0, 0, 0, 0, 0x64, 0x00}
	payloadB := []byte{0, 0, 0, 0, 0, 0, 0xC8, 0x00}
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: []LINKSourceSelection{left, right}})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x180BF401, Extended: true, Data: payloadA, Monotonic: time.Millisecond},
		{ID: 0x180BF402, Extended: true, Data: payloadB, Monotonic: 2 * time.Millisecond},
	}}
	inst, reg := runRecorded(t, "link-modules", "can0", string(raw), script, &recordingPublisher{})
	waitReceived(t, inst, 2)
	row := "growatt.bms.link.remaining_capacity"
	wantA := linkFineNative(t, left, 0x180BF401, payloadA, row)
	wantB := linkFineNative(t, right, 0x180BF402, payloadB, row)
	if wantA == wantB {
		t.Fatalf("fixture values collapsed to %s", wantA)
	}
	assertNative(t, reg, row+".01", wantA)
	assertNative(t, reg, row+".02", wantB)
}

func TestStartRegistersProjectNamespaceAndCommand(t *testing.T) {
	sel := standardPCS()
	body, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingSource{}
	factory, err := NewFactoryWithSource(func(name string) (receiver.Receiver, error) {
		if name != "can0" {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		src, err := replay.Open(replay.Script{Name: "can0", Interface: "can0", Index: 1})
		if err != nil {
			return nil, err
		}
		rec.Receiver = src
		return rec, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := factory.Plan(config("pcs-start", `{"interface":"can0"}`, string(body)))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	built, err := factory.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	inst := built.(*instance)
	reg := &memRegistry{}
	if err := inst.Start(context.Background(), nativeSession{feedSession: feedSession{}, reg: reg}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Stop(context.Background()) })
	soc := readReading(t, reg, "growatt.bms.pcs.standard.313.soc")
	if soc.Validity != "absent" {
		t.Fatalf("soc before a frame %+v", soc)
	}
	clock := pcs.Commands()[0].ID
	if !reg.hasMethod(clock) {
		t.Fatal("clock command was not registered")
	}
	buf, err := reg.call(clock, []byte(`{"year":26,"month":11,"day":26,"hour":13,"minute":45,"second":30}`))
	if err != nil {
		t.Fatal(err)
	}
	var result commandResult
	if err := json.Unmarshal(buf, &result); err != nil || !result.Accepted {
		t.Fatalf("command result %s %v", buf, err)
	}
	sent := rec.sent()
	if len(sent) != 1 || sent[0].ID().Value() != 0x211 || sent[0].ID().Extended() {
		t.Fatalf("sent %#v", sent)
	}
}

func TestPCSSummaryBitFillsRegisteredMember(t *testing.T) {
	sel := standardPCS()
	raw, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x312, Data: []byte{1, 0, 0, 0, 0, 0, 0, 0}, Monotonic: time.Millisecond},
	}}
	inst, reg := runRecorded(t, "pcs-summary", "can0", string(raw), script, &recordingPublisher{})
	waitReceived(t, inst, 1)
	deadline := time.Now().Add(5 * time.Second)
	var got readingBody
	for {
		got = readReading(t, reg, "growatt.bms.pcs.standard.312.A/0")
		if got.Value != nil && *got.Value == "1" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary bit %+v", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLinkModulesPublishDistinctPackDimensions(t *testing.T) {
	left := moduleEntry(1, "01", "QAAA", "0006", "")
	right := moduleEntry(2, "02", "QAAA", "0006", "")
	raw, err := json.Marshal(Options{Profile: ProfileLINK, CacheAge: time.Second, Sources: []LINKSourceSelection{left, right}})
	if err != nil {
		t.Fatal(err)
	}
	script := replay.Script{Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{
		{ID: 0x1805F401, Extended: true, Data: mustHex(t, "6400000000000000"), Monotonic: time.Millisecond},
		{ID: 0x1805F402, Extended: true, Data: mustHex(t, "1282ea9b10feffff"), Monotonic: 2 * time.Millisecond},
	}}
	pub := &recordingPublisher{}
	runRecorded(t, "link-packs", "can0", string(raw), script, pub)
	batches := waitBatches(t, pub, 2)
	dims := map[string]struct{}{}
	for _, batch := range batches {
		if err := batch.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, fact := range batch.FactUpserts {
			if fact.Key.FactID != "storage.pack.voltage" {
				continue
			}
			dims[subjectText(t, fact)] = struct{}{}
		}
	}
	if _, ok := dims["pack.01"]; !ok || len(dims) != 2 {
		t.Fatalf("pack dimensions %v", dims)
	}
	if _, ok := dims["pack.02"]; !ok {
		t.Fatalf("pack dimensions %v", dims)
	}
}

func TestCanceledPCSClockSubmitsNothing(t *testing.T) {
	sel := standardPCS()
	body, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingSource{}
	factory, err := NewFactoryWithSource(func(name string) (receiver.Receiver, error) {
		if name != "can0" {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		src, err := replay.Open(replay.Script{Name: "can0", Interface: "can0", Index: 1})
		if err != nil {
			return nil, err
		}
		rec.Receiver = src
		return rec, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := factory.Plan(config("pcs-cancel", `{"interface":"can0"}`, string(body)))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	built, err := factory.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	inst := built.(*instance)
	reg := &memRegistry{}
	if err := inst.Start(context.Background(), nativeSession{feedSession: feedSession{}, reg: reg}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Stop(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clock := pcs.Commands()[0].ID
	buf, err := reg.callContext(ctx, clock, []byte(`{"year":26,"month":11,"day":26,"hour":13,"minute":45,"second":30}`))
	if err == nil {
		t.Fatalf("canceled clock returned %s", buf)
	}
	if len(rec.sent()) != 0 {
		t.Fatalf("canceled clock submitted %#v", rec.sent())
	}
}

func TestDefaultFactoryUsesPinnedWriter(t *testing.T) {
	recvType := reflect.TypeOf((*receiver.Receiver)(nil)).Elem()
	if _, ok := recvType.MethodByName("Submit"); ok {
		t.Fatal("pinned receiver interface declares Submit")
	}
	sendType := reflect.TypeOf((*periodic.Sender)(nil)).Elem()
	if _, ok := sendType.MethodByName("Submit"); !ok {
		t.Fatal("pinned sender interface has no Submit")
	}
	factory := NewFactory()
	sender, err := factory.write("can0")
	if sender != nil {
		releaseWriter(sender)
	}
	if runtime.GOOS != "linux" {
		if !errors.Is(err, canbus.ErrUnsupportedPlatform) {
			t.Fatalf("default writer %v", err)
		}
	} else if err == nil && sender == nil {
		t.Fatal("default writer returned nothing")
	}
	sent := &captureSender{}
	factory.open = func(name string) (receiver.Receiver, error) {
		return replay.Open(replay.Script{Name: name, Interface: name, Index: 1})
	}
	factory.write = func(string) (periodic.Sender, error) { return sent, nil }
	sel := standardPCS()
	body, err := json.Marshal(Options{Profile: ProfilePCS, CacheAge: time.Second, PCS: &sel})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := factory.Plan(config("pcs-writer", `{"interface":"can0"}`, string(body)))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	built, err := factory.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	inst := built.(*instance)
	reg := &memRegistry{}
	if err := inst.Start(context.Background(), nativeSession{feedSession: feedSession{}, reg: reg}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Stop(context.Background()) })
	if _, ok := inst.recv.(periodic.Sender); ok {
		t.Fatal("receive-only source implements Submit")
	}
	clock := pcs.Commands()[0].ID
	if _, err := reg.call(clock, []byte(`{"year":26,"month":11,"day":26,"hour":13,"minute":45,"second":30}`)); err != nil {
		t.Fatal(err)
	}
	sent.mu.Lock()
	defer sent.mu.Unlock()
	if len(sent.got) != 1 || sent.got[0].ID().Value() != 0x211 {
		t.Fatalf("submitted %#v", sent.got)
	}
}

type captureSender struct {
	mu  sync.Mutex
	got []canbus.Frame
}

func (s *captureSender) Submit(frame canbus.Frame) (periodic.Submission, error) {
	s.mu.Lock()
	s.got = append(s.got, frame)
	s.mu.Unlock()
	return periodic.Submission{Accepted: true, Monotonic: time.Millisecond}, nil
}

func countFacts(facts []semreg.FactCandidate, id string) int {
	n := 0
	for _, fact := range facts {
		if string(fact.Key.FactID) == id {
			n++
		}
	}
	return n
}

type nativeSession struct {
	feedSession
	reg semdriver.NativeRegistry
}

func (s nativeSession) Native() semdriver.NativeRegistry { return s.reg }

type recordingSource struct {
	receiver.Receiver
	mu  sync.Mutex
	got []canbus.Frame
}

func (s *recordingSource) Submit(frame canbus.Frame) (periodic.Submission, error) {
	s.mu.Lock()
	s.got = append(s.got, frame)
	s.mu.Unlock()
	return periodic.Submission{Accepted: true, Monotonic: time.Millisecond}, nil
}

func (s *recordingSource) sent() []canbus.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]canbus.Frame(nil), s.got...)
}

func assertNative(t *testing.T, reg *memRegistry, local, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got readingBody
	for {
		got = readReading(t, reg, local)
		if got.Value != nil && *got.Value == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %+v, want %s", local, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func pcsNative(t *testing.T, sel PCSSelection, id uint32, payload []byte) string {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := NewPCSSelection(identity, sel)
	if err != nil {
		t.Fatal(err)
	}
	evidence := nsEvidence(t, "can0", 1, 1, time.Millisecond, id, false, payload)
	in := pcsInput(selection, evidence)
	var rows map[string]roweval.Evaluation
	var reason roweval.Reason
	switch id {
	case 0x319:
		res, r, err := pcs.EvaluateExtrema(selection, in)
		if err != nil {
			t.Fatal(err)
		}
		rows, reason = res.Rows, r
	case 0x320:
		res, r, err := pcs.EvaluateIdentity(selection, canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: in})
		if err != nil {
			t.Fatal(err)
		}
		rows, reason = res.Rows, r
	case 0x315:
		res, r, err := pcs.EvaluateGauge(selection, canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: in})
		if err != nil {
			t.Fatal(err)
		}
		rows, reason = res.Rows, r
	default:
		t.Fatalf("identifier %#x", id)
	}
	if reason != "" {
		t.Fatalf("identifier %#x reason %s", id, reason)
	}
	var local string
	switch id {
	case 0x319:
		local = "growatt.bms.pcs.standard.319.maximum_cell"
	case 0x320:
		local = "growatt.bms.pcs.standard.320.hardware"
	case 0x315:
		local = "growatt.bms.pcs.standard.315.capacity"
	}
	return nativeSpelling(t, rows, local)
}

func linkFineNative(t *testing.T, entry LINKSourceSelection, id uint32, payload []byte, row string) string {
	t.Helper()
	res, reason, err := link.EvaluateFine(linkSelection(t, entry), linkInput(t, entry, id, payload), canbusreg.SummaryGates{})
	if err != nil || reason != "" {
		t.Fatalf("fine %v %s", err, reason)
	}
	return nativeSpelling(t, res.Rows, row)
}

func linkIdentityNative(t *testing.T, entry LINKSourceSelection, id uint32, payload []byte, row string) string {
	t.Helper()
	res, reason, err := link.EvaluateIdentity(linkSelection(t, entry), linkInput(t, entry, id, payload))
	if err != nil || reason != "" {
		t.Fatalf("identity %v %s", err, reason)
	}
	return nativeSpelling(t, res.Rows, row)
}

func linkDiagnosticNative(t *testing.T, entry LINKSourceSelection, id uint32, payload []byte) (string, string) {
	t.Helper()
	res, reason, err := link.EvaluateDiagnostics(linkSelection(t, entry), linkInput(t, entry, id, payload))
	if err != nil || reason != "" {
		t.Fatalf("diagnostics %v %s", err, reason)
	}
	ids := make([]string, 0, len(res.Rows))
	for local, ev := range res.Rows {
		if ev.Native != nil && ev.Native.Value != nil {
			ids = append(ids, local)
		}
	}
	if len(ids) == 0 {
		t.Fatal("diagnostics produced no value")
	}
	sort.Strings(ids)
	return ids[0], nativeSpelling(t, res.Rows, ids[0])
}

func linkSelection(t *testing.T, entry LINKSourceSelection) canbusreg.Selection {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity("can0", 1)
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
	return got.Selection
}

func linkInput(t *testing.T, entry LINKSourceSelection, id uint32, payload []byte) canbusreg.LINKInput {
	t.Helper()
	selection := linkSelection(t, entry)
	return linkContext(selection.SelectionSpecValue(), nsEvidence(t, "can0", 1, 1, time.Millisecond, id, true, payload))
}

func nativeSpelling(t *testing.T, rows map[string]roweval.Evaluation, local string) string {
	t.Helper()
	ev, ok := rows[local]
	if !ok || ev.Native == nil {
		t.Fatalf("%s missing in evaluator", local)
	}
	text, ok := nativeText(ev.Native.Value)
	if !ok {
		t.Fatalf("%s has no value", local)
	}
	return text
}
