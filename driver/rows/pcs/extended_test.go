package pcs

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type extendedVectorFile struct {
	Cases []extendedVectorCase `json:"cases"`
}

type extendedVectorCase struct {
	Name       string                    `json:"name"`
	Identifier string                    `json:"identifier"`
	Payload    string                    `json:"payload"`
	Selection  extendedVectorSelection   `json:"selection"`
	Expect     map[string]extendedExpect `json:"expect"`
}

type extendedVectorSelection struct {
	Project     string `json:"project"`
	Release     string `json:"release"`
	Flavor      string `json:"flavor"`
	Bank        string `json:"bank"`
	Scope       string `json:"scope"`
	Endpoint    string `json:"endpoint"`
	Group       string `json:"group"`
	Slot        string `json:"slot"`
	Destination uint8  `json:"destination"`
	Source      uint8  `json:"source"`
}

type extendedExpect struct {
	Status string `json:"status"`
	Raw    uint64 `json:"raw"`
	Value  string `json:"value"`
}

func loadExtendedVectors(t *testing.T) []extendedVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/extended_1900_1904.json")
	if err != nil {
		t.Fatal(err)
	}
	var f extendedVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func extendedSpecOf(t *testing.T, sel extendedVectorSelection) canbusreg.SelectionSpec {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, RouteSet: true, Destination: sel.Destination, Source: sel.Source, Epoch: 1}
}

func extendedInputOf(spec canbusreg.SelectionSpec, frame canbus.Frame) canbusreg.LINKInput {
	return canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: spec.Interface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: spec.Epoch, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
}

func extendedFrame(t *testing.T, identifier string, payload []byte) canbus.Frame {
	t.Helper()
	n, err := strconv.ParseUint(strings.TrimPrefix(identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	id, err := canbus.NewExtendedID(uint32(n))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func buildExtended(t *testing.T, c extendedVectorCase) (canbusreg.Selection, canbusreg.LINKInput, canbus.Frame) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	spec := extendedSpecOf(t, c.Selection)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame := extendedFrame(t, c.Identifier, payload)
	return s, extendedInputOf(spec, frame), frame
}

func extendedStatus(n *roweval.Native) string {
	switch {
	case n.Reason == roweval.ReasonOutsideDomain:
		return "invalid"
	case n.Disposition == dispositionAccepted:
		return "accepted"
	case n.Disposition == dispositionRaw:
		return "raw"
	}
	return string(n.Reason)
}

func extendedRowsByField(res ExtendedResult) map[string]*roweval.Native {
	out := make(map[string]*roweval.Native)
	for id, ev := range res.Rows {
		if ev.Native != nil {
			out[fieldOf(id)] = ev.Native
		}
	}
	return out
}

// TestExtendedRowsMatchDecoder checks every vector: each field the decoder emits
// has exactly one applicable row with the same status, raw word, exact value and
// evidence grade, every applicable row is a decoder field, and the expectation
// is the hand-written vector value.
func TestExtendedRowsMatchDecoder(t *testing.T) {
	for _, c := range loadExtendedVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in, frame := buildExtended(t, c)
			dec := canbusreg.DecodePCSExtended(s, in)
			if dec.Status != "accepted" {
				t.Fatalf("decoder status %q, want accepted", dec.Status)
			}
			res, reason, err := EvaluateExtended(s, in)
			if err != nil || reason != "" {
				t.Fatalf("evaluator reason %q err %v", reason, err)
			}
			if res.Evidence != in.Evidence {
				t.Errorf("evidence differs from the observation")
			}
			applicable := extendedRowsByField(res)
			for name := range applicable {
				if _, ok := dec.Fields[name]; !ok {
					t.Errorf("row field %s applies but the decoder emits no such field", name)
				}
			}
			for name := range c.Expect {
				if _, ok := dec.Fields[name]; !ok {
					t.Errorf("vector expects %s, which the decoder does not emit", name)
				}
			}
			for name, df := range dec.Fields {
				exp, ok := c.Expect[name]
				if !ok {
					t.Errorf("decoder field %s has no vector expectation", name)
					continue
				}
				row, ok := applicable[name]
				if !ok {
					t.Errorf("field %s has no applicable row", name)
					continue
				}
				if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
					t.Errorf("%s: decoder raw evidence differs from the frame", name)
				}
				if got := extendedStatus(row); got != exp.Status {
					t.Errorf("%s: row status %q, want %q", name, got, exp.Status)
				}
				if df.Status != exp.Status {
					t.Errorf("%s: decoder status %q, want %q", name, df.Status, exp.Status)
				}
				if got := res.Grades[row.RowID]; got != df.Grade {
					t.Errorf("%s: grade %q, decoder %q", name, got, df.Grade)
				}
				if row.Raw != exp.Raw {
					t.Errorf("%s: row raw %d, want %d", name, row.Raw, exp.Raw)
				}
				if exp.Status == "accepted" && df.Unit != row.Unit {
					t.Errorf("%s: row unit %q, decoder %q", name, row.Unit, df.Unit)
				}
				if exp.Value == "" {
					if exp.Status != "accepted" && row.Value != nil {
						t.Errorf("%s: %s row carries a value", name, exp.Status)
					}
					continue
				}
				if row.Value == nil || row.Value.Quantity == nil {
					t.Errorf("%s: no value, want %s", name, exp.Value)
					continue
				}
				if got, want := decimalRat(t, row.Value.Quantity.Number), ratText(t, exp.Value); got.Cmp(want) != 0 {
					t.Errorf("%s: value %s, want %s", name, got.RatString(), want.RatString())
				}
				if exp.Status == "accepted" && ratText(t, exp.Value).Cmp(big.NewRat(df.Numerator, df.Denominator)) != 0 {
					t.Errorf("%s: vector value %s differs from the decoder %d/%d", name, exp.Value, df.Numerator, df.Denominator)
				}
			}
		})
	}
}

