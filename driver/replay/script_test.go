package replay_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
)

func orderScript() replay.Script {
	return replay.Script{
		Name: "order", Interface: "can0", Index: 1,
		Frames: []replay.Frame{
			{Package: "bms", ID: 0x100, Data: []byte{1}, Monotonic: 0, Sequence: 4, HasSequence: true},
			{Package: "bms", ID: 0x101, Extended: true, Data: []byte{2, 3}, Monotonic: 5 * time.Millisecond},
			{Package: "bms", ID: 0x102, Data: []byte{}, Monotonic: 9 * time.Millisecond, Sequence: 0, HasSequence: true},
		},
	}
}

func readAll(t *testing.T, src *replay.Source, n int) [][3]any {
	t.Helper()
	out := make([][3]any, 0, n)
	for i := 0; i < n; i++ {
		rec, err := src.Receive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, [3]any{append([]byte(nil), rec.Raw...), rec.Sequence, rec.Monotonic})
	}
	return out
}

func TestSourceYieldsRecordsInOrderWithRecordedTimes(t *testing.T) {
	src, err := replay.Open(orderScript())
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, src, 3)
	wantSeq := []uint64{4, 2, 0}
	wantAt := []time.Duration{0, 5 * time.Millisecond, 9 * time.Millisecond}
	for i := range got {
		frame, err := canbus.DecodeSocketCANRecord(got[i][0].([]byte))
		if err != nil {
			t.Fatal(err)
		}
		if got[i][1].(uint64) != wantSeq[i] || got[i][2].(time.Duration) != wantAt[i] {
			t.Fatalf("record %d sequence %v time %v", i, got[i][1], got[i][2])
		}
		if frame.ID().Value() != uint32(0x100+i) {
			t.Fatalf("record %d id %#x", i, frame.ID().Value())
		}
	}
	if got[1][0].([]byte)[4] != 2 || !canbusIDExtended(t, got[1][0].([]byte)) {
		t.Fatal("second record lost its extended payload")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := src.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("after last record: %v", err)
	}
}

func canbusIDExtended(t *testing.T, raw []byte) bool {
	t.Helper()
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return frame.ID().Extended()
}

func TestTwoOpensStayIndependent(t *testing.T) {
	script := orderScript()
	first, err := replay.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	second, err := replay.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	left := readAll(t, first, 3)
	script.Frames[0].Data[0] = 9
	right := readAll(t, second, 3)
	if !bytes.Equal(left[0][0].([]byte), right[0][0].([]byte)) || left[0][0].([]byte)[8] != 1 {
		t.Fatal("opened sources share the caller's buffer")
	}
	third, err := replay.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	again := readAll(t, third, 1)
	if again[0][0].([]byte)[8] != 9 {
		t.Fatal("a later open ignored the script it was given")
	}
	raw, err := replay.Write(replay.Catalog{Scripts: []replay.Script{orderScript()}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := replay.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := replay.Write(parsed)
	if err != nil || !bytes.Equal(raw, encoded) {
		t.Fatalf("rewrite drifted: %v", err)
	}
}

func TestSourceBlocksUntilClosed(t *testing.T) {
	src, err := replay.Open(replay.Script{Name: "empty", Interface: "can0", Index: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled receive: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := src.Receive(context.Background())
		done <- err
	}()
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, replay.ErrClosed) {
		t.Fatalf("closed receive: %v", err)
	}
}

func TestRawRecordWins(t *testing.T) {
	raw := make([]byte, canbus.SocketCANRecordSize)
	binary.NativeEndian.PutUint32(raw[:4], 0x123)
	raw[4] = 1
	raw[8] = 9
	src, err := replay.Open(replay.Script{
		Name: "raw", Interface: "can0", Index: 1,
		Frames: []replay.Frame{{ID: 0x456, Data: []byte{0}, Raw: raw, Monotonic: time.Millisecond}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := src.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.DecodeSocketCANRecord(rec.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Raw, raw) || frame.ID().Value() != 0x123 || frame.ID().Extended() || len(frame.Data()) != 1 || frame.Data()[0] != 9 {
		t.Fatalf("raw record was not replayed: %#x %x", frame.ID().Value(), frame.Data())
	}
	if rec.Monotonic != time.Millisecond || rec.Sequence != 1 || rec.Interface.Name() != "can0" || rec.Interface.Index() != 1 {
		t.Fatalf("record meta %+v", rec)
	}
}

func TestParseRejectsABadCatalog(t *testing.T) {
	for _, raw := range []string{
		`{"scripts":[{"name":"a","interface":"can0","index":1,"frames":[],"extra":1}]}`,
		`{"scripts":[{"name":"a","interface":"can/0","index":1,"frames":[]}]}`,
		`{"scripts":[{"name":"a","interface":"can0","index":0,"frames":[]}]}`,
		`{"scripts":[{"name":"","interface":"can0","index":1,"frames":[]}]}`,
		`{"scripts":[{"name":"a","interface":"can0","index":1,"frames":[{"id":"100","extended":false,"data":"zz","monotonic_ns":0}]}]}`,
		`{"scripts":[{"name":"a","interface":"can0","index":1,"frames":[{"id":"100","extended":false,"data":"001122334455667788","monotonic_ns":0}]}]}`,
		`{"scripts":[{"name":"a","interface":"can0","index":1,"frames":[{"id":"100","extended":false,"data":"","monotonic_ns":-1}]}]}`,
		`{"scripts":[{"name":"a","interface":"can0","index":1,"frames":[]}]}{}`,
	} {
		if _, err := replay.Parse([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
