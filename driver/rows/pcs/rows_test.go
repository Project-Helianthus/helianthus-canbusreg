package pcs

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// vectorFile is the synthetic vector file of the standard frames.
type vectorFile struct {
	Cases []vectorCase `json:"cases"`
}

type vectorCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  vectorSelection         `json:"selection"`
	Input      vectorInput             `json:"input"`
	Expect     map[string]vectorExpect `json:"expect"`
}

type vectorSelection struct {
	Project  string `json:"project"`
	Release  string `json:"release"`
	Flavor   string `json:"flavor"`
	Bank     string `json:"bank"`
	Scope    string `json:"scope"`
	Endpoint string `json:"endpoint"`
	Group    string `json:"group"`
	Slot     string `json:"slot"`
}

type vectorInput struct {
	MeasurementValid bool   `json:"measurement_valid"`
	TemperatureValid bool   `json:"temperature_valid"`
	CountValid       bool   `json:"count_valid"`
	Count            uint16 `json:"count"`
}

// vectorExpect is the independent expectation of one decoder field: its status
// and its exact decimal value.
type vectorExpect struct {
	Status string `json:"status"`
	Value  string `json:"value"`
}

func loadVectors(t *testing.T) []vectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/standard_311_314.json")
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func caseNamed(t *testing.T, name string) vectorCase {
	t.Helper()
	for _, c := range loadVectors(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no vector named %s", name)
	return vectorCase{}
}

// build turns one vector into the selection, the decoder input and the frame.
func build(t *testing.T, c vectorCase) (canbusreg.Selection, canbusreg.PCSStandardPCSInput, canbus.Frame) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := c.Selection
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(c.Identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := canbus.NewStandardID(uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame, err := canbus.NewFrame(identifier, payload)
	if err != nil {
		t.Fatal(err)
	}
	in := c.Input
	return s, canbusreg.PCSStandardPCSInput{LINKInput: canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}, PublisherResolved: true, MeasurementValid: in.MeasurementValid, TemperatureValid: in.TemperatureValid, CountValid: in.CountValid, Count: in.Count}, frame
}

// fieldOf is the decoder field key of a row: the last segment of its identifier.
func fieldOf(id string) string { return id[strings.LastIndex(id, ".")+1:] }

// decimalRat is the exact rational value of a decimal.
func decimalRat(t *testing.T, d semreg.Decimal) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", d.Coefficient, d.Exponent10))
	if !ok {
		t.Fatalf("decimal %v is not a number", d)
	}
	return r
}

func ratText(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("expectation %q is not a number", s)
	}
	return r
}

// evaluateCase runs the decoder and the evaluator on one vector and returns both.
func evaluateCase(t *testing.T, c vectorCase) (canbusreg.PCSStandardPCSResult, map[string]roweval.Evaluation, canbus.Frame) {
	t.Helper()
	s, in, frame := build(t, c)
	evals, reason, err := Evaluate(frame, Qualifiers(s, in))
	if err != nil || reason != "" {
		t.Fatalf("%s: evaluate refused: %v %v", c.Name, reason, err)
	}
	return canbusreg.DecodePCSStandard(s, in), evals, frame
}

// checkCase compares every decoder field of a vector with the one row that
// applies to it. It returns the identifiers of the rows that applied.
func checkCase(t *testing.T, c vectorCase) map[string]bool {
	t.Helper()
	r, evals, frame := evaluateCase(t, c)
	rows := make(map[string]roweval.NativeRow[Selector])
	for _, row := range Table().Native {
		rows[row.ID] = row
	}
	applied := make(map[string]bool)
	byField := make(map[string][]string)
	for id, ev := range evals {
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
		checkField(t, c.Name, name, df, rows[ids[0]], evals[ids[0]].Native, frame)
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
			if v := evals[id].Native.Value; v != nil && v.Quantity != nil {
				if got := decimalRat(t, v.Quantity.Number); got.Cmp(ratText(t, want.Value)) != 0 {
					t.Errorf("%s: %s row value %s, want %s", c.Name, name, got.RatString(), want.Value)
				}
			}
		}
	}
	return applied
}

