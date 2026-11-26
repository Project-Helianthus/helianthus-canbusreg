package pcs

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// extremaFile is the synthetic vector file of the extrema frames 0x319, 0x322 and 0x330.
type extremaFile struct {
	Cases []extremaCase `json:"cases"`
}

type extremaCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  vectorSelection         `json:"selection"`
	Input      vectorInput             `json:"input"`
	Index      extremaIndex            `json:"index"`
	Expect     map[string]vectorExpect `json:"expect"`
}

// extremaIndex is the index validity the selection supplies: the validity, the
// scope and the inclusive native bounds.
type extremaIndex struct {
	Valid   bool   `json:"valid"`
	Scope   string `json:"scope"`
	Minimum uint16 `json:"minimum"`
	Maximum uint16 `json:"maximum"`
}

func loadExtremaCases(t *testing.T) []extremaCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/standard_319_322_330.json")
	if err != nil {
		t.Fatal(err)
	}
	var f extremaFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

// buildExtrema turns one extrema vector into the selection, the decoder input and
// the frame, with the index validity of the vector.
func buildExtrema(t *testing.T, c extremaCase) (canbusreg.Selection, canbusreg.PCSStandardPCSInput, canbus.Frame) {
	t.Helper()
	s, in, frame := build(t, vectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload, Selection: c.Selection, Input: c.Input, Expect: c.Expect})
	in.IndexValid = c.Index.Valid
	in.IndexScope = c.Index.Scope
	in.IndexMinimum = c.Index.Minimum
	in.IndexMaximum = c.Index.Maximum
	return s, in, frame
}

// extremaFrameDisposition admits only the standard identifier with DLC 8 and raw
// DLC 8 of the three extrema frames.
func extremaFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x319, 0x322, 0x330:
		return ""
	}
	return roweval.ReasonNoMatch
}

// evaluateExtrema runs the decoder and the extrema rows on one observation. A
// frame the decoder refuses gives the decoder's status and no rows.
func evaluateExtrema(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSStandard(s, in)
	res.Diagnostics = decoded.Diagnostics
	if decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	if reason := extremaFrameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	t := extremaTable()
	raw := extremaRawIDs()
	q := extremaQualifiers(s, in)
	out := make(map[string]roweval.Evaluation)
	for _, row := range t.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At.Slice), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(t, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil {
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			ev.Native.Disposition = disposition(ev.Native, raw[ev.Native.RowID])
			out[ev.Native.RowID] = ev
		} else if ev.Reason != roweval.ReasonNoMatch {
			out[row.ID] = ev
		}
	}
	for _, row := range t.Native {
		if row.At.Identifier == frame.ID().Value() {
			if _, ok := out[row.ID]; !ok {
				out[row.ID] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
			}
		}
	}
	res.Rows = out
	return res, "", nil
}

// extremaRawIDs and extremaGated describe the rows of the extrema table: the rows
// the decoder never interprets, and the rows that name a gate.
func extremaRawIDs() map[string]bool {
	raw := make(map[string]bool)
	for _, s := range extremaSpecs() {
		if s.raw {
			raw[nativeRow(s.rowSpec).ID] = true
		}
	}
	return raw
}

func extremaGated() map[string]bool {
	gated := make(map[string]bool)
	for _, s := range extremaSpecs() {
		gated[nativeRow(s.rowSpec).ID] = s.gate != ""
	}
	return gated
}

