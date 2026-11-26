package link

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

type diagFile struct {
	Cases []diagCase `json:"cases"`
}

type diagCase struct {
	Name      string                `json:"name"`
	Family    string                `json:"family"`
	Payload   string                `json:"payload"`
	Selection diagSelection         `json:"selection"`
	Status    string                `json:"status"`
	Faults    *diagFaults           `json:"faults"`
	Rows      map[string]diagExpect `json:"rows"`
	Absent    []string              `json:"absent"`
}

type diagSelection struct {
	Project, Release, Choice, Group, Slot, Bank, Source, Destination, Direction, Scope string
}

type diagFaults struct {
	Block int   `json:"block"`
	Set   []int `json:"set"`
	Named []int `json:"named"`
}

type diagExpect struct {
	Value       string `json:"value"`
	Unit        string `json:"unit"`
	Disposition string `json:"disposition"`
	Raw         string `json:"raw"`
	Grade       string `json:"grade"`
}

func diagVectors(t *testing.T) []diagCase {
	t.Helper()
	data, err := os.ReadFile("testdata/diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var f diagFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func diagByte(t *testing.T, text string) uint8 {
	t.Helper()
	n, err := strconv.ParseUint(text, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	return uint8(n)
}

func diagSelect(t *testing.T, v diagSelection) (canbusreg.SelectionSpec, canbusreg.Selection) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: v.Project, Release: v.Release, Choice: v.Choice,
		Direction: v.Direction, Scope: v.Scope, Bank: v.Bank, Endpoint: v.Source, Group: v.Group, Slot: v.Slot, RouteSet: true,
		Destination: diagByte(t, v.Destination), Source: diagByte(t, v.Source), Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, s
}

func diagInput(t *testing.T, spec canbusreg.SelectionSpec, family uint16, payload []byte) canbusreg.LINKInput {
	t.Helper()
	id, err := canbus.NewExtendedID(uint32(family)<<16 | uint32(spec.Destination)<<8 | uint32(spec.Source))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: spec.Interface, Frame: frame, Sequence: 3, Monotonic: time.Second}, Epoch: 1,
		Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
}

type diagRun struct {
	spec canbusreg.SelectionSpec
	sel  canbusreg.Selection
	in   canbusreg.LINKInput
	res  Result
}

