package pcs

import (
	"encoding/hex"
	"encoding/json"
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

type diagVectorFile struct {
	Cases []diagVectorCase `json:"cases"`
}

type diagVectorCase struct {
	Name       string                    `json:"name"`
	Kind       string                    `json:"kind"`
	Identifier string                    `json:"identifier"`
	Extended   bool                      `json:"extended"`
	Payload    string                    `json:"payload"`
	Offset     int                       `json:"offset"`
	Selection  diagVectorSelection       `json:"selection"`
	Expect     map[string]diagVectorWant `json:"expect"`
}

type diagVectorSelection struct {
	Flavor                string `json:"flavor"`
	PCSFlavor             string `json:"pcs_flavor"`
	Bank                  string `json:"bank"`
	Project               string `json:"project"`
	Release               string `json:"release"`
	Scope                 string `json:"scope"`
	Endpoint              string `json:"endpoint"`
	Group                 string `json:"group"`
	Slot                  string `json:"slot"`
	LocalSummaryQualified bool   `json:"local_summary_qualified"`
}

// diagVectorWant is the independent expectation of one key: its decoder field
// status, its raw word or bit and its exact decimal value ("" when withheld).
type diagVectorWant struct {
	Status string `json:"status"`
	Raw    string `json:"raw"`
	Value  string `json:"value"`
}

func diagLoadVectors(t *testing.T) []diagVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var f diagVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no diagnostic vectors")
	}
	return f.Cases
}

// diagSelection builds the selection of one vector through the public constructor.
func diagSelection(t *testing.T, v diagVectorSelection) canbusreg.Selection {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: v.Flavor, PCSFlavor: v.PCSFlavor, ProjectCode: v.Project, Release: v.Release, Direction: "bms-to-pcs", Scope: v.Scope, Bank: v.Bank, Endpoint: v.Endpoint, Group: v.Group, Slot: v.Slot, Epoch: 1}
	if v.Flavor == "link" {
		src, err := strconv.ParseUint(v.Endpoint, 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		spec.Direction = "module-to-coordinator"
		spec.RouteSet = true
		spec.Destination = 0xf4
		spec.Source = uint8(src)
	}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("selection %+v: %v", v, err)
	}
	return s
}

