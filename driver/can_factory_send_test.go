package driver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/periodic"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/operation"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

// Fields varied below, and the production mutation each case catches:
// profile and role select the heartbeat; interface selects the bus;
// identifier, extended flag, DLC and each payload byte are the frame;
// a half period must not release a one-second wait.

type gateClock struct {
	mu      sync.Mutex
	at      time.Duration
	entered int
	wake    chan struct{}
}

func newGateClock() *gateClock {
	return &gateClock{wake: make(chan struct{})}
}

func (c *gateClock) asClock() Clock { return Clock{Now: c.now, Wait: c.wait} }

func (c *gateClock) now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *gateClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at += d
	close(c.wake)
	c.wake = make(chan struct{})
}

func (c *gateClock) wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	start := c.at
	c.entered++
	c.mu.Unlock()
	for {
		c.mu.Lock()
		due := c.at-start >= d
		wake := c.wake
		c.mu.Unlock()
		if due {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

func (c *gateClock) awaitEntered(t *testing.T, n int) {
	t.Helper()
	waitFor(t, "cadence wait", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.entered >= n
	})
}

type recordingClock struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (c *recordingClock) asClock() Clock {
	return Clock{Now: func() time.Duration { return 0 }, Wait: c.wait}
}

func (c *recordingClock) wait(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	c.mu.Unlock()
	return nil
}

func (c *recordingClock) durations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func liveClock() Clock {
	return Clock{Now: func() time.Duration { return receiver.MonotonicAt(time.Now()) }}
}

type scriptedSender struct {
	inner  periodic.Sender
	before func()
	after  func(periodic.Submission)
}

func (s *scriptedSender) Submit(frame canbus.Frame) (periodic.Submission, error) {
	if s.before != nil {
		s.before()
	}
	sub, err := s.inner.Submit(frame)
	if err == nil && sub.Accepted && s.after != nil {
		s.after(sub)
	}
	return sub, err
}

func openSend(t *testing.T, profile Profile, role V104Role, name string, clock Clock) (*Path, *periodic.Simulated) {
	t.Helper()
	sim := periodic.NewSimulated(clock.Now)
	id, err := canbus.NewInterfaceIdentity(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	path, err := NewPath(InstanceConfig{Interface: name, Options: Options{Profile: profile, V104Role: role}}, id, sim, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(path.Stop)
	return path, sim
}

func openScripted(t *testing.T, name string, clock Clock) (*Path, *periodic.Simulated, *scriptedSender) {
	t.Helper()
	sim := periodic.NewSimulated(clock.Now)
	sender := &scriptedSender{inner: sim}
	id, err := canbus.NewInterfaceIdentity(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	path, err := NewPath(InstanceConfig{Interface: name, Options: Options{Profile: ProfileCommon}}, id, sender, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(path.Stop)
	return path, sim, sender
}

func mustHex(t *testing.T, payload string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertFrame(t *testing.T, frame canbus.Frame, extended bool, id uint32, payload string) {
	t.Helper()
	raw := mustHex(t, payload)
	if frame.ID().Extended() != extended || frame.ID().Value() != id || int(frame.DLC()) != len(raw) || int(frame.RawDLC()) != len(raw) || !bytes.Equal(frame.Data(), raw) {
		t.Fatalf("frame id=%#x extended=%v dlc=%d rawDLC=%d data=%x; want %#x extended=%v %s", frame.ID().Value(), frame.ID().Extended(), frame.DLC(), frame.RawDLC(), frame.Data(), id, extended, payload)
	}
}

func standardFrame(t *testing.T, id uint32, payload string) canbus.Frame {
	t.Helper()
	identifier, err := canbus.NewStandardID(id)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(identifier, mustHex(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func extendedFrame(t *testing.T, id uint32, payload string) canbus.Frame {
	t.Helper()
	identifier, err := canbus.NewExtendedID(id)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(identifier, mustHex(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func countStandard(sim *periodic.Simulated, id uint32) int {
	n := 0
	for _, sent := range sim.Frames() {
		if !sent.Frame.ID().Extended() && sent.Frame.ID().Value() == id {
			n++
		}
	}
	return n
}

func TestInverterHeartbeatOnlyOnTheSelectedInterface(t *testing.T) {
	clock := newGateClock()
	inverter, selected := openSend(t, ProfileV104, RoleInverter, "can0", clock.asClock())
	quiet, other := openSend(t, ProfileV104, "", "can1", clock.asClock())
	if len(selected.Frames()) != 0 || len(other.Frames()) != 0 {
		t.Fatal("construction sent a frame")
	}
	if err := inverter.PublishDue(); err != nil {
		t.Fatal(err)
	}
	if err := quiet.PublishDue(); err != nil {
		t.Fatal(err)
	}
	frames := selected.Frames()
	if len(frames) != 1 || len(other.Frames()) != 0 {
		t.Fatalf("selected=%d other=%d", len(frames), len(other.Frames()))
	}
	assertFrame(t, frames[0].Frame, false, 0x301, "0b16212c37424d58")
	if err := inverter.PublishDue(); err != nil {
		t.Fatal(err)
	}
	clock.advance(500 * time.Millisecond)
	if err := inverter.PublishDue(); err != nil {
		t.Fatal(err)
	}
	if len(selected.Frames()) != 1 {
		t.Fatal("half a period sent another heartbeat")
	}
	clock.advance(500 * time.Millisecond)
	if err := inverter.PublishDue(); err != nil {
		t.Fatal(err)
	}
	if len(selected.Frames()) != 2 || countStandard(selected, 0x301) != 2 {
		t.Fatalf("frames after one second: %d", len(selected.Frames()))
	}
	assertFrame(t, selected.Frames()[1].Frame, false, 0x301, "0b16212c37424d58")
	if err := inverter.SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err != nil {
		t.Fatal(err)
	}
	if countStandard(selected, 0x301) != 2 {
		t.Fatal("a caller command emitted the heartbeat")
	}
	inverter.Stop()
	clock.advance(time.Second)
	if err := inverter.PublishDue(); err != nil {
		t.Fatal(err)
	}
	if countStandard(selected, 0x301) != 2 || len(other.Frames()) != 0 {
		t.Fatal("stopped or unselected interface emitted the heartbeat")
	}
}

func TestOtherProfilesAndStoppedPathsSendNoHeartbeat(t *testing.T) {
	for _, profile := range []Profile{ProfileCommon, ProfileV108, ProfileMonitor} {
		clock := &recordingClock{}
		path, sim := openSend(t, profile, "", "can0", clock.asClock())
		if err := path.PublishDue(); err != nil {
			t.Fatal(err)
		}
		if err := path.SendPCSUndocumented(0x301); err == nil || len(sim.Frames()) != 0 {
			t.Fatalf("%s heartbeat: %d frames, err=%v", profile, len(sim.Frames()), err)
		}
		if err := path.TriggerClockEvent(context.Background(), growattlowvoltage.ClockEvent{}); err == nil || len(sim.Frames()) != 0 {
			t.Fatalf("%s event: %d", profile, len(sim.Frames()))
		}
	}
	id, err := canbus.NewInterfaceIdentity("can1", 1)
	if err != nil {
		t.Fatal(err)
	}
	sim := periodic.NewSimulated(func() time.Duration { return 0 })
	if _, err := NewPath(InstanceConfig{Interface: "can0", Options: Options{Profile: ProfileV104, V104Role: RoleInverter}}, id, sim, Clock{Now: func() time.Duration { return 0 }}); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("mismatched interface opened a path")
	}
	if _, err := NewPath(InstanceConfig{Interface: "can0", Options: Options{Profile: ProfileV108}}, idOf(t, "can0"), sim, Clock{Now: func() time.Duration { return 0 }}); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("missing wait opened a cadence")
	}
}

func idOf(t *testing.T, name string) canbus.InterfaceIdentity {
	t.Helper()
	id, err := canbus.NewInterfaceIdentity(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestClockEventCadenceWaitsTheDocumentedPeriod(t *testing.T) {
	clock := newGateClock()
	path, sim := openSend(t, ProfileV108, "", "can0", clock.asClock())
	if err := path.PublishDue(); err != nil || len(sim.Frames()) != 0 {
		t.Fatal("startup sent a frame")
	}
	event := growattlowvoltage.ClockEvent{FrequencyEnable: true, Year: 26, Month: 11, Day: 27, Hour: 14, Minute: 30, Second: 45}
	errc := make(chan error, 1)
	go func() { errc <- path.TriggerClockEvent(context.Background(), event) }()
	clock.awaitEntered(t, 1)
	if len(sim.Frames()) != 1 {
		t.Fatalf("first send = %d", len(sim.Frames()))
	}
	assertFrame(t, sim.Frames()[0].Frame, false, 0x211, "011a0b1b0e1e2d00")
	for next := 2; next <= 5; next++ {
		clock.advance(500 * time.Millisecond)
		if len(sim.Frames()) != next-1 {
			t.Fatalf("half period released send %d", next)
		}
		clock.advance(500 * time.Millisecond)
		if next < 5 {
			clock.awaitEntered(t, next)
		} else {
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
		}
		if len(sim.Frames()) != next {
			t.Fatalf("send %d, frames %d", next, len(sim.Frames()))
		}
		assertFrame(t, sim.Frames()[next-1].Frame, false, 0x211, "011a0b1b0e1e2d00")
	}
}

func TestClockEventAndHourBoundary(t *testing.T) {
	clock := &recordingClock{}
	path, sim := openSend(t, ProfileV108, "", "can0", clock.asClock())
	event := growattlowvoltage.ClockEvent{FrequencyEnable: true, Year: 26, Month: 11, Day: 27, Hour: 14, Minute: 30, Second: 45}
	same := time.Date(2026, 11, 27, 14, 30, 0, 0, time.UTC)
	if err := path.ObserveHour(context.Background(), same, same.Add(20*time.Minute), event); err != nil || len(sim.Frames()) != 0 {
		t.Fatal("same hour sent a frame")
	}
	if err := path.ObserveHour(context.Background(), same, same.Add(time.Hour), event); err != nil {
		t.Fatal(err)
	}
	if len(sim.Frames()) != 5 {
		t.Fatalf("hour boundary frames = %d", len(sim.Frames()))
	}
	for _, got := range clock.durations() {
		if got != time.Second {
			t.Fatalf("wait = %s", got)
		}
	}
	if len(clock.durations()) != 4 {
		t.Fatalf("waits = %d", len(clock.durations()))
	}
	denied, quiet := openSend(t, ProfileV108, "", "can1", clock.asClock())
	if err := denied.TriggerClockEvent(context.Background(), growattlowvoltage.ClockEvent{Year: 19, Month: 1, Day: 1}); err == nil || len(quiet.Frames()) != 0 {
		t.Fatal("out-of-domain event sent a frame")
	}
	other, none := openSend(t, ProfileCommon, "", "can2", liveClock())
	if err := other.ObserveHour(context.Background(), same, same.Add(time.Hour), event); err == nil || len(none.Frames()) != 0 {
		t.Fatal("another profile sent the event")
	}
}

func TestSerialQueryCorrespondence(t *testing.T) {
	// Battery id, identifier, extended flag, frame number and serial bytes are varied.
	// A mutation of any of those fields, or offering oldest-first, fails one case.
	clock := &recordingClock{}
	path, sim := openSend(t, ProfileV108, "", "can0", clock.asClock())
	first, err := path.QuerySerial(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sim.Frames()) != 1 {
		t.Fatal(len(sim.Frames()))
	}
	assertFrame(t, sim.Frames()[0].Frame, false, 0x212, "0101000000000000")
	if got := path.OfferFrame(standardFrame(t, 0x324, "0002414243444546")); got.Accepted || first.Completed() {
		t.Fatal("wrong battery was accepted")
	}
	if got := path.OfferFrame(extendedFrame(t, 0x324, "0001414243444546")); got.Accepted || first.Completed() {
		t.Fatal("extended frame was accepted")
	}
	if got := path.OfferFrame(standardFrame(t, 0x323, "0001414243444546")); got.Accepted {
		t.Fatal("wrong identifier was accepted")
	}
	if got := path.OfferFrame(standardFrame(t, 0x324, "0101414243444546")); got.Accepted || first.Completed() {
		t.Fatal("continuation before frame zero was accepted")
	}
	got := path.OfferFrame(standardFrame(t, 0x324, "0001414243444546"))
	if !got.SerialAccepted || !got.Completed || got.BatteryID != 1 || hex.EncodeToString(got.Serial) != "414243444546" {
		t.Fatalf("frame zero: %+v", got)
	}
	got = path.OfferFrame(standardFrame(t, 0x324, "0147484950515253"))
	if !got.Completed || hex.EncodeToString(got.Serial) != "41424344454647484950515253" {
		t.Fatalf("continuation: %x", got.Serial)
	}
	if got = path.OfferFrame(standardFrame(t, 0x324, "0001414243444546")); got.Accepted || hex.EncodeToString(first.SerialBytes()) != "41424344454647484950515253" {
		t.Fatal("second frame zero changed the exchange")
	}
	zero, err := path.QuerySerial(0)
	if err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[1].Frame, false, 0x212, "0100000000000000")
	second, err := path.QuerySerial(2)
	if err != nil {
		t.Fatal(err)
	}
	if got = path.OfferFrame(standardFrame(t, 0x324, "0000414243444546")); !got.SerialAccepted || !zero.Completed() || second.Completed() {
		t.Fatal("newest query did not win the matching frame")
	}
	older, err := path.QuerySerial(5)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := path.QuerySerial(5)
	if err != nil {
		t.Fatal(err)
	}
	if got = path.OfferFrame(standardFrame(t, 0x324, "0005414243444546")); !newer.Completed() || older.Completed() {
		t.Fatal("same-battery frame was not kept by the newest incomplete exchange")
	}
	plain, none := openSend(t, ProfileV104, "", "can1", liveClock())
	if _, err := plain.QuerySerial(1); err == nil || len(none.Frames()) != 0 {
		t.Fatal("another profile sent a serial query")
	}
}

func TestHistoryQueryCorrespondence(t *testing.T) {
	// Battery id, identifier, frame number and the first history byte are varied.
	clock := &recordingClock{}
	path, sim := openSend(t, ProfileV108, "", "can0", clock.asClock())
	query, err := path.QueryHistory(1)
	if err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[0].Frame, false, 0x212, "0201000000000000")
	if got := path.OfferFrame(standardFrame(t, 0x325, "0101a1a2a3a4a5a6")); got.Accepted || query.Completed() {
		t.Fatal("other frame number was accepted")
	}
	if got := path.OfferFrame(standardFrame(t, 0x325, "0002a1a2a3a4a5a6")); got.Accepted || got.BodyState != "pending/raw" {
		t.Fatalf("unmatched history: %+v", got)
	}
	got := path.OfferFrame(standardFrame(t, 0x325, "0001a1a2a3a4a5a6"))
	if !got.HistoryAccepted || !got.Completed || got.BodyState != "pending/raw" || hex.EncodeToString(got.HistoryBody) != "a1a2a3a4a5a6" {
		t.Fatalf("history: %+v", got)
	}
	if got = path.OfferFrame(standardFrame(t, 0x325, "0001000000000000")); got.Accepted || query.Completed() != true || hex.EncodeToString(query.Body()) != "a1a2a3a4a5a6" {
		t.Fatal("refused offer cleared the history")
	}
	other, err := path.QueryHistory(9)
	if err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[1].Frame, false, 0x212, "0209000000000000")
	got = path.OfferFrame(standardFrame(t, 0x325, "0009000000000000"))
	if !other.Completed() || hex.EncodeToString(got.HistoryBody) != "000000000000" || query.Completed() != true {
		t.Fatal("history byte or battery was not distinguished")
	}
}

func TestPCSAndLinkCommandFrames(t *testing.T) {
	path, sim := openSend(t, ProfileCommon, "", "can0", liveClock())
	if err := path.SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[0].Frame, false, 0x211, "001a0b1a0d2d1e00")
	if err := path.SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 0, Day: 1}); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("denied clock sent a frame")
	}
	if err := path.SendPCSDiscovery(pcs.DiscoveryControlInput{Phase: 3, Parameter: 9}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[1].Frame, false, 0x6F0, "0309000000000000")
	before := len(sim.Frames())
	for _, id := range []uint32{0x212, 0x37F, 0x301} {
		if err := path.SendPCSUndocumented(id); err == nil || len(sim.Frames()) != before {
			t.Fatalf("undocumented %#x sent %d", id, len(sim.Frames()))
		}
	}
	if err := path.SendLINKClock(link.ClockInput{Destination: 245, Source: 1, Year: 2026, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30, Extra: 7}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[2].Frame, true, 0x188AF501, "ea070b1a0d2d1e07")
	v108, bus := openSend(t, ProfileV108, "", "can1", (&recordingClock{}).asClock())
	if err := v108.SendPCSUndocumented(0x212); err == nil || len(bus.Frames()) != 0 {
		t.Fatal("PCS query sent a frame")
	}
}

func TestExtendedRowsFollowTheStatedContract(t *testing.T) {
	path, sim := openSend(t, ProfileCommon, "", "can0", liveClock())
	legacy := pcs.ContractLegacyExtended19
	if err := path.SendPCSExtendedTime(legacy, pcs.ExtendedTimeInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[0].Frame, true, 0x190BDFF2, "1a0b1a0d2d1e0000")
	if err := path.SendPCSStackState(legacy, pcs.StackStateInput{CurrentCode: 0x0403, StateOfCharge: 0x0605}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[1].Frame, true, 0x191ADFF2, "0000030405060000")
	if err := path.SendPCSSecondaryQuantity(legacy, pcs.SecondaryQuantityInput{Percentage: 0x0201, ByteFour: 0x09}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[2].Frame, true, 0x191CDFF2, "0000010209000000")
	if err := path.SendPCSUpdateCoordination(legacy, pcs.UpdateCoordinationInput{Parameter: 4}); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[3].Frame, true, 0x191EDFF2, "0400000000000000")
	if err := path.SendPCSExtendedTime(pcs.ContractRelease11, pcs.ExtendedTimeInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err == nil || len(sim.Frames()) != 4 {
		t.Fatal("refused contract sent a frame")
	}
}

func TestGreeCommandsAndSetTemperature(t *testing.T) {
	path, sim := openSend(t, ProfileCommon, "", "can0", liveClock())
	if err := path.SendGreeProperty(0x0b, 0x1C800000, 54, 162); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[0].Frame, true, 480238449, "0001a2")
	if err := path.SendGreeProperty(0, 0, 32, 1); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("unknown property sent a frame")
	}
	if err := path.SendGreeProperty(0x0b, 0x1C800000, 128, 1); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("unit outside the field sent a frame")
	}
	when := canbusreg.GreeVRFTime{Year: 24, Month: 12, Day: 31, Hour: 23, Minute: 59, Second: 58, Weekday: 7}
	if err := path.SendGreeClock("time", when); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[1].Frame, true, 0x04820000, "2412312359580702")
	when.Weekday = 6
	if err := path.SendGreeClock("clock", when); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[2].Frame, true, 0x04820084, "2412312359580602")
	when.Month = 100
	if err := path.SendGreeClock("time", when); err == nil || len(sim.Frames()) != 3 {
		t.Fatal("denied clock sent a frame")
	}
	if err := bindZone(path, true, false); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), kelvinOf("29615", -2), nil); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[3].Frame, true, 0x1cbfd073, "0003e600")
	if err := bindZone(path, false, true); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), kelvinOf("29615", -2), nil); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[4].Frame, true, 0x1cbfd0f3, "0003e600")
	if err := bindZone(path, true, true); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), kelvinOf("29615", -2), nil); err == nil || len(sim.Frames()) != 5 {
		t.Fatal("two owners sent a frame")
	}
	if err := bindZone(path, false, false); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), kelvinOf("29615", -2), nil); err == nil || len(sim.Frames()) != 5 {
		t.Fatal("no owner sent a frame")
	}
	if err := bindZone(path, true, false); err != nil {
		t.Fatal(err)
	}
	if err := path.SendSetTemperature(zoneRef(t), semreg.Quantity{Number: semreg.Decimal{Coefficient: "29615", Exponent10: -2}, Unit: "unit.pascal"}, nil); err == nil || len(sim.Frames()) != 5 {
		t.Fatal("wrong unit sent a frame")
	}
}

