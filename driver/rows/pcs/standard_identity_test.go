package pcs

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type identityVectorFile struct {
	Cases []identityVectorCase `json:"cases"`
}

type identityVectorCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  vectorSelection         `json:"selection"`
	Input      identityVectorInput     `json:"input"`
	Expect     map[string]vectorExpect `json:"expect"`
}

type identityVectorInput struct {
	MeasurementValid    bool  `json:"measurement_valid"`
	ThresholdStateKnown bool  `json:"threshold_state_known"`
	AlternativeEligible bool  `json:"alternative_eligible"`
	SerialSlotValid     bool  `json:"serial_slot_valid"`
	SerialSlot          uint8 `json:"serial_slot"`
}

func loadIdentityVectors(t *testing.T) []identityVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/standard_320_325.json")
	if err != nil {
		t.Fatal(err)
	}
	var f identityVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

// buildIdentity turns one identity frame into the selection, the decoder input
// and the frame, with the given evidence sequence and monotonic time.
func buildIdentity(t *testing.T, sel vectorSelection, identifier string, payload []byte, in identityVectorInput, seq uint64, mono time.Duration) (canbusreg.Selection, canbusreg.PCSIdentityPCSInput, canbus.Frame) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("selection %s%s: %v", sel.Project, sel.Release, err)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	identifierValue, err := canbus.NewStandardID(uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(identifierValue, payload)
	if err != nil {
		t.Fatal(err)
	}
	link := canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: seq, Monotonic: mono}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
	std := canbusreg.PCSStandardPCSInput{LINKInput: link, PublisherResolved: true, MeasurementValid: in.MeasurementValid}
	return s, canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: std, ThresholdStateKnown: in.ThresholdStateKnown, AlternativeEligible: in.AlternativeEligible, SerialSlotValid: in.SerialSlotValid, SerialSlot: in.SerialSlot}, frame
}

func buildIdentityCase(t *testing.T, c identityVectorCase) (canbusreg.Selection, canbusreg.PCSIdentityPCSInput) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	s, in, _ := buildIdentity(t, c.Selection, c.Identifier, payload, c.Input, 7, 9*time.Second)
	return s, in
}

// identityStatus is the decoder status of a native row: accepted, raw, or the
// reason the row gives when it has no value.
func identityStatus(n *roweval.Native) string {
	switch n.Disposition {
	case dispositionAccepted:
		return "accepted"
	case dispositionRaw:
		return "raw"
	}
	return string(n.Reason)
}

// applicableRows returns the identity rows that apply to the observation, keyed
// by their decoder field name.
func applicableRows(res IdentityResult) map[string][]string {
	out := make(map[string][]string)
	for id, ev := range res.Rows {
		if ev.Native != nil {
			out[fieldOf(id)] = append(out[fieldOf(id)], id)
		}
	}
	return out
}

// zeroRow reports whether a row is the constant zero of the decoder (the
// placeholder and the omitted family marker), whose raw word is not the value.
func zeroRow(id string) bool {
	return strings.HasSuffix(id, ".placeholder") || fieldOf(id) == "family_omitted" || fieldOf(id) == "placeholder"
}

// sumRow reports whether a row is the aggregate gateway count, whose value is the
// sum of its low byte and the high byte, so its raw word is the low byte alone.
func sumRow(id string) bool {
	return strings.HasSuffix(id, ".gateway.count")
}

