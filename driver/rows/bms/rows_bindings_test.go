package bms

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// bindingVector is one synthetic frame of the bindings and its independent
// expectation, written by hand from the page byte layout and the conversions
// of the binding (testdata/binding_vectors.json). Rows name the native row
// without its profile prefix. A gate of "rejected" names a frame the profile's
// frame gate must refuse.
type bindingVector struct {
	Name       string                   `json:"name"`
	Table      string                   `json:"table"`
	Identifier uint32                   `json:"identifier"`
	Extended   bool                     `json:"extended"`
	DataHex    string                   `json:"data_hex"`
	Gate       string                   `json:"gate"`
	Rows       map[string]bindingExpect `json:"rows"`
}

// bindingExpect is the expected native value of a row and its candidate or the
// reason it gives no candidate.
type bindingExpect struct {
	Native    string            `json:"native"`
	Candidate *bindingCandidate `json:"candidate"`
	Reason    string            `json:"reason"`
}

// bindingCandidate is the expected storage fact, unit, SI value, subject role
// and quality of a candidate.
type bindingCandidate struct {
	Fact    string `json:"fact"`
	Unit    string `json:"unit"`
	Value   string `json:"value"`
	Subject string `json:"subject"`
	Quality string `json:"quality"`
}

// packSiteResource is the configured pack resource every vector supplies.
const packSiteResource = "pack.site-a"

// tableSpec names one table the package builds: the kind of the table and, for
// the addressed monitor, the identifier it is built for.
type tableSpec struct {
	kind string
	id   uint32
}

// tableSpecs are the tables whose bindings the independent vectors pin.
var tableSpecs = []tableSpec{
	{"v104", 787},
	{"v108", 787},
	{"monitor", 67174400},
	{"monitor", 67239936},
	{"monitor", 67305472},
	{"common_member", 787},
	{"common_aggregate", 787},
}

func readBindingVectors(t *testing.T) []bindingVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/binding_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []bindingVector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("the binding vectors are empty")
	}
	return out
}

// buildTable is the table of one spec, with its cell bindings.
func buildTable(s tableSpec) roweval.Table[Selector] {
	switch s.kind {
	case "v108":
		return withCellBindings(V108Table())
	case "monitor":
		return withCellBindings(MonitorTable(s.id))
	case "common_member":
		return withCellBindings(CommonTable(ScopeMember))
	case "common_aggregate":
		return withCellBindings(CommonTable(ScopeAggregate))
	}
	return withCellBindings(V104Table())
}

// profileOf is the profile identifier of a table kind.
func profileOf(kind string) string {
	switch kind {
	case "v108":
		return V108Profile
	case "monitor":
		return MonitorProfile
	case "common_member", "common_aggregate":
		return CommonProfile
	}
	return Profile
}

// sameFamily reports whether a vector belongs to a spec: every vector of a
// profile does, and an addressed monitor vector must share the spec's base.
func sameFamily(s tableSpec, id uint32) bool {
	if s.kind != "monitor" {
		return true
	}
	base, ok := monitorBase(id)
	want, _ := monitorBase(s.id)
	return ok && base == want
}

// runKind gates one frame with the frame gate of its table kind and evaluates
// the rows of the given table on it. A common kind sets the scope the selection
// carries, as EvaluateCommonFrame does, so a mutated table is evaluated as given.
func runKind(table roweval.Table[Selector], kind string, frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	switch kind {
	case "common_member", "common_aggregate":
		if reason := CommonFrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
		scope := ScopeMember
		if kind == "common_aggregate" {
			scope = ScopeAggregate
		}
		values := make(map[string]string, len(a.Qualifiers.Values)+1)
		for k, v := range a.Qualifiers.Values {
			values[k] = v
		}
		values[scopeQualifier] = scope
		a.Qualifiers.Values = values
	case "v108":
		if reason := V108FrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	case "monitor":
		if reason := MonitorFrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	default:
		if reason := FrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	}
	return evaluateAdmitted(table, frame, a)
}

// probe runs one vector on any table of the pool and lists its mismatches.
type probe struct {
	name string
	run  func(roweval.Table[Selector]) []string
}