func kelvinOf(coefficient string, exponent int32) semreg.Quantity {
	return semreg.Quantity{Number: semreg.Decimal{Coefficient: coefficient, Exponent10: exponent}, Unit: "unit.kelvin"}
}

func zoneRef(t *testing.T) semdriver.NodeRef {
	t.Helper()
	ref, err := ZoneKey(testBridge, "inst-gree", "zone-a").Ref()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func bindZone(path *Path, first, second bool) error {
	units := GreeUnits{
		{Anchor: "indoor-a", Unit: 0x20, Zones: []GreeZoneRelation{{Zone: "zone-a", Basis: "configured", SettingOwner: first}}},
		{Anchor: "indoor-b", Unit: 0x21, Zones: []GreeZoneRelation{{Zone: "zone-a", Basis: "configured", SettingOwner: second}}},
	}
	classes := map[string]canbusreg.GreeVRFDeviceClass{
		"indoor-a": canbusreg.GreeVRFIndoorUnit,
		"indoor-b": canbusreg.GreeVRFIndoorUnit,
	}
	return path.BindGree(testBridge, "inst-gree", units, classes)
}

func TestSynchronousSettingReadback(t *testing.T) {
	// Deadline, receipt order, generation, unit and raw value are varied.
	// Accepting a pre-submit, old-generation, wrong-unit or silent report fails.
	path, sim, sender := openScripted(t, "can0", liveClock())
	if _, err := path.ExecuteGreeSetting(context.Background(), time.Time{}, canbusreg.GreeVRFIndoorUnit, 0x20, 0x73, 0, 230); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("missing deadline sent a frame")
	}
	if _, err := path.ExecuteGreeSetting(context.Background(), time.Now().Add(-time.Second), canbusreg.GreeVRFIndoorUnit, 0x20, 0x73, 0, 230); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("expired deadline sent a frame")
	}
	var posted time.Duration
	sender.before = func() {
		at := receiver.MonotonicAt(time.Now())
		if at > 0 {
			at--
		}
		path.OfferGreeReport(settingReport(t, path.identity, 0x1effd073, []byte{0, 0xe6, 0}, sendGeneration, at))
	}
	sender.after = func(sub periodic.Submission) {
		posted = sub.Monotonic + 1
		path.OfferGreeReport(settingReport(t, path.identity, 0x1effd073, []byte{0, 0xe6, 0}, sendGeneration, posted))
	}
	out, err := path.ExecuteGreeSetting(context.Background(), time.Now().Add(2*time.Second), canbusreg.GreeVRFIndoorUnit, 0x20, 0x73, 0, 230)
	if err != nil || out.Status != canbusreg.GreeVRFReported || !out.Submission.Accepted || out.Report == nil || out.Report.Point.Received != posted || len(sim.Frames()) != 1 {
		t.Fatalf("reported %+v err=%v frames=%d posted=%s", out, err, len(sim.Frames()), posted)
	}
	assertFrame(t, sim.Frames()[0].Frame, true, 0x1cbfd073, "0003e600")
	sender.before, sender.after = nil, nil
	for _, tc := range []struct {
		name string
		id   uint32
		gen  uint64
		data []byte
	}{
		{"silence", 0, 0, nil},
		{"wrong unit", 0x1effd0f3, sendGeneration, []byte{0, 0xe6, 0}},
		{"old generation", 0x1effd073, sendGeneration + 1, []byte{0, 0xe6, 0}},
		{"other value", 0x1effd073, sendGeneration, []byte{0, 0xe7, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(sim.Frames())
			sender.after = nil
			if tc.data != nil {
				sender.after = func(sub periodic.Submission) {
					path.OfferGreeReport(settingReport(t, path.identity, tc.id, tc.data, tc.gen, sub.Monotonic+1))
				}
			}
			deadline := time.Now().Add(120 * time.Millisecond)
			got, callErr := path.ExecuteGreeSetting(context.Background(), deadline, canbusreg.GreeVRFIndoorUnit, 0x20, 0x73, 0, 230)
			if callErr != nil || got.Status != canbusreg.GreeVRFNotReported || !got.Submission.Accepted || got.Report != nil || len(sim.Frames()) != before+1 || time.Now().Before(deadline) {
				t.Fatalf("%+v err=%v frames=%d", got, callErr, len(sim.Frames()))
			}
		})
	}
}