// checkExtremaCase compares every decoder field of a vector with the one row that
// applies to it, and the expectation of the vector with both. It returns the
// identifiers of the rows that applied.
func checkExtremaCase(t *testing.T, c extremaCase) map[string]bool {
	t.Helper()
	s, in, frame := buildExtrema(t, c)
	r := canbusreg.DecodePCSStandard(s, in)
	res, reason, err := evaluateExtrema(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	if r.Status != "decoded" {
		if reason != roweval.Reason(r.Status) || res.Rows != nil || len(r.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, evaluator reason %q", c.Name, r.Status, len(r.Fields), reason)
		}
		return map[string]bool{}
	}
	if reason != "" {
		t.Fatalf("%s: decoder admits the observation, evaluator refuses it (%q)", c.Name, reason)
	}
	checkEvidence(t, c.Name, res, in, r)
	checkAttribution(t, c.Name, res, r)
	rows := make(map[string]roweval.NativeRow[Selector])
	for _, row := range extremaTable().Native {
		rows[row.ID] = row
	}
	gated := extremaGated()
	applied := make(map[string]bool)
	byField := make(map[string][]string)
	for id, ev := range res.Rows {
		if ev.Reason == roweval.ReasonNoMatch {
			continue
		}
		applied[id] = true
		byField[fieldOf(id)] = append(byField[fieldOf(id)], id)
	}
	if len(byField) != len(r.Fields) {
		t.Fatalf("%s: %d decoder fields, %d rows applied", c.Name, len(r.Fields), len(byField))
	}
	for name, df := range r.Fields {
		ids := byField[name]
		if len(ids) != 1 {
			t.Errorf("%s: field %s has %d applied rows", c.Name, name, len(ids))
			continue
		}
		checkField(t, c.Name, name, df, rows[ids[0]], gated[ids[0]], res.Rows[ids[0]].Native, frame)
	}
	for name, want := range c.Expect {
		df, ok := r.Fields[name]
		if !ok || df.Status != want.Status {
			t.Errorf("%s: %s status %q, want %q", c.Name, name, df.Status, want.Status)
		}
		if got, w := new(big.Rat).SetFrac64(df.Numerator, df.Denominator), ratText(t, want.Value); got.Cmp(w) != 0 {
			t.Errorf("%s: %s decoder value %s, want %s", c.Name, name, got.RatString(), want.Value)
		}
		for _, id := range byField[name] {
			if v := res.Rows[id].Native.Value; v != nil && v.Quantity != nil {
				if got := decimalRat(t, v.Quantity.Number); got.Cmp(ratText(t, want.Value)) != 0 {
					t.Errorf("%s: %s row value %s, want %s", c.Name, name, got.RatString(), want.Value)
				}
			}
		}
	}
	return applied
}

// indexFields are the decoder fields that the index validity condition gates.
var indexFields = map[string]bool{
	"maximum_index": true, "minimum_index": true,
	"maximum_temperature_index": true, "minimum_temperature_index": true,
	"maximum_location_fixed_aggregate": true, "minimum_location_fixed_aggregate": true,
}

func TestStandardExtremaRowsMatchDecoder(t *testing.T) {
	applied := make(map[string]bool)
	n := 0
	for _, c := range loadExtremaCases(t) {
		for id := range checkExtremaCase(t, c) {
			applied[id] = true
		}
		n++
	}
	if n == 0 {
		t.Fatal("no vectors for 319, 322 and 330")
	}
	for _, row := range extremaTable().Native {
		if !applied[row.ID] {
			t.Errorf("no vector applies to %s", row.ID)
		}
	}
}

func TestStandardExtremaIndexRowsFollowIndexValidity(t *testing.T) {
	n := 0
	for _, c := range loadExtremaCases(t) {
		if !strings.HasPrefix(c.Name, "index-") {
			continue
		}
		checkExtremaCase(t, c)
		s, in, _ := buildExtrema(t, c)
		res, _, _ := evaluateExtrema(s, in)
		r := canbusreg.DecodePCSStandard(s, in)
		for id, ev := range res.Rows {
			if ev.Native == nil || !indexFields[fieldOf(id)] {
				continue
			}
			if ev.Native.Reason == "" && r.Fields[fieldOf(id)].Status != "accepted" {
				t.Errorf("%s: %s has a value while the decoder gives %q", c.Name, id, r.Fields[fieldOf(id)].Status)
			}
			if ev.Native.Reason != "" && ev.Native.Reason != gateWithheld {
				t.Errorf("%s: %s withheld by %q, want the gate", c.Name, id, ev.Native.Reason)
			}
		}
		n++
	}
	if n < 5 {
		t.Fatalf("only %d index vectors", n)
	}
}

// TestStandardExtremaRowsRefuseNeighbouringIdentifierAndWrongLength pins that the
// evaluator refuses, with the decoder's status and no rows, a neighbouring
// identifier, a wrong DLC and an extended identifier of the extrema frames.
func TestStandardExtremaRowsRefuseNeighbouringIdentifierAndWrongLength(t *testing.T) {
	base := extremaCase{}
	for _, c := range loadExtremaCases(t) {
		if c.Name == "local-extrema-319" {
			base = c
		}
	}
	payload, err := hex.DecodeString(base.Payload)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		id     uint32
		ext    bool
		data   []byte
		status string
	}{
		{"neighbour 0x318", 0x318, false, payload, "unsupported"},
		{"neighbour 0x321", 0x321, false, payload, "unsupported"},
		{"neighbour 0x329", 0x329, false, payload, "unsupported"},
		{"neighbour 0x331", 0x331, false, payload, "unsupported"},
		{"wrong length 0x319", 0x319, false, payload[:7], "invalid"},
		{"extended 0x319", 0x319, true, payload, "invalid"},
	}
	for _, tc := range cases {
		s, in, _ := buildExtrema(t, base)
		var id canbus.Identifier
		if tc.ext {
			id, err = canbus.NewExtendedID(tc.id)
		} else {
			id, err = canbus.NewStandardID(tc.id)
		}
		if err != nil {
			t.Fatal(err)
		}
		frame, err := canbus.NewFrame(id, tc.data)
		if err != nil {
			t.Fatal(err)
		}
		in.Evidence.Frame = frame
		r := canbusreg.DecodePCSStandard(s, in)
		res, reason, err := evaluateExtrema(s, in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if r.Status != tc.status || reason != roweval.Reason(tc.status) {
			t.Errorf("%s: decoder %q, evaluator %q, want %q", tc.name, r.Status, reason, tc.status)
		}
		if res.Rows != nil {
			t.Errorf("%s: refused observation carries %d rows", tc.name, len(res.Rows))
		}
	}
}
