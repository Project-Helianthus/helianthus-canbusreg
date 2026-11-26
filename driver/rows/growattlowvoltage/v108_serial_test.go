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
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

type serialVectors struct {
	Query []struct {
		Name       string `json:"name"`
		BatteryID  uint8  `json:"battery_id"`
		Identifier string `json:"identifier"`
		Payload    string `json:"payload"`
	} `json:"query"`
	Response []struct {
		Name      string   `json:"name"`
		BatteryID uint8    `json:"battery_id"`
		Frames    []string `json:"frames"`
		Serial    string   `json:"serial"`
	} `json:"response"`
	NotResponse []struct {
		Name      string   `json:"name"`
		BatteryID uint8    `json:"battery_id"`
		Frames    []string `json:"frames"`
	} `json:"not_response"`
}

func loadSerialVectors(t *testing.T) serialVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/v108_serial.json")
	if err != nil {
		t.Fatal(err)
	}
	var v serialVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func serialFrame(t *testing.T, id uint32, extended bool, data []byte) canbus.Frame {
	t.Helper()
	var ident canbus.Identifier
	var err error
	if extended {
		ident, err = canbus.NewExtendedID(id)
	} else {
		ident, err = canbus.NewStandardID(id)
	}
	if err != nil {
		t.Fatal(err)
	}
	f, err := canbus.NewFrame(ident, data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func hexFrame(t *testing.T, s string) canbus.Frame {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return serialFrame(t, 0x324, false, b)
}

func TestV108SerialQuerySendsExactlyOneFrame(t *testing.T) {
	for _, c := range loadSerialVectors(t).Query {
		r := &recorder{}
		q, err := NewSerialQuerier(v108, r.send)
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
	q, _ := NewSerialQuerier(v108, func(f canbus.Frame) error {
		count++
		return errors.New("bus refused")
	})
	if ex, err := q.Query(1); err == nil || ex != nil || count != 1 {
		t.Fatalf("send failure: exchange %v err %v after %d attempts, want one attempt and no exchange", ex, err, count)
	}
}

func TestV108SerialResponseKeepsEveryFragmentStandardAndExtended(t *testing.T) {
	for _, c := range loadSerialVectors(t).Response {
		r := &recorder{}
		q, _ := NewSerialQuerier(v108, r.send)
		ex, err := q.Query(c.BatteryID)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range c.Frames {
			if err := ex.Offer(hexFrame(t, s)); err != nil {
				t.Fatalf("%s: frame %d: %v", c.Name, i, err)
			}
			if !ex.Completed() {
				t.Errorf("%s: not completed after frame %d", c.Name, i)
			}
		}
		got := ex.Fragments()
		if len(got) != len(c.Frames) {
			t.Fatalf("%s: %d fragments kept, want %d", c.Name, len(got), len(c.Frames))
		}
		for i, s := range c.Frames {
			want, _ := hex.DecodeString(s)
			if !bytes.Equal(got[i].Raw[:], want) {
				t.Errorf("%s: fragment %d %x, want %x", c.Name, i, got[i].Raw, want)
			}
		}
		if hex.EncodeToString(ex.SerialBytes()) != c.Serial {
			t.Errorf("%s: serial bytes %x, want %s", c.Name, ex.SerialBytes(), c.Serial)
		}
	}

	r := &recorder{}
	q, _ := NewSerialQuerier(v108, r.send)
	ex, _ := q.Query(3)
	vs := loadSerialVectors(t).Response[2]
	for _, s := range vs.Frames {
		if err := ex.Offer(hexFrame(t, s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ex.Offer(hexFrame(t, "0528293031323334")); !errors.Is(err, ErrNotSerialResponse) || len(ex.Fragments()) != len(vs.Frames) {
		t.Errorf("frame after 32 serial bytes: err %v, %d fragments", err, len(ex.Fragments()))
	}
	if err := ex.Offer(hexFrame(t, "0003000102030405")); !errors.Is(err, ErrNotSerialResponse) {
		t.Errorf("second frame 0: err %v", err)
	}
}

func TestV108SerialRejectsWrongBatteryIdRoleAndProfile(t *testing.T) {
	for _, c := range loadSerialVectors(t).NotResponse {
		r := &recorder{}
		q, _ := NewSerialQuerier(v108, r.send)
		ex, _ := q.Query(c.BatteryID)
		for _, s := range c.Frames {
			if err := ex.Offer(hexFrame(t, s)); !errors.Is(err, ErrNotSerialResponse) {
				t.Errorf("%s: err %v, want refusal", c.Name, err)
			}
		}
		if ex.Completed() || len(ex.Fragments()) != 0 {
			t.Errorf("%s: completed %v with %d fragments", c.Name, ex.Completed(), len(ex.Fragments()))
		}
	}

	good, _ := hex.DecodeString("0001414243444546")
	r := &recorder{}
	q, _ := NewSerialQuerier(v108, r.send)
	for name, f := range map[string]canbus.Frame{
		"neighbouring identifier": serialFrame(t, 0x325, false, good),
		"extended format":         serialFrame(t, 0x324, true, good),
		"short DLC":               serialFrame(t, 0x324, false, good[:7]),
		"the query identifier":    serialFrame(t, 0x212, false, good),
	} {
		ex, _ := q.Query(1)
		if err := ex.Offer(f); !errors.Is(err, ErrNotSerialResponse) || ex.Completed() {
			t.Errorf("%s: err %v completed %v", name, err, ex.Completed())
		}
	}
	var none *SerialExchange
	if none.Completed() || none.Offer(hexFrame(t, "0001414243444546")) == nil {
		t.Error("nil exchange accepted a response")
	}

	r = &recorder{}
	for _, sel := range []Selection{
		{Profile: "growatt.bms.pcs.standard"},
		{Profile: "growatt.bms.common"},
		{Profile: "growatt.bms.low_voltage.can.v1_04"},
		{Profile: Profile, ReadOnly: true},
		{},
	} {
		q, err := NewSerialQuerier(sel, r.send)
		if !errors.Is(err, ErrNotV108) || q != nil {
			t.Errorf("%+v: querier %v err %v, want refusal", sel, q, err)
		}
	}
	if _, err := NewSerialQuerier(v108, nil); err == nil {
		t.Error("missing sender accepted")
	}
	var zero *SerialQuerier
	if _, err := zero.Query(1); err == nil {
		t.Error("nil querier sent")
	}
	if _, err := (&SerialQuerier{}).Query(1); err == nil {
		t.Error("zero querier sent")
	}
	if len(r.frames) != 0 {
		t.Fatalf("refused selections sent %d frames", len(r.frames))
	}
}

func TestV108SerialCompletionOnlyForCorrespondingResponse(t *testing.T) {
	r := &recorder{}
	q, _ := NewSerialQuerier(v108, r.send)
	ex, _ := q.Query(7)
	if ex.Completed() {
		t.Fatal("completed with no response")
	}
	for _, s := range []string{"0001414243444546", "0107414243444546", "0008414243444546"} {
		_ = ex.Offer(hexFrame(t, s))
		if ex.Completed() || len(ex.Fragments()) != 0 {
			t.Fatalf("%s completed or kept a fragment", s)
		}
	}
	if err := ex.Offer(hexFrame(t, "0007414243444546")); err != nil || !ex.Completed() {
		t.Fatalf("corresponding frame 0: err %v completed %v", err, ex.Completed())
	}
	if ex.BatteryID() != 7 || len(r.frames) != 1 {
		t.Errorf("battery %d, %d frames sent", ex.BatteryID(), len(r.frames))
	}
}

func TestPCS212OutputIsUnchanged(t *testing.T) {
	var gap string
	for _, g := range pcs.IncompleteInputs() {
		if g.Identifier == 0x212 {
			gap = g.Gap
		}
	}
	want := "the page defines no operation for this input in the profiles it covers: its payload has no defined serial or history query, and the page marks it unsupported for query purposes"
	if gap != want {
		t.Errorf("PCS 212 gap %q, want unchanged text", gap)
	}
	for _, c := range pcs.Commands() {
		if c.Identifier == 0x212 {
			t.Errorf("PCS command row %s encodes 212", c.ID)
		}
	}
	if len(pcs.Commands()) != 2 {
		t.Errorf("%d PCS command rows, want 2", len(pcs.Commands()))
	}
}
