package pcs

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type bindingFile struct {
	Cases []bindingCase `json:"cases"`
}

type bindingCase struct {
	Name       string                   `json:"name"`
	Table      string                   `json:"table"`
	Identifier string                   `json:"identifier"`
	Payload    string                   `json:"payload"`
	Selection  bindingSelection         `json:"selection"`
	Input      vectorInput              `json:"input"`
	Expect     map[string]bindingExpect `json:"expect"`
}

type bindingSelection struct {
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

type bindingExpect struct {
	Outcome string `json:"outcome"`
	Fact    string `json:"fact"`
	Unit    string `json:"unit"`
	Value   string `json:"value"`
	Native  string `json:"native"`
	Reason  string `json:"reason"`
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

// observation is one frame with the qualifiers of its selection and the bound
// table of its family.
type observation struct {
	table      roweval.Table[Selector]
	frame      canbus.Frame
	qualifiers roweval.Qualifiers
	identifier uint32
	extended   bool
}

func observe(t *testing.T, c bindingCase) observation {
	t.Helper()
	sel := c.Selection
	switch c.Table {
	case "standard":
		s, in, frame := build(t, vectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload,
			Selection: vectorSelection{Project: sel.Project, Release: sel.Release, Flavor: sel.Flavor, Bank: sel.Bank, Scope: sel.Scope, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot}, Input: c.Input})
		return observation{table: BoundTable(), frame: frame, qualifiers: Qualifiers(s, in), identifier: frame.ID().Value()}
	case "extended":
		s, in, frame := buildExtended(t, extendedVectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload,
			Selection: extendedVectorSelection(sel)})
		kind, ok := extendedFrameKind(s, frame)
		if !ok {
			t.Fatalf("%s: not an extended frame kind", c.Name)
		}
		return observation{table: BoundExtendedTable(), frame: frame, qualifiers: ExtendedQualifiers(s, in, kind), identifier: kind << 16, extended: true}
	case "parallel":
		s, in, x, frame := parallelBuild(t, parallelVectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload,
			Selection: slotVectorSelection{Project: sel.Project, Release: sel.Release, Flavor: sel.Flavor, Bank: sel.Bank, Scope: sel.Scope, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot}})
		family, ok := parallelFamily(frame)
		if !ok {
			t.Fatalf("%s: not a parallel family", c.Name)
		}
		return observation{table: BoundParallelTable(), frame: frame, qualifiers: parallelQualifiers(s, in, x, family), identifier: family}
	}
	t.Fatalf("%s: unknown table %q", c.Name, c.Table)
	return observation{}
}