func bindingProbe(t *testing.T, v bindingVector) probe {
	t.Helper()
	raw := vector{Name: v.Name, Identifier: v.Identifier, Extended: v.Extended, DLC: 8, Data: v.DataHex}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, storageRoles(t))
	return probe{name: v.Name, run: func(tb roweval.Table[Selector]) []string {
		evals, reason, err := runKind(tb, v.Table, frame, a)
		if err != nil {
			return []string{fmt.Sprintf("%s: %v", v.Name, err)}
		}
		return bindingMismatches(v, evals, reason, a)
	}}
}

func cellProbe(t *testing.T, v cellVector) probe {
	t.Helper()
	frame, a := cellVectorFrame(t, v)
	return probe{name: v.Name, run: func(tb roweval.Table[Selector]) []string {
		evals, reason, err := runKind(tb, v.Profile, frame, a)
		if err != nil {
			return []string{fmt.Sprintf("%s: %v", v.Name, err)}
		}
		return cellVectorMismatches(v, evals, reason, a)
	}}
}

// pool is every vector that exercises a spec: the binding vectors of its kind
// and the cell vectors of its profile.
func pool(t *testing.T, s tableSpec) []probe {
	var out []probe
	for _, v := range readBindingVectors(t) {
		if v.Table == s.kind && sameFamily(s, v.Identifier) {
			out = append(out, bindingProbe(t, v))
		}
	}
	for _, v := range readCellVectors(t) {
		if v.Profile == s.kind && sameFamily(s, v.Identifier) {
			out = append(out, cellProbe(t, v))
		}
	}
	return out
}

// anyFails reports whether some probe of the pool fails on the table.
func anyFails(ps []probe, tb roweval.Table[Selector]) bool {
	for _, p := range ps {
		if len(p.run(tb)) > 0 {
			return true
		}
	}
	return false
}

// bindingMismatches lists every way the evaluations differ from a binding
// vector's expectation: the native value, the gate, the candidate's fact, unit,
// SI value, quality, pack dimension and subject, or the reason when no candidate
// is expected.
func bindingMismatches(v bindingVector, evals map[string]roweval.Evaluation, reason roweval.Reason, a roweval.Acquired[Selector]) []string {
	if v.Gate == "rejected" {
		if reason == "" || len(evals) > 0 {
			return []string{v.Name + ": frame admitted, want rejected"}
		}
		return nil
	}
	if reason != "" {
		return []string{fmt.Sprintf("%s: frame gate %q", v.Name, reason)}
	}
	var out []string
	for row, want := range v.Rows {
		id := profileOf(v.Table) + "." + row
		ev, ok := evals[id]
		if !ok {
			out = append(out, fmt.Sprintf("%s: row %s not evaluated", v.Name, id))
			continue
		}
		if want.Native != "" && (ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, want.Native)) {
			out = append(out, fmt.Sprintf("%s: %s native %+v, want %s", v.Name, id, ev.Native, want.Native))
		}
		if want.Candidate == nil {
			if ev.Candidate != nil || string(ev.Reason) != want.Reason {
				out = append(out, fmt.Sprintf("%s: %s candidate %+v reason %q, want none and %q", v.Name, id, ev.Candidate, ev.Reason, want.Reason))
			}
			continue
		}
		c := ev.Candidate
		if c == nil || ev.Reason != "" {
			out = append(out, fmt.Sprintf("%s: %s has no candidate (reason %q)", v.Name, id, ev.Reason))
			continue
		}
		w := want.Candidate
		if string(c.Key.FactID) != w.Fact {
			out = append(out, fmt.Sprintf("%s: %s fact %s, want %s", v.Name, id, c.Key.FactID, w.Fact))
		}
		if c.Value == nil || c.Value.Quantity == nil || string(c.Value.Quantity.Unit) != w.Unit || !decimalEquals(c.Value.Quantity.Number, w.Value) {
			out = append(out, fmt.Sprintf("%s: %s value %+v, want %s %s", v.Name, id, c.Value, w.Value, w.Unit))
		}
		if string(c.Quality.Qualification) != w.Quality {
			out = append(out, fmt.Sprintf("%s: %s quality %s, want %s", v.Name, id, c.Quality.Qualification, w.Quality))
		}
		if len(c.Key.Dimensions) != 1 || string(c.Key.Dimensions[0].ID) != packDimension || c.Key.Dimensions[0].Value.Text == nil || *c.Key.Dimensions[0].Value.Text != packSiteResource {
			out = append(out, fmt.Sprintf("%s: %s key %+v, want the pack %s", v.Name, id, c.Key.Dimensions, packSiteResource))
		}
		if ev.Subject == nil || string(ev.Subject.Role) != w.Subject {
			out = append(out, fmt.Sprintf("%s: %s subject %+v, want role %s", v.Name, id, ev.Subject, w.Subject))
		}
	}
	return out
}

