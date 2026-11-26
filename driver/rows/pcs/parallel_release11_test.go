package pcs

import (
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type parallelVectorFile struct {
	Cases []parallelVectorCase `json:"cases"`
}

type parallelVectorCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  slotVectorSelection     `json:"selection"`
	Context    map[string]int64        `json:"context"`
	Expect     map[string]vectorExpect `json:"expect"`
	Refused    string                  `json:"refused"`
}

func loadParallelVectors(t *testing.T) []parallelVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/parallel_release11.json")
	if err != nil {
		t.Fatal(err)
	}
	var f parallelVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func parallelCase(t *testing.T, name string) parallelVectorCase {
	t.Helper()
	for _, c := range loadParallelVectors(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no vector named %s", name)
	return parallelVectorCase{}
}

func parallelSelectionSpec(t *testing.T, c parallelVectorCase) canbusreg.SelectionSpec {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := c.Selection
	return canbusreg.SelectionSpec{Interface: iface, Flavor: "pcs-parallel", ProjectCode: sel.Project, Release: sel.Release, Choice: sel.Choice, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: "parallel", Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
}

// parallelBuild turns one vector into the selection, the observation, the
// context and the frame. Each context source is an accepted, proven word
// attributed to the selection itself.
func parallelBuild(t *testing.T, c parallelVectorCase) (canbusreg.Selection, canbusreg.LINKInput, canbusreg.ParallelContext, canbus.Frame) {
	t.Helper()
	spec := parallelSelectionSpec(t, c)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame := slotFrame(t, c.Identifier, c.Payload)
	in := canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: spec.Interface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
	source := func(name string) *canbusreg.NativeField {
		n, ok := c.Context[name]
		if !ok {
			return nil
		}
		return &canbusreg.NativeField{Numerator: n, Denominator: 1, Grade: "Proven", Status: "accepted", Evidence: canbusreg.Evidence{Interface: spec.Interface}, Epoch: 1, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot}
	}
	x := canbusreg.ParallelContext{SOHTenths: source("soh_tenths"), SOCTenths: source("soc_tenths"), Permission: source("permission"), State: source("state"), CategoryFirst: source("category_first"), CategorySecond: source("category_second"), PackCode: source("pack_code"), CellSumValid: source("cell_sum_valid")}
	return s, in, x, frame
}

func parallelRowsByID() map[string]roweval.NativeRow[Selector] {
	rows := make(map[string]roweval.NativeRow[Selector])
	for _, row := range ParallelReleaseTable().Native {
		rows[row.ID] = row
	}
	return rows
}

// parallelRawWord is the raw word of the frame at the row's slice.
func parallelRawWord(frame canbus.Frame, sl signalSlice) uint64 {
	p := frame.Bytes()
	at := int(sl.offset / 8)
	if sl.order == bigEndian {
		return uint64(p[at])<<8 | uint64(p[at+1])
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(p[i]) << (8 * i)
	}
	return v >> sl.offset & (1<<sl.width - 1)
}

// checkParallelCase compares every decoder field of one vector with the one
// row that applies to it, and returns the identifiers of the rows that applied.
func checkParallelCase(t *testing.T, c parallelVectorCase) map[string]bool {
	t.Helper()
	s, in, x, frame := parallelBuild(t, c)
	r := canbusreg.ParallelReleaseDecodeParallel(s, in, x)
	res, reason, err := EvaluateParallelRelease(s, in, x)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	applied := make(map[string]bool)
	if c.Refused != "" || r.Status != "accepted" {
		if r.Status != c.Refused || reason != roweval.Reason(r.Status) || res.Rows != nil || len(r.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, evaluator reason %q, want status %q", c.Name, r.Status, len(r.Fields), reason, c.Refused)
		}
		return applied
	}
	if reason != "" {
		t.Fatalf("%s: decoder accepts the observation, evaluator refuses it (%q)", c.Name, reason)
	}
	if !reflect.DeepEqual(res.Evidence, in.Evidence) {
		t.Errorf("%s: evaluator evidence differs from the observation", c.Name)
	}
	rows := parallelRowsByID()
	byField := make(map[string][]string)
	for id, ev := range res.Rows {
		applied[id] = true
		byField[fieldOf(id)] = append(byField[fieldOf(id)], id)
		if ev.Native == nil && ev.Reason != roweval.ReasonNoMatch {
			t.Errorf("%s: row %s applies without a native evaluation", c.Name, id)
		}
	}
	a := res.Attribution
	for name, df := range r.Fields {
		if df.Scope != a.Scope || df.Bank != a.Bank || df.Group != a.Group || df.Slot != a.Slot || (df.Group != "") != a.HasGroup || (df.Slot != "") != a.HasSlot {
			t.Errorf("%s: %s attribution %q/%q/%q/%q differs from the evaluator's %+v", c.Name, name, df.Scope, df.Bank, df.Group, df.Slot, a)
		}
		if !reflect.DeepEqual(df.Evidence, res.Evidence) {
			t.Errorf("%s: %s decoder evidence differs from the evaluator's", c.Name, name)
		}
		ids := byField[name]
		if len(ids) != 1 {
			t.Errorf("%s: field %s has %d applied rows", c.Name, name, len(ids))
			continue
		}
		checkParallelField(t, c.Name, name, df, rows[ids[0]], res.Rows[ids[0]].Native, frame)
	}
	for field, ids := range byField {
		if _, emitted := r.Fields[field]; emitted {
			continue
		}
		for _, id := range ids {
			if n := res.Rows[id].Native; n == nil || n.Reason != roweval.Reason(stateAbsent) {
				t.Errorf("%s: row %s applies but the decoder emits no %s", c.Name, id, field)
			}
		}
	}
	for name, want := range c.Expect {
		df, ok := r.Fields[name]
		if !ok || df.Status != want.Status {
			t.Errorf("%s: %s status %q, want %q", c.Name, name, df.Status, want.Status)
			continue
		}
		if want.Value == "" {
			continue
		}
		if got := new(big.Rat).SetFrac64(df.Numerator, df.Denominator); got.Cmp(ratText(t, want.Value)) != 0 {
			t.Errorf("%s: %s decoder value %s, want %s", c.Name, name, got.RatString(), want.Value)
		}
	}
	return applied
}

func checkParallelField(t *testing.T, name, field string, df canbusreg.NativeField, row roweval.NativeRow[Selector], nat *roweval.Native, frame canbus.Frame) {
	t.Helper()
	if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
		t.Errorf("%s: %s raw evidence differs from the frame", name, field)
	}
	if nat == nil {
		t.Fatalf("%s: %s has no native evaluation", name, field)
	}
	if !strings.HasSuffix(field, "_index") && nat.Raw != parallelRawWord(frame, row.At.Slice) {
		t.Errorf("%s: %s raw %#x, frame word %#x", name, field, nat.Raw, parallelRawWord(frame, row.At.Slice))
	}
	switch df.Status {
	case "accepted", "raw":
		if nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Fatalf("%s: %s %s in the decoder, row reason %q", name, field, df.Status, nat.Reason)
		}
		want := dispositionAccepted
		if df.Status == "raw" {
			want = dispositionRaw
		}
		if nat.Disposition != want || df.Unit != row.Unit {
			t.Errorf("%s: %s disposition %q unit %q, want %q and decoder unit %q", name, field, nat.Disposition, row.Unit, want, df.Unit)
		}
		if decimalRat(t, nat.Value.Quantity.Number).Cmp(new(big.Rat).SetFrac64(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: %s row value differs from the decoder %d/%d", name, field, df.Numerator, df.Denominator)
		}
	case "withheld":
		if nat.Reason != gateWithheld || nat.Disposition != dispositionRaw || nat.Value != nil {
			t.Errorf("%s: %s withheld in the decoder, row reason %q disposition %q", name, field, nat.Reason, nat.Disposition)
		}
	case "conflict":
		if nat.Reason != "conflict" || nat.Value != nil {
			t.Errorf("%s: %s conflict in the decoder, row reason %q", name, field, nat.Reason)
		}
	case "invalid":
		if nat.Reason != roweval.ReasonOutsideDomain || nat.Value != nil {
			t.Errorf("%s: %s invalid in the decoder, row reason %q", name, field, nat.Reason)
		}
	case "empty":
		if nat.Reason != emptyReason || nat.Disposition != dispositionEmpty {
			t.Errorf("%s: %s empty in the decoder, row reason %q disposition %q", name, field, nat.Reason, nat.Disposition)
		}
	case "unknown":
		if nat.Reason != unknownReason || nat.Value != nil {
			t.Errorf("%s: %s unknown in the decoder, row reason %q", name, field, nat.Reason)
		}
	default:
		t.Errorf("%s: %s has unexpected status %q", name, field, df.Status)
	}
}

// TestParallelReleaseRowsMatchDecoder compares the parallel rows with
// ParallelReleaseDecodeParallel on every synthetic vector: the same fields,
// statuses, raw words, units and exact values.
func TestParallelReleaseRowsMatchDecoder(t *testing.T) {
	n := 0
	for _, c := range loadParallelVectors(t) {
		checkParallelCase(t, c)
		n++
	}
	if n == 0 {
		t.Fatal("no parallel vectors")
	}
}

// TestParallelVectorsCoverEveryRow pins that every row applies to at least one
// vector, so no row is compared by nothing.
func TestParallelVectorsCoverEveryRow(t *testing.T) {
	applied := make(map[string]bool)
	for _, c := range loadParallelVectors(t) {
		for id := range checkParallelCase(t, c) {
			applied[id] = true
		}
	}
	for _, row := range ParallelReleaseTable().Native {
		if !applied[row.ID] {
			t.Errorf("no vector applies to %s", row.ID)
		}
	}
}

// TestParallelReleaseRowsCarryParallelBank pins that every row states the
// parallel bank, its paired flavor and the release-11 group in its When; that
// the evaluation attributes each value to the parallel bank; and that no row
// applies when the selection supplies the ordinary bank, the standard flavor or
// another contract group.
func TestParallelReleaseRowsCarryParallelBank(t *testing.T) {
	for _, row := range ParallelReleaseTable().Native {
		eq := row.When.Equal
		if eq[qFlavor] != "pcs-parallel" || eq[qBank] != "parallel" || eq[qContractGroup] != "L8" {
			t.Errorf("%s When %v does not carry the parallel bank, flavor and group", row.ID, eq)
		}
		if !strings.HasPrefix(row.ID, "growatt.bms.pcs.parallel.") {
			t.Errorf("%s is not a parallel-flavor identifier", row.ID)
		}
	}
	mismatches := []map[string]string{{qBank: "ordinary"}, {qFlavor: "pcs-standard"}, {qContractGroup: "L7"}}
	for _, c := range loadParallelVectors(t) {
		if c.Refused != "" {
			continue
		}
		s, in, x, frame := parallelBuild(t, c)
		res, reason, err := EvaluateParallelRelease(s, in, x)
		if err != nil || reason != "" {
			t.Fatalf("%s: evaluate refused: %v %v", c.Name, reason, err)
		}
		if res.Attribution.Bank != "parallel" {
			t.Errorf("%s: attributed to bank %q", c.Name, res.Attribution.Bank)
		}
		family, _ := parallelFamily(frame)
		for _, mismatch := range mismatches {
			q := parallelQualifiers(s, in, x, family)
			for k, v := range mismatch {
				q.Values[k] = v
			}
			rows, err := evaluateParallelRows(ParallelReleaseTable(), frame, family, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Errorf("%s with %v: %d rows apply", c.Name, mismatch, len(rows))
			}
		}
	}
}

// TestParallelReleaseRowsRefuseNeighbouringFamilyWrongLengthAndExtendedFormat
// pins that a neighbouring family, a group nibble that is not the selection's,
// a frame of the wrong length and an extended identifier are refused with the
// decoder's own status and no rows.
func TestParallelReleaseRowsRefuseNeighbouringFamilyWrongLengthAndExtendedFormat(t *testing.T) {
	base := parallelCase(t, "family-610-local-measurements")
	s, in, x, _ := parallelBuild(t, base)
	extended, err := canbus.NewExtendedID(0x611)
	if err != nil {
		t.Fatal(err)
	}
	extendedFrame, err := canbus.NewFrame(extended, []byte{0xF4, 0x81, 0x1C, 0x25, 0xE0, 0x01, 0x57, 0x03})
	if err != nil {
		t.Fatal(err)
	}
	for name, frame := range map[string]canbus.Frame{
		"neighbour family 0x601":   slotFrame(t, "0x601", base.Payload),
		"neighbour family 0x631":   slotFrame(t, "0x631", base.Payload),
		"neighbour family 0x6C1":   slotFrame(t, "0x6C1", base.Payload),
		"neighbour family 0x501":   slotFrame(t, "0x501", base.Payload),
		"other group nibble 0x612": slotFrame(t, "0x612", base.Payload),
		"wrong length 0x611":       slotFrame(t, "0x611", "F4811C25E00157"),
		"extended 0x611":           extendedFrame,
	} {
		probe := in
		probe.Evidence.Frame = frame
		res, reason, err := EvaluateParallelRelease(s, probe, x)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if reason == "" || res.Rows != nil {
			t.Errorf("%s: evaluated with reason %q and %d rows", name, reason, len(res.Rows))
		}
	}
	other, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: in.Evidence.Interface, Flavor: "pcs-parallel", ProjectCode: "QAAB", Release: "0006", Direction: "bms-to-pcs", Scope: "local", Bank: "parallel", Endpoint: "coordinator", Group: "1", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, err := EvaluateParallelRelease(other, in, x); err != nil || reason == "" {
		t.Errorf("a contract group other than the release-11 group evaluated: reason %q err %v", reason, err)
	}
}

// TestParallelReleaseTableValidatesWithoutAmbiguousMatch pins that the table
// passes the row-table validator, so a consumer can register it: the scope
// variants, the dictionary word and its index companion share selectors and
// must not be ambiguous.
func TestParallelReleaseTableValidatesWithoutAmbiguousMatch(t *testing.T) {
	if err := roweval.ValidateTable(ParallelReleaseTable(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatalf("parallel table rejected: %v", err)
	}
}

// TestParallelReleaseCurrentWordIsBiasedByTenThousandOnTheParallelBank pins the
// 610 current as the biased word of the parallel family: a word of 10000 is 0 A
// and a word of 9500 is -50 A, with the unit and the exact value.
func TestParallelReleaseCurrentWordIsBiasedByTenThousandOnTheParallelBank(t *testing.T) {
	for payload, want := range map[string]string{"F4811027E0015703": "0", "F4811C25E0015703": "-50", "F481F82AE0015703": "100"} {
		c := parallelCase(t, "family-610-local-measurements")
		c.Payload = payload
		s, in, x, _ := parallelBuild(t, c)
		res, reason, err := EvaluateParallelRelease(s, in, x)
		if err != nil || reason != "" {
			t.Fatalf("%s: %v %v", payload, reason, err)
		}
		n := res.Rows["growatt.bms.pcs.parallel.610.current"].Native
		if n == nil || n.Unit != "A" || decimalRat(t, n.Value.Quantity.Number).Cmp(ratText(t, want)) != 0 {
			t.Errorf("%s: current %+v, want %s A", payload, n, want)
		}
	}
}

// TestParallelReleaseScopeSelectsTheConversionOfTheLocalFields pins that a
// field with a scope-specific conversion has one row per scope and that only
// the row of the selection's scope applies.
func TestParallelReleaseScopeSelectsTheConversionOfTheLocalFields(t *testing.T) {
	for _, tc := range []struct {
		vector, applies, absent string
	}{
		{"family-610-local-measurements", "growatt.bms.pcs.parallel.610.local.port", "growatt.bms.pcs.parallel.610.aggregate.port"},
		{"family-610-aggregate-port-is-raw", "growatt.bms.pcs.parallel.610.aggregate.port", "growatt.bms.pcs.parallel.610.local.port"},
		{"family-650-local-temperatures-and-soh", "growatt.bms.pcs.parallel.650.local.temperature_maximum", "growatt.bms.pcs.parallel.650.aggregate.temperature_maximum"},
		{"family-6d0-aggregate-words-are-raw", "growatt.bms.pcs.parallel.6D0.aggregate.cell_sum", "growatt.bms.pcs.parallel.6D0.local.cell_sum"},
		{"family-680-local-reported-soc", "growatt.bms.pcs.parallel.680.local.soc_report", "growatt.bms.pcs.parallel.680.aggregate.soc_report"},
	} {
		s, in, x, _ := parallelBuild(t, parallelCase(t, tc.vector))
		res, reason, err := EvaluateParallelRelease(s, in, x)
		if err != nil || reason != "" {
			t.Fatalf("%s: %v %v", tc.vector, reason, err)
		}
		if _, ok := res.Rows[tc.applies]; !ok {
			t.Errorf("%s: row %s does not apply", tc.vector, tc.applies)
		}
		if _, ok := res.Rows[tc.absent]; ok {
			t.Errorf("%s: row %s applies under the other scope", tc.vector, tc.absent)
		}
	}
}

// TestParallelReleaseFramesWithoutDecodedFieldsHaveNoRow pins that the
// identifiers whose fields the decoder does not emit have no row: the decoder
// keeps 0x37F and 0x3F7 as raw status and does not decode 0x6FF.
func TestParallelReleaseFramesWithoutDecodedFieldsHaveNoRow(t *testing.T) {
	for _, row := range ParallelReleaseTable().Native {
		switch row.At.Identifier {
		case 0x37f, 0x3f7, 0x6f0, 0x6ff:
			t.Errorf("row %s declares an identifier the decoder emits no field for", row.ID)
		}
	}
	for _, name := range []string{"family-37f-emits-no-field", "family-3f7-emits-no-field", "family-6ff-is-not-decoded"} {
		checkParallelCase(t, parallelCase(t, name))
	}
}

// TestParallelReleaseRowsKeepGroupAndSlotAbsentOrPresent pins the attribution:
// an absent slot stays absent, a present coordinate keeps its text, and an
// addressed family without a group is refused as the decoder refuses it.
func TestParallelReleaseRowsKeepGroupAndSlotAbsentOrPresent(t *testing.T) {
	present := parallelCase(t, "family-510-local-release-11")
	s, in, x, _ := parallelBuild(t, present)
	res, reason, err := EvaluateParallelRelease(s, in, x)
	if err != nil || reason != "" {
		t.Fatalf("present coordinates refused: %v %v", reason, err)
	}
	if a := res.Attribution; !a.HasGroup || !a.HasSlot || a.Group != "1" || a.Slot != "2" {
		t.Errorf("present group and slot reported as %+v", a)
	}
	absent := parallelCase(t, "family-610-local-measurements")
	s, in, x, _ = parallelBuild(t, absent)
	res, reason, err = EvaluateParallelRelease(s, in, x)
	if err != nil || reason != "" {
		t.Fatalf("absent slot refused: %v %v", reason, err)
	}
	if a := res.Attribution; a.HasSlot || a.Slot != "" || !a.HasGroup {
		t.Errorf("absent slot reported as %+v", a)
	}
	absent.Selection.Group = ""
	s, in, x, _ = parallelBuild(t, absent)
	if _, reason, err = EvaluateParallelRelease(s, in, x); err != nil || reason != "invalid" {
		t.Errorf("an absent group on an addressed family: reason %q err %v, want the decoder's invalid", reason, err)
	}
}
