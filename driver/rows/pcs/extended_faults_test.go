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
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type faultVectorFile struct {
	Cases []faultVectorCase `json:"cases"`
}

type faultVectorCase struct {
	Name       string                 `json:"name"`
	Identifier string                 `json:"identifier"`
	Payload    string                 `json:"payload"`
	Selection  faultVectorSelection   `json:"selection"`
	Expect     map[string]faultExpect `json:"expect"`
}

type faultVectorSelection struct {
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

type faultExpect struct {
	Status string `json:"status"`
	Raw    uint64 `json:"raw"`
	Value  string `json:"value"`
}

func loadFaultVectors(t *testing.T) []faultVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/extended_1905_1909.json")
	if err != nil {
		t.Fatal(err)
	}
	var f faultVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func faultVector(t *testing.T, name string) faultVectorCase {
	t.Helper()
	for _, c := range loadFaultVectors(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no vector named %s", name)
	return faultVectorCase{}
}

func faultSpecOf(t *testing.T, sel faultVectorSelection) canbusreg.SelectionSpec {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, RouteSet: true, Destination: sel.Destination, Source: sel.Source, Epoch: 1}
}

func faultInputOf(spec canbusreg.SelectionSpec, frame canbus.Frame) canbusreg.LINKInput {
	return canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: spec.Interface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: spec.Epoch, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
}

func faultFrame(t *testing.T, identifier string, payload []byte) canbus.Frame {
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

func buildFault(t *testing.T, c faultVectorCase) (canbusreg.Selection, canbusreg.LINKInput, canbus.Frame) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	spec := faultSpecOf(t, c.Selection)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame := faultFrame(t, c.Identifier, payload)
	return s, faultInputOf(spec, frame), frame
}

func faultStatus(n *roweval.Native) string {
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

func faultRowsByField(res ExtendedFaultResult) map[string]*roweval.Native {
	out := make(map[string]*roweval.Native)
	for id, ev := range res.Rows {
		if ev.Native != nil {
			out[fieldOf(id)] = ev.Native
		}
	}
	return out
}

// checkFaultVector compares one vector: each field the decoder emits has
// exactly one applicable row with the same status, raw word, exact value, unit
// and evidence grade, every applicable row is a decoder field, and the
// expectation is the hand-written vector value.
func checkFaultVector(t *testing.T, c faultVectorCase) {
	t.Helper()
	s, in, frame := buildFault(t, c)
	dec := canbusreg.DecodePCSExtended(s, in)
	if dec.Status != "accepted" {
		t.Fatalf("decoder status %q, want accepted", dec.Status)
	}
	res, reason, err := EvaluateExtendedFaults(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluator reason %q err %v", reason, err)
	}
	if res.Evidence != in.Evidence {
		t.Errorf("evidence differs from the observation")
	}
	want := Attribution{Scope: c.Selection.Scope, Bank: c.Selection.Bank, Group: c.Selection.Group, Slot: c.Selection.Slot, HasGroup: c.Selection.Group != "", HasSlot: c.Selection.Slot != ""}
	if res.Attribution != want {
		t.Errorf("attribution %+v, want %+v", res.Attribution, want)
	}
	applicable := faultRowsByField(res)
	if len(applicable) != len(res.Rows) || len(applicable) != len(dec.Fields) || len(c.Expect) != len(dec.Fields) {
		t.Errorf("%d applicable rows, %d evaluated, %d decoder fields, %d expectations", len(applicable), len(res.Rows), len(dec.Fields), len(c.Expect))
	}
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
		row, ok := applicable[name]
		if !ok {
			t.Errorf("field %s has no applicable row", name)
			continue
		}
		if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
			t.Errorf("%s: decoder raw evidence differs from the frame", name)
		}
		if got := faultStatus(row); got != exp.Status {
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
			if row.Value != nil {
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
		if exp.Status == "accepted" && df.Unit != "" && ratText(t, exp.Value).Cmp(big.NewRat(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: vector value %s differs from the decoder %d/%d", name, exp.Value, df.Numerator, df.Denominator)
		}
	}
}

func runFaultVectors(t *testing.T, prefixes ...string) {
	t.Helper()
	ran := 0
	for _, c := range loadFaultVectors(t) {
		for _, p := range prefixes {
			if strings.HasPrefix(c.Name, p) {
				ran++
				t.Run(c.Name, func(t *testing.T) { checkFaultVector(t, c) })
				break
			}
		}
	}
	if ran == 0 {
		t.Fatalf("no vector of %v", prefixes)
	}
}

// TestExtendedFaultRowsMatchDecoder checks the fault blocks 0x1907 and 0x1908,
// including the 0x1918 fold: every fault byte and every fault bit the decoder
// emits has one row with the decoder's value, status and evidence.
func TestExtendedFaultRowsMatchDecoder(t *testing.T) {
	runFaultVectors(t, "1907-", "1908-")
}

// TestExtendedStateRowsMatchDecoder checks the gateway state record 0x1910.
func TestExtendedStateRowsMatchDecoder(t *testing.T) {
	runFaultVectors(t, "1910-")
}

// TestExtendedExtremaAndLimitRowsMatchDecoder checks frames 0x1905, 0x1906 and
// 0x1909 and their aggregate folds.
func TestExtendedExtremaAndLimitRowsMatchDecoder(t *testing.T) {
	runFaultVectors(t, "1905-", "1906-", "1909-")
}

// TestExtendedFaultVectorsCoverEveryRow pins that every native row applies to at
// least one vector, and that each fault bit row sees both bit values.
func TestExtendedFaultVectorsCoverEveryRow(t *testing.T) {
	applied := make(map[string]bool)
	values := make(map[string]map[uint64]bool)
	for _, c := range loadFaultVectors(t) {
		s, in, _ := buildFault(t, c)
		res, reason, err := EvaluateExtendedFaults(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", c.Name, reason, err)
		}
		for id, ev := range res.Rows {
			if ev.Native == nil {
				continue
			}
			applied[id] = true
			if values[id] == nil {
				values[id] = make(map[uint64]bool)
			}
			values[id][ev.Native.Raw] = true
		}
	}
	for _, r := range ExtendedFaultTable().Native {
		if !applied[r.ID] {
			t.Errorf("row %s applies to no vector", r.ID)
		}
		if strings.Contains(r.ID, "current_fault_bit_") && len(values[r.ID]) != 2 {
			t.Errorf("fault bit row %s sees %d distinct bit values, want 0 and 1", r.ID, len(values[r.ID]))
		}
	}
}

// TestExtendedFaultTableValidatesAndKeepsGeometry pins the table against the row
// evaluator's validation, the declared geometry of every row, the row counts of
// each frame and the bit index to byte and bit mapping of the fault blocks.
func TestExtendedFaultTableValidatesAndKeepsGeometry(t *testing.T) {
	table := ExtendedFaultTable()
	if err := roweval.ValidateTable(table, Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
	count := make(map[uint32]int)
	for _, r := range table.Native {
		kind := r.At.Identifier >> 16
		count[kind]++
		if !r.At.Extended || r.At.DLC != 8 || r.At.RawDLC != 8 || r.At.Identifier&0xffff != 0 {
			t.Errorf("row %s declares identifier %#x extended %v DLC %d raw DLC %d", r.ID, r.At.Identifier, r.At.Extended, r.At.DLC, r.At.RawDLC)
		}
		if !strings.HasPrefix(r.ID, fmt.Sprintf("growatt.bms.pcs.extended19.%X.", kind)) {
			t.Errorf("row %s does not carry the extended19 flavor and the frame %X", r.ID, kind)
		}
		if r.When.Equal[qFlavor] != "pcs-extended19" {
			t.Errorf("row %s When flavor %q", r.ID, r.When.Equal[qFlavor])
		}
		if i := strings.Index(r.ID, "current_fault_bit_"); i >= 0 {
			index, err := strconv.Atoi(r.ID[i+len("current_fault_bit_"):])
			if err != nil {
				t.Fatal(err)
			}
			wantKind := uint32(0x1907)
			if index >= 64 {
				wantKind = 0x1908
			}
			if kind != wantKind || uint32(r.Wire.BitOffset) != uint32(index%64) || r.Wire.Width != 1 {
				t.Errorf("bit row %s: frame %X offset %d width %d, want frame %X offset %d width 1", r.ID, kind, r.Wire.BitOffset, r.Wire.Width, wantKind, index%64)
			}
		}
	}
	want := map[uint32]int{0x1905: 4, 0x1906: 6, 0x1907: 72, 0x1908: 72, 0x1909: 5, 0x1910: 8}
	for kind, n := range want {
		if count[kind] != n {
			t.Errorf("frame %X has %d rows, want %d", kind, count[kind], n)
		}
	}
	if len(count) != len(want) {
		t.Errorf("rows for %d frames, want %d", len(count), len(want))
	}
}

// TestExtendedFaultRowsFollowUnitGatesAndFolds checks that the millivolt unit
// follows the selection as the decoder reads it, and that an aggregate frame
// sent as 0x191n gives the same rows as 0x190n.
func TestExtendedFaultRowsFollowUnitGatesAndFolds(t *testing.T) {
	voltage := func(name string) *roweval.Native {
		c := faultVector(t, name)
		s, in, _ := buildFault(t, c)
		res, reason, err := EvaluateExtendedFaults(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", name, reason, err)
		}
		return faultRowsByField(res)["maximum_voltage"]
	}
	local := voltage("1905-local-l2")
	if local == nil || local.Unit != "mV" || local.Disposition != dispositionAccepted || decimalRat(t, local.Value.Quantity.Number).Cmp(big.NewRat(3280, 1)) != 0 {
		t.Errorf("local QAAA0006 voltage %+v, want 3280 mV accepted", local)
	}
	for _, name := range []string{"1905-local-other-release-raw", "1905-aggregate-raw", "1905-aggregate-folded-1915-raw", "1905-gateway-endpoint-raw"} {
		n := voltage(name)
		if n == nil || n.Disposition != dispositionRaw || n.Reason != gateWithheld || n.Unit != "" || n.Value != nil || n.Raw != 3280 {
			t.Errorf("%s: voltage %+v, want the raw word 3280 withheld by its unit gate", name, n)
		}
	}
	for _, pair := range [][2]string{{"1905-aggregate-raw", "1905-aggregate-folded-1915-raw"}} {
		ids := func(name string) map[string]uint64 {
			s, in, _ := buildFault(t, faultVector(t, name))
			res, _, _ := EvaluateExtendedFaults(s, in)
			out := make(map[string]uint64)
			for id, ev := range res.Rows {
				out[id] = ev.Native.Raw
			}
			return out
		}
		a, b := ids(pair[0]), ids(pair[1])
		if len(a) != 4 || len(a) != len(b) {
			t.Fatalf("%v: %d and %d rows, want 4", pair, len(a), len(b))
		}
		for id, raw := range a {
			if got, ok := b[id]; !ok || got != raw {
				t.Errorf("%v: row %s raw %d in the fold, want %d", pair, id, got, raw)
			}
		}
	}
}

// TestExtendedStateRowsFollowGatewayAndRange checks the gateway state record:
// state and downstream address are accepted only for the gateway selection, a
// downstream address outside 1 to 8 is invalid there, and padding that is not
// zero is invalid under every selection.
func TestExtendedStateRowsFollowGatewayAndRange(t *testing.T) {
	status := func(name, field string) string {
		s, in, _ := buildFault(t, faultVector(t, name))
		res, reason, err := EvaluateExtendedFaults(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", name, reason, err)
		}
		return faultStatus(faultRowsByField(res)[field])
	}
	for _, c := range []struct{ vector, field, want string }{
		{"1910-gateway-state", "state", "accepted"},
		{"1910-gateway-state", "downstream_address", "accepted"},
		{"1910-gateway-state-address-eight", "downstream_address", "accepted"},
		{"1910-gateway-state-address-zero-invalid", "downstream_address", "invalid"},
		{"1910-gateway-state-address-nine-invalid", "downstream_address", "invalid"},
		{"1910-gateway-state-padding-nonzero", "padding_3", "invalid"},
		{"1910-gateway-state-padding-nonzero", "padding_4", "raw"},
		{"1910-gateway-state-other-selection-raw", "state", "raw"},
		{"1910-gateway-state-other-selection-raw", "downstream_address", "raw"},
		{"1910-gateway-state-other-selection-raw", "flag", "raw"},
	} {
		if got := status(c.vector, c.field); got != c.want {
			t.Errorf("%s %s: status %q, want %q", c.vector, c.field, got, c.want)
		}
	}
}

// TestExtendedFaultRowsRefuseStandardFormatWrongLengthAndNeighbouringIdentifier
// checks that a standard-format frame, a wrong length, a wrong route and a
// neighbouring identifier give the decoder's status (or no row of this file) and
// no rows.
func TestExtendedFaultRowsRefuseStandardFormatWrongLengthAndNeighbouringIdentifier(t *testing.T) {
	sel := faultVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1", Destination: 0xf2, Source: 2}
	payload := []byte{0x03, 0x00, 0xd0, 0x0c, 0x05, 0x00, 0x2c, 0x0b}
	spec := faultSpecOf(t, sel)
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
		{"seven data bytes", faultFrame(t, "0x1905F202", payload[:7]), "invalid"},
		{"wrong source", faultFrame(t, "0x1905F203", payload), "invalid"},
		{"wrong destination", faultFrame(t, "0x1905F102", payload), "invalid"},
		{"kind 0x1904 is another slice", faultFrame(t, "0x1904F202", payload), roweval.ReasonNoMatch},
		{"kind 0x1902 is another slice", faultFrame(t, "0x1902F202", payload), roweval.ReasonNoMatch},
		{"kind 0x190A is outside the decoder", faultFrame(t, "0x190AF202", payload), "unsupported"},
		{"aggregate fold on a local selection", faultFrame(t, "0x1917F202", payload), "unsupported"},
		{"gateway state kind on an endpoint route", faultFrame(t, "0x1910F202", payload), "unsupported"},
	}
	for _, c := range cases {
		in := faultInputOf(spec, c.frame)
		res, reason, err := EvaluateExtendedFaults(s, in)
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
	gwSpec := faultSpecOf(t, gw)
	gs, err := canbusreg.NewSelection(gwSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0x1909FFF4", "0x1904FFF4"} {
		in := faultInputOf(gwSpec, faultFrame(t, name, []byte{1, 0, 0, 0, 0, 0, 0, 3}))
		if res, reason, err := EvaluateExtendedFaults(gs, in); err != nil || reason == "" || res.Rows != nil {
			if name == "0x1904FFF4" && reason == roweval.ReasonNoMatch {
				continue
			}
			t.Errorf("%s on the gateway state route: reason %q err %v rows %d", name, reason, err, len(res.Rows))
		}
	}
}

// TestExtendedFaultRowsAdmitOnlyTheirOwnIdentity varies one field of the
// selection or frame identity at a time and checks that no row evaluates. Each
// case names the field it varies and the production mutation it catches:
// dropping the corresponding comparison of the decoder admission, or of the
// frame geometry the row declares, makes the case give rows.
func TestExtendedFaultRowsAdmitOnlyTheirOwnIdentity(t *testing.T) {
	base := faultVectorSelection{Project: "QAAA", Release: "0006", Flavor: "pcs-extended19", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1", Destination: 0xf2, Source: 2}
	payload := []byte{0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80}
	spec := faultSpecOf(t, base)
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	frame := faultFrame(t, "0x1907F202", payload)
	if _, reason, _ := EvaluateExtendedFaults(s, faultInputOf(spec, frame)); reason != "" {
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
		{"destination byte (route destination compare dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = faultFrame(t, "0x1907F102", payload) }},
		{"source byte (route source compare dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = faultFrame(t, "0x1907F203", payload) }},
		{"frame kind (kind range widened above)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = faultFrame(t, "0x190AF202", payload) }},
		{"identifier format (extended check dropped)", func(in *canbusreg.LINKInput) {
			id, _ := canbus.NewStandardID(0x302)
			in.Evidence.Frame, _ = canbus.NewFrame(id, payload)
		}},
		{"data length (DLC check dropped)", func(in *canbusreg.LINKInput) { in.Evidence.Frame = faultFrame(t, "0x1907F202", payload[:6]) }},
	}
	for _, m := range mutations {
		in := faultInputOf(spec, frame)
		m.apply(&in)
		res, reason, err := EvaluateExtendedFaults(s, in)
		if err != nil {
			t.Fatal(err)
		}
		if reason == "" || res.Rows != nil {
			t.Errorf("%s: reason %q with %d rows, want a refusal and no rows", m.field, reason, len(res.Rows))
		}
	}
	flavor := base
	flavor.Flavor = "pcs-standard"
	fs, err := canbusreg.NewSelection(faultSpecOf(t, flavor))
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, _ := EvaluateExtendedFaults(fs, faultInputOf(faultSpecOf(t, flavor), frame)); reason == "" {
		t.Errorf("flavor (flavor compare dropped): the standard flavor evaluated extended rows")
	}
}
