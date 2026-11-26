package growattlowvoltage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type clockVectors struct {
	Clock []struct {
		Name       string           `json:"name"`
		Args       map[string]uint8 `json:"args"`
		Identifier string           `json:"identifier"`
		Payload    string           `json:"payload"`
	} `json:"clock"`
	Refused []struct {
		Name string           `json:"name"`
		Args map[string]uint8 `json:"args"`
	} `json:"clock_refused"`
}

func loadVectors(t *testing.T) clockVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/v108_event.json")
	if err != nil {
		t.Fatal(err)
	}
	var v clockVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func eventFrom(a map[string]uint8) ClockEvent {
	return ClockEvent{
		FrequencyEnable: a["frequency_enable"] == 1, Year: a["year"], Month: a["month"], Day: a["day"],
		Hour: a["hour"], Minute: a["minute"], Second: a["second"], ClearFaultEnable: a["clear_fault_enable"] == 1,
	}
}

var v108 = Selection{Profile: Profile}

func TestV108ClockFrameEncodesDocumentedBytes(t *testing.T) {
	for _, c := range loadVectors(t).Clock {
		frame, err := EncodeClockEvent(eventFrom(c.Args))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(c.Identifier, "0x"), 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := hex.DecodeString(c.Payload)
		if frame.ID().Value() != uint32(id) || frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 || !bytes.Equal(frame.Data(), want) {
			t.Errorf("%s: got id %#x ext=%v dlc %d data %x, want standard %s dlc 8 %x", c.Name, frame.ID().Value(), frame.ID().Extended(), frame.DLC(), frame.Data(), c.Identifier, want)
		}
	}
	for _, c := range loadVectors(t).Refused {
		if _, err := EncodeClockEvent(eventFrom(c.Args)); err == nil {
			t.Errorf("%s: want refusal", c.Name)
		}
	}
}

type recorder struct {
	log    []string
	frames []canbus.Frame
	waits  []time.Duration
}

func (r *recorder) send(f canbus.Frame) error {
	r.log = append(r.log, "send")
	r.frames = append(r.frames, f)
	return nil
}

func (r *recorder) wait(_ context.Context, d time.Duration) error {
	r.log = append(r.log, "wait")
	r.waits = append(r.waits, d)
	return nil
}

func TestV108EventDispatchSendsFiveFramesAtOneSecondAfterTrigger(t *testing.T) {
	r := &recorder{}
	d, err := NewDispatcher(v108, r.send, r.wait)
	if err != nil {
		t.Fatal(err)
	}
	in := ClockEvent{FrequencyEnable: true, Year: 26, Month: 11, Day: 27, Hour: 14, Minute: 30, Second: 45}
	if err := d.Trigger(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(r.log, ","), "send,wait,send,wait,send,wait,send,wait,send"; got != want {
		t.Fatalf("sequence %s, want %s", got, want)
	}
	want, _ := hex.DecodeString("011a0b1b0e1e2d00")
	for i, f := range r.frames {
		if f.ID().Value() != 0x211 || !bytes.Equal(f.Data(), want) {
			t.Errorf("send %d: %#x %x", i+1, f.ID().Value(), f.Data())
		}
	}
	for i, w := range r.waits {
		if w != time.Second {
			t.Errorf("wait %d: %v, want 1s", i+1, w)
		}
	}

	bad := &recorder{}
	d, _ = NewDispatcher(v108, bad.send, bad.wait)
	if err := d.Trigger(context.Background(), ClockEvent{Year: 19, Month: 1, Day: 1}); err == nil || len(bad.frames) != 0 {
		t.Fatalf("out-of-domain trigger: err %v, %d frames", err, len(bad.frames))
	}

	failing := &recorder{}
	count := 0
	d, _ = NewDispatcher(v108, func(f canbus.Frame) error {
		count++
		if count == 2 {
			return errors.New("bus refused")
		}
		return failing.send(f)
	}, failing.wait)
	if err := d.Trigger(context.Background(), in); err == nil || count != 2 {
		t.Fatalf("send failure: err %v after %d attempts, want stop with no retry", err, count)
	}
}

func TestV108ClockSyncTriggersAtTheHour(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 11, 26, h, m, s, 0, time.UTC) }
	cases := []struct {
		name          string
		previous, now time.Time
		want          bool
	}{
		{"last second before the hour", at(13, 59, 58), at(13, 59, 59), false},
		{"crossing the hour", at(13, 59, 59), at(14, 0, 0), true},
		{"within the same hour after the boundary", at(14, 0, 0), at(14, 30, 0), false},
		{"first observation after a long gap", at(9, 10, 0), at(14, 10, 0), true},
		{"next day same hour", at(14, 10, 0), at(14, 10, 0).AddDate(0, 0, 1), true},
	}
	for _, c := range cases {
		if got := ClockSyncDue(c.previous, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestV108SelectionAloneGeneratesNoTraffic(t *testing.T) {
	r := &recorder{}
	d, err := NewDispatcher(v108, r.send, r.wait)
	if err != nil || d == nil {
		t.Fatalf("dispatcher: %v", err)
	}
	ClockSyncDue(time.Unix(0, 0), time.Unix(7200, 0))
	if _, err := EncodeClockEvent(ClockEvent{Year: 26, Month: 1, Day: 1}); err != nil {
		t.Fatal(err)
	}
	if len(r.frames) != 0 || len(r.waits) != 0 {
		t.Fatalf("selection, trigger decision and encoding produced %d frames and %d waits", len(r.frames), len(r.waits))
	}
}

func TestV108EventRefusesPCSAndReadOnlySelections(t *testing.T) {
	r := &recorder{}
	for _, sel := range []Selection{
		{Profile: "growatt.bms.pcs.standard"},
		{Profile: "growatt.bms.common"},
		{Profile: "growatt.bms.low_voltage.can.v1_04"},
		{Profile: Profile, ReadOnly: true},
		{},
	} {
		d, err := NewDispatcher(sel, r.send, r.wait)
		if !errors.Is(err, ErrNotV108) || d != nil {
			t.Errorf("%+v: dispatcher %v err %v, want refusal", sel, d, err)
		}
	}
	var zero *Dispatcher
	if err := zero.Trigger(context.Background(), ClockEvent{Year: 26, Month: 1, Day: 1}); err == nil {
		t.Error("nil dispatcher triggered")
	}
	if err := (&Dispatcher{}).Trigger(context.Background(), ClockEvent{Year: 26, Month: 1, Day: 1}); err == nil {
		t.Error("zero dispatcher triggered")
	}
	if len(r.frames) != 0 {
		t.Fatalf("refused selections sent %d frames", len(r.frames))
	}
}
