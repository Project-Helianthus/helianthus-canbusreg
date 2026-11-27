package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semhost "github.com/Project-Helianthus/helianthus-semreg/semreg/host"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

const (
	profileMeasurement = `{"profile":"growatt-bms-can-v104"}`
	profileEvents      = `{"profile":"growatt-bms-can-v108"}`
	profileCommon      = `{"profile":"growatt-bms-can-common"}`
	profileWatch       = `{"profile":"growatt-bms-can-monitor"}`
	profileInverter    = `{"profile":"growatt-bms-can-v104","v104_role":"inverter"}`
	profileLocal       = `{"profile":"growatt-bms-can-common","selection":{"scope":"local","flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`
)

type hostFactory struct {
	*Factory
	mu    sync.Mutex
	built map[string]*instance
}

func (h *hostFactory) Construct(ctx context.Context, plan semdriver.InstancePlan) (semdriver.Instance, error) {
	inst, err := h.Factory.Construct(ctx, plan)
	if err != nil {
		return nil, err
	}
	built, ok := inst.(*instance)
	if !ok {
		return nil, fmt.Errorf("constructed %T", inst)
	}
	h.mu.Lock()
	h.built[string(plan.Instance)] = built
	h.mu.Unlock()
	return inst, nil
}

func (h *hostFactory) take(key string) *instance {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.built[key]
}

type steadyClock struct{ at time.Time }

func (c steadyClock) Now() time.Time { return c.at }

func (c steadyClock) Sample() (uint64, error) { return 1, nil }

func (c steadyClock) Anchor(sample uint64) ([]byte, error) {
	return []byte(fmt.Sprint(sample)), nil
}

func (c steadyClock) Elapsed(anchor []byte, sample uint64) (uint64, error) {
	var base uint64
	if _, err := fmt.Sscan(string(anchor), &base); err != nil {
		return 0, err
	}
	if sample < base {
		return 0, errors.New("backward sample")
	}
	return sample - base, nil
}

func hostInstance(key, iface, options string) semhost.InstanceConfig {
	return semhost.InstanceConfig{
		Kind: Kind, Instance: semdriver.InstanceKey(key), Enabled: true,
		Transport: json.RawMessage(`{"interface":"` + iface + `"}`),
		Options:   json.RawMessage(options),
	}
}

func openHost(t *testing.T, at time.Time, scripts map[string]replay.Script, instances []semhost.InstanceConfig) (*semhost.Site, *hostFactory) {
	t.Helper()
	factory := NewFactory()
	factory.open = func(name string) (receiver.Receiver, error) {
		script, ok := scripts[name]
		if !ok {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		return replay.Open(script)
	}
	tracked := &hostFactory{Factory: factory, built: map[string]*instance{}}
	calendar := "UTC"
	site, err := semhost.Open(context.Background(), semhost.Config{
		InstallationKey: "installation.synthetic", BridgeID: "bridge.synthetic",
		StateDir: t.TempDir(), Clock: steadyClock{at: at}, Calendar: &calendar, Instances: instances,
	}, tracked)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = site.Close(context.Background()) })
	if err := site.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := site.WaitInitial(context.Background()); err != nil {
		t.Fatal(err)
	}
	return site, tracked
}

func scriptOf(iface string, index int, frames ...replay.Frame) replay.Script {
	return replay.Script{Name: iface, Interface: iface, Index: index, Frames: frames}
}

func measurementFrame() replay.Frame {
	return replay.Frame{ID: 0x313, Data: []byte{0, 1, 0, 2, 0, 0, 50, 90}, Monotonic: time.Millisecond}
}