func diagEvaluate(t *testing.T, c diagCase) diagRun {
	t.Helper()
	spec, s := diagSelect(t, c.Selection)
	family, err := strconv.ParseUint(c.Family, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	in := diagInput(t, spec, uint16(family), payload)
	res, reason, err := EvaluateDiagnostics(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	if c.Status == "invalid" {
		if reason != "invalid" {
			t.Fatalf("%s: reason %q, page invalid", c.Name, reason)
		}
	} else if reason != "" {
		t.Fatalf("%s: evaluation refused: %q", c.Name, reason)
	}
	return diagRun{spec, s, in, res}
}

func diagApplicable(res Result) map[string]roweval.Evaluation {
	out := make(map[string]roweval.Evaluation)
	for id, ev := range res.Rows {
		if ev.Native != nil {
			out[id] = ev
		}
	}
	return out
}

func diagExpectations(t *testing.T, c diagCase) map[string]diagExpect {
	t.Helper()
	want := make(map[string]diagExpect)
	for id, e := range c.Rows {
		want[id] = e
	}
	if c.Faults == nil {
		return want
	}
	set := map[int]bool{}
	for _, n := range c.Faults.Set {
		set[n] = true
	}
	named := map[int]bool{}
	for _, n := range c.Faults.Named {
		named[n] = true
	}
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for bit := 0; bit < 64; bit++ {
		index := c.Faults.Block*64 + bit
		value := payload[bit/8] >> uint(bit%8) & 1
		if set[index] != (value == 1) {
			t.Fatalf("%s: vector payload and set list disagree at bit %d", c.Name, index)
		}
		e := diagExpect{Value: strconv.Itoa(int(value)), Raw: strconv.Itoa(int(value)), Disposition: "raw", Grade: "Unknown"}
		if named[index] {
			e.Disposition, e.Grade = "accepted", "Hypothesis"
		}
		want[fmt.Sprintf("%scurrent_fault_%d", rowPrefix, index)] = e
	}
	return want
}

func diagRat(t *testing.T, text string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("expectation %q is not a number", text)
	}
	return r
}

func TestDiagnosticRowsMatchThePageVectors(t *testing.T) {
	for _, c := range diagVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			run := diagEvaluate(t, c)
			if run.res.Status != c.Status {
				t.Errorf("status %q, page %q", run.res.Status, c.Status)
			}
			got := diagApplicable(run.res)
			want := diagExpectations(t, c)
			for id := range got {
				if _, ok := want[id]; !ok {
					t.Errorf("row %s applies but the page vector does not name it", id)
				}
			}
			for id, e := range want {
				ev, ok := got[id]
				if !ok {
					t.Errorf("row %s does not apply", id)
					continue
				}
				n := ev.Native
				if n.Unit != e.Unit || n.Disposition != e.Disposition || strconv.FormatUint(n.Raw, 10) != e.Raw || run.res.Grades[id] != e.Grade {
					t.Errorf("row %s: unit %q disposition %q raw %d grade %q, page %q %q %s %q", id, n.Unit, n.Disposition, n.Raw, run.res.Grades[id], e.Unit, e.Disposition, e.Raw, e.Grade)
				}
				if e.Value == "" {
					if n.Value != nil {
						t.Errorf("row %s: a withheld word keeps no value, got %+v", id, n.Value)
					}
					continue
				}
				if n.Value == nil || n.Value.Quantity == nil || ratOf(t, n.Value.Quantity.Number).Cmp(diagRat(t, e.Value)) != 0 {
					t.Errorf("row %s: value %+v, page %s", id, n.Value, e.Value)
				}
			}
			for _, id := range c.Absent {
				if _, ok := got[id]; ok {
					t.Errorf("row %s applies under a selection the page excludes it from", id)
				}
			}
		})
	}
}

func diagDecoded(run diagRun) (canbusreg.NativeResult, map[string]canbusreg.NativeField) {
	decoded := canbusreg.DecodeLINKDiagnostics(run.sel, run.in)
	fields := make(map[string]canbusreg.NativeField)
	for name, f := range decoded.Fields {
		fields[name] = f
	}
	if decoded.Status != "accepted" || run.in.Evidence.Frame.ID().Value()>>16 != frameSelected {
		return decoded, fields
	}
	for j, name := range []string{"selected_a_index", "selected_b_index"} {
		dtc := canbusreg.DecodeDTC(run.sel, run.in, j*2)
		if f, ok := dtc.Fields["numeric_index"]; ok {
			fields[name] = f
		}
	}
	return decoded, fields
}

func diagFieldOf(id string) string {
	return id[strings.LastIndex(id, ".")+1:]
}