// checkField compares one decoder field with its row: the raw word, the raw
// evidence, the status and, for an accepted field, the exact native value.
func checkField(t *testing.T, name, field string, df canbusreg.NativeField, row roweval.NativeRow[Selector], nat *roweval.Native, frame canbus.Frame) {
	t.Helper()
	if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
		t.Errorf("%s: %s raw evidence differs from the frame", name, field)
	}
	if nat == nil {
		t.Fatalf("%s: %s has no native evaluation", name, field)
	}
	got := int64(nat.Raw)
	if row.At.Slice.signed {
		got = int64(int16(nat.Raw))
	}
	if got != df.Numerator {
		t.Errorf("%s: %s raw %d, decoder %d", name, field, got, df.Numerator)
	}
	gated := row.Decode.Hook != ""
	switch df.Status {
	case "accepted":
		if nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Fatalf("%s: %s accepted by the decoder, row reason %q", name, field, nat.Reason)
		}
		if df.Unit != row.Unit {
			t.Errorf("%s: %s unit %q, row %q", name, field, df.Unit, row.Unit)
		}
		if decimalRat(t, nat.Value.Quantity.Number).Cmp(new(big.Rat).SetFrac64(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: %s row value differs from the decoder %d/%d", name, field, df.Numerator, df.Denominator)
		}
	case "raw":
		if gated && nat.Reason != gateWithheld {
			t.Errorf("%s: %s raw in the decoder, row reason %q", name, field, nat.Reason)
		}
		if !gated && (nat.Reason != "" || nat.Value == nil) {
			t.Errorf("%s: %s ungated raw field, row reason %q", name, field, nat.Reason)
		}
	case "invalid":
		if nat.Reason != roweval.ReasonOutsideDomain {
			t.Errorf("%s: %s invalid in the decoder, row reason %q", name, field, nat.Reason)
		}
	default:
		t.Errorf("%s: %s has unexpected status %q", name, field, df.Status)
	}
}

func TestStandardLimitAndSummaryRowsMatchDecoder(t *testing.T) {
	n := 0
	for _, c := range loadVectors(t) {
		if c.Identifier == "0x311" || c.Identifier == "0x312" {
			checkCase(t, c)
			n++
		}
	}
	if n == 0 {
		t.Fatal("no vectors for 311 and 312")
	}
}

func TestStandardMeasurementRowsMatchDecoder(t *testing.T) {
	n := 0
	for _, c := range loadVectors(t) {
		if c.Identifier == "0x313" {
			checkCase(t, c)
			n++
		}
	}
	if n == 0 {
		t.Fatal("no vectors for 313")
	}
}

func TestStandardCapacityRowsMatchDecoder(t *testing.T) {
	n := 0
	for _, c := range loadVectors(t) {
		if c.Identifier == "0x314" {
			checkCase(t, c)
			n++
		}
	}
	if n == 0 {
		t.Fatal("no vectors for 314")
	}
}

// TestStandardVectorsCoverEveryRow pins that every native row of the standard
// table applies to at least one vector, so no row is compared by nothing.
func TestStandardVectorsCoverEveryRow(t *testing.T) {
	applied := make(map[string]bool)
	for _, c := range loadVectors(t) {
		for id := range checkCase(t, c) {
			applied[id] = true
		}
	}
	for _, row := range Table().Native {
		if !applied[row.ID] {
			t.Errorf("no vector applies to %s", row.ID)
		}
	}
}

func TestStandardRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	s, in, _ := build(t, base)
	for _, id := range []uint32{0x310, 0x315, 0x321} {
		identifier, _ := canbus.NewStandardID(id)
		frame, _ := canbus.NewFrame(identifier, []byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 0x64, 0xe4})
		assertRefused(t, fmt.Sprintf("neighbour %#x", id), s, in, frame)
	}
	short, _ := canbus.NewStandardID(0x313)
	frame, _ := canbus.NewFrame(short, []byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 0x64})
	assertRefused(t, "wrong length", s, in, frame)
	extended, _ := canbus.NewExtendedID(0x313)
	frame, _ = canbus.NewFrame(extended, []byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 0x64, 0xe4})
	assertRefused(t, "extended format", s, in, frame)
}

// assertRefused checks that the decoder yields no field and that no row applies.
func assertRefused(t *testing.T, name string, s canbusreg.Selection, in canbusreg.PCSStandardPCSInput, frame canbus.Frame) {
	t.Helper()
	in.Evidence.Frame = frame
	r := canbusreg.DecodePCSStandard(s, in)
	if len(r.Fields) != 0 {
		t.Errorf("%s: decoder emitted %d fields", name, len(r.Fields))
	}
	evals, reason, err := Evaluate(frame, Qualifiers(s, in))
	if err != nil {
		t.Fatal(err)
	}
	if reason != roweval.ReasonNoMatch || evals != nil {
		t.Errorf("%s: evaluator admitted the frame (%q)", name, reason)
	}
}