// diagFrame builds one frame of the given identifier, format and payload.
func diagFrame(t *testing.T, identifier string, extended bool, payload string) canbus.Frame {
	t.Helper()
	id, err := strconv.ParseUint(strings.TrimPrefix(identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	var ident canbus.Identifier
	if extended {
		ident, err = canbus.NewExtendedID(uint32(id))
	} else {
		ident, err = canbus.NewStandardID(uint32(id))
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	f, err := canbus.NewFrame(ident, data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// diagLinkInput is the observation of a frame under the selection's own context.
func diagLinkInput(s canbusreg.Selection, f canbus.Frame) canbusreg.LINKInput {
	c := s.SelectionSpecValue()
	return canbusreg.LINKInput{
		Evidence: canbusreg.Evidence{Interface: c.Interface, Frame: f, Sequence: 7, Monotonic: 9 * time.Second},
		Epoch:    c.Epoch, Direction: c.Direction, Scope: c.Scope, Bank: c.Bank, Endpoint: c.Endpoint, Group: c.Group, Slot: c.Slot,
		FieldAge: time.Second,
	}
}

func diagSummaryInput(s canbusreg.Selection, f canbus.Frame, qualified bool) canbusreg.PCSSummaryPCSInput {
	return canbusreg.PCSSummaryPCSInput{
		PCSStandardPCSInput:   canbusreg.PCSStandardPCSInput{LINKInput: diagLinkInput(s, f), PublisherResolved: true},
		LocalSummaryQualified: qualified,
	}
}

// diagToken is the row-identifier token of a PCS flavor.
func diagToken(flavor string) string {
	if flavor == "pcs-parallel" {
		return "parallel"
	}
	return "standard"
}

// diagRat is the exact rational value of a native decimal value.
func diagRat(t *testing.T, v *semreg.Value) *big.Rat {
	t.Helper()
	if v == nil || v.Kind != semreg.ValueQuantity || v.Quantity == nil {
		t.Fatalf("native value is not a quantity")
	}
	r, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", v.Quantity.Number.Coefficient, v.Quantity.Number.Exponent10))
	if !ok {
		t.Fatalf("native value %v is not a number", v.Quantity.Number)
	}
	return r
}

// diagCheckWant compares one vector expectation with the row and the decoder.
// The decoder status and the raw word are checked against the vector; the
// value is checked against the row, with "" meaning withheld.
func diagCheckWant(t *testing.T, key string, want diagVectorWant, status string, present bool, ev roweval.Evaluation) {
	t.Helper()
	if want.Status == "absent" {
		if present {
			t.Errorf("%s: decoder emits the field, vector says absent", key)
		}
		if ev.Native == nil || ev.Native.Reason == "" || ev.Native.Value != nil {
			t.Errorf("%s: absent field has a native value", key)
		}
		return
	}
	if !present {
		t.Fatalf("%s: decoder emits no field", key)
	}
	if status != want.Status {
		t.Errorf("%s: decoder status %q, vector %q", key, status, want.Status)
	}
	if ev.Native == nil {
		t.Fatalf("%s: no native row", key)
	}
	if fmt.Sprint(ev.Native.Raw) != want.Raw {
		t.Errorf("%s: raw %d, vector %s", key, ev.Native.Raw, want.Raw)
	}
	if want.Value == "" {
		if ev.Native.Value != nil {
			t.Errorf("%s: withheld key has a value", key)
		}
		return
	}
	if ev.Native.Value == nil {
		t.Fatalf("%s: row withholds the value (%s)", key, ev.Native.Reason)
	}
	got := diagRat(t, ev.Native.Value)
	wantRat, ok := new(big.Rat).SetString(want.Value)
	if !ok || got.Cmp(wantRat) != 0 {
		t.Errorf("%s: value %s, vector %s", key, got.RatString(), want.Value)
	}
}

// TestSummaryBitRowsMatchDecoder checks every summary bit of every summary
// vector against the decoder: the bit value, the disposition the decoder gives
// the key, the withholding and the vector's independent expectation.
func TestSummaryBitRowsMatchDecoder(t *testing.T) {
	for _, c := range diagLoadVectors(t) {
		if c.Kind != "summary" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			s := diagSelection(t, c.Selection)
			frame := diagFrame(t, c.Identifier, c.Extended, c.Payload)
			in := diagSummaryInput(s, frame, c.Selection.LocalSummaryQualified)
			dec := canbusreg.DecodePCSSummaries(s, in)
			res, reason, err := EvaluateSummaryBits(s, in)
			if err != nil || reason != "" {
				t.Fatalf("evaluate refused: %v %v", reason, err)
			}
			frameToken := fmt.Sprintf("%X", frame.ID().Value())
			if len(dec.Bits) == 0 {
				t.Fatalf("decoder emits no summary bits")
			}
			for key, b := range dec.Bits {
				id := fmt.Sprintf("growatt.bms.pcs.%s.%s.%s", diagToken(c.Selection.Flavor), frameToken, key)
				ev, ok := res.Rows[id]
				if !ok || ev.Native == nil {
					t.Fatalf("%s: no native row", id)
				}
				if ev.Native.RowID != id {
					t.Errorf("%s: row id %q", id, ev.Native.RowID)
				}
				status := b.Field.Status
				kept := status == "accepted" || status == "raw"
				if (ev.Native.Reason == "") != kept {
					t.Errorf("%s: row reason %q, decoder status %q", id, ev.Native.Reason, status)
				}
				if ev.Native.Raw != uint64(b.Field.Numerator) {
					t.Errorf("%s: raw %d, decoder numerator %d", id, ev.Native.Raw, b.Field.Numerator)
				}
				if kept && ev.Native.Disposition != status {
					t.Errorf("%s: disposition %q, decoder status %q", id, ev.Native.Disposition, status)
				}
				if kept != (ev.Native.Value != nil) {
					t.Errorf("%s: value presence differs from the disposition", id)
				}
			}
			for key, want := range c.Expect {
				b, ok := dec.Bits[key]
				id := fmt.Sprintf("growatt.bms.pcs.%s.%s.%s", diagToken(c.Selection.Flavor), frameToken, key)
				diagCheckWant(t, key, want, b.Field.Status, ok, res.Rows[id])
			}
		})
	}
}

// TestDiagnosticIndexRowsMatchDecoder checks the selected_word and numeric_index
// rows of every DTC vector against the decoder and the vector expectations.
func TestDiagnosticIndexRowsMatchDecoder(t *testing.T) {
	for _, c := range diagLoadVectors(t) {
		if c.Kind != "dtc" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			s := diagSelection(t, c.Selection)
			frame := diagFrame(t, c.Identifier, c.Extended, c.Payload)
			in := diagLinkInput(s, frame)
			dec := canbusreg.DecodeDTC(s, in, c.Offset)
			res, reason, err := EvaluateDTC(s, in, c.Offset)
			if err != nil || reason != "" {
				t.Fatalf("evaluate refused: %v %v", reason, err)
			}
			fam, ok := diagDTCFamilyOf(frame)
			if !ok {
				t.Fatal("frame family not admitted")
			}
			word, hasWord := dec.Fields[dtcSelectedWord]
			if !hasWord {
				t.Fatal("decoder emits no selected word")
			}
			selID := fmt.Sprintf("growatt.bms.%s.%s.offset%d.%s", fam.path, fam.token, c.Offset, dtcSelectedWord)
			selEv := res.Rows[selID]
			if selEv.Native == nil || selEv.Native.Reason != "" {
				t.Fatalf("%s: selected word withheld: %+v", selID, selEv.Native)
			}
			if selEv.Native.Raw != uint64(word.Numerator) {
				t.Errorf("%s: raw %d, decoder %d", selID, selEv.Native.Raw, word.Numerator)
			}
			if selEv.Native.Disposition != word.Status {
				t.Errorf("%s: disposition %q, decoder %q", selID, selEv.Native.Disposition, word.Status)
			}
			numID := fmt.Sprintf("growatt.bms.%s.%s.offset%d.%s", fam.path, fam.token, c.Offset, dtcNumericIndex)
			idx, hasIndex := dec.Fields[dtcNumericIndex]
			numEv := res.Rows[numID]
			if hasIndex {
				if numEv.Native == nil || numEv.Native.Reason != "" || numEv.Native.Value == nil {
					t.Fatalf("%s: index withheld though decoder emits it", numID)
				}
				if got := diagRat(t, numEv.Native.Value); got.Cmp(big.NewRat(idx.Numerator, 1)) != 0 {
					t.Errorf("%s: index %s, decoder %d", numID, got.RatString(), idx.Numerator)
				}
				if numEv.Native.Raw != uint64(word.Numerator) {
					t.Errorf("%s: raw word %d, decoder %d", numID, numEv.Native.Raw, word.Numerator)
				}
			} else if numEv.Native == nil || numEv.Native.Reason == "" || numEv.Native.Value != nil {
				t.Errorf("%s: index value present though decoder emits none", numID)
			}
			for key, want := range c.Expect {
				id := fmt.Sprintf("growatt.bms.%s.%s.offset%d.%s", fam.path, fam.token, c.Offset, key)
				f, present := dec.Fields[key]
				ev := res.Rows[id]
				diagCheckWant(t, key, want, f.Status, present, ev)
			}
		})
	}
}

// TestDiagnosticRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat
// checks that no diagnostic row applies to a neighbouring identifier, a wrong
// length or the extended format, and that the decoder refuses the same frames.
func TestDiagnosticRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	pcs := diagSelection(t, diagVectorSelection{Flavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "coordinator"})
	legacy := diagSelection(t, diagVectorSelection{Flavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0006", Scope: "local", Endpoint: "coordinator"})
	link := diagSelection(t, diagVectorSelection{Flavor: "link", PCSFlavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "1A"})
	eight := "0200000000000000"
	cases := []struct {
		name     string
		s        canbusreg.Selection
		id       string
		extended bool
		payload  string
		dtc      bool
	}{
		{"summary-neighbour-0x313", pcs, "0x313", false, eight, false},
		{"summary-neighbour-0x311", pcs, "0x311", false, eight, false},
		{"summary-wrong-length-0x312", pcs, "0x312", false, "02000000000000", false},
		{"summary-extended-0x312", pcs, "0x312", true, eight, false},
		{"summary-extended-0x323", pcs, "0x323", true, eight, false},
		{"dtc-neighbour-0x334", pcs, "0x334", false, "1009000000000000", true},
		{"dtc-wrong-length-0x333", pcs, "0x333", false, "10090000000000", true},
		{"dtc-extended-0x333", pcs, "0x333", true, "1009000000000000", true},
		{"dtc-parallel-word-on-standard-member", pcs, "0x52A", false, "1062000000000000", true},
		{"dtc-link-frame-on-standard-member", pcs, "0x1853F41A", true, "0910000000000000", true},
		{"dtc-standard-frame-on-link-member", link, "0x333", false, "1009000000000000", true},
		{"dtc-count-zero-selection", legacy, "0x333", false, "1009000000000000", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := diagFrame(t, c.id, c.extended, c.payload)
			if !c.dtc {
				in := diagSummaryInput(c.s, f, true)
				if len(canbusreg.DecodePCSSummaries(c.s, in).Bits) != 0 {
					t.Fatal("decoder admits a frame that must be refused")
				}
				res, reason, err := EvaluateSummaryBits(c.s, in)
				if err != nil || reason == "" || len(res.Rows) != 0 {
					t.Fatalf("rows applied: reason %q err %v rows %d", reason, err, len(res.Rows))
				}
				return
			}
			in := diagLinkInput(c.s, f)
			if _, ok := canbusreg.DecodeDTC(c.s, in, 0).Fields[dtcSelectedWord]; ok {
				t.Fatal("decoder admits a frame that must be refused")
			}
			res, reason, err := EvaluateDTC(c.s, in, 0)
			if err != nil || reason == "" || len(res.Rows) != 0 {
				t.Fatalf("rows applied: reason %q err %v rows %d", reason, err, len(res.Rows))
			}
		})
	}
}

// TestSummaryBitsWithoutStatedConditionStayRaw checks that a summary bit the
// decoder keeps raw (no stated condition class, or an unsupported contract)
// is a raw native value and is never reported accepted.
func TestSummaryBitsWithoutStatedConditionStayRaw(t *testing.T) {
	stayed := 0
	for _, c := range diagLoadVectors(t) {
		if c.Kind != "summary" {
			continue
		}
		s := diagSelection(t, c.Selection)
		f := diagFrame(t, c.Identifier, c.Extended, c.Payload)
		in := diagSummaryInput(s, f, c.Selection.LocalSummaryQualified)
		dec := canbusreg.DecodePCSSummaries(s, in)
		res, _, err := EvaluateSummaryBits(s, in)
		if err != nil {
			t.Fatal(err)
		}
		frameToken := fmt.Sprintf("%X", f.ID().Value())
		for key, b := range dec.Bits {
			if b.Field.Status != "raw" {
				continue
			}
			stayed++
			ev := res.Rows[fmt.Sprintf("growatt.bms.pcs.%s.%s.%s", diagToken(c.Selection.Flavor), frameToken, key)]
			if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Disposition != "raw" || ev.Native.Value == nil {
				t.Errorf("%s %s: raw bit is not a raw native value: %+v", c.Name, key, ev.Native)
			}
		}
	}
	if stayed == 0 {
		t.Fatal("no raw summary bit in the vectors")
	}
}

// TestDiagnosticBankPairing checks that a PCS flavor pairs only with its bank,
// so no diagnostic row can be attributed to the other bank.
func TestDiagnosticBankPairing(t *testing.T) {
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		flavor, bank string
		paired       bool
	}{
		{"pcs-standard", "ordinary", true},
		{"pcs-parallel", "parallel", true},
		{"pcs-standard", "parallel", false},
		{"pcs-parallel", "ordinary", false},
	} {
		_, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: iface, Flavor: c.flavor, ProjectCode: "QAAA", Release: "0011", Direction: "bms-to-pcs", Scope: "local", Bank: c.bank, Endpoint: "coordinator", Epoch: 1})
		if c.paired && err != nil {
			t.Errorf("%s/%s: paired selection refused: %v", c.flavor, c.bank, err)
		}
		if !c.paired && err == nil {
			t.Errorf("%s/%s: unpaired selection admitted", c.flavor, c.bank)
		}
	}
}