// checkVectorsWithPrefix runs every binding vector whose name has one of the
// prefixes on the table of its own kind and fails on any mismatch.
func checkVectorsWithPrefix(t *testing.T, prefixes ...string) {
	t.Helper()
	n := 0
	for _, v := range readBindingVectors(t) {
		matched := false
		for _, p := range prefixes {
			matched = matched || strings.HasPrefix(v.Name, p)
		}
		if !matched {
			continue
		}
		n++
		for _, m := range bindingProbe(t, v).run(buildTable(tableSpec{v.Table, v.Identifier})) {
			t.Error(m)
		}
	}
	if n == 0 {
		t.Fatalf("no vector has the prefixes %v", prefixes)
	}
}

// TestCommonBindingsGiveExactSIValuesForNonzeroInputsInBothScopes pins the
// common projection's voltage, current, SOC and SOH under member and aggregate.
func TestCommonBindingsGiveExactSIValuesForNonzeroInputsInBothScopes(t *testing.T) {
	checkVectorsWithPrefix(t, "common-")
}

// TestV108BindingsGiveExactSIValuesForNonzeroInputs pins the V1.08 voltage,
// current, SOC, SOH and average temperature in kelvin, negative case included.
func TestV108BindingsGiveExactSIValuesForNonzeroInputs(t *testing.T) {
	checkVectorsWithPrefix(t, "v108-")
}

// TestV104AndMonitorBindingsGiveExactSIValuesForNonzeroInputs pins the V1.04
// and addressed monitor voltage and current.
func TestV104AndMonitorBindingsGiveExactSIValuesForNonzeroInputs(t *testing.T) {
	checkVectorsWithPrefix(t, "v104-", "monitor-")
}

// TestSOCOneHundredAndOneKeepsMeasurementBytesAndAdmitsOnlyOneHundred pins that
// SOC 101 changes only byte 6 of the 0x313 frame, that the V1.04 frame gate
// refuses it, and that the common projection keeps its native SOC of 101 with no
// candidate.
func TestSOCOneHundredAndOneKeepsMeasurementBytesAndAdmitsOnlyOneHundred(t *testing.T) {
	byName := map[string]bindingVector{}
	for _, v := range readBindingVectors(t) {
		if strings.HasPrefix(v.Name, "soc-") {
			byName[v.Name] = v
		}
	}
	pairs := 0
	for name, v := range byName {
		if !strings.HasSuffix(name, "-101") {
			continue
		}
		twin, ok := byName[strings.TrimSuffix(name, "-101")+"-100"]
		if !ok {
			t.Errorf("%s has no SOC 100 twin", name)
			continue
		}
		pairs++
		if v.DataHex[:12] != twin.DataHex[:12] || v.DataHex[14:] != twin.DataHex[14:] || v.DataHex[12:14] != "65" || twin.DataHex[12:14] != "64" {
			t.Errorf("%s does not change only byte 6 from 0x64 to 0x65", name)
		}
	}
	if pairs == 0 {
		t.Fatal("no SOC 100 and 101 pair")
	}
	checkVectorsWithPrefix(t, "soc-")
}