func profileScripts(t *testing.T) (map[string]replay.Script, []semhost.InstanceConfig) {
	t.Helper()
	file, err := os.ReadFile("replay/testdata/frames.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := replay.Parse(file)
	if err != nil || len(catalog.Scripts) != 1 {
		t.Fatalf("catalogue: %v scripts %d", err, len(catalog.Scripts))
	}
	scripts := map[string]replay.Script{
		"can0": scriptOf("can0", 1, measurementFrame()),
		"can1": scriptOf("can1", 2, measurementFrame()),
		"can2": scriptOf("can2", 3, measurementFrame()),
		"can3": scriptOf("can3", 4, measurementFrame()),
		"can4": scriptOf("can4", 5, replay.Frame{ID: 0x04010001, Extended: true, Data: []byte{0x64, 0x00, 0x3a, 0x75, 0, 0, 0, 0}, Monotonic: time.Millisecond}),
		"can5": scriptOf("can5", 6, replay.Frame{ID: 0x311, Data: []byte{1, 2, 3}, Monotonic: time.Millisecond}),
		"can6": catalog.Scripts[0],
	}
	instances := []semhost.InstanceConfig{
		hostInstance("alpha", "can0", profileMeasurement),
		hostInstance("bravo", "can1", profileEvents),
		hostInstance("local", "can2", profileLocal),
		hostInstance("plain", "can3", profileCommon),
		hostInstance("watch", "can4", profileWatch),
		hostInstance("limits", "can5", profileLocal),
		hostInstance("families", "can6", profileMeasurement),
	}
	return scripts, instances
}

func subjectOf(fact semreg.FactCandidate) string {
	if len(fact.Key.Dimensions) == 1 && fact.Key.Dimensions[0].Value.Text != nil {
		return *fact.Key.Dimensions[0].Value.Text
	}
	return ""
}

func publishedLines(t *testing.T, site *semhost.Site) string {
	t.Helper()
	cut, err := site.Tree().SemanticInput(semdriver.Filter{}).Capture(context.Background(), semdriver.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, snap := range cut.Snapshots {
		for _, env := range snap.Facts {
			for _, cand := range env.Candidates {
				if cand.Value == nil || cand.Value.Quantity == nil {
					lines = append(lines, subjectOf(cand)+" "+string(cand.Key.FactID)+" other")
					continue
				}
				q := cand.Value.Quantity
				lines = append(lines, fmt.Sprintf("%s %s %s %s %d", subjectOf(cand), cand.Key.FactID, q.Unit, q.Number.Coefficient, q.Number.Exponent10))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestSecondHostReplaysConfiguredProfiles(t *testing.T) {
	left := profileCapture(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	right := profileCapture(t, time.Date(2024, 6, 7, 8, 9, 10, 0, time.UTC))
	if left != right {
		t.Fatalf("publications differ\n%s\n%s", left, right)
	}
	requirePublished(t, left, "can/alpha/battery", "storage.state.soc", "unit.ratio", "1/2")
	requirePublished(t, left, "can/alpha/battery", "storage.pack.voltage", "unit.volt", "1/100")
	requirePublished(t, left, "can/alpha/battery", "storage.pack.current", "unit.ampere", "1/5")
	requirePublished(t, left, "can/bravo/battery", "storage.state.soc", "unit.ratio", "1/2")
	requirePublished(t, left, "can/bravo/battery", "storage.state.soh", "unit.ratio", "9/10")
	requirePublished(t, left, "can/local/battery", "storage.state.soc", "unit.ratio", "1/2")
	requirePublished(t, left, "can/local/battery", "storage.pack.voltage", "unit.volt", "1/100")
	requirePublished(t, left, "can/local/battery", "storage.pack.current", "unit.ampere", "1/5")
	requirePublished(t, left, "can/watch/battery", "storage.pack.voltage", "unit.volt", "10/1")
	requirePublished(t, left, "can/watch/battery", "storage.pack.current", "unit.ampere", "1/1")
}

func profileCapture(t *testing.T, at time.Time) string {
	t.Helper()
	scripts, instances := profileScripts(t)
	site, tracked := openHost(t, at, scripts, instances)
	for key, iface := range map[string]string{
		"alpha": "can0", "bravo": "can1", "local": "can2", "plain": "can3",
		"watch": "can4", "limits": "can5", "families": "can6",
	} {
		inst := tracked.take(key)
		want := uint64(len(scripts[iface].Frames))
		waitReceived(t, inst, want)
		received, skipped, _ := inst.counts()
		if received != want || skipped != 0 {
			t.Fatalf("%s received=%d skipped=%d frames=%d", key, received, skipped, want)
		}
	}
	return settledLines(t, site)
}

// settledLines waits until the host capture repeats and includes the measurement
// state of charge. A receive count moves before that frame's batch commits.
func settledLines(t *testing.T, site *semhost.Site) string {
	t.Helper()
	var prev string
	same := 0
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := publishedLines(t, site)
		if got == prev && strings.Contains(got, "can/alpha/battery storage.state.soc ") {
			same++
			if same >= 2 {
				return got
			}
		} else {
			same = 0
		}
		prev = got
		if time.Now().After(deadline) {
			t.Fatalf("capture did not settle\n%s", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func requirePublished(t *testing.T, capture, subject, fact, unit, value string) {
	t.Helper()
	want, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatal(value)
	}
	prefix := subject + " " + fact + " "
	found := 0
	for _, line := range strings.Split(capture, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, prefix))
		if len(fields) != 3 || fields[0] != unit {
			t.Fatalf("%s line %q", fact, line)
		}
		exponent, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		got, ok := new(big.Rat).SetString(fields[1] + "e" + strconv.Itoa(exponent))
		if !ok || got.Cmp(want) != 0 {
			t.Fatalf("%s = %s e%d, want %s %s", fact, fields[1], exponent, value, unit)
		}
		found++
	}
	if found != 1 {
		t.Fatalf("%s %s count %d\n%s", subject, fact, found, capture)
	}
}

func waitReceived(t *testing.T, inst *instance, n uint64) {
	t.Helper()
	waitFor(t, "received", func() bool {
		got, _, _ := inst.counts()
		return got >= n
	})
}

func commandScripts() (map[string]replay.Script, []semhost.InstanceConfig) {
	ifaces := []string{"can0", "can1", "can2", "can3", "can4"}
	options := []string{profileInverter, profileMeasurement, profileEvents, profileCommon, profileWatch}
	keys := []string{"inverter", "observer", "events", "commands", "monitor"}
	scripts := map[string]replay.Script{}
	var instances []semhost.InstanceConfig
	for i, iface := range ifaces {
		scripts[iface] = scriptOf(iface, i+1)
		instances = append(instances, hostInstance(keys[i], iface, options[i]))
	}
	return scripts, instances
}

func bindSend(t *testing.T, inst *instance, iface string, index int, clock Clock) (*Path, *replay.Bus) {
	t.Helper()
	bus, err := replay.NewBus(clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := canbus.NewInterfaceIdentity(iface, index)
	if err != nil {
		t.Fatal(err)
	}
	path, err := inst.OpenSend(identity, bus, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(path.Stop)
	return path, bus
}

func appendNew(t *testing.T, b *strings.Builder, name string, bus *replay.Bus, from int) int {
	t.Helper()
	frames := bus.Frames()
	for _, sent := range frames[from:] {
		kind := "standard"
		if sent.Frame.ID().Extended() {
			kind = "extended"
		}
		if _, err := fmt.Fprintf(b, "%s %s %x %s %d\n", name, kind, sent.Frame.ID().Value(), hex.EncodeToString(sent.Frame.Data()), sent.Monotonic); err != nil {
			t.Fatal(err)
		}
	}
	return len(frames)
}

func TestSecondHostCommandsMatchAcrossRuns(t *testing.T) {
	left := commandTrace(t)
	right := commandTrace(t)
	if left != right {
		t.Fatalf("traces differ\n%s\n%s", left, right)
	}
	for _, line := range []string{
		"inverter standard 301 0b16212c37424d58 0\n",
		"inverter standard 301 0b16212c37424d58 1000000000\n",
		"inverter standard 211 001a0b1a0d2d1e00 1000000000\n",
		"events standard 211 011a0b1b0e1e2d00 0\n",
		"events standard 212 0101000000000000 0\n",
		"commands standard 211 001a0b1a0d2d1e00 0\n",
		"commands extended 188af501 ea070b1a0d2d1e07 0\n",
		"commands extended 1c9fdb71 0001a2 0\n",
		"commands extended 1cbfd073 0003e600 0\n",
	} {
		if !strings.Contains(left, line) {
			t.Fatalf("missing %s\n%s", line, left)
		}
	}
	if strings.Count(left, "inverter standard 301 ") != 2 || strings.Contains(left, "observer ") || strings.Contains(left, "monitor ") {
		t.Fatalf("heartbeat on an unselected path\n%s", left)
	}
	if strings.Count(left, "events standard 211 ") != 5 || strings.Count(left, "events wait 1000000000\n") != 4 {
		t.Fatalf("event cadence\n%s", left)
	}
}

func commandTrace(t *testing.T) string {
	t.Helper()
	scripts, instances := commandScripts()
	_, tracked := openHost(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), scripts, instances)
	heart := newGateClock()
	events := &recordingClock{}
	still := Clock{Now: func() time.Duration { return 0 }}
	paths := map[string]*Path{}
	buses := map[string]*replay.Bus{}
	paths["inverter"], buses["inverter"] = bindSend(t, tracked.take("inverter"), "can0", 1, heart.asClock())
	paths["observer"], buses["observer"] = bindSend(t, tracked.take("observer"), "can1", 2, heart.asClock())
	paths["events"], buses["events"] = bindSend(t, tracked.take("events"), "can2", 3, events.asClock())
	paths["commands"], buses["commands"] = bindSend(t, tracked.take("commands"), "can3", 4, still)
	paths["monitor"], buses["monitor"] = bindSend(t, tracked.take("monitor"), "can4", 5, still)
	var b strings.Builder
	traceHeartbeat(t, &b, paths, buses, heart)
	traceEvents(t, &b, paths["events"], buses["events"], events)
	traceCommands(t, &b, paths, buses)
	return b.String()
}

func traceHeartbeat(t *testing.T, b *strings.Builder, paths map[string]*Path, buses map[string]*replay.Bus, clock *gateClock) {
	t.Helper()
	if err := paths["inverter"].PublishDue(); err != nil {
		t.Fatal(err)
	}
	if err := paths["observer"].PublishDue(); err != nil {
		t.Fatal(err)
	}
	n := appendNew(t, b, "inverter", buses["inverter"], 0)
	if n != 1 || len(buses["observer"].Frames()) != 0 {
		t.Fatalf("first due inverter=%d observer=%d", n, len(buses["observer"].Frames()))
	}
	assertFrame(t, buses["inverter"].Frames()[0].Frame, false, 0x301, "0b16212c37424d58")
	if err := paths["inverter"].PublishDue(); err != nil || len(buses["inverter"].Frames()) != 1 {
		t.Fatal("a due check inside the same second sent another heartbeat")
	}
	clock.advance(time.Second)
	if err := paths["inverter"].PublishDue(); err != nil {
		t.Fatal(err)
	}
	n = appendNew(t, b, "inverter", buses["inverter"], n)
	if err := paths["inverter"].SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err != nil {
		t.Fatal(err)
	}
	n = appendNew(t, b, "inverter", buses["inverter"], n)
	if strings.Count(b.String(), "inverter standard 301 ") != 2 {
		t.Fatal("a caller command emitted the heartbeat")
	}
	paths["inverter"].Stop()
	clock.advance(time.Second)
	if err := paths["inverter"].PublishDue(); err != nil || len(buses["inverter"].Frames()) != n {
		t.Fatal("stopped inverter emitted a frame")
	}
}

func traceEvents(t *testing.T, b *strings.Builder, path *Path, bus *replay.Bus, clock *recordingClock) {
	t.Helper()
	if err := path.PublishDue(); err != nil || len(bus.Frames()) != 0 {
		t.Fatal("startup sent a frame")
	}
	event := growattlowvoltage.ClockEvent{FrequencyEnable: true, Year: 26, Month: 11, Day: 27, Hour: 14, Minute: 30, Second: 45}
	if err := path.TriggerClockEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	appendNew(t, b, "events", bus, 0)
	for _, wait := range clock.durations() {
		if wait != time.Second {
			t.Fatalf("wait %s", wait)
		}
		if _, err := fmt.Fprintf(b, "events wait %d\n", wait); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := path.QuerySerial(1); err != nil {
		t.Fatal(err)
	}
	appendNew(t, b, "events", bus, 5)
	before := len(bus.Frames())
	if err := path.SendPCSUndocumented(0x212); err == nil || len(bus.Frames()) != before {
		t.Fatal("undocumented query sent a frame")
	}
	if err := path.TriggerClockEvent(context.Background(), growattlowvoltage.ClockEvent{Year: 19, Month: 1, Day: 1}); err == nil || len(bus.Frames()) != before {
		t.Fatal("out-of-domain event sent a frame")
	}
}

func traceCommands(t *testing.T, b *strings.Builder, paths map[string]*Path, buses map[string]*replay.Bus) {
	t.Helper()
	path, bus := paths["commands"], buses["commands"]
	if err := path.SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err != nil {
		t.Fatal(err)
	}
	n := appendNew(t, b, "commands", bus, 0)
	if err := path.SendPCSClock(pcs.ClockInput{Month: 0, Day: 1}); err == nil || len(bus.Frames()) != n {
		t.Fatal("denied clock sent a frame")
	}
	if err := path.SendLINKClock(link.ClockInput{Destination: 245, Source: 1, Year: 2026, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30, Extra: 7}); err != nil {
		t.Fatal(err)
	}
	n = appendNew(t, b, "commands", bus, n)
	if err := path.SendGreeProperty(0x0b, 0x1C800000, 54, 162); err != nil {
		t.Fatal(err)
	}
	n = appendNew(t, b, "commands", bus, n)
	if err := path.SendGreeProperty(0, 0, 32, 1); err == nil || len(bus.Frames()) != n {
		t.Fatal("unknown property sent a frame")
	}
	if err := bindZone(path, true, false); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), kelvinOf("29615", -2), nil); err != nil {
		t.Fatal(err)
	}
	n = appendNew(t, b, "commands", bus, n)
	if err := path.SendSetTemperature(zoneRef(t), semreg.Quantity{Number: semreg.Decimal{Coefficient: "29615", Exponent10: -2}, Unit: "unit.pascal"}, nil); err == nil || len(bus.Frames()) != n {
		t.Fatal("wrong unit sent a frame")
	}
	monitor, quiet := paths["monitor"], buses["monitor"]
	if err := monitor.PublishDue(); err != nil || len(quiet.Frames()) != 0 {
		t.Fatal("monitor heartbeat")
	}
	if err := monitor.TriggerClockEvent(context.Background(), growattlowvoltage.ClockEvent{}); err == nil || len(quiet.Frames()) != 0 {
		t.Fatal("monitor event sent a frame")
	}
	when := canbusreg.GreeVRFTime{Year: 24, Month: 12, Day: 31, Hour: 23, Minute: 59, Second: 58, Weekday: 7}
	if err := monitor.SendGreeClock("time", when); err == nil || len(quiet.Frames()) != 0 {
		t.Fatal("monitor clock sent a frame")
	}
}