// TestDiagnosticRowsRetainNativeRecordOnRefusal checks that the native record
// supplied to the summary decoder is kept as diagnostic evidence when the
// decoder refuses the observation, and that no row or condition is derived.
func TestDiagnosticRowsRetainNativeRecordOnRefusal(t *testing.T) {
	s := diagSelection(t, diagVectorSelection{Flavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "coordinator"})
	f := diagFrame(t, "0x312", false, "0100000000000000")
	in := diagSummaryInput(s, f, true)
	in.PublisherResolved = false
	record := []byte{0xde, 0xad, 0xbe, 0xef}
	in.NativeRecord = record
	res, reason, err := EvaluateSummaryBits(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" || len(res.Rows) != 0 {
		t.Fatalf("refused observation has rows: reason %q rows %d", reason, len(res.Rows))
	}
	if hex.EncodeToString(res.Diagnostics) != hex.EncodeToString(record) {
		t.Fatalf("diagnostic record %x, want %x", res.Diagnostics, record)
	}
}

// TestDiagnosticRowsDeclareFrameIdentifierAndFormat checks that each DTC row
// declares the identifier and format of the frame it reads, not a fixed one.
func TestDiagnosticRowsDeclareFrameIdentifierAndFormat(t *testing.T) {
	member := diagSelection(t, diagVectorSelection{Flavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "coordinator"})
	for _, c := range []struct {
		id       string
		extended bool
		flavor   string
	}{
		{"0x333", false, "pcs-standard"},
		{"0x52A", false, "pcs-parallel"},
		{"0x1853F41A", true, "link"},
	} {
		frame := diagFrame(t, c.id, c.extended, "1009000000000000")
		fam, ok := diagDTCFamilyOf(frame)
		if !ok {
			t.Fatalf("%s: family not admitted", c.id)
		}
		if fam.flavor != c.flavor {
			t.Fatalf("%s: family flavor %q", c.id, fam.flavor)
		}
		for _, spec := range diagDTCSpecs(fam, member, 0, frame) {
			if spec.row.At.Identifier != frame.ID().Value() || spec.row.At.Extended != c.extended || spec.row.At.DLC != 8 || spec.row.At.RawDLC != 8 {
				t.Errorf("%s: row %s declares identifier %#x extended %v, frame %#x extended %v", c.id, spec.id, spec.row.At.Identifier, spec.row.At.Extended, frame.ID().Value(), c.extended)
			}
		}
	}
}

