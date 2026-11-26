package pcs

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// slotVectorFile is the synthetic vector file of the slot frames.
type slotVectorFile struct {
	Cases []slotVectorCase `json:"cases"`
}

type slotVectorCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  slotVectorSelection     `json:"selection"`
	Input      slotVectorInput         `json:"input"`
	Expect     map[string]vectorExpect `json:"expect"`
}

type slotVectorSelection struct {
	Project  string `json:"project"`
	Release  string `json:"release"`
	Choice   string `json:"choice"`
	Flavor   string `json:"flavor"`
	Bank     string `json:"bank"`
	Scope    string `json:"scope"`
	Endpoint string `json:"endpoint"`
	Group    string `json:"group"`
	Slot     string `json:"slot"`
}

type slotVectorInput struct {
	PublisherUnresolved bool              `json:"publisher_unresolved"`
	MeasurementValid    bool              `json:"measurement_valid"`
	SlotValid           bool              `json:"slot_valid"`
	NativeSlot          uint8             `json:"native_slot"`
	GroupValid          bool              `json:"group_valid"`
	NativeGroup         uint8             `json:"native_group"`
	Member              bool              `json:"member"`
	FieldStates         map[string]string `json:"field_states"`
	FieldAgesMS         map[string]int64  `json:"field_ages_ms"`
}

