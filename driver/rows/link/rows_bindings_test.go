package link

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type bindingFile struct {
	Cases []bindingCase `json:"cases"`
}

type bindingCase struct {
	Name       string                   `json:"name"`
	Table      string                   `json:"table"`
	Family     string                   `json:"family"`
	Payload    string                   `json:"payload"`
	Selection  vectorSelection          `json:"selection"`
	Fine       *fineGates               `json:"fine"`
	CountAware bool                     `json:"count_aware"`
	CountValid bool                     `json:"count_valid"`
	Expect     map[string]bindingExpect `json:"expect"`
}

type bindingExpect struct {
	Outcome string `json:"outcome"`
	Fact    string `json:"fact"`
	Unit    string `json:"unit"`
	Value   string `json:"value"`
}

func loadBindingCases(t *testing.T) []bindingCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/bindings_state.json")
	if err != nil {
		t.Fatal(err)
	}
	var f bindingFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func bindingAcquired(frame canbus.Frame, roles roweval.Roles) roweval.Acquired[Selector] {
	source, epoch, binding := semreg.SourceID("source.synthetic"), semreg.SourceEpochID("epoch.synthetic"), semreg.NativeBindingID("binding.synthetic")
	generation := semreg.Uint64("2")
	evidence := []semreg.EvidenceRef{{Owner: "driver.synthetic", Kind: "evidence.can.frame", Digest: semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(frame.Data()))), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
	received := semreg.TimePoint{UnixNanoseconds: "100", ClockID: "clock.synthetic", UncertaintyNS: "0"}
	mono := semreg.MonotonicPoint{ClockEpochID: "clock:synthetic", Nanoseconds: semreg.Uint64("9000000000")}
	template := semreg.FactCandidate{
		CandidateID: "candidate.synthetic", Revision: "41",
		Quality: semreg.Quality{Assertion: semreg.AssertionObserved, Qualification: semreg.QualificationCandidate, Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood, Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessFresh, Reasons: []semreg.DefinitionID{}},
		Times: semreg.Times{ReceivedAt: received, ReceiptMonotonic: mono, EvaluatedAt: semreg.TimePoint{UnixNanoseconds: "200", ClockID: "clock.synthetic", UncertaintyNS: "0"}, EvaluateMonotonic: mono,
			SourceAt: &semreg.TimePoint{UnixNanoseconds: "90", ClockID: "clock.synthetic", UncertaintyNS: "5"}, PhenomenonAt: &semreg.TimePoint{UnixNanoseconds: "80", ClockID: "clock.synthetic", UncertaintyNS: "7"}},
		FreshnessPolicy: semreg.FreshnessPolicy{PolicyID: receivePolicy, Version: "1.0.0", FreshForNS: "1000", RetainForNS: "2000", MaxWallUncertaintyNS: "0"},
		BindingID:       &binding, SourceEpochID: &epoch, DriverGeneration: &generation, Evidence: evidence,
		Origin: semreg.OriginRef{OriginID: "origin.synthetic", Kind: semreg.OriginNativeObservation, SourceID: &source, SourceEpochID: &epoch, BindingID: &binding, Evidence: evidence},
	}
	return roweval.Acquired[Selector]{Template: template, Roles: roles, Receipt: roweval.Receipt{ReceivedAt: received, Monotonic: mono}}
}

