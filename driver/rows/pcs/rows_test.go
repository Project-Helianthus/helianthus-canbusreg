package pcs

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
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
	MeasurementValid    bool              `json:"measurement_valid"`
	TemperatureValid    bool              `json:"temperature_valid"`
	CountValid          bool              `json:"count_valid"`
	Count               uint16            `json:"count"`
	PublisherUnresolved bool              `json:"publisher_unresolved"`
	FieldStates         map[string]string `json:"field_states"`
	FieldAgesMS         map[string]int64  `json:"field_ages_ms"`
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
	ages := make(map[string]time.Duration, len(in.FieldAgesMS))
	for field, ms := range in.FieldAgesMS {
		ages[field] = time.Duration(ms) * time.Millisecond
	}
	return s, canbusreg.PCSStandardPCSInput{LINKInput: canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}, PublisherResolved: !in.PublisherUnresolved, MeasurementValid: in.MeasurementValid, TemperatureValid: in.TemperatureValid, CountValid: in.CountValid, Count: in.Count, FieldStates: in.FieldStates, FieldAges: ages}, frame
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

// gatedRows reports, for each native row identifier, whether the row names a
// gate. Every row has a hook; only a gated row withholds a raw field.
func gatedRows() map[string]bool {
	gated := make(map[string]bool)
	for _, s := range standardSpecs() {
		gated[nativeRow(s).ID] = s.gate != ""
	}
	return gated
}

// evaluateCase runs the decoder and the evaluator on one admitted vector and
// returns both with the frame.
func evaluateCase(t *testing.T, c vectorCase) (canbusreg.PCSStandardPCSResult, Result, canbus.Frame) {
	t.Helper()
	s, in, frame := build(t, c)
	res, reason, err := Evaluate(s, in)
	if err != nil || reason != "" {
		t.Fatalf("%s: evaluate refused: %v %v", c.Name, reason, err)
	}
	return canbusreg.DecodePCSStandard(s, in), res, frame
}

// checkEvidence compares the evidence the evaluator retains with the observation
// and with the evidence of every decoder field: both sides must name the same
// receipt and frame.
func checkEvidence(t *testing.T, name string, res Result, in canbusreg.PCSStandardPCSInput, r canbusreg.PCSStandardPCSResult) {
	t.Helper()
	if !reflect.DeepEqual(res.Evidence, in.Evidence) {
		t.Errorf("%s: evaluator evidence differs from the observation", name)
	}
	for field, df := range r.Fields {
		if !reflect.DeepEqual(df.Evidence, res.Evidence) {
			t.Errorf("%s: %s decoder evidence differs from the evaluator's", name, field)
		}
	}
}