func loadSlotVectors(t *testing.T) []slotVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/standard_326_334.json")
	if err != nil {
		t.Fatal(err)
	}
	var f slotVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func slotCase(t *testing.T, name string) slotVectorCase {
	t.Helper()
	for _, c := range loadSlotVectors(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no vector named %s", name)
	return slotVectorCase{}
}

// slotFrame builds a frame of the standard identifier from its hexadecimal
// identifier and payload.
func slotFrame(t *testing.T, identifier, payload string) canbus.Frame {
	t.Helper()
	id, err := strconv.ParseUint(strings.TrimPrefix(identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := canbus.NewStandardID(uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(standard, data)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

// slotBuild turns one vector into the selection, the slot input and the frame.
// The member, when the vector names one, is the selection itself, so it shares
// the reporting member context the dictionary requires.
func slotBuild(t *testing.T, c slotVectorCase) (canbusreg.Selection, canbusreg.PCSSlotPCSInput, canbus.Frame) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := c.Selection
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Choice: sel.Choice, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame := slotFrame(t, c.Identifier, c.Payload)
	in := c.Input
	ages := make(map[string]time.Duration, len(in.FieldAgesMS))
	for field, ms := range in.FieldAgesMS {
		ages[field] = time.Duration(ms) * time.Millisecond
	}
	pcs := canbusreg.PCSSlotPCSInput{
		PCSIdentityPCSInput: canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: canbusreg.PCSStandardPCSInput{
			LINKInput:         canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second},
			PublisherResolved: !in.PublisherUnresolved,
			MeasurementValid:  in.MeasurementValid,
			FieldStates:       in.FieldStates,
			FieldAges:         ages,
		}},
		SlotValid:   in.SlotValid,
		GroupValid:  in.GroupValid,
		NativeSlot:  in.NativeSlot,
		NativeGroup: in.NativeGroup,
	}
	if in.Member {
		member := s
		pcs.Member = &member
	}
	return s, pcs, frame
}

// slotRowsByID maps every slot row identifier to its row.
func slotRowsByID() map[string]roweval.NativeRow[Selector] {
	rows := make(map[string]roweval.NativeRow[Selector])
	for _, row := range SlotTable().Native {
		rows[row.ID] = row
	}
	return rows
}

// slotRawWord is the big-endian 16-bit or the 8-bit word of the frame at the
// row's slice: the raw evidence a slot row must keep.
func slotRawWord(frame canbus.Frame, sl signalSlice) uint64 {
	p := frame.Bytes()
	at := int(sl.offset / 8)
	if sl.width == 16 {
		return uint64(p[at])<<8 | uint64(p[at+1])
	}
	return uint64(p[at])
}

// checkSlotCase compares every decoder field of one vector with the one row
// that applies to it. An applicable row the decoder does not emit must be
// absent (reason "absent"). A vector the decoder does not admit must be refused
// by the evaluator with the decoder's status and no rows. It returns the
// identifiers of the rows that applied.
func checkSlotCase(t *testing.T, c slotVectorCase) map[string]bool {
	t.Helper()
	s, in, frame := slotBuild(t, c)
	r := canbusreg.DecodePCSSlots(s, in)
	res, reason, err := EvaluateSlots(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	applied := make(map[string]bool)
	if r.Status != "decoded" {
		if reason != roweval.Reason(r.Status) || res.Rows != nil || len(r.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, evaluator reason %q", c.Name, r.Status, len(r.Fields), reason)
		}
		return applied
	}
	if reason != "" {
		t.Fatalf("%s: decoder admits the observation, evaluator refuses it (%q)", c.Name, reason)
	}
	if !reflect.DeepEqual(res.Evidence, in.Evidence) {
		t.Errorf("%s: evaluator evidence differs from the observation", c.Name)
	}
	for field, df := range r.Fields {
		if !reflect.DeepEqual(df.Evidence, res.Evidence) {
			t.Errorf("%s: %s decoder evidence differs from the evaluator's", c.Name, field)
		}
	}
	checkSlotAttribution(t, c.Name, res, r)
	rows := slotRowsByID()
	byField := make(map[string][]string)
	for id, ev := range res.Rows {
		if ev.Reason == roweval.ReasonNoMatch {
			continue
		}
		applied[id] = true
		byField[fieldOf(id)] = append(byField[fieldOf(id)], id)
	}
	for name, df := range r.Fields {
		ids := byField[name]
		if len(ids) != 1 {
			t.Errorf("%s: field %s has %d applied rows", c.Name, name, len(ids))
			continue
		}
		checkSlotField(t, c.Name, name, df, rows[ids[0]], res.Rows[ids[0]].Native, frame)
	}
	for field, ids := range byField {
		if _, emitted := r.Fields[field]; emitted {
			continue
		}
		for _, id := range ids {
			if n := res.Rows[id].Native; n == nil || n.Reason != "absent" {
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

// checkSlotAttribution compares the selection attribution the evaluator keeps
// with the group and slot of every decoder field: an absent coordinate is
// absent on both sides, and a present one has the same text.
func checkSlotAttribution(t *testing.T, name string, res SlotResult, r canbusreg.NativeResult) {
	t.Helper()
	a := res.Attribution
	for field, df := range r.Fields {
		if df.Scope != a.Scope || df.Bank != a.Bank {
			t.Errorf("%s: %s scope/bank %q/%q, evaluator %q/%q", name, field, df.Scope, df.Bank, a.Scope, a.Bank)
		}
		if df.Group != a.Group || df.Slot != a.Slot {
			t.Errorf("%s: %s group/slot %q/%q, evaluator %q/%q", name, field, df.Group, df.Slot, a.Group, a.Slot)
		}
		if (df.Group != "") != a.HasGroup || (df.Slot != "") != a.HasSlot {
			t.Errorf("%s: %s presence of group and slot differs from the evaluator", name, field)
		}
	}
}

// checkSlotField compares one decoder field with its row: the raw word, the
// status and, for an accepted field, the exact native value. A withheld field
// keeps its raw word and its reason; a dictionary index is not a wire word.
func checkSlotField(t *testing.T, name, field string, df canbusreg.NativeField, row roweval.NativeRow[Selector], nat *roweval.Native, frame canbus.Frame) {
	t.Helper()
	if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
		t.Errorf("%s: %s raw evidence differs from the frame", name, field)
	}
	if nat == nil {
		t.Fatalf("%s: %s has no native evaluation", name, field)
	}
	if !strings.HasSuffix(field, "_index") && nat.Raw != slotRawWord(frame, row.At.Slice) {
		t.Errorf("%s: %s raw %#x, frame word %#x", name, field, nat.Raw, slotRawWord(frame, row.At.Slice))
	}
	switch df.Status {
	case "accepted":
		if nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Fatalf("%s: %s accepted by the decoder, row reason %q", name, field, nat.Reason)
		}
		if nat.Disposition != dispositionAccepted {
			t.Errorf("%s: %s accepted by the decoder, row disposition %q", name, field, nat.Disposition)
		}
		if df.Unit != row.Unit {
			t.Errorf("%s: %s unit %q, row %q", name, field, df.Unit, row.Unit)
		}
		if decimalRat(t, nat.Value.Quantity.Number).Cmp(new(big.Rat).SetFrac64(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: %s row value differs from the decoder %d/%d", name, field, df.Numerator, df.Denominator)
		}
	case "raw":
		if nat.Reason != gateWithheld || nat.Disposition != dispositionRaw {
			t.Errorf("%s: %s raw in the decoder, row reason %q disposition %q", name, field, nat.Reason, nat.Disposition)
		}
	case "empty":
		if nat.Reason != emptyReason || nat.Disposition != dispositionEmpty {
			t.Errorf("%s: %s empty in the decoder, row reason %q disposition %q", name, field, nat.Reason, nat.Disposition)
		}
	case "invalid":
		if nat.Reason != roweval.ReasonOutsideDomain || nat.Value != nil {
			t.Errorf("%s: %s invalid in the decoder, row reason %q", name, field, nat.Reason)
		}
	case "absent", "stale", "unsupported", "conflict", "unassigned":
		if nat.Reason != roweval.Reason(df.Status) {
			t.Errorf("%s: %s %s in the decoder, row reason %q", name, field, df.Status, nat.Reason)
		}
	default:
		t.Errorf("%s: %s has unexpected status %q", name, field, df.Status)
	}
}

// slotValue is the exact native value of one applicable row of an evaluation.
func slotValue(t *testing.T, res SlotResult, id string) *big.Rat {
	t.Helper()
	ev, ok := res.Rows[id]
	if !ok || ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
		t.Fatalf("row %s has no value: %+v", id, ev)
	}
	return decimalRat(t, ev.Native.Value.Quantity.Number)
}

// TestSlotRowsMatchDecoder compares the slot rows with DecodePCSSlots on every
// synthetic vector: the same fields, statuses, raw words and exact values.
func TestSlotRowsMatchDecoder(t *testing.T) {
	n := 0
	for _, c := range loadSlotVectors(t) {
		checkSlotCase(t, c)
		n++
	}
	if n == 0 {
		t.Fatal("no slot vectors")
	}
}

// TestSlotVectorsCoverEveryRow pins that every slot row applies to at least one
// vector (an applicable absent row included), so no row is compared by nothing.
func TestSlotVectorsCoverEveryRow(t *testing.T) {
	applied := make(map[string]bool)
	for _, c := range loadSlotVectors(t) {
		for id := range checkSlotCase(t, c) {
			applied[id] = true
		}
	}
	for _, row := range SlotTable().Native {
		if !applied[row.ID] {
			t.Errorf("no vector applies to %s", row.ID)
		}
	}
}

// TestSlotCurrentWordOfFourThousandIsZeroOnOrdinaryBankAndMinusSixHundredOnParallelBank
// is the row form of growatt_pcs_slot_bank_test.go: the same release-11 0x326
// current word of 4000 is 0.0 A on the ordinary bank and -600.0 A on the
// parallel bank, each attributed to its own row. The name spells the numbers
// out because the behavior-name guard rejects a letter followed by digits.
func TestSlotCurrentWordOfFourThousandIsZeroOnOrdinaryBankAndMinusSixHundredOnParallelBank(t *testing.T) {
	cases := []struct {
		vector, id string
		other      string
		want       string
	}{
		{"slot-326-ordinary-word-4000", "growatt.bms.pcs.standard.326.current", "growatt.bms.pcs.parallel.326.current", "0"},
		{"slot-326-parallel-word-4000", "growatt.bms.pcs.parallel.326.current", "growatt.bms.pcs.standard.326.current", "-600"},
	}
	for _, tc := range cases {
		c := slotCase(t, tc.vector)
		s, in, _ := slotBuild(t, c)
		r := canbusreg.DecodePCSSlots(s, in)
		decoded := r.Fields["current"]
		if got := new(big.Rat).SetFrac64(decoded.Numerator, decoded.Denominator); got.Cmp(ratText(t, tc.want)) != 0 {
			t.Errorf("%s: decoder current %s, want %s", tc.vector, got.RatString(), tc.want)
		}
		res, reason, err := EvaluateSlots(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: evaluate refused: %v %v", tc.vector, reason, err)
		}
		if got := slotValue(t, res, tc.id); got.Cmp(ratText(t, tc.want)) != 0 {
			t.Errorf("%s: row %s value %s, want %s", tc.vector, tc.id, got.RatString(), tc.want)
		}
		if _, ok := res.Rows[tc.other]; ok {
			t.Errorf("%s: the other bank's row %s applies to this selection", tc.vector, tc.other)
		}
		if n := res.Rows[tc.id].Native; n.Unit != "A" || n.Disposition != dispositionAccepted {
			t.Errorf("%s: unit %q disposition %q", tc.vector, n.Unit, n.Disposition)
		}
	}
}

// TestSlotCurrentAndSystemCurrentAreDistinctRowsOnOneSelection evaluates the
// 0x313 system current and the 0x326 slot current on one release-11 ordinary
// selection: the two rows have distinct identifiers and keep their own values.
func TestSlotCurrentAndSystemCurrentAreDistinctRowsOnOneSelection(t *testing.T) {
	c := slotCase(t, "slot-326-ordinary-word-4000")
	s, slotIn, _ := slotBuild(t, c)
	slotRes, reason, err := EvaluateSlots(s, slotIn)
	if err != nil || reason != "" {
		t.Fatalf("slot evaluate refused: %v %v", reason, err)
	}
	const systemID, slotID = "growatt.bms.pcs.standard.313.current", "growatt.bms.pcs.standard.326.current"
	if systemID == slotID {
		t.Fatal("the 313 and 326 current share an identifier")
	}
	for _, row := range Table().Native {
		if strings.Contains(row.ID, ".326.") {
			t.Errorf("the standard table names a 326 row %s", row.ID)
		}
	}
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	frame := slotFrame(t, "0x313", "0000006400000000")
	standardIn := canbusreg.PCSStandardPCSInput{
		LINKInput:         canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: slotIn.Direction, Scope: slotIn.Scope, Bank: slotIn.Bank, Endpoint: slotIn.Endpoint, Group: slotIn.Group, Slot: slotIn.Slot, FieldAge: time.Second},
		PublisherResolved: true,
		MeasurementValid:  true,
	}
	systemRes, reason, err := Evaluate(s, standardIn)
	if err != nil || reason != "" {
		t.Fatalf("system evaluate refused: %v %v", reason, err)
	}
	if got := decimalRat(t, systemRes.Rows[systemID].Native.Value.Quantity.Number); got.Cmp(ratText(t, "10")) != 0 {
		t.Errorf("313 current %s, want 10", got.RatString())
	}
	if got := slotValue(t, slotRes, slotID); got.Cmp(ratText(t, "0")) != 0 {
		t.Errorf("326 current %s, want 0", got.RatString())
	}
	if _, ok := slotRes.Rows[systemID]; ok {
		t.Errorf("the slot observation evaluates the system row %s", systemID)
	}
}

// TestSlotTableValidatesWithoutAmbiguousMatch pins that the exported slot table
// passes the row-table validator, so a consumer can register it: the 0x333 word
// and its index companion share one selector and must not be ambiguous.
func TestSlotTableValidatesWithoutAmbiguousMatch(t *testing.T) {
	if err := roweval.ValidateTable(SlotTable(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatalf("slot table rejected: %v", err)
	}
}

// TestSlotCurrentRowsDeclareNoSlotRequirement pins that the 0x326 current rows
// declare no slot-coordinate requirement, as DecodePCSSlots has none: the
// coordinate is attribution, and the gate is the slot association the input
// supplies.
func TestSlotCurrentRowsDeclareNoSlotRequirement(t *testing.T) {
	rows := slotRowsByID()
	for _, id := range []string{"growatt.bms.pcs.standard.326.current", "growatt.bms.pcs.parallel.326.current"} {
		row, ok := rows[id]
		if !ok {
			t.Fatalf("no row %s", id)
		}
		if len(row.When.Requires) != 0 {
			t.Errorf("%s declares requires %v, want none", id, row.When.Requires)
		}
	}
}

// TestSlotCurrentRowsDeclareSlotContextWithoutCoordinate pins that the 0x326
// current rows declare their slot context in When, and that an observation
// with no slot coordinate still yields its current with the coordinate absent.
func TestSlotCurrentRowsDeclareSlotContextWithoutCoordinate(t *testing.T) {
	rows := slotRowsByID()
	for _, id := range []string{"growatt.bms.pcs.standard.326.current", "growatt.bms.pcs.parallel.326.current"} {
		row, ok := rows[id]
		if !ok {
			t.Fatalf("no row %s", id)
		}
		if row.When.Equal[qSlotContext] != slotFrameContext {
			t.Errorf("%s When does not declare the slot context: %v", id, row.When.Equal)
		}
	}
	c := slotCase(t, "slot-326-ordinary-word-4000")
	c.Selection.Slot = ""
	s, in, _ := slotBuild(t, c)
	if r := canbusreg.DecodePCSSlots(s, in); r.Status != "decoded" {
		t.Fatalf("decoder refuses the observation without a coordinate: %s", r.Status)
	}
	res, reason, err := EvaluateSlots(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused without a coordinate: %v %v", reason, err)
	}
	if got := slotValue(t, res, "growatt.bms.pcs.standard.326.current"); got.Cmp(ratText(t, "0")) != 0 {
		t.Errorf("current without a coordinate %s, want 0", got.RatString())
	}
	if res.Attribution.HasSlot || res.Attribution.Slot != "" {
		t.Errorf("coordinate present without a coordinate: %+v", res.Attribution)
	}
}

// TestSlotRowsRefuseUnpairedFlavorAndBank pins that a flavor and a bank that do
// not pair never make a selection, and that the rows refuse a pairing the
// selection would not give: no slot row applies to a mismatched qualifier set.
func TestSlotRowsRefuseUnpairedFlavorAndBank(t *testing.T) {
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ flavor, bank string }{{"pcs-standard", "parallel"}, {"pcs-parallel", "ordinary"}} {
		spec := canbusreg.SelectionSpec{Interface: iface, Flavor: pair.flavor, ProjectCode: "QAAA", Release: "0011", Direction: "bms-to-pcs", Scope: "local", Bank: pair.bank, Endpoint: "coordinator", Group: "0", Slot: "2", Epoch: 1}
		if _, err := canbusreg.NewSelection(spec); !errors.Is(err, canbusreg.ErrSelectionBankPairing) {
			t.Errorf("%s with bank %s: selection error %v, want the pairing refusal", pair.flavor, pair.bank, err)
		}
	}
	for _, c := range []slotVectorCase{slotCase(t, "slot-326-ordinary-word-4000"), slotCase(t, "slot-333-dictionary-index-5")} {
		s, in, frame := slotBuild(t, c)
		for _, mismatch := range []map[string]string{{qBank: "parallel"}, {qFlavor: "pcs-parallel"}} {
			q := slotQualifiers(s, in)
			for key, value := range mismatch {
				q.Values[key] = value
			}
			rows, err := evaluateSlotRows(SlotTable(), frame, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Errorf("%s with %v: %d rows apply to a mismatched pairing", c.Name, mismatch, len(rows))
			}
		}
	}
}

// TestSlotRowsKeepGroupAndSlotAbsentOrPresent pins the attribution: an absent
// group or slot stays absent in the evaluation and is never reported as zero,
// and a present one keeps its text.
func TestSlotRowsKeepGroupAndSlotAbsentOrPresent(t *testing.T) {
	absent := slotCase(t, "slot-326-ordinary-word-4000")
	absent.Selection.Group, absent.Selection.Slot = "", ""
	s, in, _ := slotBuild(t, absent)
	res, reason, err := EvaluateSlots(s, in)
	if err != nil || reason != "" {
		t.Fatalf("absent coordinates refused: %v %v", reason, err)
	}
	if res.Attribution.HasGroup || res.Attribution.HasSlot || res.Attribution.Group != "" || res.Attribution.Slot != "" {
		t.Errorf("absent group and slot reported as %+v", res.Attribution)
	}
	// DecodePCSSlots admits the current word of 4000 without a slot coordinate
	// (the gate is the input's slot association, not the coordinate text), so
	// the row keeps the decoded value while the coordinate stays absent.
	decoded := canbusreg.DecodePCSSlots(s, in).Fields["current"]
	if decoded.Status != "accepted" {
		t.Fatalf("decoder current status %q without a slot coordinate, want accepted", decoded.Status)
	}
	if got := slotValue(t, res, "growatt.bms.pcs.standard.326.current"); got.Cmp(ratText(t, "0")) != 0 {
		t.Errorf("slot current without a slot coordinate %s, want 0", got.RatString())
	}

	present := slotCase(t, "slot-333-dictionary-index-5")
	s, in, _ = slotBuild(t, present)
	res, reason, err = EvaluateSlots(s, in)
	if err != nil || reason != "" {
		t.Fatalf("present coordinates refused: %v %v", reason, err)
	}
	if !res.Attribution.HasGroup || !res.Attribution.HasSlot || res.Attribution.Group != "0" || res.Attribution.Slot != "2" {
		t.Errorf("present group and slot reported as %+v", res.Attribution)
	}
}

// TestSlotRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat pins
// that the slot rows refuse a neighbouring identifier, a frame of the wrong
// length and an extended identifier with the decoder's own status and no rows.
func TestSlotRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	base := slotCase(t, "slot-326-ordinary-word-4000")
	s, in, _ := slotBuild(t, base)
	iface := in.Evidence.Interface
	extended, err := canbus.NewExtendedID(0x326)
	if err != nil {
		t.Fatal(err)
	}
	extendedFrame, err := canbus.NewFrame(extended, []byte{0x02, 0x13, 0x88, 0x0F, 0xA0, 0x5A, 0x00, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	for name, frame := range map[string]canbus.Frame{
		"neighbour 0x325":    slotFrame(t, "0x325", "0213880FA05A0000"),
		"neighbour 0x335":    slotFrame(t, "0x335", "0213880FA05A0000"),
		"wrong length 0x326": slotFrame(t, "0x326", "0213880FA05A00"),
		"extended 0x326":     extendedFrame,
	} {
		probe := in
		probe.Evidence.Frame = frame
		probe.Evidence.Interface = iface
		res, reason, err := EvaluateSlots(s, probe)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if reason == "" || res.Rows != nil {
			t.Errorf("%s: evaluated with reason %q and %d rows", name, reason, len(res.Rows))
		}
	}
}

// TestSlotAcceptanceFilterSelectsEverySlotTest keeps the focused acceptance
// filter and the test names aligned: the filter is built from the names the
// ticket requires, and each must be a test function of this file that the
// filter selects. The bank-offset test carries its numbers spelled out, so a
// filter written with digits would silently skip it.
func TestSlotAcceptanceFilterSelectsEverySlotTest(t *testing.T) {
	required := []string{
		"TestSlotRowsMatchDecoder",
		"TestSlotCurrentWordOfFourThousandIsZeroOnOrdinaryBankAndMinusSixHundredOnParallelBank",
		"TestSlotCurrentAndSystemCurrentAreDistinctRowsOnOneSelection",
		"TestSlotRowsRefuseUnpairedFlavorAndBank",
		"TestSlotRowsKeepGroupAndSlotAbsentOrPresent",
		"TestSlotRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat",
	}
	file, err := parser.ParseFile(token.NewFileSet(), "standard_slots_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			declared[fn.Name.Name] = true
		}
	}
	filter := regexp.MustCompile(strings.Join(required, "|"))
	for _, name := range required {
		if !declared[name] {
			t.Errorf("required test %s is not declared", name)
		}
		if got := filter.FindString(name); got != name {
			t.Errorf("filter selects %q for %s", got, name)
		}
	}
	if !declared["TestSlotAcceptanceFilterSelectsEverySlotTest"] {
		t.Error("filter guard not declared")
	}
}