// TestV104SOHCandidateNeedsTheValidityFlag pins that V1.04 SOH 80 in byte 7 with
// the validity flag false keeps the native value and gives no candidate, and the
// same value with the flag set gives the candidate.
func TestV104SOHCandidateNeedsTheValidityFlag(t *testing.T) {
	checkVectorsWithPrefix(t, "v104-soh-")
}

// TestEveryBindingIsNeededByAnIndependentVector removes each binding in turn
// and requires a vector of its table to fail without it.
func TestEveryBindingIsNeededByAnIndependentVector(t *testing.T) {
	for _, s := range tableSpecs {
		base := buildTable(s)
		ps := pool(t, s)
		for i, b := range base.Bindings {
			if !anyFails(ps, withoutBinding(base, i)) {
				t.Errorf("%s (%s) on %s: no independent vector needs this binding", b.NativeID, b.Read.Fact.ID, s.kind)
			}
		}
	}
}

// TestEveryBindingConversionIsPinnedByIndependentInput doubles the divisor of
// each binding in turn, requiring a vector to fail, and requires each binding to
// have an independent expectation of its fact with a nonzero candidate value.
func TestEveryBindingConversionIsPinnedByIndependentInput(t *testing.T) {
	for _, s := range tableSpecs {
		base := buildTable(s)
		ps := pool(t, s)
		exp := expectationsFor(t, s)
		for i, b := range base.Bindings {
			if !anyFails(ps, withDivisorDoubled(base, i)) {
				t.Errorf("%s (%s) on %s: doubling the divisor fails no vector", b.NativeID, b.Read.Fact.ID, s.kind)
			}
			row := strings.TrimPrefix(b.NativeID, profileOf(s.kind)+".")
			if !exp.pinned(row, string(b.Read.Fact.ID)) {
				t.Errorf("%s (%s) on %s: no nonzero independent expectation", b.NativeID, b.Read.Fact.ID, s.kind)
			}
		}
	}
}

// withoutBinding is the table with binding i removed.
func withoutBinding(t roweval.Table[Selector], i int) roweval.Table[Selector] {
	out := append([]roweval.BindingRow(nil), t.Bindings[:i]...)
	t.Bindings = append(out, t.Bindings[i+1:]...)
	return t
}

// withDivisorDoubled is the table with the divisor of binding i doubled.
func withDivisorDoubled(t roweval.Table[Selector], i int) roweval.Table[Selector] {
	bindings := append([]roweval.BindingRow(nil), t.Bindings...)
	b := bindings[i]
	r := *b.Read
	n := *r.ToRegistry.Numeric
	n.Den *= 2
	r.ToRegistry.Numeric = &n
	b.Read = &r
	bindings[i] = b
	t.Bindings = bindings
	return t
}

// expectation is one independent expected candidate of a spec's rows.
type expectation struct {
	row, fact, value string
}

// expectationSet is the independent expectations of one spec.
type expectationSet []expectation

// pinned reports whether some expectation names the row and fact with a
// candidate value that is nonzero: a zero input cannot pin a divisor. The value
// is nonzero when a digit other than zero remains after the sign and point.
func (e expectationSet) pinned(row, fact string) bool {
	for _, x := range e {
		if x.row == row && x.fact == fact && strings.Trim(x.value, "-0.") != "" {
			return true
		}
	}
	return false
}

// expectationsFor collects the expected candidates of the binding vectors and
// the cell vectors of a spec.
func expectationsFor(t *testing.T, s tableSpec) expectationSet {
	var out expectationSet
	for _, v := range readBindingVectors(t) {
		if v.Table != s.kind || !sameFamily(s, v.Identifier) {
			continue
		}
		for row, want := range v.Rows {
			if want.Candidate != nil {
				out = append(out, expectation{row, want.Candidate.Fact, want.Candidate.Value})
			}
		}
	}
	for _, v := range readCellVectors(t) {
		if v.Profile != s.kind || !sameFamily(s, v.Identifier) {
			continue
		}
		for row, want := range v.Rows {
			if want.Candidate != nil {
				out = append(out, expectation{row, want.Candidate.Fact, want.Candidate.Value})
			}
		}
	}
	return out
}