func settingReport(t *testing.T, iface canbus.InterfaceIdentity, id uint32, payload []byte, generation uint64, at time.Duration) canbusreg.GreeVRFSettingReport {
	t.Helper()
	frame := extendedFrame(t, id, hex.EncodeToString(payload))
	ev := canbusreg.Evidence{Frame: frame, Interface: iface, Sequence: 1, Monotonic: at}
	decoded := canbusreg.DecodeGreeVRFSettingReports(canbusreg.GreeVRFObservation{Generation: generation, Evidence: ev})
	if len(decoded) != 1 {
		t.Fatalf("decoded %d reports for %#x", len(decoded), id)
	}
	decoded[0].Generation = generation
	decoded[0].Point.Received = at
	return decoded[0]
}

type methodBook struct {
	list []recordedMethod
}

type recordedMethod struct {
	spec semdriver.MethodSpec
	call semdriver.NativeMethod
}

func (b *methodBook) Method(_ semdriver.NodeRef, spec semdriver.MethodSpec, call semdriver.NativeMethod) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	b.list = append(b.list, recordedMethod{spec, call})
	return nil
}

func (b *methodBook) Node(semdriver.NodeSpec) (semdriver.NodeRef, error) {
	return semdriver.NodeRef{}, nil
}
func (b *methodBook) ReadMember(semdriver.NodeRef, semdriver.MemberSpec, semdriver.NativeReader) error {
	return nil
}
func (b *methodBook) View(semdriver.ViewSpec) error { return nil }

