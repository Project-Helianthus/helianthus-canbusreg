package link

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type pcsBindingFile struct {
	Cases []pcsBindingCase `json:"cases"`
}

type pcsBindingCase struct {
	Name      string                      `json:"name"`
	Frame     string                      `json:"frame"`
	Payload   string                      `json:"payload"`
	Selection pcsBindingSelection         `json:"selection"`
	Input     pcsBindingInput             `json:"input"`
	Index     *pcsBindingIndex            `json:"index"`
	Resolve   pcsBindingResolve           `json:"resolve"`
	Expect    map[string]pcsBindingExpect `json:"expect"`
}

type pcsBindingSelection struct {
	Project  string `json:"project"`
	Release  string `json:"release"`
	Flavor   string `json:"flavor"`
	Bank     string `json:"bank"`
	Scope    string `json:"scope"`
	Endpoint string `json:"endpoint"`
	Group    string `json:"group"`
	Slot     string `json:"slot"`
}

type pcsBindingInput struct {
	MeasurementValid bool `json:"measurement_valid"`
	SlotOK           bool `json:"slot_ok"`
}

type pcsBindingIndex struct {
	Valid   bool   `json:"valid"`
	Scope   string `json:"scope"`
	Minimum uint16 `json:"minimum"`
	Maximum uint16 `json:"maximum"`
}

type pcsBindingResolve struct {
	Maximum bool `json:"maximum"`
	Minimum bool `json:"minimum"`
}

type pcsBindingExpect struct {
	Outcome string `json:"outcome"`
	Fact    string `json:"fact"`
	Unit    string `json:"unit"`
	Value   string `json:"value"`
	Role    string `json:"role"`
	Native  string `json:"native"`
	Reason  string `json:"reason"`
}

func loadPCSBindingCases(t *testing.T) []pcsBindingCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/bindings_pcs.json")
	if err != nil {
		t.Fatal(err)
	}
	var f pcsBindingFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no vectors")
	}
	return f.Cases
}

// pcsObservation is one frame with the decoder's own evaluation of its native
// rows and the qualifiers the bound table is evaluated under.
type pcsObservation struct {
	frame      canbus.Frame
	identifier uint32
	qualifiers roweval.Qualifiers
	decoded    map[string]roweval.Evaluation
}

func pcsObserve(t *testing.T, c pcsBindingCase) pcsObservation {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := c.Selection
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	id, err := strconv.ParseUint(c.Frame, 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := canbus.NewStandardID(uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(ident, payload)
	if err != nil {
		t.Fatal(err)
	}
	link := canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
	obs := pcsObservation{frame: frame, identifier: uint32(id), decoded: map[string]roweval.Evaluation{}}
	standard := canbusreg.PCSStandardPCSInput{LINKInput: link, PublisherResolved: true, MeasurementValid: c.Input.MeasurementValid}
	if c.Index != nil {
		standard.IndexValid, standard.IndexScope, standard.IndexMinimum, standard.IndexMaximum = c.Index.Valid, c.Index.Scope, c.Index.Minimum, c.Index.Maximum
	}
	switch c.Frame {
	case "326", "327":
		slotNumber, _ := strconv.Atoi(payloadByte(payload, 0))
		in := canbusreg.PCSSlotPCSInput{
			PCSIdentityPCSInput: canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: standard},
			SlotValid:           true, NativeSlot: uint8(slotNumber), GroupValid: true, NativeGroup: 0,
		}
		if !c.Input.SlotOK {
			in.NativeSlot++
		}
		res, _, err := pcs.EvaluateSlots(s, in)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		obs.decoded = res.Rows
		measured := c.Input.MeasurementValid && c.Input.SlotOK
		slotOK := c.Input.SlotOK
		obs.qualifiers = roweval.Qualifiers{Values: map[string]string{
			"flavor": sel.Flavor, "bank": sel.Bank, "slot-context": "slot-frame",
			"slot-326": pcsBool(slotOK), "measured-326": pcsBool(measured), "slot-327": pcsBool(slotOK), "measured-327": pcsBool(measured),
		}}
	default:
		res, _, err := pcs.EvaluateExtrema(s, standard)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		obs.decoded = res.Rows
		obs.qualifiers = pcs.Qualifiers(s, standard)
	}
	return obs
}

func payloadByte(p []byte, at int) string { return strconv.Itoa(int(p[at])) }

func pcsBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// pcsRoles resolves the pack role of the slot and, as the case says, the
// cell roles of the maximum and of the minimum, over the independent catalog.
func pcsRoles(t *testing.T, c pcsBindingCase, all bool) roweval.Roles {
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
			if e.Ref.ID == "storage.service.pack" || e.Ref.ID == "storage.service.cell" {
				index.Services = append(index.Services, ref)
			}
		case "capability":
			index.Capabilities = append(index.Capabilities, ref)
		case "fact":
			index.Fields = append(index.Fields, ref)
		}
	}
	bind := func(kind, name string) roweval.RoleBinding {
		capability := semreg.CapabilityInstanceID("capability." + kind + "." + name)
		return roweval.RoleBinding{ServiceInstance: semreg.ServiceInstanceID("service." + kind + "." + name), CapabilityInstance: &capability}
	}
	roles := roweval.Roles{Subjects: map[string]roweval.RoleBinding{packRole: bind("pack", c.Selection.Bank)}, Catalog: []semreg.DefinitionIndex{index}}
	if all || c.Resolve.Maximum {
		roles.Subjects[cellMaximumRole] = bind("cell", "maximum")
	}
	if all || c.Resolve.Minimum {
		roles.Subjects[cellMinimumRole] = bind("cell", "minimum")
	}
	return roles
}

