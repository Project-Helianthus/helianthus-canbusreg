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
		if nat.Unit != df.Unit || string(nat.Value.Quantity.Unit) != df.Unit {
			t.Errorf("%s: %s returned units %q and %q, decoder %q", name, field, nat.Unit, nat.Value.Quantity.Unit, df.Unit)
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
		assertRefused(t, fmt.Sprintf("neighbour %#x", id), s, in, frame, "unsupported")
	}
	short, _ := canbus.NewStandardID(0x313)
	frame, _ := canbus.NewFrame(short, []byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 0x64})
	assertRefused(t, "wrong length", s, in, frame, "invalid")
	extended, _ := canbus.NewExtendedID(0x313)
	frame, _ = canbus.NewFrame(extended, []byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 0x64, 0xe4})
	assertRefused(t, "extended format", s, in, frame, "invalid")
}

// assertRefused checks that the decoder refuses the frame with the given status
// and yields no field, and that the evaluator gives that same status as its
// reason with no rows, so a caller can tell a malformed frame from an
// out-of-table identifier by the reason.
func assertRefused(t *testing.T, name string, s canbusreg.Selection, in canbusreg.PCSStandardPCSInput, frame canbus.Frame, status string) {
	t.Helper()
	in.Evidence.Frame = frame
	r := canbusreg.DecodePCSStandard(s, in)
	if r.Status != status || len(r.Fields) != 0 {
		t.Errorf("%s: decoder status %q with %d fields, want %q and none", name, r.Status, len(r.Fields), status)
	}
	res, reason, err := Evaluate(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if reason != roweval.Reason(status) || res.Rows != nil {
		t.Errorf("%s: evaluator reason %q with rows %v, want decoder status %q and none", name, reason, res.Rows != nil, status)
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

// TestStandardTemperatureWithheldWithoutTemperatureValidity pins that an
// unvalidated 0x313 temperature gives no typed value on either contract: the row
// is withheld with its raw word kept, while the voltage, current, state of charge
// and state of health of the same frame stay accepted with their values. The
// gate is the decoder's temperature validity: removing it from the row makes the
// temperature accepted and fails this test, and removing it from the decoder
// fails the parity vectors that carry temperature_valid false.
func TestStandardTemperatureWithheldWithoutTemperatureValidity(t *testing.T) {
	cases := []struct {
		vector, voltageRow, temperatureRow string
		rawWord                            uint16
	}{
		{"temperature-unvalidated-313-withholds-temperature", rowPrefix + "313.legacy.voltage", rowPrefix + "313.legacy.temperature", 0xFFF6},
		{"release-11-temperature-unvalidated-313-withholds-average", rowPrefix + "313.release11.voltage", rowPrefix + "313.release11.temperature", 0x00FA},
	}
	for _, tc := range cases {
		t.Run(tc.vector, func(t *testing.T) {
			_, res, _ := evaluateCase(t, caseNamed(t, tc.vector))
			temp := res.Rows[tc.temperatureRow].Native
			if temp == nil || temp.Value != nil || temp.Unit != "" || temp.Reason != gateWithheld || temp.Disposition != dispositionRaw {
				t.Fatalf("%s: unvalidated temperature gives a typed value: %+v", tc.temperatureRow, temp)
			}
			if temp.Raw != uint64(tc.rawWord) {
				t.Errorf("%s: raw %#x, want %#x", tc.temperatureRow, temp.Raw, tc.rawWord)
			}
			accepted := map[string]string{
				tc.voltageRow:             "53.1",
				rowPrefix + "313.current": "-1",
				rowPrefix + "313.soc":     "100",
				rowPrefix + "313.soh":     "100",
			}
			for id, want := range accepted {
				nat := res.Rows[id].Native
				if nat == nil || nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
					t.Errorf("%s: no typed value while only the temperature is unvalidated (reason %v)", id, res.Rows[id].Reason)
					continue
				}
				if decimalRat(t, nat.Value.Quantity.Number).Cmp(ratText(t, want)) != 0 {
					t.Errorf("%s: value %v, want %s", id, nat.Value.Quantity.Number, want)
				}
			}
		})
	}
}

// TestStandardLimitRowsWithheldWithoutMeasurementValidity pins that an
// unvalidated local 0x311 gives no typed limit: the voltage limit and the local
// charge and discharge limits are withheld with their raw words kept, while the
// hint bits, which the decoder gates on the local scope alone, stay accepted.
// The gate is the decoder's measurement validity: removing it from the row makes
// the limits accepted and fails this test and the parity vector that carries
// measurement_valid false.
func TestStandardLimitRowsWithheldWithoutMeasurementValidity(t *testing.T) {
	_, res, _ := evaluateCase(t, caseNamed(t, "unmeasured-local-311-withholds-limits"))
	withheld := map[string]uint64{
		rowPrefix + "311.voltage_limit":          0x1388,
		rowPrefix + "311.direct.charge_limit":    0x0064,
		rowPrefix + "311.direct.discharge_limit": 0x0032,
	}
	for id, raw := range withheld {
		nat := res.Rows[id].Native
		if nat == nil || nat.Value != nil || nat.Unit != "" || nat.Reason != gateWithheld || nat.Disposition != dispositionRaw {
			t.Fatalf("%s: unvalidated limit gives a typed value: %+v", id, nat)
		}
		if nat.Raw != raw {
			t.Errorf("%s: raw %#x, want %#x", id, nat.Raw, raw)
		}
	}
	for _, id := range []string{rowPrefix + "311.raw_state", rowPrefix + "311.charge_hint"} {
		nat := res.Rows[id].Native
		if nat == nil || nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Errorf("%s: hint withheld with measurement invalid (reason %v)", id, res.Rows[id].Reason)
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

// TestIntegerCodecRefusesUnrepresentableValues pins that the integer codec
// refuses a value outside the declared wire width instead of wrapping it.
func TestIntegerCodecRefusesUnrepresentableValues(t *testing.T) {
	descs := Descriptors()
	unsigned := descs.Codecs["unsigned"]
	signed := descs.Codecs["signed"]
	refuse := []struct {
		name  string
		codec roweval.Codec
		value semreg.Decimal
		width uint16
	}{
		{"unsigned 8-bit 256", unsigned, semreg.Decimal{Coefficient: "256"}, 8},
		{"unsigned 8-bit 26 x 10", unsigned, semreg.Decimal{Coefficient: "26", Exponent10: 1}, 8},
		{"unsigned 8-bit negative", unsigned, semreg.Decimal{Coefficient: "-1"}, 8},
		{"signed 16-bit 32768", signed, semreg.Decimal{Coefficient: "32768"}, 16},
		{"signed 16-bit -32769", signed, semreg.Decimal{Coefficient: "-32769"}, 16},
		{"signed 16-bit 3277 x 10", signed, semreg.Decimal{Coefficient: "3277", Exponent10: 1}, 16},
		{"zero width", unsigned, semreg.Decimal{Coefficient: "0"}, 0},
		{"huge exponent", unsigned, semreg.Decimal{Coefficient: "1", Exponent10: 1000000}, 16},
	}
	for _, c := range refuse {
		if _, err := c.codec.Encode(c.value, c.width); err == nil {
			t.Errorf("%s: encode succeeded, want refusal", c.name)
		}
	}
	accept := []struct {
		name  string
		codec roweval.Codec
		value semreg.Decimal
		width uint16
		want  uint64
	}{
		{"unsigned 8-bit 255", unsigned, semreg.Decimal{Coefficient: "255"}, 8, 0xFF},
		{"signed 16-bit 32767", signed, semreg.Decimal{Coefficient: "32767"}, 16, 0x7FFF},
		{"signed 16-bit -32768", signed, semreg.Decimal{Coefficient: "-32768"}, 16, 0x8000},
		{"signed 8-bit -1", signed, semreg.Decimal{Coefficient: "-1"}, 8, 0xFF},
		{"unsigned 16-bit 1234 x 10^0", unsigned, semreg.Decimal{Coefficient: "1234"}, 16, 0x04D2},
	}
	for _, c := range accept {
		got, err := c.codec.Encode(c.value, c.width)
		if err != nil || got != c.want {
			t.Errorf("%s: got %#x, %v; want %#x", c.name, got, err, c.want)
		}
	}
}

func rowSpecID(field string, frame uint32) string {
	for _, s := range standardSpecs() {
		if s.frame == frame && s.field == field && s.layout != "gateway" && s.layout != "release11" {
			return nativeRow(s).ID
		}
	}
	return ""
}

func TestStandardResultsKeepEvidenceGradeApartFromAcceptance(t *testing.T) {
	dec, res, _ := evaluateCase(t, caseNamed(t, "legacy-local-311-limits-and-hints"))
	hint := rowSpecID("charge_hint", 0x311)
	if res.Rows[hint].Native.Disposition != dispositionAccepted || res.Grades[hint] != "Hypothesis" {
		t.Errorf("accepted hint: disposition %q grade %q", res.Rows[hint].Native.Disposition, res.Grades[hint])
	}
	_, mres, _ := evaluateCase(t, caseNamed(t, "legacy-local-measured-313"))
	volt := rowSpecID("voltage", 0x313)
	if mres.Rows[volt].Native.Disposition != dispositionAccepted || mres.Grades[volt] != "Proven" {
		t.Errorf("accepted voltage: disposition %q grade %q", mres.Rows[volt].Native.Disposition, mres.Grades[volt])
	}
	for id, g := range res.Grades {
		if want := dec.Fields[rowField(id)].Grade; g != want {
			t.Errorf("%s grade %q, decoder %q", id, g, want)
		}
	}
}

func TestIntegerCodecUnsignedRoundTripsFullRange(t *testing.T) {
	codec := Descriptors().Codecs["unsigned"]
	for _, v := range []uint64{0, 1, 10, 1<<63 - 1, 1 << 63, 1<<63 + 1, 10000000000000000000, 1<<64 - 1} {
		d, err := codec.Decode(v, 64)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(d.Coefficient, "-") {
			t.Errorf("%d decoded negative: %+v", v, d)
		}
		back, err := codec.Encode(d, 64)
		if err != nil || back != v {
			t.Errorf("%d round trip gave %d, %v", v, back, err)
		}
	}
}

// observedDecoder is one decoder run over one vector: the field keys the decoder
// emitted and the native rows the matching evaluator applied to the same bytes.
type observedDecoder struct {
	decoder string
	vector  string
	keys    []string
	applied map[string]roweval.Evaluation
}

// appliedRows keeps the rows of an evaluation that applied to the observation.
func appliedRows(rows map[string]roweval.Evaluation) map[string]roweval.Evaluation {
	out := make(map[string]roweval.Evaluation)
	for id, ev := range rows {
		if ev.Native != nil && ev.Reason != roweval.ReasonNoMatch {
			out[id] = ev
		}
	}
	return out
}

func fieldKeys(r canbusreg.NativeResult) []string {
	keys := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		keys = append(keys, k)
	}
	return keys
}

// diagVariants is the diagnostic vector file with, for each of its vectors, the
// neighbouring cases the file does not spell out: the second word offset of the
// frame family of every code-word vector, and the parallel summary frame 0x323
// with every bit clear and every bit set.
func diagVariants(t *testing.T) []diagVectorCase {
	t.Helper()
	out := diagLoadVectors(t)
	secondOffset := map[string]int{"0x333": 4, "0x522": 3}
	for _, c := range diagLoadVectors(t) {
		switch c.Kind {
		case "dtc":
			v := c
			v.Name += "-other-offset"
			if o, ok := secondOffset[c.Identifier]; ok {
				v.Offset = o
			} else {
				v.Offset = 2
			}
			out = append(out, v)
		case "summary":
			if c.Identifier == "0x312" && c.Selection.Flavor == "pcs-parallel" {
				for _, payload := range []string{"0000000000000000", "ffffffffffffffff"} {
					v := c
					v.Name += "-323-" + payload[:2]
					v.Identifier, v.Payload = "0x323", payload
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// parallelFaultVariants is the parallel identity vector file with the fault frames
// 0x503, 0x663 and 0x673 carrying every bit clear, every bit set and a mixed
// pattern, the frames whose bit rows no vector of the file reaches.
func parallelFaultVariants(t *testing.T) []parallelIdentityVectorCase {
	t.Helper()
	out := parallelLoadVectors(t)
	for _, f := range []struct {
		identifier string
		project    [2]string
	}{{"0x503", [2]string{"QAAB", "0004"}}, {"0x663", [2]string{"QAAB", "0002"}}, {"0x673", [2]string{"QAAB", "0002"}}} {
		for _, payload := range []string{"0000000000000000", "ffffffffffffffff", "0180040820408010"} {
			out = append(out, parallelIdentityVectorCase{Name: f.identifier + "-" + payload, Identifier: f.identifier, Payload: payload, Selection: sel(f.project)})
		}
	}
	return out
}

// decoderCorpus runs each of the nine decoders and its evaluator over the vector
// corpus of its frames. A vector the decoder refuses emits no key and is kept
// out, as the decoder-versus-evaluator tests of each family check its refusal.
func decoderCorpus(t *testing.T) []observedDecoder {
	t.Helper()
	var out []observedDecoder
	add := func(decoder, vector string, keys []string, rows map[string]roweval.Evaluation) {
		if len(keys) > 0 {
			out = append(out, observedDecoder{decoder, vector, keys, appliedRows(rows)})
		}
	}
	must := func(vector string, reason roweval.Reason, err error) {
		if err != nil {
			t.Fatalf("%s: %v", vector, err)
		}
		if reason != "" {
			t.Fatalf("%s: evaluator refuses an observation the decoder admits (%q)", vector, reason)
		}
	}
	standard := func(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput, name string, eval func(canbusreg.Selection, canbusreg.PCSStandardPCSInput) (Result, roweval.Reason, error)) {
		dec := canbusreg.DecodePCSStandard(s, in)
		if dec.Status != "decoded" {
			return
		}
		res, reason, err := eval(s, in)
		must(name, reason, err)
		add("DecodePCSStandard", name, fieldKeys(dec.NativeResult), res.Rows)
	}
	for _, c := range loadVectors(t) {
		s, in, _ := build(t, c)
		standard(s, in, c.Name, Evaluate)
	}
	for _, c := range loadExtremaCases(t) {
		s, in, _ := buildExtrema(t, c)
		standard(s, in, c.Name, EvaluateExtrema)
	}
	for _, c := range loadGaugeVectors(t) {
		s, in, _ := buildGauge(t, c)
		dec := canbusreg.DecodePCSGauge(s, in)
		if dec.Status != "decoded" {
			continue
		}
		res, reason, err := EvaluateGauge(s, in)
		must(c.Name, reason, err)
		add("DecodePCSGauge", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range loadIdentityVectors(t) {
		s, in := buildIdentityCase(t, c)
		dec := canbusreg.DecodePCSIdentity(s, in)
		if dec.Status != "decoded" {
			continue
		}
		res, reason, err := EvaluateIdentity(s, in)
		must(c.Name, reason, err)
		add("DecodePCSIdentity", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range loadSlotVectors(t) {
		s, in, _ := slotBuild(t, c)
		dec := canbusreg.DecodePCSSlots(s, in)
		if dec.Status != "decoded" {
			continue
		}
		res, reason, err := EvaluateSlots(s, in)
		must(c.Name, reason, err)
		add("DecodePCSSlots", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range diagVariants(t) {
		s := diagSelection(t, c.Selection)
		frame := diagFrame(t, c.Identifier, c.Extended, c.Payload)
		switch c.Kind {
		case "summary":
			in := diagSummaryInput(s, frame, c.Selection.LocalSummaryQualified)
			dec := canbusreg.DecodePCSSummaries(s, in)
			keys := fieldKeys(dec.NativeResult)
			for k := range dec.Bits {
				keys = append(keys, k)
			}
			if len(keys) == 0 {
				continue
			}
			res, reason, err := EvaluateSummaryBits(s, in)
			must(c.Name, reason, err)
			add("DecodePCSSummaries", c.Name, keys, res.Rows)
		case "dtc":
			in := diagLinkInput(s, frame)
			dec := canbusreg.DecodeDTC(s, in, c.Offset)
			if len(dec.Fields) == 0 {
				continue
			}
			res, reason, err := EvaluateDTC(s, in, c.Offset)
			must(c.Name, reason, err)
			add("DecodeDTC", c.Name, fieldKeys(dec), res.Rows)
		}
	}
	for _, c := range loadExtendedVectors(t) {
		s, in, _ := buildExtended(t, c)
		dec := canbusreg.DecodePCSExtended(s, in)
		if len(dec.Fields) == 0 {
			continue
		}
		res, reason, err := EvaluateExtended(s, in)
		must(c.Name, reason, err)
		add("DecodePCSExtended", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range loadFaultVectors(t) {
		s, in, _ := buildFault(t, c)
		dec := canbusreg.DecodePCSExtended(s, in)
		if len(dec.Fields) == 0 {
			continue
		}
		res, reason, err := EvaluateExtendedFaults(s, in)
		must(c.Name, reason, err)
		add("DecodePCSExtended", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range parallelFaultVariants(t) {
		s := diagSelection(t, c.Selection)
		in := diagLinkInput(s, diagFrame(t, c.Identifier, c.Extended, c.Payload))
		dec := canbusreg.ParallelIdentityDecodeParallel(s, in)
		if len(dec.Fields) == 0 {
			continue
		}
		res, reason, err := EvaluateParallelIdentity(s, in)
		must(c.Name, reason, err)
		add("ParallelIdentityDecodeParallel", c.Name, fieldKeys(dec), res.Rows)
	}
	for _, c := range loadParallelVectors(t) {
		s, in, x, _ := parallelBuild(t, c)
		dec := canbusreg.ParallelReleaseDecodeParallel(s, in, x)
		if len(dec.Fields) == 0 {
			continue
		}
		res, reason, err := EvaluateParallelRelease(s, in, x)
		must(c.Name, reason, err)
		add("ParallelReleaseDecodeParallel", c.Name, fieldKeys(dec), res.Rows)
	}
	return out
}

// TestEveryDecoderFieldKeyHasARow pins that each of the nine decoders is
// exercised by the corpus, and that every field key a decoder emits on a vector
// is carried, for the same bytes, by a native row of the combined table.
func TestEveryDecoderFieldKeyHasARow(t *testing.T) {
	table := make(map[string]bool)
	for _, r := range CombinedTable().Native {
		table[r.ID] = true
	}
	seen := make(map[string]int)
	for _, o := range decoderCorpus(t) {
		seen[o.decoder]++
		for _, key := range o.keys {
			found := false
			for id := range o.applied {
				if fieldOf(id) == key && table[id] {
					found = true
				}
			}
			if !found {
				t.Errorf("%s on %s: field %s has no applied native row", o.decoder, o.vector, key)
			}
		}
	}
	for _, d := range []string{"DecodePCSStandard", "DecodePCSGauge", "DecodePCSIdentity", "DecodePCSSlots", "DecodePCSSummaries", "DecodePCSExtended", "ParallelIdentityDecodeParallel", "ParallelReleaseDecodeParallel", "DecodeDTC"} {
		if seen[d] == 0 {
			t.Errorf("no vector exercises %s", d)
		}
	}
}

// TestEveryRowHasAVector pins that the row identifiers of the combined table are
// unique and that every native row applies to the bytes of at least one vector.
func TestEveryRowHasAVector(t *testing.T) {
	ids := make(map[string]bool)
	for _, r := range CombinedTable().Native {
		if ids[r.ID] {
			t.Errorf("duplicate native row %s", r.ID)
		}
		ids[r.ID] = true
	}
	applied := make(map[string]bool)
	for _, o := range decoderCorpus(t) {
		for id := range o.applied {
			applied[id] = true
		}
	}
	for id := range ids {
		if !applied[id] {
			t.Errorf("no vector applies to %s", id)
		}
	}
}

// pairedBank is the bank a PCS flavor pairs with.
var pairedBank = map[string]string{"pcs-standard": "ordinary", "pcs-parallel": "parallel"}

// TestBankDependentRowsCarryTheirBank pins that every row whose conditions name
// a bank-paired PCS flavor (standard or parallel) or a bank names both, as a pair, and that a row of the parallel
// flavor names the parallel bank.
func TestBankDependentRowsCarryTheirBank(t *testing.T) {
	banked := 0
	for _, r := range CombinedTable().Native {
		flavor, hasFlavor := r.When.Equal[qFlavor]
		if _, paired := pairedBank[flavor]; !paired {
			hasFlavor = false
		}
		bank, hasBank := r.When.Equal[qBank]
		if strings.Contains(r.ID, ".parallel.") && !hasBank {
			t.Errorf("%s: parallel row carries no bank", r.ID)
		}
		if !hasFlavor && !hasBank {
			continue
		}
		banked++
		if hasFlavor != hasBank {
			t.Errorf("%s: flavor %q and bank %q are not stated together", r.ID, flavor, bank)
			continue
		}
		if pairedBank[flavor] != bank {
			t.Errorf("%s: flavor %q is paired with bank %q, the row states %q", r.ID, flavor, pairedBank[flavor], bank)
		}
	}
	if banked == 0 {
		t.Fatal("no row carries a bank")
	}
}

// TestStandardLimitWordsPublishNoLimitBindingUnderAnySelection evaluates every
// 0x311 row of the bound table on each 0x311 vector under each pairing of flavor
// and bank, and checks no fact candidate is produced and no binding names a
// 0x311 row or a limit fact.
func TestStandardLimitWordsPublishNoLimitBindingUnderAnySelection(t *testing.T) {
	for _, b := range Bindings() {
		if strings.Contains(b.NativeID, ".311.") || strings.Contains(string(b.Read.Fact.ID), "storage.limit.") {
			t.Errorf("%s is bound to %s", b.NativeID, b.Read.Fact.ID)
		}
	}
	roles := packRoles(t, "pack.ordinary")
	table := BoundTable()
	evaluated := 0
	for _, c := range loadVectors(t) {
		if c.Identifier != "0x311" {
			continue
		}
		for _, flavor := range []string{"pcs-standard", "pcs-parallel"} {
			for _, bank := range []string{"ordinary", "parallel"} {
				v := c
				v.Selection.Flavor, v.Selection.Bank = flavor, bank
				if _, err := func() (canbusreg.Selection, error) {
					spec := canbusreg.SelectionSpec{Flavor: flavor, ProjectCode: v.Selection.Project, Release: v.Selection.Release, Direction: "bms-to-pcs", Scope: v.Selection.Scope, Bank: bank, Endpoint: v.Selection.Endpoint, Group: v.Selection.Group, Slot: v.Selection.Slot, Epoch: 1}
					iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
					spec.Interface = iface
					if err != nil {
						return canbusreg.Selection{}, err
					}
					return canbusreg.NewSelection(spec)
				}(); err != nil {
					continue
				}
				s, in, frame := build(t, v)
				o := observation{table: table, frame: frame, qualifiers: Qualifiers(s, in), identifier: frame.ID().Value()}
				for _, row := range table.Native {
					if !strings.Contains(row.ID, ".311.") {
						continue
					}
					ev, _ := o.evaluate(t, row, roles)
					evaluated++
					if ev.Candidate != nil {
						t.Errorf("%s under %s/%s on %s gives a candidate", row.ID, flavor, bank, c.Name)
					}
				}
			}
		}
	}
	if evaluated == 0 {
		t.Fatal("no 0x311 row was evaluated")
	}
}