func (b *methodBook) find(local string) recordedMethod {
	for _, method := range b.list {
		if string(method.spec.Local) == local {
			return method
		}
	}
	return recordedMethod{}
}

func TestNativeCommandMethods(t *testing.T) {
	clock := &recordingClock{}
	path, sim := openSend(t, ProfileV108, "", "can0", clock.asClock())
	book := &methodBook{}
	node, err := InterfaceKey(testBridge, "inst-send", "can0").Ref()
	if err != nil {
		t.Fatal(err)
	}
	if err := path.RegisterMethods(book, node); err != nil || len(sim.Frames()) != 0 {
		t.Fatal(err)
	}
	event := book.find(growattlowvoltage.ClockEventRowID)
	if event.call == nil || event.spec.Schema != schemaV108 || event.spec.Scope != scopeNative {
		t.Fatal("event method was not registered")
	}
	body := []byte(`{"frequency_enable":true,"year":26,"month":11,"day":27,"hour":14,"minute":30,"second":45,"clear_fault_enable":false}`)
	raw, err := event.call(context.Background(), body)
	if err != nil || len(sim.Frames()) != 5 || string(raw) != `{"accepted":true}` {
		t.Fatalf("event result %s err=%v frames=%d", raw, err, len(sim.Frames()))
	}
	if _, err := event.call(context.Background(), []byte(`{"year":26,"month":11,"extra":1}`)); err == nil || len(sim.Frames()) != 5 {
		t.Fatal("unknown field sent a frame")
	}
	serial := book.find(growattlowvoltage.SerialQueryRowID)
	if _, err := serial.call(context.Background(), []byte(`{"battery_id":0}`)); err != nil {
		t.Fatal(err)
	}
	assertFrame(t, sim.Frames()[5].Frame, false, 0x212, "0100000000000000")
	if _, err := serial.call(context.Background(), []byte(`{}`)); err == nil || len(sim.Frames()) != 6 {
		t.Fatal("missing battery id sent a query")
	}
	monitor, quiet := openSend(t, ProfileMonitor, "", "can1", liveClock())
	empty := &methodBook{}
	if err := monitor.RegisterMethods(empty, node); err != nil || len(empty.list) != 0 || len(quiet.Frames()) != 0 {
		t.Fatal("monitor registered a method")
	}
}