func pcsAcquired(frame canbus.Frame, roles roweval.Roles) roweval.Acquired[pcs.Selector] {
	base := bindingAcquired(frame, roles)
	base.Template.FreshnessPolicy.PolicyID = pcsReceivePolicy
	return roweval.Acquired[pcs.Selector]{Template: base.Template, Roles: roles, Receipt: base.Receipt}
}

func pcsRowByID(t *testing.T, table roweval.Table[pcs.Selector], id string) roweval.NativeRow[pcs.Selector] {
	t.Helper()
	for _, r := range table.Native {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no native row %s", id)
	return roweval.NativeRow[pcs.Selector]{}
}

func pcsEvaluate(t *testing.T, o pcsObservation, table roweval.Table[pcs.Selector], row roweval.NativeRow[pcs.Selector], roles roweval.Roles) (roweval.Evaluation, roweval.Acquired[pcs.Selector], error) {
	t.Helper()
	a := pcsAcquired(o.frame, roles)
	a.Selector = pcs.Selector{Identifier: o.identifier, DLC: o.frame.DLC(), RawDLC: o.frame.RawDLC(), Slice: row.At.Slice}
	a.Payload = o.frame.Data()
	a.Qualifiers = o.qualifiers
	a.Descriptors = pcs.Descriptors()
	ev, err := roweval.Evaluate(table, a)
	return ev, a, err
}

func pcsAssertFact(t *testing.T, name string, ev roweval.Evaluation, want pcsBindingExpect, c pcsBindingCase, a roweval.Acquired[pcs.Selector]) {
	t.Helper()
	if ev.Reason != "" || ev.Candidate == nil || ev.Subject == nil || ev.Candidate.Value == nil || ev.Candidate.Value.Quantity == nil {
		t.Errorf("%s: no candidate: reason %q (%s)", name, ev.Reason, ev.Detail)
		return
	}
	cell := want.Role != packRole
	service, capability, dimension := semreg.DefinitionID("storage.service.pack"), semreg.DefinitionID("storage.capability.read.pack"), packDimension
	instanceKind, instanceName := "pack", c.Selection.Bank
	if cell {
		service, capability, dimension = "storage.service.cell", "storage.capability.read.cell", cellDimension
		instanceKind, instanceName = "cell", want.Role[len("cell."):]
	}
	got := *ev.Candidate
	text := want.Role
	key := semreg.FactKey{PackID: storagePackID, PackVersion: storagePackVersion, FactID: semreg.DefinitionID(want.Fact), Dimensions: []semreg.Dimension{{ID: dimension, Value: semreg.Value{Kind: semreg.ValueText, Text: &text}}}}
	if !reflect.DeepEqual(got.Key, key) {
		t.Errorf("%s: key %+v, want %+v", name, got.Key, key)
	}
	pack := semreg.PackRef{ID: storagePackID, Version: storagePackVersion}
	instance := semreg.CapabilityInstanceID("capability." + instanceKind + "." + instanceName)
	subject := &roweval.Subject{Role: want.Role, Service: semreg.DefinitionRef{Pack: pack, ID: service}, Capability: semreg.DefinitionRef{Pack: pack, ID: capability}, ServiceInstance: semreg.ServiceInstanceID("service." + instanceKind + "." + instanceName), CapabilityInstance: &instance}
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

func pcsNativeNumber(t *testing.T, name string, ev roweval.Evaluation, want string) {
	t.Helper()
	if ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
		t.Errorf("%s: no native value (reason %q)", name, ev.Reason)
		return
	}
	if ratOf(t, ev.Native.Value.Quantity.Number).Cmp(ratText(t, want)) != 0 {
		t.Errorf("%s: native %+v, want %s", name, ev.Native.Value.Quantity.Number, want)
	}
}

func boundPCSIDs() map[string]bool {
	ids := map[string]bool{}
	for _, b := range PCSBindings() {
		ids[b.NativeID] = true
	}
	return ids
}

func checkPCSCase(t *testing.T, c pcsBindingCase) {
	t.Helper()
	o := pcsObserve(t, c)
	table := PCSBoundTable()
	roles := pcsRoles(t, c, false)
	if err := roweval.ValidateTable(table, pcs.Descriptors(), pcsRoles(t, c, true)); err != nil {
		t.Fatalf("%s: table: %v", c.Name, err)
	}
	bound := boundPCSIDs()
	for id, want := range c.Expect {
		name := c.Name + "/" + id
		row := pcsRowByID(t, table, id)
		ev, a, err := pcsEvaluate(t, o, table, row, roles)
		applied := ev.Native != nil && ev.Native.RowID == id
		switch want.Outcome {
		case "no_match":
			if applied {
				t.Errorf("%s: the row applies although its selection conditions do not hold", name)
			}
			continue
		case "fact", "unresolved", "withheld", "native", "decoded":
		default:
			t.Fatalf("%s: unknown outcome %q", name, want.Outcome)
		}
		if !applied {
			t.Errorf("%s: the row does not apply: reason %q", name, ev.Reason)
			continue
		}
		if want.Outcome == "decoded" {
			dec, ok := o.decoded[id]
			if bound[id] || !ok || dec.Native == nil || dec.Native.Reason != "" {
				t.Errorf("%s: want an accepted decoder value of an unbound row", name)
				continue
			}
			pcsNativeNumber(t, name, dec, want.Native)
			continue
		}
		if dec, ok := o.decoded[id]; ok && dec.Native != nil {
			if dec.Native.Reason != ev.Native.Reason || !reflect.DeepEqual(dec.Native.Value, ev.Native.Value) {
				t.Errorf("%s: decoder native %+v reason %q, evaluator %+v reason %q", name, dec.Native.Value, dec.Native.Reason, ev.Native.Value, ev.Native.Reason)
			}
		} else if want.Outcome != "withheld" {
			t.Errorf("%s: the decoder evaluation has no native row", name)
		}
		switch want.Outcome {
		case "fact":
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			pcsAssertFact(t, name, ev, want, c, a)
		case "unresolved":
			var in *roweval.InputError
			if !errors.As(err, &in) || in.Kind != roweval.InputUnresolvedRole {
				t.Errorf("%s: want the unresolved role error, got %v", name, err)
			}
			if ev.Candidate != nil || ev.Subject != nil {
				t.Errorf("%s: a candidate was produced for an unresolved cell", name)
			}
			pcsNativeNumber(t, name, ev, want.Native)
		case "withheld":
			if err != nil || ev.Candidate != nil || ev.Subject != nil || ev.Reason != roweval.Reason(want.Reason) || ev.Native.Reason != roweval.Reason(want.Reason) {
				t.Errorf("%s: want withheld %s, got reason %q native reason %q err %v", name, want.Reason, ev.Reason, ev.Native.Reason, err)
			}
		case "native":
			if bound[id] {
				t.Errorf("%s: a native-only row is bound", name)
			}
			if err != nil || ev.Candidate != nil || ev.Reason != roweval.ReasonNoBinding {
				t.Errorf("%s: want no binding, got reason %q err %v", name, ev.Reason, err)
			}
			pcsNativeNumber(t, name, ev, want.Native)
		}
	}
	for _, row := range table.Native {
		if row.At.Identifier != o.identifier {
			continue
		}
		if _, named := c.Expect[row.ID]; named {
			continue
		}
		ev, _, err := pcsEvaluate(t, o, table, row, roles)
		if ev.Native != nil && ev.Native.RowID == row.ID && (ev.Candidate != nil || err != nil && !bound[row.ID]) {
			t.Errorf("%s: unnamed row %s gives a candidate or an error: %v", c.Name, row.ID, err)
		}
	}
}

func TestPCSSlotVoltageAndCurrentBindToThePackOfTheSlotInSIOnBothBanks(t *testing.T) {
	for _, c := range loadPCSBindingCases(t) {
		if c.Frame != "326" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) { checkPCSCase(t, c) })
	}
}