func (o observation) row(t *testing.T, id string) roweval.NativeRow[Selector] {
	t.Helper()
	for _, r := range o.table.Native {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no native row %s", id)
	return roweval.NativeRow[Selector]{}
}

func (o observation) evaluate(t *testing.T, row roweval.NativeRow[Selector], roles roweval.Roles) (roweval.Evaluation, error) {
	t.Helper()
	return roweval.Evaluate(o.table, o.acquired(row, roles))
}

func (o observation) acquired(row roweval.NativeRow[Selector], roles roweval.Roles) roweval.Acquired[Selector] {
	a := bindingAcquired(o.frame, roles)
	a.Selector = Selector{Identifier: o.identifier, Extended: o.extended, DLC: o.frame.DLC(), RawDLC: o.frame.RawDLC(), Slice: row.At.Slice}
	a.Payload = o.frame.Data()
	a.Qualifiers = o.qualifiers
	a.Descriptors = Descriptors()
	return a
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

// packRoles resolves the pack role of the selected bank to the given service
// instance, over the storage definitions of the independent element catalog.
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

// assertFact checks one candidate: the exact key on the resolved pack, the
// subject of the selected bank, the registry unit, the exact value and the
// caller-owned metadata.
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
	if got.Value.Kind != semreg.ValueQuantity || string(q.Unit) != want.Unit || decimalRat(t, q.Number).Cmp(ratText(t, want.Value)) != 0 {
		t.Errorf("%s: value %+v, want %s %s", name, got.Value, want.Value, want.Unit)
	}
	got.Key, got.Value = a.Template.Key, a.Template.Value
	if !reflect.DeepEqual(got, a.Template) {
		t.Errorf("%s: the candidate changed caller-owned metadata", name)
	}
}

func assertNoCandidate(t *testing.T, name string, ev roweval.Evaluation) {
	t.Helper()
	if ev.Candidate != nil || ev.Subject != nil {
		t.Errorf("%s: a candidate was produced: %+v", name, ev.Candidate)
	}
}

// checkBindingCase evaluates every native row of the frame under the case and
// compares each one with the independent expectation; a row not named by the
// case must give no candidate.
func checkBindingCase(t *testing.T, c bindingCase, instance string) {
	t.Helper()
	o := observe(t, c)
	roles := packRoles(t, instance)
	if err := roweval.ValidateTable(o.table, Descriptors(), roles); err != nil {
		t.Fatalf("%s: table: %v", c.Name, err)
	}
	for id := range c.Expect {
		o.row(t, id)
	}
	// The evaluator matches an observation to the one row whose slice and
	// conditions hold, so each distinct slice is evaluated once and its result
	// belongs to the row it names.
	type result struct {
		ev roweval.Evaluation
		a  roweval.Acquired[Selector]
	}
	results := make(map[string]result)
	for _, row := range o.table.Native {
		if row.At.Identifier != o.identifier {
			continue
		}
		a := o.acquired(row, roles)
		ev, err := roweval.Evaluate(o.table, a)
		if err != nil {
			t.Fatalf("%s: %s: %v", c.Name, row.ID, err)
		}
		if ev.Native != nil {
			results[ev.Native.RowID] = result{ev, a}
		}
	}
	for id, r := range results {
		if _, named := c.Expect[id]; !named {
			assertNoCandidate(t, c.Name+"/"+id, r.ev)
		}
	}
	for id, want := range c.Expect {
		name := c.Name + "/" + id
		r, applied := results[id]
		ev, a := r.ev, r.a
		if want.Outcome == "no_match" {
			if applied {
				t.Errorf("%s: the row applies although its selection conditions do not hold", name)
			}
			continue
		}
		if !applied {
			t.Errorf("%s: the row does not apply", name)
			continue
		}
		switch want.Outcome {
		case "fact":
			assertFact(t, name, ev, want, instance, a)
		case "no_binding":
			assertNoCandidate(t, name, ev)
			if ev.Reason != roweval.ReasonNoBinding || ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil || decimalRat(t, ev.Native.Value.Quantity.Number).Cmp(ratText(t, want.Native)) != 0 {
				t.Errorf("%s: want the native value %s with no binding, got reason %q native %+v", name, want.Native, ev.Reason, ev.Native)
			}
		case "withheld":
			assertNoCandidate(t, name, ev)
			if ev.Reason != roweval.Reason(want.Reason) || ev.Native == nil || ev.Native.Reason != roweval.Reason(want.Reason) {
				t.Errorf("%s: want withheld %s, got reason %q native %+v", name, want.Reason, ev.Reason, ev.Native)
			}
		default:
			t.Fatalf("%s: unknown outcome %q", name, want.Outcome)
		}
	}
}

func caseByName(t *testing.T, name string) bindingCase {
	t.Helper()
	for _, c := range loadBindingCases(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no binding vector named %s", name)
	return bindingCase{}
}

func checkNamed(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		c := caseByName(t, n)
		t.Run(n, func(t *testing.T) { checkBindingCase(t, c, "pack."+c.Selection.Bank) })
	}
}

func TestBindingsGiveSocSohCurrentAndPackVoltageInSI(t *testing.T) {
	checkNamed(t,
		"legacy-local-313", "legacy-local-313-percent-zero", "legacy-local-313-percent-hundred",
		"legacy-local-313-percent-above-hundred", "legacy-local-313-positive-current",
		"legacy-local-313-group-and-slot-absent", "legacy-local-313-group-and-slot-at-boundary",
		"release11-local-313-average-25C", "extended-1902-local-pack-and-port", "extended-1902-current-offset",
		"extended-1902-local-group-and-slot-absent", "parallel-610-local", "parallel-610-group-and-slot-present")
}

func TestLegacyPackVoltageAndExtended1902PackVoltageBindButRelease11PortVoltageDoesNot(t *testing.T) {
	checkNamed(t, "legacy-local-313", "release11-local-313-average-25C", "extended-1902-local-pack-and-port", "extended-1902-other-release-is-withheld")
	for _, id := range []string{rowPrefix + "313.release11.voltage", extendedID(0x1902, "port_voltage"), parallelRowPrefix + "610.voltage", parallelRowPrefix + "610.local.port"} {
		for _, b := range Bindings() {
			if b.NativeID == id {
				t.Errorf("%s has a binding to %s", id, b.Read.Fact.ID)
			}
		}
	}
}

func TestRelease11AverageTemperatureBindsInKelvinOnlyWithValidCount(t *testing.T) {
	checkNamed(t, "release11-local-313-average-25C", "release11-local-313-negative-average", "release11-local-313-count-one",
		"release11-local-313-count-zero", "release11-local-313-count-invalid", "release11-local-313-temperature-invalid",
		"release11-local-313-measurement-invalid")
}

func TestLegacyMinimumTemperatureStaysNative(t *testing.T) {
	checkNamed(t, "legacy-local-313", "legacy-313-count-unresolved-keeps-minimum-native")
	for _, b := range Bindings() {
		if b.NativeID == rowPrefix+"313.legacy.temperature" {
			t.Errorf("the legacy coded minimum temperature is bound to %s", b.Read.Fact.ID)
		}
	}
}

func TestLimitWordsOf311HaveNoBinding(t *testing.T) {
	checkNamed(t, "legacy-local-311-limit-words")
	for _, b := range Bindings() {
		if strings.Contains(b.NativeID, ".311.") {
			t.Errorf("%s is bound to %s", b.NativeID, b.Read.Fact.ID)
		}
		switch b.Read.Fact.ID {
		case "storage.limit.charge_current", "storage.limit.discharge_current":
			t.Errorf("%s gives a limit fact", b.NativeID)
		}
	}
}

func TestBindingsDeclareNoWayBack(t *testing.T) {
	roles := packRoles(t, "pack.ordinary")
	for name, table := range map[string]roweval.Table[Selector]{"standard": BoundTable(), "extended": BoundExtendedTable(), "parallel": BoundParallelTable()} {
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

func TestUnresolvedPackGivesNoCandidate(t *testing.T) {
	c := caseByName(t, "legacy-local-313")
	o := observe(t, c)
	row := o.row(t, rowPrefix+"313.soc")
	ev, err := o.evaluate(t, row, roweval.Roles{Catalog: packRoles(t, "pack.ordinary").Catalog})
	var input *roweval.InputError
	if !errors.As(err, &input) || input.Kind != roweval.InputUnresolvedRole || ev.Candidate != nil {
		t.Fatalf("an unresolved pack: error %v, candidate %+v", err, ev.Candidate)
	}
}

func TestBindingsAttributeEachBankToItsOwnPack(t *testing.T) {
	ordinary := observe(t, caseByName(t, "legacy-local-313"))
	parallel := observe(t, caseByName(t, "parallel-610-local"))
	for _, tc := range []struct {
		o        observation
		row      string
		instance string
	}{{ordinary, rowPrefix + "313.current", "pack.ordinary"}, {parallel, parallelRowPrefix + "610.current", "pack.parallel"}} {
		row := tc.o.row(t, tc.row)
		a := tc.o.acquired(row, packRoles(t, tc.instance))
		ev, err := roweval.Evaluate(tc.o.table, a)
		if err != nil || ev.Subject == nil || ev.Subject.ServiceInstance != semreg.ServiceInstanceID("service."+tc.instance) {
			t.Errorf("%s: subject %+v (%v), want the pack of %s", tc.row, ev.Subject, err, tc.instance)
		}
	}
	// The same frame under the other flavor and bank pairing gives no
	// candidate: the binding applies to the bank of its own selection.
	o := observe(t, caseByName(t, "legacy-local-313"))
	o.qualifiers.Values[qBank] = "parallel"
	row := o.row(t, rowPrefix+"313.soc")
	ev, err := o.evaluate(t, row, packRoles(t, "pack.ordinary"))
	if err != nil || ev.Reason != roweval.ReasonNoMatch || ev.Candidate != nil {
		t.Errorf("standard row under the parallel bank: %q %v", ev.Reason, err)
	}
	spec := mustSelection(t, caseByName(t, "legacy-local-313")).SelectionSpecValue()
	spec.Bank = "parallel"
	if _, err := canbusreg.NewSelection(spec); !errors.Is(err, canbusreg.ErrSelectionBankPairing) {
		t.Errorf("pcs-standard with the parallel bank: %v", err)
	}
}

func mustSelection(t *testing.T, c bindingCase) canbusreg.Selection {
	t.Helper()
	sel := c.Selection
	s, _, _ := build(t, vectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload,
		Selection: vectorSelection{Project: sel.Project, Release: sel.Release, Flavor: sel.Flavor, Bank: sel.Bank, Scope: sel.Scope, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot}, Input: c.Input})
	return s
}

func TestBindingsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	o := observe(t, caseByName(t, "legacy-local-313"))
	row := o.row(t, rowPrefix+"313.soc")
	roles := packRoles(t, "pack.ordinary")
	payload := o.frame.Data()
	standard := func(id uint32, data []byte) canbus.Frame {
		i, err := canbus.NewStandardID(id)
		if err != nil {
			t.Fatal(err)
		}
		f, err := canbus.NewFrame(i, data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	extended := func(id uint32, data []byte) canbus.Frame {
		i, err := canbus.NewExtendedID(id)
		if err != nil {
			t.Fatal(err)
		}
		f, err := canbus.NewFrame(i, data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	for name, frame := range map[string]canbus.Frame{
		"neighbouring-identifier": standard(0x315, payload),
		"seven-data-bytes":        standard(0x313, payload[:7]),
		"extended-format":         extended(0x313, payload),
	} {
		other := o
		other.frame, other.identifier, other.extended = frame, frame.ID().Value(), frame.ID().Extended()
		ev, err := other.evaluate(t, row, roles)
		if err != nil || ev.Reason != roweval.ReasonNoMatch || ev.Candidate != nil {
			t.Errorf("%s: reason %q candidate %+v err %v", name, ev.Reason, ev.Candidate, err)
		}
	}
}

func TestBindingsMatchIndependentSpecificationExpectations(t *testing.T) {
	cases := loadBindingCases(t)
	facts := make(map[string]bool)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			checkBindingCase(t, c, "pack."+c.Selection.Bank)
		})
		for _, e := range c.Expect {
			if e.Outcome == "fact" {
				facts[e.Fact] = true
			}
		}
	}
	for _, f := range []string{"storage.state.soc", "storage.state.soh", "storage.pack.current", "storage.pack.voltage", "storage.temperature.pack"} {
		if !facts[f] {
			t.Errorf("no vector gives a %s candidate", f)
		}
	}
	covered := make(map[string]bool)
	for _, c := range cases {
		for id, e := range c.Expect {
			if e.Outcome == "fact" {
				covered[id] = true
			}
		}
	}
	for _, b := range Bindings() {
		if !covered[b.NativeID] {
			t.Errorf("binding %s has no vector", b.NativeID)
		}
	}
}

type exemplarFixture struct {
	Identifier  uint32 `json:"identifier"`
	Extended    bool   `json:"extended"`
	DLC         uint8  `json:"dlc"`
	Data        string `json:"data_hex"`
	Source      uint8  `json:"source"`
	Destination uint8  `json:"destination"`
	Direction   string `json:"direction"`
	Project     string `json:"project"`
	Release     string `json:"release"`
	Scope       string `json:"scope"`
	Bank        string `json:"bank"`
	Endpoint    string `json:"endpoint"`
	Group       string `json:"group"`
	Slot        string `json:"slot"`
	Pack        struct {
		Counts int64  `json:"counts"`
		Volts  string `json:"volts"`
	} `json:"pack"`
	Port struct {
		Counts int64  `json:"counts"`
		Volts  string `json:"volts"`
	} `json:"port"`
}

func TestPCSPackBindingMatchesExemplarSignedPattern(t *testing.T) {
	const fixturePath = "../../examples/can/testdata/growatt_pcs_addressed_voltage.json"
	const patternPath = "../../examples/can/growatt_pcs_rows.go"
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fx exemplarFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	pattern, err := os.ReadFile(patternPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("operand SHA-256 %x, signed pattern SHA-256 %x", sha256.Sum256(raw), sha256.Sum256(pattern))
	// The signed pattern binds the B0-B1 pack voltage to the storage pack at
	// 0.1 V per count and leaves the B4-B5 port voltage native.
	text := string(pattern)
	for _, part := range []string{`"storage.pack.voltage", "unit.volt", 1)`, `"pack_voltage", "unit.volt"`, `"port_voltage", "unit.volt"`} {
		if !strings.Contains(text, part) {
			t.Fatalf("the signed pattern no longer states %s", part)
		}
	}
	if strings.Count(text, "packRead(") != 1 || !strings.Contains(text, "packRead(pack.ID,") {
		t.Fatal("the signed pattern binds more than the pack voltage")
	}

	c := bindingCase{Name: "exemplar-1902", Table: "extended", Identifier: fmt.Sprintf("0x%X", fx.Identifier), Payload: strings.ToUpper(fx.Data),
		Selection: bindingSelection{Project: fx.Project, Release: fx.Release, Flavor: "pcs-extended19", Bank: fx.Bank, Scope: fx.Scope, Endpoint: fx.Endpoint, Group: fx.Group, Slot: fx.Slot, Destination: fx.Destination, Source: fx.Source}}
	if !fx.Extended || fx.DLC != 8 || fx.Direction != "bms-to-pcs" {
		t.Fatalf("exemplar fixture frame: %+v", fx)
	}
	o := observe(t, c)
	roles := packRoles(t, "pack.ordinary")
	pack := o.row(t, extendedID(0x1902, "pack_voltage"))
	a := o.acquired(pack, roles)
	ev, err := roweval.Evaluate(o.table, a)
	if err != nil {
		t.Fatal(err)
	}
	assertFact(t, "exemplar pack voltage", ev, bindingExpect{Fact: "storage.pack.voltage", Unit: "unit.volt", Value: fx.Pack.Volts}, "pack.ordinary", a)
	if ev.Native == nil || ev.Native.Raw != uint64(fx.Pack.Counts) {
		t.Errorf("native raw word %+v, want %d counts", ev.Native, fx.Pack.Counts)
	}

	port := o.row(t, extendedID(0x1902, "port_voltage"))
	pev, err := o.evaluate(t, port, roles)
	if err != nil || pev.Reason != roweval.ReasonNoBinding || pev.Candidate != nil || pev.Native == nil || pev.Native.Raw != uint64(fx.Port.Counts) ||
		decimalRat(t, pev.Native.Value.Quantity.Number).Cmp(ratText(t, fx.Port.Volts)) != 0 {
		t.Errorf("port voltage %+v: %q %v", pev.Native, pev.Reason, err)
	}

	// The donor's native output, retained separately from the binding.
	s, in, _ := buildExtended(t, extendedVectorCase{Name: c.Name, Identifier: c.Identifier, Payload: c.Payload, Selection: extendedVectorSelection{Project: fx.Project, Release: fx.Release, Flavor: "pcs-extended19", Bank: fx.Bank, Scope: fx.Scope, Endpoint: fx.Endpoint, Group: fx.Group, Slot: fx.Slot, Destination: fx.Destination, Source: fx.Source}})
	donor := canbusreg.DecodePCSExtended(s, in)
	if donor.Status != "accepted" {
		t.Fatalf("donor status %q", donor.Status)
	}
	for field, counts := range map[string]int64{"pack_voltage": fx.Pack.Counts, "port_voltage": fx.Port.Counts} {
		f, ok := donor.Fields[field]
		if !ok || f.Numerator != counts || f.Denominator != 10 || f.Unit != "V" {
			t.Errorf("donor %s: %+v, want %d/10 V", field, f, counts)
		}
	}
}
