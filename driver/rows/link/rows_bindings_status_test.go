package link

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type statusVectorFile struct {
	ClassWords    []string       `json:"class_words"`
	UnboundFacts  []string       `json:"unbound_facts"`
	BoundFacts    []string       `json:"bound_facts"`
	StatusCases   []statusVector `json:"status_cases"`
	ExtremumCases []statusVector `json:"extremum_cases"`
	AggregateCase []statusVector `json:"aggregate_cases"`
}

type statusVector struct {
	Name      string            `json:"name"`
	Family    string            `json:"family"`
	Payload   string            `json:"payload"`
	Selection diagSelection     `json:"selection"`
	Rows      map[string]string `json:"rows"`
}

func loadStatusVectors(t *testing.T) statusVectorFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/bindings_status.json")
	if err != nil {
		t.Fatal(err)
	}
	var f statusVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.StatusCases) == 0 || len(f.ExtremumCases) == 0 || len(f.AggregateCase) == 0 || len(f.ClassWords) == 0 {
		t.Fatal("incomplete status vectors")
	}
	return f
}

func (v statusVector) input(t *testing.T) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	spec, s := diagSelect(t, v.Selection)
	family, err := strconv.ParseUint(v.Family, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(v.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return s, diagInput(t, spec, uint16(family), payload)
}

func statesAClass(words []string, meaning string) bool {
	lower := strings.ToLower(meaning)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func boundFactCounts() map[string]int {
	counts := map[string]int{}
	for _, b := range AllTable().Bindings {
		if b.Read != nil {
			counts[string(b.Read.Fact.ID)]++
		}
	}
	return counts
}

func TestNoStatusInterlockExtremumOrDischargeFactIsBoundBecauseTheEvidenceStatesNoClassOrCell(t *testing.T) {
	f := loadStatusVectors(t)
	counts := boundFactCounts()
	for _, fact := range f.UnboundFacts {
		if counts[fact] != 0 {
			t.Errorf("%s has %d bindings", fact, counts[fact])
		}
	}
	if len(statusBindings()) != 0 {
		t.Errorf("status bindings: %d", len(statusBindings()))
	}
	for _, c := range f.StatusCases {
		s, in := c.input(t)
		dec := canbusreg.DecodeLINKDiagnostics(s, in)
		if len(dec.Fields) == 0 {
			t.Errorf("%s: the decoder states nothing", c.Name)
		}
		for key, field := range dec.Fields {
			if statesAClass(f.ClassWords, field.Meaning) {
				t.Errorf("%s: %s states a class: %q", c.Name, key, field.Meaning)
			}
			if strings.Contains(key, "index") && statesAClass(f.ClassWords, canbusreg.LabelCondition(s, field).Meaning) {
				t.Errorf("%s: %s label states a class", c.Name, key)
			}
		}
		res, reason, err := EvaluateDiagnostics(s, in)
		if err != nil || reason != "" {
			t.Fatalf("%s: %q %v", c.Name, reason, err)
		}
		natives := 0
		for id, ev := range res.Rows {
			if ev.Candidate != nil || ev.Subject != nil {
				t.Errorf("%s: %s gives a candidate", c.Name, id)
			}
			if ev.Native != nil && ev.Native.Reason == "" {
				natives++
			}
		}
		if natives == 0 {
			t.Errorf("%s: no native value", c.Name)
		}
	}
}

func TestCellExtremaStayNativeWithIndexAndLocationEvidenceOnBothBanks(t *testing.T) {
	f := loadStatusVectors(t)
	for _, c := range f.ExtremumCases {
		t.Run(c.Name, func(t *testing.T) {
			s, in := c.input(t)
			res, reason, err := Evaluate(s, in)
			if err != nil || reason != "" {
				t.Fatalf("%q %v", reason, err)
			}
			if res.Attribution.Bank != c.Selection.Bank || res.Attribution.HasGroup != (c.Selection.Group != "") {
				t.Errorf("attribution %+v", res.Attribution)
			}
			for id, want := range c.Rows {
				ev, ok := res.Rows[id]
				if !ok || ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
					t.Errorf("%s: no native value", id)
					continue
				}
				if ratOf(t, ev.Native.Value.Quantity.Number).Cmp(diagRat(t, want)) != 0 {
					t.Errorf("%s: value %+v, want %s", id, ev.Native.Value.Quantity.Number, want)
				}
				if ev.Candidate != nil || ev.Subject != nil {
					t.Errorf("%s: a native extremum gives a candidate", id)
				}
				if ev.Native.Raw != ratUint(t, want) {
					t.Errorf("%s: raw word %d, want %s", id, ev.Native.Raw, want)
				}
			}
		})
	}
}

func ratUint(t *testing.T, text string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAggregateValuesStayNativeAndNeverReachAMemberPack(t *testing.T) {
	f := loadStatusVectors(t)
	bound := map[string]bool{}
	for _, b := range AllTable().Bindings {
		bound[b.NativeID] = true
	}
	for _, c := range f.AggregateCase {
		t.Run(c.Name, func(t *testing.T) {
			s, in := c.input(t)
			res, reason, err := EvaluateAggregate(s, in)
			if err != nil || reason != "" {
				t.Fatalf("%q %v", reason, err)
			}
			for id, want := range c.Rows {
				if bound[id] {
					t.Errorf("%s is bound", id)
				}
				ev, ok := res.Rows[id]
				if !ok || ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
					t.Errorf("%s: no native value", id)
					continue
				}
				if ratOf(t, ev.Native.Value.Quantity.Number).Cmp(diagRat(t, want)) != 0 || ev.Native.Unit != "" {
					t.Errorf("%s: value %+v unit %q, want %s unitless", id, ev.Native.Value.Quantity.Number, ev.Native.Unit, want)
				}
				if ev.Candidate != nil || ev.Subject != nil {
					t.Errorf("%s: an aggregate value gives a candidate", id)
				}
			}
		})
	}
}

func TestAssembledTableValidatesPartByPartAndCarriesEveryBinding(t *testing.T) {
	table := AllTable()
	roles := packRoles(t, "pack.ordinary")
	bindings := map[string][]roweval.BindingRow{}
	for _, b := range table.Bindings {
		bindings[b.NativeID] = append(bindings[b.NativeID], b)
	}
	native := map[string]bool{}
	partRows := 0
	for _, p := range tableParts() {
		own := map[string]bool{}
		for _, r := range p.Native {
			if native[r.ID] && !own[r.ID] {
				t.Errorf("%s is in two parts", r.ID)
			}
			own[r.ID], native[r.ID] = true, true
		}
		partRows += len(p.Native)
		p.Bindings = nil
		for id := range own {
			p.Bindings = append(p.Bindings, bindings[id]...)
		}
		if err := roweval.ValidateTable(p, Descriptors(), roles); err != nil {
			t.Errorf("part: %v", err)
		}
		for _, r := range p.Native {
			if _, ok := table.Hooks[r.Decode.Hook]; !ok {
				t.Errorf("hook %s is missing", r.Decode.Hook)
			}
		}
	}
	if partRows != len(table.Native) {
		t.Errorf("%d native rows, parts %d", len(table.Native), partRows)
	}
	for _, b := range table.Bindings {
		if !native[b.NativeID] {
			t.Errorf("binding of %s names no native row", b.NativeID)
		}
	}
}

func TestEveryBoundElementIsExposedWithoutSwitchOrWithdrawalRule(t *testing.T) {
	f := loadStatusVectors(t)
	table := AllTable()
	check := func(id, text string) {
		lower := strings.ToLower(text)
		for _, word := range []string{"withdraw", "graph", "switch"} {
			if strings.Contains(lower, word) {
				t.Errorf("%s: %q names a %s rule", id, text, word)
			}
		}
	}
	for _, r := range table.Native {
		check(r.ID, r.ID+" "+r.SpecRef+" "+r.Decode.Hook)
		for k, v := range r.When.Equal {
			check(r.ID, k+"="+v)
		}
	}
	counts := map[string]int{}
	for _, b := range table.Bindings {
		if b.Read == nil || b.Write != nil || b.Read.Policy != receivePolicy {
			t.Errorf("%s: not a read binding under the receive policy", b.NativeID)
		}
		counts[string(b.Read.Fact.ID)]++
		check(b.NativeID, b.SpecRef)
		for k, v := range b.When.Equal {
			check(b.NativeID, k+"="+v)
		}
	}
	for _, fact := range f.BoundFacts {
		if counts[fact] == 0 {
			t.Errorf("bound element %s has no binding", fact)
		}
		delete(counts, fact)
	}
	for fact := range counts {
		t.Errorf("binding of %s is not an expected element", fact)
	}
}

func TestNativeOnlyListingCoversEveryUnboundRowWithItsElement(t *testing.T) {
	bound := map[string]bool{}
	for _, b := range Bindings() {
		bound[b.NativeID] = true
	}
	listed := map[string]NativeOnlyRow{}
	var order []string
	for _, row := range NativeOnly() {
		if _, dup := listed[row.ID]; dup {
			t.Errorf("%s is listed twice", row.ID)
		}
		if bound[row.ID] || row.Reason == "" {
			t.Errorf("%s: bound %v, reason %q", row.ID, bound[row.ID], row.Reason)
		}
		listed[row.ID] = row
		order = append(order, row.ID)
	}
	if !sort.StringsAreSorted(order) {
		t.Error("the listing is not in row id order")
	}
	for _, r := range AllTable().Native {
		if _, ok := listed[r.ID]; !ok && !bound[r.ID] {
			t.Errorf("%s is neither bound nor listed", r.ID)
		}
	}
	want := map[string]string{
		"cell_voltage_1": "storage.cell.voltage", "cell_voltage_16": "storage.cell.voltage", "1806.mean": "storage.cell.voltage_mean",
		"1806.maximum": "storage.cell.voltage_maximum / storage.cell.voltage_minimum", "1806.minimum_index": "storage.cell.voltage_maximum / storage.cell.voltage_minimum",
		"enable_3": "storage.cell.balancing_enabled", "temperature_2": "storage.temperature.sensor", "remaining_capacity": "storage.capacity.remaining",
		"cycles": "storage.cycle.count", "port_voltage": "storage.pack.port_voltage", "project_code": "storage.identity", "serial_byte_1": "storage.identity",
		"current_fault_0": "storage.status.alarm / storage.status.warning / storage.status.protection", "soc_status": "", "limit": "", "180F.current": "",
		"discharge_limit": "value limit of storage.pack.current",
	}
	for short, element := range want {
		row, ok := listed[rowPrefix+short]
		if !ok {
			t.Errorf("%s is not listed", short)
			continue
		}
		if row.Element != element {
			t.Errorf("%s: element %q, want %q", short, row.Element, element)
		}
	}
	for _, short := range []string{"pack_voltage", "1805.current", "1807.b4", "whole_estimate", "capacity", "full_capacity", "1862.mean", "extra_average"} {
		if _, ok := listed[rowPrefix+short]; ok {
			t.Errorf("%s is bound and listed as native only", short)
		}
	}
}