func TestDiagnosticRowsEqualTheFrozenDecoder(t *testing.T) {
	for _, c := range diagVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			run := diagEvaluate(t, c)
			decoded, fields := diagDecoded(run)
			if decoded.Status != run.res.Status {
				t.Errorf("decoder status %q, evaluator %q", decoded.Status, run.res.Status)
			}
			got := diagApplicable(run.res)
			seen := make(map[string]bool)
			for id, ev := range got {
				name := diagFieldOf(id)
				if strings.HasSuffix(id, "_index") {
					name = id[strings.LastIndex(id, ".")+1:]
				}
				field, ok := fields[name]
				n := ev.Native
				if !ok {
					if n.Disposition != dispositionEmpty && n.Disposition != dispositionUnknown {
						t.Errorf("row %s has no decoder field", id)
					}
					continue
				}
				seen[name] = true
				if field.Status != n.Disposition {
					t.Errorf("%s: decoder status %q, evaluator %q", id, field.Status, n.Disposition)
				}
				if field.Unit != n.Unit || field.Grade != run.res.Grades[id] {
					t.Errorf("%s: decoder unit %q grade %q, evaluator %q %q", id, field.Unit, field.Grade, n.Unit, run.res.Grades[id])
				}
				if n.Value != nil && n.Value.Quantity != nil {
					want := big.NewRat(field.Numerator, field.Denominator)
					if ratOf(t, n.Value.Quantity.Number).Cmp(want) != 0 {
						t.Errorf("%s: decoder value %v, evaluator %+v", id, want, n.Value.Quantity.Number)
					}
				}
				if strings.HasSuffix(name, "_index") {
					continue
				}
				if field.Evidence != run.res.Evidence || field.Raw != run.in.Evidence.Frame.Bytes() || field.Group != run.res.Attribution.Group || field.Slot != run.res.Attribution.Slot || field.Bank != run.res.Attribution.Bank {
					t.Errorf("%s: decoder evidence or attribution differs from the evaluator", id)
				}
				if strings.HasPrefix(name, "current_fault_") && (field.Meaning != "") != (n.Disposition == dispositionAccepted) {
					t.Errorf("%s: decoder meaning %q, evaluator %q", id, field.Meaning, n.Disposition)
				}
			}
			for name := range fields {
				if !seen[name] {
					t.Errorf("decoder field %s has no applicable row", name)
				}
			}
		})
	}
}

func TestEveryDiagnosticRowAndVariantHasAVector(t *testing.T) {
	type variant struct{ id, disposition string }
	covered := make(map[variant]bool)
	for _, c := range diagVectors(t) {
		run := diagEvaluate(t, c)
		for id, ev := range diagApplicable(run.res) {
			covered[variant{id, ev.Native.Disposition}] = true
		}
	}
	specs := diagnosticSpecs()
	for _, s := range specs {
		d := s.disposition
		var want []string
		switch {
		case s.index:
			want = []string{dispositionAccepted, dispositionUnknown, dispositionEmpty}
		case strings.Contains(s.id, ".reserved_") || strings.HasSuffix(s.id, "selected_a") || strings.HasSuffix(s.id, "selected_b"):
			want = []string{d(0), d(1)}
		default:
			want = []string{d(0)}
		}
		for _, w := range want {
			if !covered[variant{s.id, w}] {
				t.Errorf("row %s with disposition %s has no vector", s.id, w)
			}
		}
	}
}