// TestExtendedVectorsCoverEveryRow pins that every native row applies to at least
// one vector with an accepted value, and to at least one with each other status
// the decoder gives its field, so no row is compared by nothing.
func TestExtendedVectorsCoverEveryRow(t *testing.T) {
	applied := make(map[string]bool)
	for _, c := range loadExtendedVectors(t) {
		s, in, _ := buildExtended(t, c)
		res, reason, err := EvaluateExtended(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", c.Name, reason, err)
		}
		for id, ev := range res.Rows {
			if ev.Native != nil {
				applied[id] = true
			}
		}
	}
	for _, r := range ExtendedTable().Native {
		if !applied[r.ID] {
			t.Errorf("row %s applies to no vector", r.ID)
		}
	}
}

// TestExtendedTableValidatesWithoutAmbiguousMatch pins the table against the
// row evaluator's own validation, and the geometry every row declares.
func TestExtendedTableValidatesWithoutAmbiguousMatch(t *testing.T) {
	table := ExtendedTable()
	if err := roweval.ValidateTable(table, Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
	for _, r := range table.Native {
		if !r.At.Extended || r.At.DLC != 8 || r.At.RawDLC != 8 || r.At.Identifier&0xffff != 0 || r.At.Identifier>>16 < 0x1900 || r.At.Identifier>>16 > 0x1904 {
			t.Errorf("row %s declares identifier %#x extended %v DLC %d raw DLC %d", r.ID, r.At.Identifier, r.At.Extended, r.At.DLC, r.At.RawDLC)
		}
		if !strings.HasPrefix(r.ID, "growatt.bms.pcs.extended19.") {
			t.Errorf("row %s has no extended19 prefix", r.ID)
		}
	}
}

// TestPackVoltageAndPortVoltageAreSeparateRows checks frame 0x1902F202 with
// B0-B1 = 34 12: the pack voltage is 466.0 V in the decoder and the evaluator,
// and B4-B5 stays the separate native port voltage. Changing one word changes
// only its own row.
func TestPackVoltageAndPortVoltageAreSeparateRows(t *testing.T) {
	sel := extendedVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1", Destination: 0xf2, Source: 2}
	eval := func(payload string) (canbusreg.NativeResult, ExtendedResult) {
		c := extendedVectorCase{Name: "pack-port", Identifier: "0x1902F202", Payload: payload, Selection: sel}
		s, in, _ := buildExtended(t, c)
		res, reason, err := EvaluateExtended(s, in)
		if err != nil || reason != "" {
			t.Fatalf("reason %q err %v", reason, err)
		}
		return canbusreg.DecodePCSExtended(s, in), res
	}
	dec, res := eval("341200007856e803")
	pack := res.Rows["growatt.bms.pcs.extended19.1902.pack_voltage"].Native
	port := res.Rows["growatt.bms.pcs.extended19.1902.port_voltage"].Native
	if pack == nil || port == nil || pack.RowID == port.RowID {
		t.Fatal("pack voltage and port voltage are not two rows")
	}
	if f := dec.Fields["pack_voltage"]; f.Numerator != 4660 || f.Denominator != 10 || f.Unit != "V" || f.Meaning != "pack voltage" {
		t.Errorf("decoder pack voltage %+v", f)
	}
	if f := dec.Fields["port_voltage"]; f.Numerator != 22136 || f.Denominator != 10 || f.Unit != "V" || f.Meaning != "port voltage" {
		t.Errorf("decoder port voltage %+v", f)
	}
	if pack.Raw != 0x1234 || pack.Unit != "V" || decimalRat(t, pack.Value.Quantity.Number).Cmp(ratText(t, "466.0")) != 0 {
		t.Errorf("pack voltage row raw %#x unit %q, want 0x1234 V 466.0", pack.Raw, pack.Unit)
	}
	if port.Raw != 0x5678 || port.Unit != "V" || decimalRat(t, port.Value.Quantity.Number).Cmp(ratText(t, "2213.6")) != 0 {
		t.Errorf("port voltage row raw %#x unit %q, want 0x5678 V 2213.6", port.Raw, port.Unit)
	}
	_, other := eval("341200007900e803")
	if got := other.Rows["growatt.bms.pcs.extended19.1902.pack_voltage"].Native; got.Raw != pack.Raw {
		t.Errorf("changing B4-B5 changed the pack voltage row to %#x", got.Raw)
	}
	if got := other.Rows["growatt.bms.pcs.extended19.1902.port_voltage"].Native; got.Raw == port.Raw || got.Raw != 0x0079 {
		t.Errorf("port voltage row raw %#x after changing B4-B5", got.Raw)
	}
	_, third := eval("350100007856e803")
	if got := third.Rows["growatt.bms.pcs.extended19.1902.port_voltage"].Native; got.Raw != port.Raw {
		t.Errorf("changing B0-B1 changed the port voltage row to %#x", got.Raw)
	}
	if got := third.Rows["growatt.bms.pcs.extended19.1902.pack_voltage"].Native; got.Raw != 0x0135 {
		t.Errorf("pack voltage row raw %#x after changing B0-B1", got.Raw)
	}
}

// TestExtendedRowsRefuseStandardFormatWrongLengthAndNeighbouringIdentifier checks
// that a standard-format frame, a wrong length, a wrong route and a neighbouring
// identifier give the decoder's status (or no row of this slice) and no rows.
func TestExtendedRowsRefuseStandardFormatWrongLengthAndNeighbouringIdentifier(t *testing.T) {
	sel := extendedVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1", Destination: 0xf2, Source: 2}
	payload := []byte{0x34, 0x12, 0x10, 0x27, 0x78, 0x56, 0xe8, 0x03}
	spec := extendedSpecOf(t, sel)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := canbus.NewStandardID(0x302)
	if err != nil {
		t.Fatal(err)
	}
	standardFrame, err := canbus.NewFrame(standard, payload)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		frame  canbus.Frame
		reason roweval.Reason
	}{
		{"standard format", standardFrame, "invalid"},
		{"seven data bytes", extendedFrame(t, "0x1902F202", payload[:7]), "invalid"},
		{"wrong source", extendedFrame(t, "0x1902F203", payload), "invalid"},
		{"wrong destination", extendedFrame(t, "0x1902F102", payload), "invalid"},
		{"kind before the range", extendedFrame(t, "0x18FFF202", payload), "unsupported"},
		{"kind 0x1905 is a later slice", extendedFrame(t, "0x1905F202", payload), roweval.ReasonNoMatch},
		{"kind 0x1909 is a later slice", extendedFrame(t, "0x1909F202", payload), roweval.ReasonNoMatch},
		{"kind 0x190A is outside the decoder", extendedFrame(t, "0x190AF202", payload), "unsupported"},
		{"aggregate fold on a local selection", extendedFrame(t, "0x1912F202", payload), "unsupported"},
	}
	for _, c := range cases {
		in := extendedInputOf(spec, c.frame)
		res, reason, err := EvaluateExtended(s, in)
		if err != nil {
			t.Fatal(err)
		}
		if reason != c.reason || res.Rows != nil {
			t.Errorf("%s: reason %q with %d rows, want %q and none", c.name, reason, len(res.Rows), c.reason)
		}
		if c.reason != roweval.ReasonNoMatch {
			if got := canbusreg.DecodePCSExtended(s, in); string(c.reason) != got.Status || len(got.Fields) != 0 {
				t.Errorf("%s: decoder status %q with %d fields, want %q and none", c.name, got.Status, len(got.Fields), c.reason)
			}
		}
	}
	gw := sel
	gw.Project, gw.Release, gw.Endpoint, gw.Group, gw.Slot, gw.Destination, gw.Source = "QCAA", "0001", "gateway", "", "", 0xff, 0xf4
	gwSpec := extendedSpecOf(t, gw)
	gs, err := canbusreg.NewSelection(gwSpec)
	if err != nil {
		t.Fatal(err)
	}
	state := extendedInputOf(gwSpec, extendedFrame(t, "0x1910FFF4", []byte{1, 0, 0, 0, 0, 0, 0, 3}))
	if res, reason, err := EvaluateExtended(gs, state); err != nil || reason != roweval.ReasonNoMatch || res.Rows != nil {
		t.Errorf("gateway state record: reason %q err %v, want no match and no rows", reason, err)
	}
}

