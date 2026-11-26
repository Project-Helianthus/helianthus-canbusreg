package link

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

type pageFile struct {
	Cases []pageCase `json:"cases"`
}

type pageCase struct {
	Name        string               `json:"name"`
	Evaluator   string               `json:"evaluator"`
	Family      string               `json:"family"`
	Payload     string               `json:"payload"`
	Selection   pageSelection        `json:"selection"`
	Status      string               `json:"status"`
	Reason      string               `json:"reason"`
	Rows        map[string]pageValue `json:"rows"`
	Attribution *pageAttribution     `json:"attribution"`
}

type pageSelection struct {
	Project, Release, Choice, Direction, Scope, Bank, Source, Destination, Group, Slot string
}

type pageAttribution struct {
	Bank     string `json:"bank"`
	Group    string `json:"group"`
	Slot     string `json:"slot"`
	HasGroup bool   `json:"has_group"`
	HasSlot  bool   `json:"has_slot"`
}

type pageValue struct {
	Value       string `json:"value"`
	Unit        string `json:"unit"`
	Disposition string `json:"disposition"`
	Raw         string `json:"raw"`
}

func pageCases(t *testing.T) []pageCase {
	t.Helper()
	data, err := os.ReadFile("testdata/aggregate_identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var f pageFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func pageByte(t *testing.T, text string) uint8 {
	t.Helper()
	n, err := strconv.ParseUint(text, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	return uint8(n)
}

func pageSpec(t *testing.T, v pageSelection) canbusreg.SelectionSpec {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: v.Project, Release: v.Release, Choice: v.Choice,
		Direction: v.Direction, Scope: v.Scope, Bank: v.Bank, Endpoint: v.Source, Group: v.Group, Slot: v.Slot, RouteSet: true,
		Destination: pageByte(t, v.Destination), Source: pageByte(t, v.Source), Epoch: 1}
}

func pageObservation(t *testing.T, spec canbusreg.SelectionSpec, family string, payload []byte, sequence uint64) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	kind, err := strconv.ParseUint(family, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	id, err := canbus.NewExtendedID(uint32(kind)<<16 | uint32(spec.Destination)<<8 | uint32(spec.Source))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	evidence := canbusreg.Evidence{Interface: spec.Interface, Frame: frame, Sequence: sequence, Monotonic: time.Duration(sequence) * time.Second}
	return s, canbusreg.LINKInput{Evidence: evidence, Epoch: spec.Epoch, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank,
		Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
}

func pageInput(t *testing.T, c pageCase) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return pageObservation(t, pageSpec(t, c.Selection), c.Family, payload, 3)
}

func pageEvaluate(t *testing.T, c pageCase) (Result, roweval.Reason) {
	t.Helper()
	s, in := pageInput(t, c)
	evaluate := EvaluateAggregate
	if c.Evaluator == "identity" {
		evaluate = EvaluateIdentity
	}
	res, reason, err := evaluate(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	return res, reason
}

func reasonText(r roweval.Reason) string {
	switch r {
	case "":
		return ""
	case roweval.ReasonNoMatch:
		return "no-match"
	}
	return string(r)
}

func appliedRows(res Result) map[string]roweval.Evaluation {
	out := make(map[string]roweval.Evaluation)
	for id, ev := range res.Rows {
		if ev.Reason != roweval.ReasonNoMatch || ev.Native != nil {
			out[id] = ev
		}
	}
	return out
}

func pageRat(t *testing.T, text string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("%q is not a number", text)
	}
	return r
}

func lastSegment(id string) string { return id[strings.LastIndex(id, ".")+1:] }

func TestAggregateAndIdentityRowsMatchThePageVectors(t *testing.T) {
	for _, c := range pageCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			res, reason := pageEvaluate(t, c)
			if res.Status != c.Status || reasonText(reason) != c.Reason {
				t.Fatalf("status %q reason %q, page %q %q", res.Status, reasonText(reason), c.Status, c.Reason)
			}
			got := appliedRows(res)
			for id := range got {
				if _, ok := c.Rows[id]; !ok {
					t.Errorf("row %s applies but the page vector does not name it", id)
				}
			}
			for id, want := range c.Rows {
				ev, ok := got[id]
				if !ok || ev.Native == nil {
					t.Errorf("row %s does not apply", id)
					continue
				}
				n := ev.Native
				if n.Unit != want.Unit || n.Disposition != want.Disposition || strconv.FormatUint(n.Raw, 10) != want.Raw {
					t.Errorf("row %s: unit %q disposition %q raw %d, page %q %q %s", id, n.Unit, n.Disposition, n.Raw, want.Unit, want.Disposition, want.Raw)
				}
				if want.Disposition == "invalid" {
					if n.Value != nil || n.Reason == "" {
						t.Errorf("row %s: an invalid word keeps no value and states its reason", id)
					}
					continue
				}
				if n.Value == nil || n.Value.Quantity == nil || ratOf(t, n.Value.Quantity.Number).Cmp(pageRat(t, want.Value)) != 0 {
					t.Errorf("row %s: value %+v, page %s", id, n.Value, want.Value)
				}
			}
			if c.Attribution != nil {
				a := res.Attribution
				w := c.Attribution
				if a.Bank != w.Bank || a.Group != w.Group || a.Slot != w.Slot || a.HasGroup != w.HasGroup || a.HasSlot != w.HasSlot {
					t.Errorf("attribution %+v, page %+v", a, *w)
				}
			}
		})
	}
}

