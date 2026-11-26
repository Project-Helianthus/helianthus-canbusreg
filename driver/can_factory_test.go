package driver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
)

type fakeReceiver struct {
	identity canbus.InterfaceIdentity
	records  chan receiver.Record
	mu       sync.Mutex
	closed   int
}

func newFakeReceiver(t *testing.T, name string, index int) *fakeReceiver {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeReceiver{identity: identity, records: make(chan receiver.Record, 64)}
}

func (r *fakeReceiver) Identity() canbus.InterfaceIdentity { return r.identity }

func (r *fakeReceiver) Receive(ctx context.Context) (receiver.Record, error) {
	select {
	case rec := <-r.records:
		return rec, nil
	case <-ctx.Done():
		return receiver.Record{}, ctx.Err()
	}
}

func (r *fakeReceiver) Close() error {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
	return nil
}

func (r *fakeReceiver) closes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *fakeReceiver) push(sequence uint64, id uint32, extended bool, data ...byte) {
	raw := make([]byte, canbus.SocketCANRecordSize)
	if extended {
		id |= 0x80000000
	}
	binary.NativeEndian.PutUint32(raw[:4], id)
	raw[4] = byte(len(data))
	copy(raw[8:], data)
	r.records <- receiver.Record{Interface: r.identity, Raw: raw, Sequence: sequence, Monotonic: time.Duration(sequence) * time.Millisecond}
}

func config(instance string, transport, options string) semdriver.Config {
	return semdriver.Config{Instance: instance, Enabled: true, Transport: json.RawMessage(transport), Options: json.RawMessage(options)}
}

func factoryWith(r *fakeReceiver, openErr error) *Factory {
	f := NewFactory()
	f.open = func(name string) (receiver.Receiver, error) {
		if openErr != nil {
			return nil, openErr
		}
		return r, nil
	}
	return f
}

func startedInstance(t *testing.T, f *Factory, c semdriver.Config) *instance {
	t.Helper()
	plans, err := f.Plan(c)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	inst, err := f.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Stop(context.Background()) })
	return inst.(*instance)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (i *instance) counts() (received, skipped uint64, latest any) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.received, i.skipped, i.latest
}