// TestGatewayCountRowAddsHighByteAndKeepsBothBytes checks the aggregate gateway
// count of 0x323: the gateway row gives B0 + 256 x B3, its raw word is B0, the
// direct count row does not apply, and B3 stays its own raw row.
func TestGatewayCountRowAddsHighByteAndKeepsBothBytes(t *testing.T) {
	sel := vectorSelection{Project: "QCAA", Release: "0001", Flavor: "pcs-standard", Bank: "ordinary", Scope: "aggregate", Endpoint: "gateway", Group: "", Slot: ""}
	payload := []byte{0x03, 0x00, 0x0a, 0x02, 0x11, 0x22, 0x33, 0x44}
	s, in, _ := buildIdentity(t, sel, "0x323", payload, identityVectorInput{}, 7, 9*time.Second)
	res, reason, err := EvaluateIdentity(s, in)
	if err != nil || reason != "" {
		t.Fatalf("reason %q err %v", reason, err)
	}
	gw, ok := res.Rows["growatt.bms.pcs.standard.323.gateway.count"]
	if !ok || gw.Native == nil {
		t.Fatalf("gateway count row missing")
	}
	if gw.Native.Disposition != dispositionAccepted || gw.Native.Raw != 3 || gw.Native.Value == nil || gw.Native.Value.Quantity == nil {
		t.Fatalf("gateway count: disposition %q raw %d, want accepted raw 3 with a value", gw.Native.Disposition, gw.Native.Raw)
	}
	if got := decimalRat(t, gw.Native.Value.Quantity.Number).RatString(); got != "515" {
		t.Errorf("gateway count value %s, want 515", got)
	}
	if direct, ok := res.Rows["growatt.bms.pcs.standard.323.count"]; ok && direct.Native != nil {
		t.Errorf("direct count row applies to the aggregate gateway")
	}
	high, ok := res.Rows["growatt.bms.pcs.standard.323.separate_3"]
	if !ok || high.Native == nil || high.Native.Raw != 2 || high.Native.Disposition != dispositionRaw {
		t.Errorf("B3 is not kept as its own raw row")
	}
}

// TestGatewayCountRowKeepsDecimalBoundaries checks that the aggregate gateway count
// of 0x323 is the decoder's exact count for every byte pair, including low bytes
// that are multiples of ten (whose decimal is normalized as a power of ten) and
// sums that end in zero: B0 = 10 and B0 = 8 with B3 = 2 give 522 and 520.
func TestGatewayCountRowKeepsDecimalBoundaries(t *testing.T) {
	sel := vectorSelection{Project: "QCAA", Release: "0001", Flavor: "pcs-standard", Bank: "ordinary", Scope: "aggregate", Endpoint: "gateway", Group: "", Slot: ""}
	cases := []struct {
		low, high byte
		want      string
	}{
		{0x0a, 0x02, "522"},
		{0x08, 0x02, "520"},
		{0x00, 0x02, "512"},
		{0xfa, 0x02, "762"},
		{0x14, 0x00, "20"},
		{0x03, 0x02, "515"},
	}
	for _, c := range cases {
		payload := []byte{c.low, 0x00, 0x00, c.high, 0x11, 0x22, 0x33, 0x44}
		s, in, _ := buildIdentity(t, sel, "0x323", payload, identityVectorInput{}, 7, 9*time.Second)
		res, reason, err := EvaluateIdentity(s, in)
		if err != nil || reason != "" {
			t.Fatalf("B0 %#x B3 %#x: reason %q err %v", c.low, c.high, reason, err)
		}
		gw, ok := res.Rows["growatt.bms.pcs.standard.323.gateway.count"]
		if !ok || gw.Native == nil || gw.Native.Value == nil || gw.Native.Value.Quantity == nil {
			t.Fatalf("B0 %#x B3 %#x: gateway count value missing", c.low, c.high)
		}
		if gw.Native.Disposition != dispositionAccepted {
			t.Fatalf("B0 %#x B3 %#x: disposition %q, want accepted", c.low, c.high, gw.Native.Disposition)
		}
		if got := decimalRat(t, gw.Native.Value.Quantity.Number).RatString(); got != c.want {
			t.Errorf("B0 %#x B3 %#x: gateway count %s, want %s", c.low, c.high, got, c.want)
		}
	}
}