func TestAggregateAndIdentityRowsEqualTheFrozenDecoder(t *testing.T) {
	for _, c := range pageCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in := pageInput(t, c)
			res, _ := pageEvaluate(t, c)
			decoded := canbusreg.DecodeLINKTelemetry(s, in)
			serial := strings.EqualFold(c.Family, "18FB")
			if !serial && decoded.Status != res.Status {
				t.Errorf("decoder status %q, evaluator %q", decoded.Status, res.Status)
			}
			if serial && len(decoded.Fields) != 0 {
				t.Errorf("the telemetry decoder emits fields for a serial fragment: %d", len(decoded.Fields))
			}
			status := map[string]string{"accepted": dispositionAccepted, "raw": dispositionRaw, "invalid": dispositionInvalid}
			seen := make(map[string]bool)
			for id, ev := range appliedRows(res) {
				if serial {
					continue
				}
				field, ok := decoded.Fields[lastSegment(id)]
				if !ok {
					t.Errorf("row %s has no decoder field", id)
					continue
				}
				seen[lastSegment(id)] = true
				n := ev.Native
				if status[field.Status] != n.Disposition || field.Unit != n.Unit {
					t.Errorf("%s: decoder %q %q, evaluator %q %q", id, field.Status, field.Unit, n.Disposition, n.Unit)
				}
				if field.Numerator != int64(n.Raw) || field.Denominator != 1 {
					t.Errorf("%s: decoder word %d/%d, evaluator raw %d", id, field.Numerator, field.Denominator, n.Raw)
				}
				if n.Value != nil && ratOf(t, n.Value.Quantity.Number).Cmp(big.NewRat(field.Numerator, field.Denominator)) != 0 {
					t.Errorf("%s: decoder value %d, evaluator %+v", id, field.Numerator, n.Value.Quantity.Number)
				}
				if field.Evidence != res.Evidence || field.Group != res.Attribution.Group || field.Slot != res.Attribution.Slot || field.Bank != res.Attribution.Bank || field.Scope != res.Attribution.Scope {
					t.Errorf("%s: decoder evidence or attribution differs from the evaluator", id)
				}
			}
			for name := range decoded.Fields {
				if !seen[name] {
					t.Errorf("decoder field %s has no applicable row", name)
				}
			}
		})
	}
}

func TestSerialFragmentRowsEqualTheAssembledSerial(t *testing.T) {
	spec := pageSpec(t, pageSelection{Project: "QAAA", Release: "0006", Direction: "module-to-coordinator", Scope: "local", Bank: "parallel", Source: "0A", Destination: "F4", Group: "2", Slot: "4"})
	s, identityInput := pageObservation(t, spec, "1802", []byte("QAAA0006"), 1)
	assembler := canbusreg.NewLINKIdentityAssembler(s)
	if r := assembler.DecodeLINKIdentity(identityInput); r.Status != "accepted" {
		t.Fatalf("identity %q", r.Status)
	}
	var fromRows []byte
	for at, p := range []string{"0f31323334353637", "2f38394142434445", "4f46470000000000"} {
		payload, _ := hex.DecodeString(p)
		sel, in := pageObservation(t, spec, "18FB", payload, uint64(at+2))
		if r := assembler.DecodeLINKIdentity(in); r.Status != "accepted" {
			t.Fatalf("fragment %d: %q", at, r.Status)
		}
		res, reason, err := EvaluateIdentity(sel, in)
		if err != nil || reason != "" {
			t.Fatalf("fragment %d: %q %v", at, reason, err)
		}
		length := (payload[0] & 0x1f) + 1
		ids := res.Rows[rowPrefix+"serial_length_code"].Native
		fragment := res.Rows[rowPrefix+"serial_fragment"].Native
		if ids.Raw+1 != uint64(length) || fragment.Raw != uint64(at) {
			t.Errorf("fragment %d: length code %d fragment %d", at, ids.Raw, fragment.Raw)
		}
		count := 7
		if at == 2 {
			count = 2
		}
		for i := 1; i <= count; i++ {
			fromRows = append(fromRows, byte(res.Rows[rowPrefix+"serial_byte_"+strconv.Itoa(i)].Native.Raw))
		}
	}
	snapshot := assembler.SnapshotLINKIdentity()
	if !snapshot.HasSerial || string(snapshot.Serial[:]) != string(fromRows) || string(fromRows) != "123456789ABCDEFG" {
		t.Errorf("assembled %q, rows %q", snapshot.Serial[:], fromRows)
	}
}