func TestPlanOnePlanPerConfiguredInterfaceAndProfile(t *testing.T) {
	f := NewFactory()
	for _, tc := range []struct{ name, key, transport, options string }{
		{"v104", "a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`},
		{"v108", "b", `{"interface":"can1"}`, `{"profile":"growatt-bms-can-v108"}`},
		{"common", "c", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-common"}`},
		{"monitor", "d", `{"interface":"can2"}`, `{"profile":"growatt-bms-can-monitor"}`},
	} {
		plans, err := f.Plan(config(tc.key, tc.transport, tc.options))
		if err != nil || len(plans) != 1 {
			t.Fatalf("%s: %v %v", tc.name, plans, err)
		}
		p := plans[0]
		var tr TransportConfig
		if err := json.Unmarshal([]byte(tc.transport), &tr); err != nil {
			t.Fatal(err)
		}
		if p.Driver != Kind || string(p.Instance) != tc.key || len(p.Transports) != 1 || p.Transports[0].ConfigKey != tr.Interface || p.Transports[0].Provider != transportProvider {
			t.Fatalf("%s: unexpected plan %+v", tc.name, p)
		}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	disabled := config("e", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`)
	disabled.Enabled = false
	if plans, err := f.Plan(disabled); err != nil || len(plans) != 0 {
		t.Fatalf("disabled instance planned: %v %v", plans, err)
	}
	if d := f.Descriptor(); d.Validate() != nil {
		t.Fatal("descriptor invalid")
	}
}

func TestPlanRejectsInvalidConfiguration(t *testing.T) {
	f := NewFactory()
	for name, tc := range map[string]struct{ transport, options, want string }{
		"empty interface":     {`{"interface":""}`, `{"profile":"growatt-bms-can-v104"}`, "invalid CAN interface"},
		"bad interface name":  {`{"interface":"can/0"}`, `{"profile":"growatt-bms-can-v104"}`, "invalid CAN interface"},
		"unknown transport":   {`{"interface":"can0","extra":1}`, `{"profile":"growatt-bms-can-v104"}`, "unknown field"},
		"trailing transport":  {`{"interface":"can0"} {}`, `{"profile":"growatt-bms-can-v104"}`, "trailing"},
		"unknown profile":     {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-v109"}`, "unsupported profile"},
		"missing profile":     {`{"interface":"can0"}`, `{}`, "unsupported profile"},
		"unknown option":      {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104","x":1}`, "unknown field"},
		"unrecognized role":   {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104","v104_role":"bms"}`, "unrecognized v104_role"},
		"role on v108":        {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-v108","v104_role":"inverter"}`, "applies only"},
		"role on common":      {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-common","v104_role":"inverter"}`, "applies only"},
		"selection on v104":   {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104","selection":{"scope":"local"}}`, "applies only"},
		"selection no scope":  {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-common","selection":{"flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`, "explicit scope"},
		"selection bad scope": {`{"interface":"can0"}`, `{"profile":"growatt-bms-can-common","selection":{"scope":"both","flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`, "explicit scope"},
		"empty options":       {`{"interface":"can0"}`, ``, "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.Plan(config("x", tc.transport, tc.options))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := f.Plan(config("", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`)); err == nil {
		t.Fatal("empty instance key planned")
	}
}

func TestConfiguredV104RoleDecodesTypedAndAbsentStaysObserving(t *testing.T) {
	with, err := ParseConfig(json.RawMessage(`{"interface":"can0"}`), json.RawMessage(`{"profile":"growatt-bms-can-v104","v104_role":"inverter"}`))
	if err != nil || with.Options.V104Role != RoleInverter {
		t.Fatalf("%+v %v", with, err)
	}
	without, err := ParseConfig(json.RawMessage(`{"interface":"can0"}`), json.RawMessage(`{"profile":"growatt-bms-can-v104"}`))
	if err != nil || without.Options.V104Role != "" {
		t.Fatalf("%+v %v", without, err)
	}
}

func TestConstructRequiresAPlannedInstance(t *testing.T) {
	f := NewFactory()
	plan := semdriver.InstancePlan{Driver: Kind, Instance: "never"}
	if _, err := f.Construct(context.Background(), plan); err == nil {
		t.Fatal("unplanned instance constructed")
	}
	if _, err := f.Construct(context.Background(), semdriver.InstancePlan{Driver: "other", Instance: "never"}); err == nil {
		t.Fatal("foreign driver constructed")
	}
}

func TestConstructOpensNothingStartOpensOnlyThroughReceiver(t *testing.T) {
	opened := 0
	f := NewFactory()
	f.open = func(string) (receiver.Receiver, error) { opened++; return newFakeReceiver(t, "can0", 3), nil }
	plans, err := f.Plan(config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := f.Construct(context.Background(), plans[0])
	if err != nil || opened != 0 {
		t.Fatalf("construct opened %d: %v", opened, err)
	}
	if err := inst.Start(context.Background(), nil); err != nil || opened != 1 {
		t.Fatalf("start opened %d: %v", opened, err)
	}
	if err := inst.Start(context.Background(), nil); err == nil {
		t.Fatal("second start accepted")
	}
	if rep := inst.Stop(context.Background()); !rep.Confirmed {
		t.Fatalf("stop: %+v", rep)
	}
}

func TestStartOpenFailureIsReportedAndPlatformIsNamed(t *testing.T) {
	boom := errors.New("boom")
	inst := startFailing(t, factoryWith(nil, boom))
	if !errors.Is(inst, boom) {
		t.Fatalf("err = %v", inst)
	}
	if runtime.GOOS == "linux" {
		return
	}
	real := NewFactory()
	err := startFailing(t, real)
	if !errors.Is(err, canbus.ErrUnsupportedPlatform) || !strings.Contains(err.Error(), runtime.GOOS) {
		t.Fatalf("err = %v", err)
	}
}

func startFailing(t *testing.T, f *Factory) error {
	t.Helper()
	plans, err := f.Plan(config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := f.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	err = inst.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("start succeeded")
	}
	if rep := inst.Stop(context.Background()); !rep.Confirmed {
		t.Fatalf("stop of a never-started instance: %+v", rep)
	}
	return err
}

func TestStartRejectsReceiverBoundToAnotherInterface(t *testing.T) {
	r := newFakeReceiver(t, "can9", 9)
	err := startFailing(t, factoryWith(r, nil))
	if !strings.Contains(err.Error(), "can9") || r.closes() != 1 {
		t.Fatalf("err = %v closes = %d", err, r.closes())
	}
}

func TestEachProfileSelectsItsOwnDecoder(t *testing.T) {
	v104 := func(r *fakeReceiver) {
		r.push(1, 0x311, false, 0, 1, 0, 2, 0, 3, 0, 0)
		r.push(2, 0x312, false, 0, 0, 0, 0, 4, 0, 0, 16)
		r.push(3, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	}
	t.Run("v104 assembles the tuple", func(t *testing.T) {
		r := newFakeReceiver(t, "can0", 3)
		inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`))
		v104(r)
		waitFor(t, "tuple", func() bool { _, _, l := inst.counts(); return l != nil })
		_, _, latest := inst.counts()
		if _, ok := latest.(canbusreg.GrowattLowVoltageBMSV104Projection); !ok {
			t.Fatalf("latest = %T", latest)
		}
	})
	t.Run("v108 does not route to v104", func(t *testing.T) {
		r := newFakeReceiver(t, "can0", 3)
		inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v108"}`))
		v104(r)
		waitFor(t, "frames", func() bool { n, _, _ := inst.counts(); return n == 3 })
		if _, _, latest := inst.counts(); latest != nil {
			if _, bad := latest.(canbusreg.GrowattLowVoltageBMSV104Projection); bad {
				t.Fatalf("v108 produced a v104 projection")
			}
		}
		profile, err := canbusreg.NewGrowattBMSProfile(r.identity, canbusreg.BMSRevisionV108)
		if err != nil {
			t.Fatal(err)
		}
		want := profile.Classify(evidenceOf(t, r, 3, 0x313, false, []byte{0, 1, 0, 2, 0, 0, 50, 90}))
		_, _, latest := inst.counts()
		if want.Projection == nil || latest == nil {
			t.Fatalf("v108 retained %v, independent classification %v", latest, want.Projection)
		}
	})
	t.Run("common retains raw only short frame with unknown scope", func(t *testing.T) {
		r := newFakeReceiver(t, "can0", 3)
		inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-common"}`))
		r.push(1, 0x311, false, 1, 2, 3)
		waitFor(t, "frame", func() bool { _, _, l := inst.counts(); return l != nil })
		_, _, latest := inst.counts()
		got, ok := latest.(canbusreg.GrowattLowVoltageBMSCommonProjection)
		if !ok || got.Scope != "unknown" || got.Limits != nil || got.RawEvidence.Frame.PayloadLength() != 3 {
			t.Fatalf("latest = %#v", latest)
		}
	})
	for _, tc := range []struct{ scope, want string }{{"local", "member"}, {"aggregate", "aggregate"}} {
		t.Run("common selection "+tc.scope, func(t *testing.T) {
			r := newFakeReceiver(t, "can0", 3)
			options := `{"profile":"growatt-bms-can-common","selection":{"scope":"` + tc.scope + `","flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`
			inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, options))
			r.push(1, 0x312, false, 0, 0, 0, 0, 4, 0, 0, 16)
			waitFor(t, "frame", func() bool { _, _, l := inst.counts(); return l != nil })
			_, _, latest := inst.counts()
			got := latest.(canbusreg.GrowattLowVoltageBMSCommonProjection)
			if got.Scope != tc.want || got.Status == nil || got.Status.PackCount != 4 {
				t.Fatalf("latest = %#v", got)
			}
		})
	}
	t.Run("monitor decodes its addressed frame", func(t *testing.T) {
		r := newFakeReceiver(t, "can0", 3)
		inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-monitor"}`))
		monitor, _ := canbusreg.NewGrowattMonitorAddressed(r.identity)
		var id uint32
		for _, candidate := range []uint32{0x18010001, 0x18020001, 0x18040001, 0x18050001, 0x08010001} {
			if _, ok := monitor.Apply(evidenceOf(t, r, 1, candidate, true, make([]byte, 8))); ok {
				id = candidate
				break
			}
		}
		if id == 0 {
			t.Skip("no live addressed monitor identifier among the probes")
		}
		r.push(1, id, true, make([]byte, 8)...)
		waitFor(t, "frame", func() bool { _, _, l := inst.counts(); return l != nil })
		if _, _, l := inst.counts(); l == nil {
			t.Fatal("no monitor report")
		} else if _, ok := l.(canbusreg.GrowattMonitorReport); !ok {
			t.Fatalf("latest = %T", l)
		}
	})
}

func evidenceOf(t *testing.T, r *fakeReceiver, sequence uint64, id uint32, extended bool, data []byte) canbusreg.Evidence {
	t.Helper()
	probe := newFakeReceiver(t, r.identity.Name(), r.identity.Index())
	probe.push(sequence, id, extended, data...)
	rec := <-probe.records
	frame, err := canbus.DecodeSocketCANRecord(rec.Raw)
	if err != nil {
		t.Fatal(err)
	}
	e := canbusreg.Evidence{Frame: frame, Interface: rec.Interface, Sequence: rec.Sequence, Monotonic: rec.Monotonic}
	copy(e.RawRecord[:], rec.Raw)
	return e
}

func TestMalformedRecordPublishesNothingAndLoopContinues(t *testing.T) {
	r := newFakeReceiver(t, "can0", 3)
	inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-common"}`))
	r.records <- receiver.Record{Interface: r.identity, Raw: []byte{1, 2, 3}, Sequence: 1}
	r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	waitFor(t, "valid frame", func() bool { n, _, _ := inst.counts(); return n == 1 })
	n, skipped, latest := inst.counts()
	got := latest.(canbusreg.GrowattLowVoltageBMSCommonProjection)
	if n != 1 || skipped != 1 || got.RawEvidence.Sequence != 2 || got.Measurements == nil {
		t.Fatalf("received %d skipped %d latest %#v", n, skipped, got)
	}
}

func TestEvidenceKeepsNativeRecordFields(t *testing.T) {
	r := newFakeReceiver(t, "can0", 3)
	inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-common"}`))
	r.push(42, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	waitFor(t, "frame", func() bool { n, _, _ := inst.counts(); return n == 1 })
	_, _, latest := inst.counts()
	e := latest.(canbusreg.GrowattLowVoltageBMSCommonProjection).RawEvidence
	want := evidenceOf(t, r, 42, 0x313, false, []byte{0, 1, 0, 2, 0, 0, 50, 90})
	if e != want || e.Sequence != 42 || e.Monotonic != 42*time.Millisecond || e.Interface != r.identity {
		t.Fatalf("evidence %+v want %+v", e, want)
	}
}

func TestFenceStopsDecodingAndStopClosesOnce(t *testing.T) {
	r := newFakeReceiver(t, "can0", 3)
	inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-common"}`))
	r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	waitFor(t, "frame", func() bool { n, _, _ := inst.counts(); return n == 1 })
	inst.FenceWithdrawal()
	r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	time.Sleep(20 * time.Millisecond)
	if n, _, _ := inst.counts(); n != 1 {
		t.Fatalf("fenced instance decoded %d frames", n)
	}
	if rep := inst.Stop(context.Background()); !rep.Confirmed || r.closes() != 1 {
		t.Fatalf("stop %+v closes %d", rep, r.closes())
	}
	if rep := inst.Stop(context.Background()); !rep.Confirmed || r.closes() != 1 {
		t.Fatalf("second stop %+v closes %d", rep, r.closes())
	}
}

func TestTwoInstancesOfOneProfileShareNothing(t *testing.T) {
	f := NewFactory()
	rs := map[string]*fakeReceiver{"can0": newFakeReceiver(t, "can0", 3), "can1": newFakeReceiver(t, "can1", 4)}
	f.open = func(name string) (receiver.Receiver, error) { return rs[name], nil }
	var insts []*instance
	for k, name := range []string{"can0", "can1"} {
		plans, err := f.Plan(config(string(rune('a'+k)), `{"interface":"`+name+`"}`, `{"profile":"growatt-bms-can-common"}`))
		if err != nil {
			t.Fatal(err)
		}
		inst, err := f.Construct(context.Background(), plans[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := inst.Start(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		defer inst.Stop(context.Background())
		insts = append(insts, inst.(*instance))
	}
	rs["can0"].push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	waitFor(t, "first", func() bool { n, _, _ := insts[0].counts(); return n == 1 })
	time.Sleep(10 * time.Millisecond)
	if n, _, _ := insts[1].counts(); n != 0 {
		t.Fatalf("second instance saw %d frames of the first interface", n)
	}
}