type capturedRegistrar struct {
	exec semdriver.Executor
}

func (r *capturedRegistrar) RegisterExecutor(_ semdriver.SettingRoute, exec semdriver.Executor) (func(), error) {
	r.exec = exec
	return func() { r.exec = nil }, nil
}

func TestSetTemperatureAdmission(t *testing.T) {
	// Operation, generation, argument field, owner and limits are varied.
	path, sim := openSend(t, ProfileCommon, "", "can0", liveClock())
	if err := bindZone(path, true, false); err != nil {
		t.Fatal(err)
	}
	producer, err := GreeEquipmentKey(testBridge, "inst-gree", "indoor-a").Ref()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte{8})
	limits := &semdriver.ValueLimits{
		Min: &semreg.Quantity{Number: semreg.Decimal{Coefficient: "28", Exponent10: 1}, Unit: "unit.kelvin"},
		Max: &semreg.Quantity{Number: semreg.Decimal{Coefficient: "303"}, Unit: "unit.kelvin"}, Origin: semdriver.LimitDeviceQuery,
		Evidence: []semreg.EvidenceRef{{Owner: "driver.canbusreg", Kind: "evidence.synthetic.frame", Digest: semreg.Digest(fmt.Sprintf("sha256:%x", sum)), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}},
	}
	reg := &capturedRegistrar{}
	if _, err := path.RegisterSetTemperature(reg, semdriver.SettingRoute{
		For: semdriver.Element{Subject: zoneRef(t)}, Owner: semdriver.SourceRef{Producer: producer}, Limits: limits,
	}); err != nil {
		t.Fatal(err)
	}
	qty := kelvinOf("29615", -2)
	out, err := reg.exec.Execute(context.Background(), temperatureDispatch("1", setTemperatureID, setpointField, &qty))
	if err != nil || out.Outcome != operation.OutcomeAcknowledgedUnverified || len(sim.Frames()) != 1 {
		t.Fatalf("%+v %v frames=%d", out, err, len(sim.Frames()))
	}
	assertFrame(t, sim.Frames()[0].Frame, true, 0x1cbfd073, "0003e600")
	denied := []semdriver.Dispatch{
		temperatureDispatch("9", setTemperatureID, setpointField, &qty),
		temperatureDispatch("1", "thermal.operation.other", setpointField, &qty),
		temperatureDispatch("1", setTemperatureID, "thermal.setpoint.other", &qty),
	}
	for _, call := range denied {
		if _, err := reg.exec.Execute(context.Background(), call); err == nil || len(sim.Frames()) != 1 {
			t.Fatal("denied admission sent a frame")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.exec.Execute(ctx, temperatureDispatch("1", setTemperatureID, setpointField, &qty)); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("cancelled call sent a frame")
	}
	low := kelvinOf("27995", -2)
	if _, err := reg.exec.Execute(context.Background(), temperatureDispatch("1", setTemperatureID, setpointField, &low)); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("limit sent a frame")
	}
	if err := bindZone(path, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.exec.Execute(context.Background(), temperatureDispatch("1", setTemperatureID, setpointField, &qty)); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("removed owner sent a frame")
	}
	if err := bindZone(path, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.exec.Execute(context.Background(), temperatureDispatch("1", setTemperatureID, setpointField, &qty)); err == nil || len(sim.Frames()) != 1 {
		t.Fatal("replacement owner sent a frame")
	}
}

