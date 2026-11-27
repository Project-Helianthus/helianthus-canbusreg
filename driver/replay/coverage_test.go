package replay_test

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
)

func addFamily(into *[]replay.Frame, seen map[string]bool, pkg string, id uint32, extended bool, data []byte) {
	if id == 0 || len(data) > canbus.MaxClassicDataLength {
		return
	}
	kind := "s"
	if extended {
		kind = "e"
	}
	key := pkg + "/" + strconv.FormatUint(uint64(id), 16) + "/" + kind
	if seen[key] {
		return
	}
	seen[key] = true
	*into = append(*into, replay.Frame{Package: pkg, ID: id, Extended: extended, Data: append([]byte(nil), data...)})
}

func addFrame(into *[]replay.Frame, seen map[string]bool, pkg string, frame canbus.Frame) {
	addFamily(into, seen, pkg, frame.ID().Value(), frame.ID().Extended(), frame.Data())
}

func zeros(n uint8) []byte {
	if n == 0 || n > canbus.MaxClassicDataLength {
		n = canbus.MaxClassicDataLength
	}
	return make([]byte, n)
}

func finish(frames []replay.Frame) replay.Catalog {
	for i := range frames {
		frames[i].Monotonic = time.Duration(i+1) * time.Millisecond
	}
	return replay.Catalog{Scripts: []replay.Script{{
		Name: "families", Interface: "can6", Index: 7, Frames: frames,
	}}}
}

func requirePackages(frames []replay.Frame) error {
	got := map[string]bool{}
	for _, frame := range frames {
		got[frame.Package] = true
	}
	for _, name := range []string{"bms", "pcs", "link", "gree", "growattlowvoltage"} {
		if !got[name] {
			return errMissingPackage(name)
		}
	}
	return nil
}

type errMissingPackage string

func (e errMissingPackage) Error() string { return "replay fixture has no " + string(e) + " frame" }

func TestFixturesCoverEveryRowFamily(t *testing.T) {
	want, err := catalogue()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := replay.Write(want)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile("testdata/frames.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file, encoded) {
		t.Fatalf("fixture has %d bytes, catalogue has %d", len(file), len(encoded))
	}
	parsed, err := replay.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	src, err := replay.Open(parsed.Scripts[0])
	if err != nil {
		t.Fatal(err)
	}
	var previous time.Duration
	for i := range parsed.Scripts[0].Frames {
		rec, err := src.Receive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rec.Sequence != uint64(i+1) || rec.Monotonic <= previous {
			t.Fatalf("frame %d sequence %d time %s", i, rec.Sequence, rec.Monotonic)
		}
		previous = rec.Monotonic
	}
}