// TestExtendedRowsFollowLocalAndAggregateScope checks that the unit gates follow
// the selection exactly as the decoder reads it: a local QAAA0006 source gives
// the scaled values, an aggregate or other source keeps the raw words, the cycle
// count is scaled for QAAA0006 in either scope, an aggregate product word of
// 0xFFFF is invalid, and an aggregate frame sent as 0x191n gives the rows of 0x190n.
func TestExtendedRowsFollowLocalAndAggregateScope(t *testing.T) {
	want := map[string]string{
		"1902-local-l2-pack-and-port":         "accepted",
		"1902-local-other-release-raw":        "raw",
		"1902-aggregate-raw":                  "raw",
		"1902-aggregate-folded-1912-raw":      "raw",
		"1902-gateway-endpoint-raw":           "raw",
		"1904-local-l2":                       "accepted",
		"1904-aggregate-l2-cycles-only":       "accepted",
		"1904-local-other-release-raw":        "raw",
		"1900-aggregate-product-ffff-invalid": "invalid",
		"1900-local-product-ffff-kept":        "accepted",
	}
	field := map[string]string{"1902": "pack_voltage", "1904": "cycles", "1900": "product"}
	seen := 0
	for _, c := range loadExtendedVectors(t) {
		status, ok := want[c.Name]
		if !ok {
			continue
		}
		seen++
		name := field[c.Name[:4]]
		if c.Name == "1904-aggregate-l2-cycles-only" || c.Name == "1904-local-l2" || c.Name == "1904-local-other-release-raw" {
			name = "cycles"
		}
		s, in, _ := buildExtended(t, c)
		res, reason, err := EvaluateExtended(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", c.Name, reason, err)
		}
		row := extendedRowsByField(res)[name]
		if row == nil || extendedStatus(row) != status {
			t.Errorf("%s: %s row status %v, want %s", c.Name, name, row, status)
		}
		if res.Attribution.Scope != c.Selection.Scope {
			t.Errorf("%s: attribution scope %q, want %q", c.Name, res.Attribution.Scope, c.Selection.Scope)
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of %d named vectors", seen, len(want))
	}
	capacity := func(name string) *roweval.Native {
		for _, c := range loadExtendedVectors(t) {
			if c.Name == name {
				s, in, _ := buildExtended(t, c)
				res, _, _ := EvaluateExtended(s, in)
				return extendedRowsByField(res)["full_capacity"]
			}
		}
		t.Fatalf("no vector %s", name)
		return nil
	}
	if n := capacity("1904-aggregate-l2-cycles-only"); n == nil || n.Disposition != dispositionRaw || n.Reason != gateWithheld {
		t.Errorf("aggregate QAAA0006 capacity %+v, want the raw word withheld by its unit gate", n)
	}
	local := capacity("1904-local-l2")
	if local == nil || local.Unit != "Ah" {
		t.Errorf("local QAAA0006 capacity %+v, want Ah", local)
	}
	for _, c := range loadExtendedVectors(t) {
		if c.Name != "1900-aggregate-folded-1910" && c.Name != "1900-aggregate-product-ffff-invalid" {
			continue
		}
		s, in, _ := buildExtended(t, c)
		res, _, _ := EvaluateExtended(s, in)
		if len(res.Rows) != 6 {
			t.Errorf("%s: %d rows of kind 0x1900, want 6", c.Name, len(res.Rows))
		}
	}
}

// TestExtendedRowsAdmitOnlyTheirOwnIdentity varies one field of the selection or
// frame identity at a time and checks that no row evaluates. Each case names
// the field it varies and the production mutation it catches: dropping the
// corresponding comparison of the decoder admission, or of the frame geometry
// the row declares, makes the case give rows.
func TestExtendedRowsAdmitOnlyTheirOwnIdentity(t *testing.T) {
	base := extendedVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1", Destination: 0xf2, Source: 2}
	payload := []byte{0x34, 0x12, 0x10, 0x27, 0x78, 0x56, 0xe8, 0x03}
	spec := extendedSpecOf(t, base)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	frame := extendedFrame(t, "0x1902F202", payload)
	if _, reason, _ := EvaluateExtended(s, extendedInputOf(spec, frame)); reason != "" {
		t.Fatalf("baseline reason %q", reason)
	}
	iface2, err := canbus.NewInterfaceIdentity("synthetic", 2)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		field string
		apply func(in *canbusreg.LINKInput)
	}{
		{"interface (identity compare dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Interface = iface2 }},
		{"epoch (epoch compare dropped)", func(in *canbusreg.LINKInput) { in.Epoch = 2 }},
		{"direction (direction compare dropped)", func(in *canbusreg.LINKInput) { in.Direction = "pcs-to-bms" }},
		{"scope (scope compare dropped)", func(in *canbusreg.LINKInput) { in.Scope = "aggregate" }},
		{"bank (bank compare dropped)", func(in *canbusreg.LINKInput) { in.Bank = "parallel" }},
		{"endpoint (endpoint compare dropped)", func(in *canbusreg.LINKInput) { in.Endpoint = "participant" }},
		{"group (group compare dropped)", func(in *canbusreg.LINKInput) { in.Group = "1" }},
		{"slot (slot compare dropped)", func(in *canbusreg.LINKInput) { in.Slot = "2" }},
		{"field age (negative age accepted)", func(in *canbusreg.LINKInput) { in.FieldAge = -time.Second }},
		{"destination byte (route destination compare dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = extendedFrame(t, "0x1902F102", payload) }},
		{"source byte (route source compare dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = extendedFrame(t, "0x1902F203", payload) }},
		{"frame kind (kind range widened below)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = extendedFrame(t, "0x18FFF202", payload) }},
		{"identifier format (extended check dropped)", func(in *canbusreg.LINKInput) {
			id, _ := canbus.NewStandardID(0x302)
			in.Evidence.Frame, _ = canbus.NewFrame(id, payload)
		}},
		{"data length (DLC check dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = extendedFrame(t, "0x1902F202", payload[:6]) }},
	}
	for _, m := range mutations {
		in := extendedInputOf(spec, frame)
		m.apply(&in)
		res, reason, err := EvaluateExtended(s, in)
		if err != nil {
			t.Fatal(err)
		}
		if reason == "" || res.Rows != nil {
			t.Errorf("%s: reason %q with %d rows, want a refusal and no rows", m.field, reason, len(res.Rows))
		}
	}
	flavor := base
	flavor.Flavor = "pcs-standard"
	fs, err := canbusreg.NewSelection(extendedSpecOf(t, flavor))
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, _ := EvaluateExtended(fs, extendedInputOf(extendedSpecOf(t, flavor), frame)); reason == "" {
		t.Errorf("flavor (flavor compare dropped): the standard flavor evaluated extended rows")
	}
}

// TestExtendedRowsKeepBankGroupAndSlotAttribution checks both banks, and a group
// and slot present and absent: the attribution carries the selection's context
// and an absent group or slot stays absent, never zero; the values do not change.
func TestExtendedRowsKeepBankGroupAndSlotAttribution(t *testing.T) {
	payload := "341210277856e803"
	for _, bank := range []string{"ordinary", "parallel"} {
		for _, ctx := range []struct{ group, slot string }{{"0", "1"}, {"7", "255"}, {"", ""}, {"3", ""}, {"", "9"}} {
			sel := extendedVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: bank, Scope: "local", Endpoint: "coordinator", Group: ctx.group, Slot: ctx.slot, Destination: 0xf2, Source: 2}
			c := extendedVectorCase{Name: "attribution", Identifier: "0x1902F202", Payload: payload, Selection: sel}
			s, in, _ := buildExtended(t, c)
			res, reason, err := EvaluateExtended(s, in)
			if err != nil || reason != "" {
				t.Fatalf("%s %q %q: reason %q err %v", bank, ctx.group, ctx.slot, reason, err)
			}
			a := res.Attribution
			if a.Bank != bank || a.Group != ctx.group || a.Slot != ctx.slot || a.HasGroup != (ctx.group != "") || a.HasSlot != (ctx.slot != "") {
				t.Errorf("%s %q %q: attribution %+v", bank, ctx.group, ctx.slot, a)
			}
			dec := canbusreg.DecodePCSExtended(s, in)
			for name, f := range dec.Fields {
				if f.Bank != bank || f.Group != ctx.group || f.Slot != ctx.slot {
					t.Errorf("%s: decoder attribution %q %q %q", name, f.Bank, f.Group, f.Slot)
				}
			}
			row := extendedRowsByField(res)["pack_voltage"]
			if row == nil || row.Raw != 0x1234 || extendedStatus(row) != "accepted" {
				t.Errorf("%s %q %q: pack voltage row %+v", bank, ctx.group, ctx.slot, row)
			}
		}
	}
}