// TestIdentityRowsMatchDecoder checks every identity vector: each field the decoder
// emits has exactly one applicable row with the same status, raw word and value,
// and every applicable row is a field the decoder emits. The expectations are the
// hand-written vector values.
func TestIdentityRowsMatchDecoder(t *testing.T) {
	for _, c := range loadIdentityVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in := buildIdentityCase(t, c)
			dec := canbusreg.DecodePCSIdentity(s, in)
			if dec.Status != "decoded" {
				t.Fatalf("decoder status %q, want decoded", dec.Status)
			}
			res, reason, err := EvaluateIdentity(s, in)
			if err != nil || reason != "" {
				t.Fatalf("evaluator reason %q err %v", reason, err)
			}
			applicable := applicableRows(res)
			for name := range applicable {
				if _, ok := dec.Fields[name]; !ok {
					t.Errorf("row field %s applies but the decoder emits no such field", name)
				}
			}
			for name, df := range dec.Fields {
				exp, ok := c.Expect[name]
				if !ok {
					t.Errorf("decoder field %s has no vector expectation", name)
					continue
				}
				ids := applicable[name]
				if len(ids) != 1 {
					t.Errorf("field %s: %d applicable rows, want 1", name, len(ids))
					continue
				}
				row := res.Rows[ids[0]].Native
				if got := identityStatus(row); got != exp.Status {
					t.Errorf("%s: status %q, want %q", ids[0], got, exp.Status)
				}
				if df.Status != exp.Status {
					t.Errorf("%s: decoder status %q, want %q", name, df.Status, exp.Status)
				}
				if got := res.Grades[ids[0]]; got != df.Grade {
					t.Errorf("%s: grade %q, decoder grade %q", ids[0], got, df.Grade)
				}
				if !zeroRow(ids[0]) && !sumRow(ids[0]) && row.Raw != uint64(df.Numerator) {
					t.Errorf("%s: raw %d, decoder numerator %d", ids[0], row.Raw, df.Numerator)
				}
				if exp.Value != "" {
					if row.Value == nil || row.Value.Quantity == nil {
						t.Errorf("%s: no value, want %s", ids[0], exp.Value)
						continue
					}
					if got, want := decimalRat(t, row.Value.Quantity.Number).RatString(), ratText(t, exp.Value).RatString(); got != want {
						t.Errorf("%s: value %s, want %s", ids[0], got, want)
					}
				}
			}
			for name := range c.Expect {
				if _, ok := dec.Fields[name]; !ok {
					t.Errorf("vector expects field %s, which the decoder does not emit", name)
				}
			}
		})
	}
}

// TestIdentityRowsKeepDecoderGradeWithDisposition checks that an accepted row
// keeps the evidence grade the decoder gives its field: the 0x323 threshold and
// the aggregate gateway count are Hypothesis although accepted, the 0x321 family
// is Proven. The grade is read from the result, not from the disposition.
func TestIdentityRowsKeepDecoderGradeWithDisposition(t *testing.T) {
	want := map[string]struct{ field, grade string }{
		"identity-323-threshold-accepted-contract-l8":    {"threshold", "Hypothesis"},
		"identity-323-gateway-aggregate-count-high-byte": {"count", "Hypothesis"},
		"identity-321-family-capable-contract-l8":        {"family", "Proven"},
	}
	found := 0
	for _, c := range loadIdentityVectors(t) {
		w, ok := want[c.Name]
		if !ok {
			continue
		}
		found++
		t.Run(c.Name, func(t *testing.T) {
			s, in := buildIdentityCase(t, c)
			res, reason, err := EvaluateIdentity(s, in)
			if err != nil || reason != "" {
				t.Fatalf("evaluator reason %q err %v", reason, err)
			}
			ids := applicableRows(res)[w.field]
			if len(ids) != 1 {
				t.Fatalf("field %s: %d applicable rows, want 1", w.field, len(ids))
			}
			if got := identityStatus(res.Rows[ids[0]].Native); got != "accepted" {
				t.Fatalf("%s: disposition %q, want accepted", ids[0], got)
			}
			if got := res.Grades[ids[0]]; got != w.grade {
				t.Errorf("%s: grade %q, want %q", ids[0], got, w.grade)
			}
		})
	}
	if found != len(want) {
		t.Fatalf("found %d of %d named vectors", found, len(want))
	}
}