// checkAttribution compares the selection attribution the evaluator retains with
// the group and slot of every decoder field: an absent coordinate is absent on
// both sides, and a present one has the same text.
func checkAttribution(t *testing.T, name string, res Result, r canbusreg.PCSStandardPCSResult) {
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

// checkCase compares every decoder field of a vector with the one row that
// applies to it. A vector the decoder does not admit must be refused by the
// evaluator with the decoder's status and no rows. It returns the identifiers
// of the rows that applied.
func checkCase(t *testing.T, c vectorCase) map[string]bool {
	t.Helper()
	s, in, frame := build(t, c)
	r := canbusreg.DecodePCSStandard(s, in)
	res, reason, err := Evaluate(s, in)
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
	for _, row := range Table().Native {
		rows[row.ID] = row
	}
	gated := gatedRows()
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

// checkField compares one decoder field with its row: the raw word, the status
// and, for an accepted field, the exact native value. A field the decoder
// withholds by an explicit state gives that state as the row reason.
func checkField(t *testing.T, name, field string, df canbusreg.NativeField, row roweval.NativeRow[Selector], gated bool, nat *roweval.Native, frame canbus.Frame) {
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
		if nat.Disposition != dispositionRaw {
			t.Errorf("%s: %s raw in the decoder, row disposition %q", name, field, nat.Disposition)
		}
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
		if nat.Value != nil || nat.Unit != "" {
			t.Errorf("%s: %s invalid in the decoder, row still carries a typed value in %q", name, field, nat.Unit)
		}
	case "absent", "stale", "unsupported", "conflict", "unassigned":
		if nat.Reason != roweval.Reason(df.Status) {
			t.Errorf("%s: %s %s in the decoder, row reason %q", name, field, df.Status, nat.Reason)
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

// TestStandardRowsFollowDecoderAdmission pins that the evaluator refuses every
// observation the decoder refuses, with the decoder's status and no rows: an
// unresolved publisher, a direction, bank, endpoint or age mismatch.
func TestStandardRowsFollowDecoderAdmission(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	cases := []struct {
		name   string
		mutate func(*canbusreg.PCSStandardPCSInput)
		status string
	}{
		{"publisher unresolved", func(in *canbusreg.PCSStandardPCSInput) { in.PublisherResolved = false }, "conflict"},
		{"direction mismatch", func(in *canbusreg.PCSStandardPCSInput) { in.Direction = "pcs-to-bms" }, "invalid"},
		{"bank mismatch", func(in *canbusreg.PCSStandardPCSInput) { in.Bank = "parallel" }, "invalid"},
		{"endpoint mismatch", func(in *canbusreg.PCSStandardPCSInput) { in.Endpoint = "gateway" }, "invalid"},
		{"negative frame age", func(in *canbusreg.PCSStandardPCSInput) { in.FieldAge = -time.Second }, "invalid"},
	}
	for _, tc := range cases {
		s, in, _ := build(t, base)
		tc.mutate(&in)
		r := canbusreg.DecodePCSStandard(s, in)
		if r.Status != tc.status || len(r.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, want %q", tc.name, r.Status, len(r.Fields), tc.status)
		}
		res, reason, err := Evaluate(s, in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if reason != roweval.Reason(tc.status) || res.Rows != nil {
			t.Errorf("%s: evaluator reason %q with rows %v, want %q and none", tc.name, reason, res.Rows != nil, tc.status)
		}
	}
}

// TestStandardRowsKeepObservationEvidence pins that two receipts of the same
// bytes and selection are told apart in the evaluation: the evaluator keeps the
// receipt of each observation, the decoder agrees with it, and the row values
// are the same.
func TestStandardRowsKeepObservationEvidence(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	s, first, _ := build(t, base)
	firstRes, reason, err := Evaluate(s, first)
	if err != nil || reason != "" {
		t.Fatalf("first receipt: %q %v", reason, err)
	}
	later := first
	later.Evidence.Sequence++
	laterRes, reason, err := Evaluate(s, later)
	if err != nil || reason != "" {
		t.Fatalf("later receipt: %q %v", reason, err)
	}
	if firstRes.Evidence.Sequence == laterRes.Evidence.Sequence {
		t.Fatal("two receipts of the same frame keep one sequence")
	}
	checkEvidence(t, "later receipt", laterRes, later, canbusreg.DecodePCSStandard(s, later))
	for id, ev := range firstRes.Rows {
		other := laterRes.Rows[id]
		if ev.Reason != other.Reason || (ev.Native != nil && other.Native != nil && ev.Native.Raw != other.Native.Raw) {
			t.Errorf("%s differs between receipts of the same bytes", id)
		}
	}
}

// TestStandardRowsRetainSeparateNativeRecord pins that the evaluation keeps a
// native record supplied apart from the transport evidence. A 0x313 record given
// only in NativeRecord, with an empty raw record in the evidence, is decoded, and
// the evaluator returns the same record bytes the decoder retains.
func TestStandardRowsRetainSeparateNativeRecord(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	s, in, frame := build(t, base)
	record := make([]byte, canbus.SocketCANRecordSize)
	binary.NativeEndian.PutUint32(record[:4], frame.ID().Value())
	record[4] = byte(len(frame.Data()))
	copy(record[8:], frame.Data())
	in.NativeRecord = record
	in.Evidence.RawRecord = [canbus.SocketCANRecordSize]byte{}
	r := canbusreg.DecodePCSStandard(s, in)
	if r.Status != "decoded" {
		t.Fatalf("decoder status %q, want decoded", r.Status)
	}
	res, reason, err := Evaluate(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused: %q %v", reason, err)
	}
	if !bytes.Equal(res.Diagnostics, record) {
		t.Errorf("evaluator keeps %x, want the supplied record %x", res.Diagnostics, record)
	}
	if !bytes.Equal(res.Diagnostics, r.Diagnostics) {
		t.Errorf("evaluator record %x differs from the decoder record %x", res.Diagnostics, r.Diagnostics)
	}
}

// TestStandardRowsKeepRecordOfRejectedObservation pins that a rejected
// observation, whether conflicted (unresolved publisher) or invalid (epoch
// mismatch), keeps the separately supplied native record and emits no rows.
func TestStandardRowsKeepRecordOfRejectedObservation(t *testing.T) {
	base := caseNamed(t, "legacy-local-measured-313")
	cases := map[string]struct {
		mutate func(*canbusreg.PCSStandardPCSInput)
		reason roweval.Reason
	}{
		"conflict": {func(in *canbusreg.PCSStandardPCSInput) { in.PublisherResolved = false }, "conflict"},
		"invalid":  {func(in *canbusreg.PCSStandardPCSInput) { in.Epoch++ }, "invalid"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, in, frame := build(t, base)
			record := make([]byte, canbus.SocketCANRecordSize)
			binary.NativeEndian.PutUint32(record[:4], frame.ID().Value())
			record[4] = byte(len(frame.Data()))
			copy(record[8:], frame.Data())
			in.NativeRecord = record
			in.Evidence.RawRecord = [canbus.SocketCANRecordSize]byte{}
			tc.mutate(&in)
			want := canbusreg.DecodePCSStandard(s, in)
			res, reason, err := Evaluate(s, in)
			if err != nil || reason != tc.reason || want.Status != string(tc.reason) {
				t.Fatalf("reason %q err %v decoder status %q, want %q", reason, err, want.Status, tc.reason)
			}
			if !bytes.Equal(res.Diagnostics, record) || !bytes.Equal(res.Diagnostics, want.Diagnostics) {
				t.Errorf("evaluator keeps %x, want the supplied record %x", res.Diagnostics, record)
			}
			if len(res.Rows) != 0 {
				t.Errorf("rejected observation emitted %d rows", len(res.Rows))
			}
		})
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

// assertRefused checks that the decoder yields no field and that the evaluator
// refuses the frame by the frame disposition: no rows, reason no_match.
func assertRefused(t *testing.T, name string, s canbusreg.Selection, in canbusreg.PCSStandardPCSInput, frame canbus.Frame) {
	t.Helper()
	in.Evidence.Frame = frame
	r := canbusreg.DecodePCSStandard(s, in)
	if len(r.Fields) != 0 {
		t.Errorf("%s: decoder emitted %d fields", name, len(r.Fields))
	}
	res, reason, err := Evaluate(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if reason != roweval.ReasonNoMatch || res.Rows != nil {
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
	gotB, gotA := attributionOf(t, base), attributionOf(t, absent)
	if want := (Attribution{Scope: "local", Bank: "ordinary", Group: "0", Slot: "1", HasGroup: true, HasSlot: true}); gotB != want {
		t.Errorf("selected group and slot: %+v, want %+v", gotB, want)
	}
	if want := (Attribution{Scope: "local", Bank: "ordinary"}); gotA != want {
		t.Errorf("absent group and slot: %+v, want %+v", gotA, want)
	}

	if rb.Fields["voltage"].Group != "0" || rb.Fields["voltage"].Slot != "1" || ra.Fields["voltage"].Group != "" || ra.Fields["voltage"].Slot != "" {
		t.Fatalf("decoder context: %q/%q and %q/%q", rb.Fields["voltage"].Group, rb.Fields["voltage"].Slot, ra.Fields["voltage"].Group, ra.Fields["voltage"].Slot)
	}
	for id, ev := range eb.Rows {
		other := ea.Rows[id].Native
		if ea.Rows[id].Reason != ev.Reason || (ev.Native != nil && (other == nil || other.Raw != ev.Native.Raw)) {
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
	// The evaluator's own bank qualifier is tested on the rows directly: a
	// deliberate rejection gives every row no_match with no native value, and
	// an evaluation failure is an error or a non-empty reason, never a silent pass.
	s, in, frame := build(t, base)
	q := Qualifiers(s, in)
	q.Values[qBank] = "parallel"
	evals, reason, err := evaluateRows(Table(), frame, q)
	if err != nil {
		t.Fatalf("parallel-bank qualifier: %v", err)
	}
	if reason != "" {
		t.Fatalf("parallel-bank qualifier refused the frame (%q), not the bank", reason)
	}
	if len(evals) == 0 {
		t.Fatal("no row was evaluated under the parallel-bank qualifier")
	}
	for id, ev := range evals {
		if ev.Reason != roweval.ReasonNoMatch || ev.Native != nil {
			t.Errorf("%s applies under the parallel bank (reason %q)", id, ev.Reason)
		}
	}
}

// assertNoRowApplies checks that a parallel-flavor selection gets no standard
// row: the decoder yields no field and the evaluator refuses with its status.
func assertNoRowApplies(t *testing.T, c vectorCase) {
	t.Helper()
	s, in, _ := build(t, c)
	r := canbusreg.DecodePCSStandard(s, in)
	if len(r.Fields) != 0 {
		t.Errorf("%s: decoder emitted %d fields", c.Name, len(r.Fields))
	}
	res, reason, err := Evaluate(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	if reason != roweval.Reason(r.Status) || res.Rows != nil {
		t.Errorf("%s: evaluator reason %q with rows, decoder status %q", c.Name, reason, r.Status)
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
		if el.Rows[id].Reason == roweval.ReasonNoMatch || er.Rows[id].Reason != roweval.ReasonNoMatch {
			t.Errorf("%s applies to the wrong contract", id)
		}
	}
	for _, id := range []string{rowPrefix + "313.release11.voltage", rowPrefix + "313.release11.temperature"} {
		if er.Rows[id].Reason == roweval.ReasonNoMatch || el.Rows[id].Reason != roweval.ReasonNoMatch {
			t.Errorf("%s applies to the wrong contract", id)
		}
	}
}

// attributionOf evaluates one admitted vector and returns the attribution its
// evaluation retains.
func attributionOf(t *testing.T, c vectorCase) Attribution {
	t.Helper()
	_, res, _ := evaluateCase(t, c)
	return res.Attribution
}

// TestStandardRowsDeclaredGeometryMustMatchFrame pins that the table's declared
// identifier, format, DLC and raw DLC are compared with the observation. A row
// whose declared geometry differs from the frame applies to no observation of
// that frame, so a misdeclared table cannot pass the parity harness.
func TestStandardRowsDeclaredGeometryMustMatchFrame(t *testing.T) {
	c := caseNamed(t, "legacy-local-measured-313")
	s, in, frame := build(t, c)
	base := Table()
	if evals, _, _ := evaluateRows(base, frame, Qualifiers(s, in)); len(evals) == 0 {
		t.Fatal("the unmodified table applies no row to the frame")
	}
	mutations := map[string]func(*Selector){
		"raw DLC 7":  func(a *Selector) { a.RawDLC = 7 },
		"DLC 7":      func(a *Selector) { a.DLC = 7 },
		"identifier": func(a *Selector) { a.Identifier = 0x312 },
		"extended":   func(a *Selector) { a.Extended = true },
	}
	for name, mutate := range mutations {
		table := roweval.Table[Selector]{Hooks: base.Hooks}
		for _, row := range base.Native {
			mutate(&row.At)
			table.Native = append(table.Native, row)
		}
		evals, _, err := evaluateRows(table, frame, Qualifiers(s, in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for id, ev := range evals {
			if ev.Native != nil || ev.Reason != roweval.ReasonNoMatch {
				t.Errorf("%s: %s applies to the frame (reason %q)", name, id, ev.Reason)
			}
		}
	}
}

// TestStandardRawRowsAreRawDispositions pins that a field the decoder never
// interprets keeps its raw word and is marked raw, while an accepted field is
// marked accepted, so a consumer tells the two apart in the native result.
func TestStandardRawRowsAreRawDispositions(t *testing.T) {
	c := caseNamed(t, "summary-words-312")
	_, res, _ := evaluateCase(t, c)
	for _, id := range []string{rowPrefix + "312.reported_count", rowPrefix + "312.status_a"} {
		nat := res.Rows[id].Native
		if nat == nil || nat.Value == nil || nat.Reason != "" {
			t.Fatalf("%s: no value", id)
		}
		if nat.Disposition != dispositionRaw {
			t.Errorf("%s: disposition %q, want raw", id, nat.Disposition)
		}
	}
	if nat := res.Rows[rowPrefix+"312.reported_count"].Native; nat.Raw != 3 {
		t.Errorf("reported_count raw %d, want 3", nat.Raw)
	}
	_, res, _ = evaluateCase(t, caseNamed(t, "legacy-local-measured-313"))
	if nat := res.Rows[rowPrefix+"313.legacy.voltage"].Native; nat == nil || nat.Disposition != dispositionAccepted {
		t.Errorf("accepted voltage disposition %+v", nat)
	}
}