func TestPCSSlotCellExtremaBindToTheResolvedCellAndStayNativeWhenUnresolved(t *testing.T) {
	for _, c := range loadPCSBindingCases(t) {
		if c.Frame != "327" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) { checkPCSCase(t, c) })
	}
}

func TestPCSModuleAndAggregateCellExtremaResolveEachIndependentlyOfTheSlotFrame(t *testing.T) {
	for _, c := range loadPCSBindingCases(t) {
		if c.Frame != "319" && c.Frame != "330" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) { checkPCSCase(t, c) })
	}
}

func TestPCSBindingsRepeatNoNativeRowAndNoFactOfAnotherBindingTable(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range PCSBindings() {
		if seen[b.NativeID] {
			t.Errorf("%s is bound twice", b.NativeID)
		}
		seen[b.NativeID] = true
		for _, other := range pcs.Bindings() {
			if other.NativeID == b.NativeID {
				t.Errorf("%s is already bound by the PCS package", b.NativeID)
			}
		}
		for _, other := range Bindings() {
			if other.NativeID == b.NativeID {
				t.Errorf("%s is already bound by a LINK binding", b.NativeID)
			}
		}
		if b.Write != nil || b.Read == nil || b.Read.ToRegistry.Hook != "" {
			t.Errorf("%s: a binding with a way back or without a read row", b.NativeID)
		}
	}
	if len(seen) != 12 {
		t.Errorf("bound native rows: %d, want 12", len(seen))
	}
}