func TestStandardRowsSeparateBanksAndGroupAndSlotContext(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	absent := base
	absent.Name = "legacy-local-313-without-group-and-slot"
	absent.Selection.Group, absent.Selection.Slot = "", ""
	rb, eb, _ := evaluateCase(t, base)
	ra, ea, _ := evaluateCase(t, absent)
	if rb.Fields["voltage"].Group != "0" || rb.Fields["voltage"].Slot != "1" || ra.Fields["voltage"].Group != "" || ra.Fields["voltage"].Slot != "" {
		t.Fatalf("decoder context: %q/%q and %q/%q", rb.Fields["voltage"].Group, rb.Fields["voltage"].Slot, ra.Fields["voltage"].Group, ra.Fields["voltage"].Slot)
	}
	for id, ev := range eb {
		if ea[id].Reason != ev.Reason || (ev.Native != nil && ea[id].Native.Raw != ev.Native.Raw) {
			t.Errorf("%s differs with the group and slot absent", id)
		}
	}
	parallel := base
	parallel.Name = "parallel-bank-standard-313"
	parallel.Selection.Flavor, parallel.Selection.Bank = "pcs-parallel", "parallel"
	assertNoRowApplies(t, parallel)
	s, _, _ := build(t, base)
	spec := s.SelectionSpecValue()
	spec.Bank = "parallel"
	if _, err := canbusreg.NewSelection(spec); !errors.Is(err, canbusreg.ErrSelectionBankPairing) {
		t.Fatalf("pcs-standard with the parallel bank: %v", err)
	}
	s, in, frame := build(t, base)
	q := Qualifiers(s, in)
	q.Values[qBank] = "parallel"
	evals, _, _ := Evaluate(frame, q)
	for id, ev := range evals {
		if ev.Reason != roweval.ReasonNoMatch {
			t.Errorf("%s applies under the parallel bank", id)
		}
	}
}

// assertNoRowApplies checks that a parallel-flavor selection gets no standard row.
func assertNoRowApplies(t *testing.T, c vectorCase) {
	t.Helper()
	s, in, frame := build(t, c)
	if r := canbusreg.DecodePCSStandard(s, in); len(r.Fields) != 0 {
		t.Errorf("%s: decoder emitted %d fields", c.Name, len(r.Fields))
	}
	evals, _, _ := Evaluate(frame, Qualifiers(s, in))
	for id, ev := range evals {
		if ev.Reason != roweval.ReasonNoMatch {
			t.Errorf("%s: %s applies to the parallel flavor", c.Name, id)
		}
	}
}

func TestStandardMeasurementRowsKeepLegacyAndRelease11MeaningsApart(t *testing.T) {
	legacy := caseNamed(t, "legacy-local-measured-313")
	release := caseNamed(t, "release-11-local-average-temperature-313")
	rl, el, _ := evaluateCase(t, legacy)
	rr, er, _ := evaluateCase(t, release)
	if rl.Fields["voltage"].Meaning != "pack voltage" || rl.Fields["temperature"].Meaning != "coded minimum temperature" {
		t.Errorf("legacy meanings: %q, %q", rl.Fields["voltage"].Meaning, rl.Fields["temperature"].Meaning)
	}
	if rr.Fields["voltage"].Meaning != "output-port voltage" || rr.Fields["temperature"].Meaning != "count-aware fine average temperature" {
		t.Errorf("release-11 meanings: %q, %q", rr.Fields["voltage"].Meaning, rr.Fields["temperature"].Meaning)
	}
	for _, id := range []string{rowPrefix + "313.legacy.voltage", rowPrefix + "313.legacy.temperature"} {
		if el[id].Reason == roweval.ReasonNoMatch || er[id].Reason != roweval.ReasonNoMatch {
			t.Errorf("%s applies to the wrong contract", id)
		}
	}
	for _, id := range []string{rowPrefix + "313.release11.voltage", rowPrefix + "313.release11.temperature"} {
		if er[id].Reason == roweval.ReasonNoMatch || el[id].Reason != roweval.ReasonNoMatch {
			t.Errorf("%s applies to the wrong contract", id)
		}
	}
}
