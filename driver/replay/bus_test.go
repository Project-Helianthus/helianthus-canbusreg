package replay_test

import (
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
)

func mustFrame(t *testing.T, id uint32, data []byte) canbus.Frame {
	t.Helper()
	identifier, err := canbus.NewStandardID(id)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(identifier, data)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestBusKeepsSubmittedFramesInOrder(t *testing.T) {
	if _, err := replay.NewBus(nil); err == nil {
		t.Fatal("nil clock was accepted")
	}
	var at time.Duration
	bus, err := replay.NewBus(func() time.Duration { return at })
	if err != nil {
		t.Fatal(err)
	}
	at = 10
	if _, err := bus.Submit(mustFrame(t, 0x100, []byte{1})); err != nil {
		t.Fatal(err)
	}
	at = 25
	if _, err := bus.Submit(mustFrame(t, 0x101, []byte{2, 3})); err != nil {
		t.Fatal(err)
	}
	got := bus.Frames()
	if len(got) != 2 || got[0].Monotonic != 10 || got[1].Monotonic != 25 {
		t.Fatalf("stamps %+v", got)
	}
	if got[0].Frame.ID().Value() != 0x100 || got[1].Frame.ID().Value() != 0x101 || got[0].Frame.Data()[0] != 1 || got[1].Frame.Data()[1] != 3 {
		t.Fatalf("order %x then %x", got[0].Frame.Data(), got[1].Frame.Data())
	}
	again := bus.Frames()
	again[0].Frame = got[1].Frame
	if bus.Frames()[0].Frame.ID().Value() != 0x100 {
		t.Fatal("frames alias the bus buffer")
	}
}