func temperatureDispatch(generation string, operationID semreg.DefinitionID, field semreg.DefinitionID, qty *semreg.Quantity) semdriver.Dispatch {
	return semdriver.Dispatch{
		Guard: operation.GuardClaims{DriverGeneration: semreg.Uint64(generation)},
		Request: semdriver.OperationRequest{Operation: semreg.DefinitionRef{ID: operationID}, Arguments: []semreg.TypedField{{
			ID: field, Value: semreg.Value{Kind: semreg.ValueQuantity, Quantity: qty},
		}}},
	}
}

func TestDeniedInvocationSendsNothing(t *testing.T) {
	path, sim := openSend(t, ProfileMonitor, "", "can0", liveClock())
	if err := path.SendPCSClock(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("monitor sent a frame")
	}
	if err := path.SendGreeClock("time", canbusreg.GreeVRFTime{Year: 24, Month: 12, Day: 31, Hour: 23, Minute: 59, Second: 58, Weekday: 7}); err == nil || len(sim.Frames()) != 0 {
		t.Fatal("monitor sent a clock")
	}
	common, bus := openSend(t, ProfileCommon, "", "can1", liveClock())
	common.Stop()
	if err := common.SendLINKClock(link.ClockInput{Destination: 245, Source: 1, Year: 2026, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30, Extra: 7}); err == nil || len(bus.Frames()) != 0 {
		t.Fatal("stopped path sent a frame")
	}
}