func TestDiagnosticTableIsValidAndIdsAreUnique(t *testing.T) {
	if err := roweval.ValidateTable(DiagnosticsTable(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
	telemetry := make(map[string]bool)
	for _, row := range Table().Native {
		telemetry[row.ID] = true
	}
	for _, row := range DiagnosticsTable().Native {
		if telemetry[row.ID] {
			t.Errorf("row %s is also a telemetry row", row.ID)
		}
	}
}

func TestSharedDiagnosticNamesTakeTheFrameQualifier(t *testing.T) {
	emitted := map[string]map[string]bool{}
	for _, s := range diagnosticSpecs() {
		name := strings.TrimPrefix(strings.TrimPrefix(s.row.field, "coordinator_"), "")
		if emitted[name] == nil {
			emitted[name] = map[string]bool{}
		}
		emitted[name][fmt.Sprintf("%04X", s.row.frame)] = true
	}
	for _, s := range diagnosticSpecs() {
		qualified := strings.Count(strings.TrimPrefix(s.id, rowPrefix), ".") == 1
		name := strings.TrimPrefix(s.row.field, "coordinator_")
		families := len(emitted[name])
		if strings.HasPrefix(s.row.field, "coordinator_") {
			if qualified {
				t.Errorf("row %s: the coordinator prefix already separates it", s.id)
			}
			continue
		}
		shared := families > 1 || sharedNames[s.row.field] || name == "additional_state" || strings.HasPrefix(name, "state_") || strings.HasPrefix(name, "reserved_")
		if qualified != shared {
			t.Errorf("row %s: qualified %v, shared %v", s.id, qualified, shared)
		}
		if qualified && !strings.HasPrefix(s.id, fmt.Sprintf("%s%04X.", rowPrefix, s.row.frame)) {
			t.Errorf("row %s does not carry its own frame", s.id)
		}
	}
}

func diagSpecFor(t *testing.T, project, release, choice string) (canbusreg.SelectionSpec, canbusreg.Selection) {
	t.Helper()
	return diagSelect(t, diagSelection{Project: project, Release: release, Choice: choice, Group: "0", Slot: "1", Bank: "ordinary", Source: "02", Destination: "F4", Direction: "module-to-coordinator", Scope: "local"})
}

func TestFaultNamesMatchTheDecoderForEveryBitAndDictionary(t *testing.T) {
	for _, d := range []struct{ project, release, choice string }{{"QAAA", "0011", ""}, {"QAAa", "9011", "release11"}, {"QAAA", "0007", ""}, {"QAAb", "0011", ""}} {
		spec, s := diagSpecFor(t, d.project, d.release, d.choice)
		if d.project == "QAAb" {
			continue
		}
		for block := 0; block < 3; block++ {
			payload := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
			in := diagInput(t, spec, uint16(0x1850+block), payload)
			decoded := canbusreg.DecodeLINKDiagnostics(s, in)
			res, _, err := EvaluateDiagnostics(s, in)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Status != res.Status {
				t.Fatalf("%v block %d: decoder %q evaluator %q", d, block, decoded.Status, res.Status)
			}
			for bit := 0; bit < 64 && decoded.Status == "accepted"; bit++ {
				index := block*64 + bit
				id := fmt.Sprintf("%scurrent_fault_%d", rowPrefix, index)
				field := decoded.Fields[fmt.Sprintf("current_fault_%d", index)]
				ev := res.Rows[id]
				if ev.Native == nil || ev.Native.Disposition != field.Status || res.Grades[id] != field.Grade || (field.Meaning != "") != conditionNamed(index, dictionaryOf(s)) {
					t.Fatalf("%v bit %d: decoder %+v evaluator %+v", d, index, field, ev.Native)
				}
			}
		}
	}
}

func dictionaryOf(s canbusreg.Selection) string {
	q := DiagnosticQualifiers(s, canbusreg.LINKInput{})
	return q.Values[qDictionary]
}

func TestDiagnosticIndexMatchesTheDecoderForEveryWord(t *testing.T) {
	for _, d := range []struct{ project, release, choice, dictionary string }{{"QAAA", "0011", "", dictionaryLegacy}, {"QAAa", "9011", "release11", dictionaryModern}} {
		spec, s := diagSpecFor(t, d.project, d.release, d.choice)
		if dictionaryOf(s) != d.dictionary {
			t.Fatalf("dictionary %q, want %q", dictionaryOf(s), d.dictionary)
		}
		for word := 0; word < 65536; word++ {
			payload := []byte{byte(word), byte(word >> 8), 0, 0, 0, 0, 0, 0}
			in := diagInput(t, spec, frameSelected, payload)
			field, ok := canbusreg.DecodeDTC(s, in, 0).Fields["numeric_index"]
			got, accepted := diagnosticIndex(uint64(word), d.dictionary)
			if ok != accepted || ok && field.Numerator != int64(got) {
				t.Fatalf("%s word %d: decoder %v %d, row %v %d", d.dictionary, word, ok, field.Numerator, accepted, got)
			}
		}
		count := legacyCount
		if d.dictionary == dictionaryModern {
			count = modernCount
		}
		for index := 0; index < count; index++ {
			word, _ := canbusreg.EncodeDTC(s, index)
			if got, ok := diagnosticIndex(uint64(word), d.dictionary); !ok || got != uint64(index) {
				t.Errorf("%s index %d: word %d resolves to %d %v", d.dictionary, index, word, got, ok)
			}
		}
	}
}

func TestDiagnosticEvaluationRefusesWhatTheDecoderRefuses(t *testing.T) {
	spec, s := diagSpecFor(t, "QAAA", "0011", "")
	payload := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	base := diagInput(t, spec, 0x1850, payload)
	for name, mutate := range map[string]func(*canbusreg.LINKInput){
		"asserted bank":      func(in *canbusreg.LINKInput) { in.Bank = "parallel" },
		"asserted group":     func(in *canbusreg.LINKInput) { in.Group = "1" },
		"asserted slot":      func(in *canbusreg.LINKInput) { in.Slot = "9" },
		"asserted endpoint":  func(in *canbusreg.LINKInput) { in.Endpoint = "03" },
		"asserted direction": func(in *canbusreg.LINKInput) { in.Direction = "coordinator-to-module" },
		"asserted scope":     func(in *canbusreg.LINKInput) { in.Scope = "aggregate" },
		"epoch":              func(in *canbusreg.LINKInput) { in.Epoch = 2 },
		"negative field age": func(in *canbusreg.LINKInput) { in.FieldAge = -time.Second },
		"zero sequence":      func(in *canbusreg.LINKInput) { in.Evidence.Sequence = 0 },
		"negative monotonic": func(in *canbusreg.LINKInput) { in.Evidence.Monotonic = -1 },
		"wrong data length": func(in *canbusreg.LINKInput) {
			in.Evidence.Frame = diagInput(t, spec, 0x1850, payload[:7]).Evidence.Frame
		},
		"standard identifier": func(in *canbusreg.LINKInput) {
			id, _ := canbus.NewStandardID(0x185)
			in.Evidence.Frame, _ = canbus.NewFrame(id, payload)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			res, reason, err := EvaluateDiagnostics(s, in)
			if err != nil || reason != "invalid" || len(res.Rows) != 0 || canbusreg.DecodeLINKDiagnostics(s, in).Status != "invalid" {
				t.Errorf("%q %v %d", reason, err, len(res.Rows))
			}
		})
	}
	t.Run("source address FF", func(t *testing.T) {
		c := spec
		c.Source, c.Endpoint = 0xff, "FF"
		if _, err := canbusreg.NewSelection(c); err == nil {
			t.Error("a LINK selection with endpoint FF was accepted")
		}
	})
	t.Run("source address of the coordinator on the module route", func(t *testing.T) {
		c := spec
		c.Source, c.Endpoint = 0xf4, "F4"
		other, err := canbusreg.NewSelection(c)
		if err != nil {
			t.Skip("selection refused before evaluation")
		}
		in := diagInput(t, c, 0x1850, payload)
		_, reason, _ := EvaluateDiagnostics(other, in)
		if reason != "invalid" || canbusreg.DecodeLINKDiagnostics(other, in).Status != "invalid" {
			t.Errorf("%q", reason)
		}
	})
}

func TestDiagnosticAttributionKeepsAbsentGroupAndSlotAndEachBank(t *testing.T) {
	for _, bank := range []string{"ordinary", "parallel"} {
		for _, ctx := range []struct{ group, slot string }{{"", ""}, {"3", "5"}} {
			c := diagCase{Name: bank, Family: "1850", Payload: "0100000000000000", Status: "accepted",
				Selection: diagSelection{Project: "QAAA", Release: "0011", Group: ctx.group, Slot: ctx.slot, Bank: bank, Source: "0A", Destination: "F4", Direction: "module-to-coordinator", Scope: "local"}}
			run := diagEvaluate(t, c)
			a := run.res.Attribution
			if a.Bank != bank || a.Group != ctx.group || a.Slot != ctx.slot || a.HasGroup != (ctx.group != "") || a.HasSlot != (ctx.slot != "") {
				t.Errorf("%s %+v: attribution %+v", bank, ctx, a)
			}
			if run.res.Status != "accepted" || len(diagApplicable(run.res)) != 64 {
				t.Errorf("%s %+v: status %q rows %d", bank, ctx, run.res.Status, len(diagApplicable(run.res)))
			}
		}
	}
}