func packRoles(t *testing.T, instance string) roweval.Roles {
	t.Helper()
	type catalogRef struct {
		ID    semreg.DefinitionID
		Owner struct{ Pack semreg.PackRef }
	}
	var catalog struct {
		Elements []struct {
			Ref  catalogRef
			Kind string `json:"element_kind"`
		}
	}
	raw, err := os.ReadFile("../../../driverdata/families/mapping-gates/element-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	index := semreg.DefinitionIndex{Pack: semreg.PackRef{ID: storagePackID, Version: storagePackVersion}}
	for _, e := range catalog.Elements {
		if e.Ref.Owner.Pack != index.Pack {
			continue
		}
		ref := semreg.DefinitionRef{Pack: index.Pack, ID: e.Ref.ID}
		switch e.Kind {
		case "service":
			if e.Ref.ID == "storage.service.pack" {
				index.Services = append(index.Services, ref)
			}
		case "capability":
			index.Capabilities = append(index.Capabilities, ref)
		case "fact":
			index.Fields = append(index.Fields, ref)
		}
	}
	capability := semreg.CapabilityInstanceID("capability." + instance)
	return roweval.Roles{Subjects: map[string]roweval.RoleBinding{packRole: {ServiceInstance: semreg.ServiceInstanceID("service." + instance), CapabilityInstance: &capability}}, Catalog: []semreg.DefinitionIndex{index}}
}

func assertFact(t *testing.T, name string, ev roweval.Evaluation, want bindingExpect, instance string, a roweval.Acquired[Selector]) {
	t.Helper()
	if ev.Reason != "" || ev.Candidate == nil || ev.Subject == nil || ev.Candidate.Value == nil || ev.Candidate.Value.Quantity == nil {
		t.Errorf("%s: no candidate: reason %q (%s)", name, ev.Reason, ev.Detail)
		return
	}
	got := *ev.Candidate
	packText := packRole
	key := semreg.FactKey{PackID: storagePackID, PackVersion: storagePackVersion, FactID: semreg.DefinitionID(want.Fact), Dimensions: []semreg.Dimension{{ID: packDimension, Value: semreg.Value{Kind: semreg.ValueText, Text: &packText}}}}
	if !reflect.DeepEqual(got.Key, key) {
		t.Errorf("%s: key %+v, want %+v", name, got.Key, key)
	}
	pack := semreg.PackRef{ID: storagePackID, Version: storagePackVersion}
	capability := semreg.CapabilityInstanceID("capability." + instance)
	subject := &roweval.Subject{Role: packRole, Service: semreg.DefinitionRef{Pack: pack, ID: "storage.service.pack"}, Capability: semreg.DefinitionRef{Pack: pack, ID: "storage.capability.read.pack"}, ServiceInstance: semreg.ServiceInstanceID("service." + instance), CapabilityInstance: &capability}
	if !reflect.DeepEqual(ev.Subject, subject) {
		t.Errorf("%s: subject %+v, want %+v", name, ev.Subject, subject)
	}
	q := got.Value.Quantity
	if got.Value.Kind != semreg.ValueQuantity || string(q.Unit) != want.Unit || ratOf(t, q.Number).Cmp(ratText(t, want.Value)) != 0 {
		t.Errorf("%s: value %+v, want %s %s", name, got.Value, want.Value, want.Unit)
	}
	got.Key, got.Value = a.Template.Key, a.Template.Value
	if !reflect.DeepEqual(got, a.Template) {
		t.Errorf("%s: the candidate changed caller-owned metadata", name)
	}
}

func checkBindingCase(t *testing.T, c bindingCase) {
	t.Helper()
	instance := "pack." + c.Selection.Bank
	var (
		table roweval.Table[Selector]
		frame canbus.Frame
		qs    roweval.Qualifiers
	)
	if c.Table == "fine" {
		s, in, gates := fineInput(t, fineCase{Name: c.Name, Family: c.Family, Payload: c.Payload, Selection: c.Selection, Fine: c.Fine})
		table, frame, qs = BoundFineTable(), in.Evidence.Frame, FineQualifiers(s, in, gates)
	} else {
		s, in := caseInput(t, vectorCase{Name: c.Name, Family: c.Family, Payload: c.Payload, Selection: c.Selection})
		table, frame, qs = BoundTable(), in.Evidence.Frame, Qualifiers(s, in)
	}
	qs = BindingQualifiers(qs, c.CountAware, c.CountValid)
	roles := packRoles(t, instance)
	if err := roweval.ValidateTable(table, Descriptors(), roles); err != nil {
		t.Fatalf("%s: table: %v", c.Name, err)
	}
	native := make(map[string]roweval.Evaluation)
	acquired := make(map[string]roweval.Acquired[Selector])
	family := uint16(frame.ID().Value() >> 16)
	for _, row := range table.Native {
		if row.At.Family != family {
			continue
		}
		a := bindingAcquired(frame, roles)
		a.Selector, a.Payload, a.Qualifiers, a.Descriptors = frameSelector(frame, row.At), frame.Data(), qs, Descriptors()
		ev, err := roweval.Evaluate(table, a)
		if err != nil {
			t.Fatalf("%s: %s: %v", c.Name, row.ID, err)
		}
		if ev.Native != nil {
			native[ev.Native.RowID], acquired[ev.Native.RowID] = ev, a
		}
	}
	for id, ev := range native {
		if _, named := c.Expect[id]; !named && ev.Candidate != nil {
			t.Errorf("%s/%s: a candidate was produced without an expectation", c.Name, id)
		}
	}
	for id, want := range c.Expect {
		name := c.Name + "/" + id
		ev, ok := native[id]
		if !ok {
			t.Errorf("%s: the native row does not apply", name)
			continue
		}
		switch want.Outcome {
		case "fact":
			assertFact(t, name, ev, want, instance, acquired[id])
		case "none":
			if ev.Candidate != nil || ev.Subject != nil || ev.Native == nil {
				t.Errorf("%s: want the native value only, got candidate %+v", name, ev.Candidate)
			}
		default:
			t.Fatalf("%s: unknown outcome %q", name, want.Outcome)
		}
	}
}

func TestBindingsGiveTheBoundElementsInSIOnEveryAssertedBank(t *testing.T) {
	for _, c := range loadBindingCases(t) {
		t.Run(c.Name, func(t *testing.T) { checkBindingCase(t, c) })
	}
}

func TestEveryBindingHasAVectorThatBinds(t *testing.T) {
	bound := map[string]map[string]bool{}
	for _, c := range loadBindingCases(t) {
		for id, e := range c.Expect {
			if e.Outcome == "fact" {
				if bound[id] == nil {
					bound[id] = map[string]bool{}
				}
				bound[id][c.Selection.Bank] = true
			}
		}
	}
	for _, b := range Bindings() {
		bank := b.When.Equal[qBank]
		if !bound[b.NativeID][bank] {
			t.Errorf("%s has no binding vector under the %s bank", b.NativeID, bank)
		}
	}
}

func TestBindingsDeclareNoWayBack(t *testing.T) {
	roles := packRoles(t, "pack.ordinary")
	for name, table := range map[string]roweval.Table[Selector]{"telemetry": BoundTable(), "fine": BoundFineTable()} {
		if len(table.Bindings) == 0 {
			t.Errorf("%s: no bindings", name)
		}
		for _, b := range table.Bindings {
			if b.Write != nil || b.Read == nil {
				t.Errorf("%s: %s is not a read-only binding", name, b.NativeID)
			}
			got, err := roweval.Invert(table, roweval.Writing[Selector]{NativeID: b.NativeID, SubjectRole: packRole, Descriptors: Descriptors()})
			if err != nil || got.Payload != nil || got.Target != "" || got.Reason == "" {
				t.Errorf("%s: %s gives a write: %+v %v", name, b.NativeID, got, err)
			}
		}
		if err := roweval.ValidateTable(table, Descriptors(), roles); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEveryBindingNamesOneBankAndBothBanksAreBound(t *testing.T) {
	banks := map[string]map[string]bool{}
	for _, b := range Bindings() {
		bank := b.When.Equal[qBank]
		if bank != "ordinary" && bank != "parallel" {
			t.Errorf("%s: bank %q", b.NativeID, bank)
		}
		if banks[b.NativeID] == nil {
			banks[b.NativeID] = map[string]bool{}
		}
		banks[b.NativeID][bank] = true
	}
	for id, got := range banks {
		if !got["ordinary"] || !got["parallel"] {
			t.Errorf("%s is bound for only %v", id, got)
		}
	}
}

func TestPortVoltageSocStatusLimitCodesAndRemainingCapacityStayNative(t *testing.T) {
	for _, id := range []string{rowPrefix + "port_voltage", rowPrefix + "soc_status", rowPrefix + "discharge_limit", rowPrefix + "charge_limit",
		rowPrefix + "1806.mean", rowPrefix + "remaining_capacity", rowPrefix + "temperature_maximum", rowPrefix + "temperature_minimum"} {
		for _, b := range Bindings() {
			if b.NativeID == id {
				t.Errorf("%s is bound to %s", id, b.Read.Fact.ID)
			}
		}
	}
	for _, b := range Bindings() {
		switch b.Read.Fact.ID {
		case "storage.capacity.discharge", "storage.state.soh", "storage.temperature.cell", "storage.limit.charge_current", "storage.limit.discharge_current":
			t.Errorf("%s gives %s", b.NativeID, b.Read.Fact.ID)
		}
	}
}

func TestSourcePrioritiesAreDistinctPerElementAndFinerRepresentationsOutrank(t *testing.T) {
	byFact := map[semreg.DefinitionID]map[int32]string{}
	score := map[string]int32{}
	for _, p := range SourcePriorities() {
		if byFact[p.Fact] == nil {
			byFact[p.Fact] = map[int32]string{}
		}
		if other, dup := byFact[p.Fact][p.Priority]; dup {
			t.Errorf("%s and %s share priority %d on %s", p.NativeID, other, p.Priority, p.Fact)
		}
		byFact[p.Fact][p.Priority] = p.NativeID
		score[p.NativeID] = p.Priority
	}
	if score[rowPrefix+"1807.b4"] <= score[rowPrefix+"whole_estimate"] {
		t.Errorf("fine state of charge %d does not outrank the whole-percent estimate %d", score[rowPrefix+"1807.b4"], score[rowPrefix+"whole_estimate"])
	}
	if score[rowPrefix+"1862.mean"] <= score[rowPrefix+"extra_average"] {
		t.Errorf("1862 mean %d does not outrank the 1808 average %d", score[rowPrefix+"1862.mean"], score[rowPrefix+"extra_average"])
	}
	bindings := map[string]bool{}
	for _, b := range Bindings() {
		bindings[b.NativeID] = true
	}
	for id := range score {
		if !bindings[id] {
			t.Errorf("priority for %s which has no binding", id)
		}
	}
	for id := range bindings {
		if _, ok := score[id]; !ok {
			t.Errorf("binding %s has no priority", id)
		}
	}
}