// TestDiagnosticResultsKeepAttribution checks that a DTC result keeps the bank
// and slot its observation was decoded under, so two observations of the same
// row identifiers under different associations are told apart.
func TestDiagnosticResultsKeepAttribution(t *testing.T) {
	first := diagSelection(t, diagVectorSelection{Flavor: "link", PCSFlavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "1A", Slot: "3"})
	second := diagSelection(t, diagVectorSelection{Flavor: "link", PCSFlavor: "pcs-standard", Bank: "ordinary", Project: "QAAA", Release: "0011", Scope: "local", Endpoint: "1A", Slot: "4"})
	frame := diagFrame(t, "0x1853F41A", true, "0910000000000000")
	resA, _, err := EvaluateDTC(first, diagLinkInput(first, frame), 0)
	if err != nil {
		t.Fatal(err)
	}
	resB, _, err := EvaluateDTC(second, diagLinkInput(second, frame), 0)
	if err != nil {
		t.Fatal(err)
	}
	if resA.Attribution.Slot != "3" || !resA.Attribution.HasSlot {
		t.Errorf("first slot attribution %+v", resA.Attribution)
	}
	if resB.Attribution.Slot != "4" || !resB.Attribution.HasSlot {
		t.Errorf("second slot attribution %+v", resB.Attribution)
	}
	if resA.Attribution == resB.Attribution {
		t.Errorf("distinct associations share one attribution: %+v", resA.Attribution)
	}
}