func TestPCSNativeRowsWithNoStatedElementStayUnboundAndNoInterlockOrSocIsBound(t *testing.T) {
	bound := boundPCSIDs()
	for _, id := range []string{
		"growatt.bms.pcs.standard.326.soc_low", "growatt.bms.pcs.parallel.326.soc_low",
		"growatt.bms.pcs.standard.326.slot", "growatt.bms.pcs.standard.327.maximum_index", "growatt.bms.pcs.standard.327.minimum_index",
		"growatt.bms.pcs.standard.322.temperature_maximum", "growatt.bms.pcs.standard.322.temperature_minimum",
		"growatt.bms.pcs.standard.333.dtc_a", "growatt.bms.pcs.standard.334.state",
		"growatt.bms.pcs.standard.330.maximum_location", "growatt.bms.pcs.standard.330.maximum_index",
	} {
		if bound[id] {
			t.Errorf("%s is bound", id)
		}
	}
	for _, b := range PCSBindings() {
		switch b.Read.Fact.ID {
		case "storage.state.soc", "storage.status.interlock", "storage.temperature.cell", "storage.temperature.pack":
			t.Errorf("%s feeds %s", b.NativeID, b.Read.Fact.ID)
		}
	}
}

func TestPCSBindingsCarryTheNativeRowSelectionAndTheDistinctSourcePriorities(t *testing.T) {
	table := PCSBoundTable()
	for _, b := range PCSBindings() {
		row := pcsRowByID(t, table, b.NativeID)
		for _, key := range []string{"flavor", "bank"} {
			if b.When.Equal[key] == "" || b.When.Equal[key] != row.When.Equal[key] {
				t.Errorf("%s: %s %q, native row %q", b.NativeID, key, b.When.Equal[key], row.When.Equal[key])
			}
		}
	}
	linkScore := map[string]int32{}
	for _, p := range SourcePriorities() {
		linkScore[string(p.Fact)] = max(linkScore[string(p.Fact)], p.Priority)
	}
	byFact := map[semreg.DefinitionID]map[string]map[int32]string{}
	for _, p := range PCSSourcePriorities() {
		bank := "ordinary"
		if len(p.NativeID) > len("growatt.bms.pcs.parallel.") && p.NativeID[:len("growatt.bms.pcs.parallel.")] == "growatt.bms.pcs.parallel." {
			bank = "parallel"
		}
		if byFact[p.Fact] == nil {
			byFact[p.Fact] = map[string]map[int32]string{}
		}
		if byFact[p.Fact][bank] == nil {
			byFact[p.Fact][bank] = map[int32]string{}
		}
		if other, dup := byFact[p.Fact][bank][p.Priority]; dup {
			t.Errorf("%s and %s share priority %d on %s", p.NativeID, other, p.Priority, p.Fact)
		}
		byFact[p.Fact][bank][p.Priority] = p.NativeID
		if top, ok := linkScore[string(p.Fact)]; ok && p.Priority >= top {
			t.Errorf("%s: priority %d does not rank below the LINK module source %d", p.NativeID, p.Priority, top)
		}
	}
	got := PCSSourcePriorities()
	sort.Slice(got, func(i, j int) bool { return got[i].NativeID < got[j].NativeID })
	if len(got) != len(PCSBindings()) {
		t.Errorf("priorities %d, bindings %d", len(got), len(PCSBindings()))
	}
}