func TestEveryAggregateAndIdentityRowHasAVectorAndDistinctIds(t *testing.T) {
	covered := make(map[string]string)
	invalid := make(map[string]bool)
	for _, c := range pageCases(t) {
		for id, want := range c.Rows {
			covered[id] = want.Unit
			if want.Disposition == "invalid" {
				invalid[id] = true
			}
		}
	}
	seen := make(map[string]bool)
	for _, table := range []roweval.Table[Selector]{AggregateTable(), IdentityTable()} {
		if err := roweval.ValidateTable(table, Descriptors(), roweval.Roles{}); err != nil {
			t.Fatal(err)
		}
		for _, row := range table.Native {
			if _, ok := covered[row.ID]; !ok {
				t.Errorf("row %s has no vector", row.ID)
			}
			if seen[row.ID] {
				t.Errorf("row id %s is defined twice", row.ID)
			}
			seen[row.ID] = true
			if strings.HasPrefix(row.ID, rowPrefix+"1809.") || calendarFields[lastSegment(row.ID)] {
				continue
			}
			if row.Unit != "" {
				t.Errorf("row %s carries a unit the page does not give", row.ID)
			}
		}
	}
	for field := range calendarFields {
		if !invalid[rowPrefix+field] {
			t.Errorf("calendar row %s has no invalid vector", field)
		}
	}
	for id := range covered {
		if !seen[id] {
			t.Errorf("vector names row %s that no table defines", id)
		}
	}
}

func TestAggregateRowIdsTakeTheFrameWhereAnotherFamilyEmitsTheName(t *testing.T) {
	ids := make(map[string]bool)
	for _, row := range AggregateTable().Native {
		ids[row.ID] = true
	}
	for _, id := range []string{"growatt.bms.link.180D.maximum_index", "growatt.bms.link.180D.minimum_index", "growatt.bms.link.180F.current", "growatt.bms.link.limit", "growatt.bms.link.count", "growatt.bms.link.quotient"} {
		if !ids[id] {
			t.Errorf("missing row %s", id)
		}
	}
	for _, id := range []string{"growatt.bms.link.current", "growatt.bms.link.maximum_index", "growatt.bms.link.minimum_index"} {
		if ids[id] {
			t.Errorf("unqualified row %s must not exist", id)
		}
	}
	for _, row := range IdentityTable().Native {
		if row.At.Family == 0x1809 && lastSegment(row.ID) == "status" && row.ID != "growatt.bms.link.1809.status" {
			t.Errorf("clock status row %s lacks the frame qualifier", row.ID)
		}
	}
}

func TestAggregateAndIdentityEvaluationRefusesWhatTheDecoderRefuses(t *testing.T) {
	cases := map[string]pageCase{}
	for _, c := range pageCases(t) {
		cases[c.Name] = c
	}
	bases := []pageCase{cases["coordinator state bytes"], cases["clock report with a calendar date"], cases["first serial fragment"]}
	for _, base := range bases {
		for name, mutate := range map[string]func(*canbusreg.LINKInput){
			"asserted bank":      func(in *canbusreg.LINKInput) { in.Bank = "parallel" },
			"asserted group":     func(in *canbusreg.LINKInput) { in.Group = "1" },
			"asserted slot":      func(in *canbusreg.LINKInput) { in.Slot = "9" },
			"asserted endpoint":  func(in *canbusreg.LINKInput) { in.Endpoint = "03" },
			"epoch":              func(in *canbusreg.LINKInput) { in.Epoch = 2 },
			"negative field age": func(in *canbusreg.LINKInput) { in.FieldAge = -time.Second },
			"direction":          func(in *canbusreg.LINKInput) { in.Direction = "bms-to-pcs" },
			"scope":              func(in *canbusreg.LINKInput) { in.Scope = "other" },
		} {
			t.Run(base.Family+" "+name, func(t *testing.T) {
				s, in := pageInput(t, base)
				mutate(&in)
				var res Result
				var reason roweval.Reason
				if base.Evaluator == "aggregate" {
					res, reason, _ = EvaluateAggregate(s, in)
				} else {
					res, reason, _ = EvaluateIdentity(s, in)
				}
				if reason != "invalid" || len(res.Rows) != 0 || canbusreg.DecodeLINKTelemetry(s, in).Status != "invalid" {
					t.Errorf("%q %d", reason, len(res.Rows))
				}
			})
		}
	}
	t.Run("standard identifier", func(t *testing.T) {
		c := cases["coordinator state bytes"]
		s, in := pageInput(t, c)
		id, err := canbus.NewStandardID(0x10d)
		if err != nil {
			t.Fatal(err)
		}
		in.Evidence.Frame, err = canbus.NewFrame(id, make([]byte, 8))
		if err != nil {
			t.Fatal(err)
		}
		if res, reason, _ := EvaluateAggregate(s, in); reason != "invalid" || len(res.Rows) != 0 {
			t.Errorf("%q %d", reason, len(res.Rows))
		}
	})
	t.Run("source address FF", func(t *testing.T) {
		spec := pageSpec(t, cases["clock report with a calendar date"].Selection)
		spec.Source, spec.Endpoint = 0xff, "FF"
		if _, err := canbusreg.NewSelection(spec); err == nil {
			t.Error("a LINK selection with endpoint FF was accepted")
		}
	})
}
