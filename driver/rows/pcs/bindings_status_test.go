package pcs

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type statusFile struct {
	ClassWords  []string     `json:"class_words"`
	StatusFacts []string     `json:"status_facts"`
	BoundFacts  []string     `json:"bound_facts"`
	Cases       []statusCase `json:"cases"`
}

type statusCase struct {
	diagVectorCase
	Classified []string `json:"classified"`
}

func loadStatusFile(t *testing.T) statusFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/bindings_status.json")
	if err != nil {
		t.Fatal(err)
	}
	var f statusFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 || len(f.ClassWords) == 0 || len(f.StatusFacts) == 0 || len(f.BoundFacts) == 0 {
		t.Fatal("incomplete status vectors")
	}
	return f
}

func statesClass(words []string, meaning string) bool {
	lower := strings.ToLower(meaning)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// decoderMeanings returns, by row identifier, the meaning the merged decoder
// states for each key of the case, and how many keys it decoded.
func decoderMeanings(t *testing.T, c statusCase) map[string]string {
	t.Helper()
	s := diagSelection(t, c.Selection)
	f := diagFrame(t, c.Identifier, c.Extended, c.Payload)
	out := map[string]string{}
	switch c.Kind {
	case "summary":
		dec := canbusreg.DecodePCSSummaries(s, diagSummaryInput(s, f, c.Selection.LocalSummaryQualified))
		for key, b := range dec.Bits {
			out[fmt.Sprintf("growatt.bms.pcs.%s.%X.%s", diagToken(c.Selection.Flavor), f.ID().Value(), key)] = b.Field.Meaning
		}
	case "dtc":
		dec := canbusreg.DecodeDTC(s, diagLinkInput(s, f), c.Offset)
		fam, ok := diagDTCFamilyOf(f)
		if !ok {
			t.Fatalf("%s: family", c.Name)
		}
		for key, field := range dec.Fields {
			id := fmt.Sprintf("growatt.bms.%s.%s.offset%d.%s", fam.path, fam.token, c.Offset, key)
			out[id] = field.Meaning
			if key == dtcNumericIndex {
				out[id] += " | " + canbusreg.LabelCondition(s, field).Meaning
			}
		}
	default:
		t.Fatalf("%s: kind %q", c.Name, c.Kind)
	}
	if len(out) == 0 {
		t.Fatalf("%s: the decoder states nothing", c.Name)
	}
	return out
}

func statusBindingIDs(facts []string) []string {
	var ids []string
	for _, b := range Bindings() {
		for _, f := range facts {
			if b.Read != nil && string(b.Read.Fact.ID) == f {
				ids = append(ids, b.NativeID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// TestStatusBindingsFollowDecoderConditionClass checks that the set of status
// bindings is exactly the set of keys whose decoder meaning states a class.
func TestStatusBindingsFollowDecoderConditionClass(t *testing.T) {
	f := loadStatusFile(t)
	var stated []string
	for _, c := range f.Cases {
		var got []string
		for id, meaning := range decoderMeanings(t, c) {
			if statesClass(f.ClassWords, meaning) {
				got = append(got, id)
				stated = append(stated, id)
			}
		}
		if len(got) != len(c.Classified) {
			t.Errorf("%s: decoder states a class for %v, vector expects %v", c.Name, got, c.Classified)
		}
	}
	sort.Strings(stated)
	bound := statusBindingIDs(f.StatusFacts)
	if strings.Join(stated, ",") != strings.Join(bound, ",") {
		t.Errorf("status bindings %v, keys with a stated class %v", bound, stated)
	}
	for _, b := range statusBindings() {
		if b.Read == nil || b.Write != nil {
			t.Errorf("%s is not a read-only binding", b.NativeID)
		}
	}
}

// TestUnclassifiedBitsStayNativeAndUnbound checks that a summary bit or index
// with no stated class keeps its native value and gives no candidate or binding.
func TestUnclassifiedBitsStayNativeAndUnbound(t *testing.T) {
	f := loadStatusFile(t)
	bound := map[string]bool{}
	for _, b := range Bindings() {
		bound[b.NativeID] = true
	}
	native := map[string]bool{}
	for _, r := range CombinedTable().Native {
		native[r.ID] = true
	}
	checked := 0
	for _, c := range f.Cases {
		for id, meaning := range decoderMeanings(t, c) {
			if statesClass(f.ClassWords, meaning) {
				continue
			}
			checked++
			if bound[id] {
				t.Errorf("%s: %s has no stated class and is bound", c.Name, id)
			}
			if !native[id] {
				t.Errorf("%s: %s is not a native row", c.Name, id)
			}
		}
		if c.Kind != "summary" {
			continue
		}
		s := diagSelection(t, c.Selection)
		in := diagSummaryInput(s, diagFrame(t, c.Identifier, c.Extended, c.Payload), c.Selection.LocalSummaryQualified)
		res, _, err := EvaluateSummaryBits(s, in)
		if err != nil {
			t.Fatal(err)
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
			t.Errorf("%s: no native summary value", c.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no unclassified key checked")
	}
}

// TestFullTableValidates checks that the combined table passes roweval
// validation. The gauge, identity and diagnostic rows read one payload slice as
// several named fields and are evaluated one row at a time, so validating them
// together is an ambiguous match by design: each row is validated alone with
// its hook and bindings, and each table whose rows are evaluated together is
// validated whole with its bindings.
func TestFullTableValidates(t *testing.T) {
	table := CombinedTable()
	d := Descriptors()
	for name, order := range identityDescriptors().ByteOrders {
		if _, ok := d.ByteOrders[name]; !ok {
			d.ByteOrders[name] = order
		}
	}
	roles := packRoles(t, "pack.ordinary")
	ids := map[string]bool{}
	bindings := map[string][]roweval.BindingRow{}
	for _, b := range table.Bindings {
		bindings[b.NativeID] = append(bindings[b.NativeID], b)
	}
	for _, r := range table.Native {
		if ids[r.ID] {
			t.Errorf("duplicate native row %s", r.ID)
		}
		ids[r.ID] = true
		single := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{r}, Hooks: table.Hooks, Bindings: bindings[r.ID]}
		if err := roweval.ValidateTable(single, d, roles); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
	}
	for _, b := range table.Bindings {
		if !ids[b.NativeID] {
			t.Errorf("binding of %s names no native row", b.NativeID)
		}
	}
	for name, p := range map[string]roweval.Table[Selector]{"standard": FullTable(), "slot": SlotTable(), "extended": ExtendedTable(), "extended faults": ExtendedFaultTable(), "parallel identity": ParallelIdentityTable(), "parallel release": ParallelReleaseTable()} {
		own := map[string]bool{}
		for _, r := range p.Native {
			own[r.ID] = true
		}
		p.Bindings = nil
		for _, b := range table.Bindings {
			if own[b.NativeID] {
				p.Bindings = append(p.Bindings, b)
			}
		}
		if err := roweval.ValidateTable(p, d, roles); err != nil {
			t.Errorf("%s table: %v", name, err)
		}
	}
	parts := 0
	identity, identityHooks, _ := identityRows()
	for _, p := range []roweval.Table[Selector]{FullTable(), gaugeTable(), SlotTable(), {Native: identity, Hooks: identityHooks}, diagnosticTable(), ExtendedTable(), ExtendedFaultTable(), ParallelIdentityTable(), ParallelReleaseTable()} {
		parts += len(p.Native)
		for name := range p.Hooks {
			if _, ok := table.Hooks[name]; !ok {
				t.Errorf("hook %s is missing", name)
			}
		}
	}
	if parts != len(table.Native) {
		t.Errorf("table has %d native rows, its parts %d", len(table.Native), parts)
	}
}

// TestEveryBoundElementIsExposedWithoutSwitchOrWithdrawalRule checks that every
// bound element has a read binding on a published native row, and that no row
// or binding carries a withdrawal rule or a storage-graph switch.
func TestEveryBoundElementIsExposedWithoutSwitchOrWithdrawalRule(t *testing.T) {
	f := loadStatusFile(t)
	table := CombinedTable()
	native := map[string]bool{}
	for _, r := range table.Native {
		native[r.ID] = true
		for _, text := range []string{r.ID, r.SpecRef, r.Decode.Hook} {
			assertNoRule(t, r.ID, text)
		}
		for k, v := range r.When.Equal {
			assertNoRule(t, r.ID, k+"="+v)
		}
	}
	facts := map[string]int{}
	for _, b := range table.Bindings {
		if !native[b.NativeID] {
			t.Errorf("%s: bound row is not exposed natively", b.NativeID)
		}
		if b.Read == nil || b.Write != nil || b.Read.Policy != receivePolicy {
			t.Errorf("%s: not a read binding under the receive policy", b.NativeID)
		}
		facts[string(b.Read.Fact.ID)]++
		for k, v := range b.When.Equal {
			assertNoRule(t, b.NativeID, k+"="+v)
		}
		assertNoRule(t, b.NativeID, b.SpecRef)
	}
	for _, fact := range f.BoundFacts {
		if facts[fact] == 0 {
			t.Errorf("bound element %s has no binding", fact)
		}
		delete(facts, fact)
	}
	for fact := range facts {
		t.Errorf("binding of %s is not an expected element", fact)
	}
}

func assertNoRule(t *testing.T, id, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, word := range []string{"withdraw", "graph", "switch"} {
		if strings.Contains(lower, word) {
			t.Errorf("%s: %q names a %s rule", id, text, word)
		}
	}
}