// TestSerialFragmentRowsKeepEachFragmentAsEvidence feeds the three frames of one
// serial through the rows and the decoder's assembler. Every fragment keeps its own
// byte rows as raw evidence, with its frame and sequence, and the assembler keeps
// each fragment; no row assembles the serial.
func TestSerialFragmentRowsKeepEachFragmentAsEvidence(t *testing.T) {
	sel := vectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-standard", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1"}
	fragments := []struct {
		payload string
		seq     uint64
		mono    time.Duration
	}{
		{"0003414243444546", 7, 9 * time.Second},
		{"01474849" + "4a4b4c4d", 8, 10 * time.Second},
		{"024e4f50" + "00000000", 9, 11 * time.Second},
	}
	in := identityVectorInput{SerialSlotValid: true, SerialSlot: 3}
	var asm canbusreg.PCSIdentitySerialAssembler
	for i, fr := range fragments {
		payload, err := hex.DecodeString(fr.payload)
		if err != nil {
			t.Fatal(err)
		}
		s, pin, frame := buildIdentity(t, sel, "0x324", payload, in, fr.seq, fr.mono)
		res, reason, err := EvaluateIdentity(s, pin)
		if err != nil || reason != "" {
			t.Fatalf("fragment %d: reason %q err %v", i, reason, err)
		}
		if res.Evidence.Sequence != fr.seq || res.Evidence.Frame != frame {
			t.Errorf("fragment %d: evidence sequence %d or frame differs", i, res.Evidence.Sequence)
		}
		for at, b := range payload {
			id := fmt.Sprintf("growatt.bms.pcs.standard.324.byte_%d", at)
			ev, ok := res.Rows[id]
			if !ok || ev.Native == nil {
				t.Fatalf("fragment %d: row %s missing", i, id)
			}
			if ev.Native.Disposition != dispositionRaw || ev.Native.Raw != uint64(b) || ev.Native.Value == nil {
				t.Errorf("fragment %d %s: disposition %q raw %d, want raw %d with a value", i, id, ev.Native.Disposition, ev.Native.Raw, b)
			}
		}
		out := asm.Consume(s, pin)
		wantStatus := "pending"
		if i == 2 {
			wantStatus = "complete"
		}
		if out.Status != wantStatus {
			t.Fatalf("fragment %d: assembler status %q, want %q", i, out.Status, wantStatus)
		}
		if out.Fragments[i].Evidence.Sequence != fr.seq && out.Status == "complete" {
			t.Errorf("fragment %d: assembler kept sequence %d", i, out.Fragments[i].Evidence.Sequence)
		}
	}
}

// TestIdentityRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat checks
// that a neighbouring identifier, a wrong length and the extended format give the
// decoder's status as the reason and no rows.
func TestIdentityRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	sel := vectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-standard", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1"}
	base := []byte{0x11, 0x22, 0x33, 0x01, 0x02, 0x03, 0x04, 0x00}
	s, pin, _ := buildIdentity(t, sel, "0x320", base, identityVectorInput{}, 7, 9*time.Second)
	cases := []struct {
		name   string
		frame  func() (canbus.Frame, error)
		status string
	}{
		{"neighbour 0x322", func() (canbus.Frame, error) {
			id, _ := canbus.NewStandardID(0x322)
			return canbus.NewFrame(id, base)
		}, "unsupported"},
		{"neighbour 0x326", func() (canbus.Frame, error) {
			id, _ := canbus.NewStandardID(0x326)
			return canbus.NewFrame(id, base)
		}, "unsupported"},
		{"wrong length", func() (canbus.Frame, error) {
			id, _ := canbus.NewStandardID(0x320)
			return canbus.NewFrame(id, base[:7])
		}, "invalid"},
		{"extended format", func() (canbus.Frame, error) {
			id, _ := canbus.NewExtendedID(0x320)
			return canbus.NewFrame(id, base)
		}, "invalid"},
	}
	for _, c := range cases {
		frame, err := c.frame()
		if err != nil {
			t.Fatal(err)
		}
		in := pin
		in.Evidence.Frame = frame
		if got := canbusreg.DecodePCSIdentity(s, in); got.Status != c.status || len(got.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, want %q and none", c.name, got.Status, len(got.Fields), c.status)
		}
		res, reason, err := EvaluateIdentity(s, in)
		if err != nil {
			t.Fatal(err)
		}
		if reason != roweval.Reason(c.status) || res.Rows != nil {
			t.Errorf("%s: evaluator reason %q with rows, want %q and none", c.name, reason, c.status)
		}
	}
}
