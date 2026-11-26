package growattlowvoltage

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type historyVectors struct {
	Query []struct {
		Name       string `json:"name"`
		BatteryID  uint8  `json:"battery_id"`
		Identifier string `json:"identifier"`
		Payload    string `json:"payload"`
	} `json:"query"`
	Response []struct {
		Name      string `json:"name"`
		BatteryID uint8  `json:"battery_id"`
		Frame     string `json:"frame"`
		Body      string `json:"body"`
	} `json:"response"`
	NotResponse []struct {
		Name      string `json:"name"`
		BatteryID uint8  `json:"battery_id"`
		Frame     string `json:"frame"`
	} `json:"not_response"`
}

func loadHistoryVectors(t *testing.T) historyVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/v108_history.json")
	if err != nil {
		t.Fatal(err)
	}
	var v historyVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func historyFrame(t *testing.T, s string) canbus.Frame {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return serialFrame(t, 0x325, false, b)
}

func TestV108HistoryQuerySendsExactlyOneFrame(t *testing.T) {
	for _, c := range loadHistoryVectors(t).Query {
		r := &recorder{}
		q, err := NewHistoryQuerier(v108, r.send)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.frames) != 0 {
			t.Fatalf("%s: constructing sent %d frames", c.Name, len(r.frames))
		}
		ex, err := q.Query(c.BatteryID)
		if err != nil || ex == nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if len(r.frames) != 1 || len(r.waits) != 0 {
			t.Fatalf("%s: %d frames, %d waits, want exactly one frame", c.Name, len(r.frames), len(r.waits))
		}
		id, _ := strconv.ParseUint(strings.TrimPrefix(c.Identifier, "0x"), 16, 32)
		want, _ := hex.DecodeString(c.Payload)
		f := r.frames[0]
		if f.ID().Value() != uint32(id) || f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 || !bytes.Equal(f.Data(), want) {
			t.Errorf("%s: got id %#x ext=%v data %x, want standard %s %x", c.Name, f.ID().Value(), f.ID().Extended(), f.Data(), c.Identifier, want)
		}
		if ex.Completed() {
			t.Errorf("%s: completed before any response", c.Name)
		}
	}

	count := 0
	q, _ := NewHistoryQuerier(v108, func(f canbus.Frame) error {
		count++
		return errors.New("bus refused")
	})
	if ex, err := q.Query(1); err == nil || ex != nil || count != 1 {
		t.Fatalf("send failure: exchange %v err %v after %d attempts, want one attempt and no exchange", ex, err, count)
	}
}

func TestV108HistoryResponseAcceptsOnlyFrameNumberZeroAndTargetBatteryId(t *testing.T) {
	for _, c := range loadHistoryVectors(t).Response {
		r := &recorder{}
		q, _ := NewHistoryQuerier(v108, r.send)
		ex, err := q.Query(c.BatteryID)
		if err != nil {
			t.Fatal(err)
		}
		if err := ex.Offer(historyFrame(t, c.Frame)); err != nil || !ex.Completed() {
			t.Fatalf("%s: err %v completed %v", c.Name, err, ex.Completed())
		}
		want, _ := hex.DecodeString(c.Frame)
		raw, ok := ex.Raw()
		if !ok || !bytes.Equal(raw[:], want) {
			t.Errorf("%s: raw %x, want %x", c.Name, raw, want)
		}
		if err := ex.Offer(historyFrame(t, c.Frame)); !errors.Is(err, ErrNotHistoryResponse) {
			t.Errorf("%s: second response err %v", c.Name, err)
		}
		if len(r.frames) != 1 {
			t.Errorf("%s: %d frames sent, want the one query", c.Name, len(r.frames))
		}
	}
}

func TestV108HistoryResponseWithOtherFrameNumberOrBatteryIdIsRejected(t *testing.T) {
	for _, c := range loadHistoryVectors(t).NotResponse {
		r := &recorder{}
		q, _ := NewHistoryQuerier(v108, r.send)
		ex, _ := q.Query(c.BatteryID)
		if err := ex.Offer(historyFrame(t, c.Frame)); !errors.Is(err, ErrNotHistoryResponse) {
			t.Errorf("%s: err %v, want refusal", c.Name, err)
		}
		if _, ok := ex.Raw(); ex.Completed() || ok {
			t.Errorf("%s: completed or kept raw bytes", c.Name)
		}
	}

	good, _ := hex.DecodeString("0001a1a2a3a4a5a6")
	r := &recorder{}
	q, _ := NewHistoryQuerier(v108, r.send)
	for name, f := range map[string]canbus.Frame{
		"neighbouring identifier": serialFrame(t, 0x324, false, good),
		"extended format":         serialFrame(t, 0x325, true, good),
		"short DLC":               serialFrame(t, 0x325, false, good[:7]),
		"the query identifier":    serialFrame(t, 0x212, false, good),
	} {
		ex, _ := q.Query(1)
		if err := ex.Offer(f); !errors.Is(err, ErrNotHistoryResponse) || ex.Completed() {
			t.Errorf("%s: err %v completed %v", name, err, ex.Completed())
		}
	}
	var none *HistoryExchange
	if none.Completed() || none.Offer(historyFrame(t, "0001a1a2a3a4a5a6")) == nil {
		t.Error("nil exchange accepted a response")
	}
}

func TestV108HistoryBodyStaysRaw(t *testing.T) {
	for _, c := range loadHistoryVectors(t).Response {
		r := &recorder{}
		q, _ := NewHistoryQuerier(v108, r.send)
		ex, _ := q.Query(c.BatteryID)
		if ex.BodyState() != "pending/raw" || ex.Body() != nil {
			t.Errorf("%s: body %x state %s before a response", c.Name, ex.Body(), ex.BodyState())
		}
		if err := ex.Offer(historyFrame(t, c.Frame)); err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(ex.Body()) != c.Body || ex.BodyState() != "pending/raw" {
			t.Errorf("%s: body %x state %s, want raw %s pending/raw", c.Name, ex.Body(), ex.BodyState(), c.Body)
		}
	}
}

func TestV108ReadOnlySelectionCannotDispatchHistoryQuery(t *testing.T) {
	r := &recorder{}
	for _, sel := range []Selection{
		{Profile: "growatt.bms.pcs.standard"},
		{Profile: "growatt.bms.common"},
		{Profile: "growatt.bms.low_voltage.can.v1_04"},
		{Profile: Profile, ReadOnly: true},
		{},
	} {
		q, err := NewHistoryQuerier(sel, r.send)
		if !errors.Is(err, ErrNotV108) || q != nil {
			t.Errorf("%+v: querier %v err %v, want refusal", sel, q, err)
		}
	}
	if _, err := NewHistoryQuerier(v108, nil); err == nil {
		t.Error("missing sender accepted")
	}
	var zero *HistoryQuerier
	if _, err := zero.Query(1); err == nil {
		t.Error("nil querier sent")
	}
	if _, err := (&HistoryQuerier{}).Query(1); err == nil {
		t.Error("zero querier sent")
	}
	if len(r.frames) != 0 {
		t.Fatalf("refused selections sent %d frames", len(r.frames))
	}
}
