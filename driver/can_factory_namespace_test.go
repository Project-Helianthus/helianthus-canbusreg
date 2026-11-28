package driver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/runtime/tree"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/bms"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

type heldMember struct {
	spec semdriver.MemberSpec
	read semdriver.NativeReader
	node semdriver.NodeRef
}

type memRegistry struct {
	members []heldMember
	methods []semdriver.MethodSpec
	invoke  []semdriver.NativeMethod
	view    semdriver.ViewSpec
	viewed  bool
}

func (m *memRegistry) Root() semdriver.NodeRef {
	return semdriver.NodeRef{Bridge: "bridge", ID: "node/root"}
}

func (m *memRegistry) Node(spec semdriver.NodeSpec) (semdriver.NodeRef, error) {
	ref := semdriver.NodeRef{Bridge: "bridge", ID: semreg.ResourceID("node/" + string(spec.Local))}
	if err := ref.Validate(); err != nil {
		return semdriver.NodeRef{}, err
	}
	return ref, nil
}

func (m *memRegistry) ReadMember(node semdriver.NodeRef, spec semdriver.MemberSpec, read semdriver.NativeReader) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	m.members = append(m.members, heldMember{spec: spec, read: read, node: node})
	return nil
}

func (m *memRegistry) Method(_ semdriver.NodeRef, spec semdriver.MethodSpec, call semdriver.NativeMethod) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	m.methods = append(m.methods, spec)
	m.invoke = append(m.invoke, call)
	return nil
}

func (m *memRegistry) View(spec semdriver.ViewSpec) error {
	m.view, m.viewed = spec, true
	return nil
}

func (m *memRegistry) find(local string) heldMember {
	for _, member := range m.members {
		if string(member.spec.Local) == local {
			return member
		}
	}
	return heldMember{}
}

func (m *memRegistry) call(local string, arg []byte) ([]byte, error) {
	return m.callContext(context.Background(), local, arg)
}

func (m *memRegistry) callContext(ctx context.Context, local string, arg []byte) ([]byte, error) {
	for i, spec := range m.methods {
		if string(spec.Local) == local {
			return m.invoke[i](ctx, arg)
		}
	}
	return nil, errors.New("method missing")
}

type quietLife struct{}

func (quietLife) State(tree.InstanceRef) (tree.InstanceState, error) {
	return tree.InstanceState{Name: "live", Generation: 1}, nil
}

func (quietLife) OnFence(func(tree.Fence)) func() { return func() {} }

func plannedNamespace(t *testing.T, key, iface, options string) *instance {
	t.Helper()
	f := NewFactory()
	plans, err := f.Plan(config(key, `{"interface":"`+iface+`"}`, options))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan %s: %v %v", key, plans, err)
	}
	inst, err := f.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*instance)
}

func nsEvidence(t *testing.T, name string, index int, seq uint64, mono time.Duration, id uint32, extended bool, data []byte) canbusreg.Evidence {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	var ident canbus.Identifier
	if extended {
		ident, err = canbus.NewExtendedID(id)
	} else {
		ident, err = canbus.NewStandardID(id)
	}
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(ident, data)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.Evidence{Frame: frame, Interface: identity, Sequence: seq, Monotonic: mono}
}

func nsRecord(t *testing.T, name string, index int, seq uint64, mono time.Duration, id uint32, extended bool, rawDlc byte, data []byte) canbusreg.Evidence {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, canbus.SocketCANRecordSize)
	wire := id
	if extended {
		wire |= 0x80000000
	}
	binary.NativeEndian.PutUint32(raw[:4], wire)
	raw[4] = byte(len(data))
	raw[7] = rawDlc
	copy(raw[8:], data)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return canbusreg.Evidence{Frame: frame, Interface: identity, Sequence: seq, Monotonic: mono}
}

func rowBySuffix(t *testing.T, rows []roweval.NativeRow[bms.Selector], suffix string) string {
	t.Helper()
	var id string
	for _, row := range rows {
		if strings.HasSuffix(row.ID, suffix) {
			if id != "" {
				t.Fatalf("two rows end in %s", suffix)
			}
			id = row.ID
		}
	}
	if id == "" {
		t.Fatalf("no row ending %s", suffix)
	}
	return id
}

func quantityText(t *testing.T, v *semreg.Value) string {
	t.Helper()
	if v == nil || v.Kind != semreg.ValueQuantity || v.Quantity == nil {
		t.Fatalf("quantity %+v", v)
	}
	number := v.Quantity.Number
	if number.Exponent10 == 0 {
		return number.Coefficient
	}
	return number.Coefficient + "e" + strconv.FormatInt(int64(number.Exponent10), 10)
}

func readReading(t *testing.T, reg *memRegistry, local string) readingBody {
	t.Helper()
	member := reg.find(local)
	if member.read == nil {
		t.Fatalf("missing member %s", local)
	}
	buf, err := member.read(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", local, err)
	}
	var body readingBody
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("%s: %s: %v", local, buf, err)
	}
	return body
}

func readJSON(t *testing.T, reg *memRegistry, local string, into any) {
	t.Helper()
	member := reg.find(local)
	if member.read == nil {
		t.Fatalf("missing member %s", local)
	}
	buf, err := member.read(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", local, err)
	}
	if err := json.Unmarshal(buf, into); err != nil {
		t.Fatalf("%s: %s: %v", local, buf, err)
	}
}

func commonOptions(scope string) string {
	if scope == "" {
		return `{"profile":"growatt-bms-can-common"}`
	}
	return `{"profile":"growatt-bms-can-common","selection":{"scope":"` + scope + `","flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`
}

func registerPlanned(t *testing.T, key, iface, options string) (*instance, *memRegistry) {
	t.Helper()
	inst := plannedNamespace(t, key, iface, options)
	reg := &memRegistry{}
	if err := inst.registerNamespace(reg); err != nil {
		t.Fatal(err)
	}
	return inst, reg
}

func TestNamespaceRowCarriesValueUnitValidityAndTime(t *testing.T) {
	soc := rowBySuffix(t, bms.V104Table().Native, ".soc")
	left, leftReg := registerPlanned(t, "inst-left", "can0", `{"profile":"growatt-bms-can-v104"}`)
	right, rightReg := registerPlanned(t, "inst-right", "can1", `{"profile":"growatt-bms-can-v104"}`)
	leftAt := 4 * time.Millisecond
	rightAt := 9 * time.Millisecond
	leftFrame := nsEvidence(t, "can0", 1, 4, leftAt, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 50, 0})
	rightFrame := nsEvidence(t, "can1", 2, 9, rightAt, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 40, 0})
	leftEval, leftReason, err := bms.EvaluateFrame(bms.V104Table(), leftFrame.Frame, left.acquired(leftFrame, bms.V104Table().Bindings))
	if err != nil || leftReason != "" || leftEval[soc].Native == nil {
		t.Fatalf("left evaluation %v %q", err, leftReason)
	}
	rightEval, rightReason, err := bms.EvaluateFrame(bms.V104Table(), rightFrame.Frame, right.acquired(rightFrame, bms.V104Table().Bindings))
	if err != nil || rightReason != "" || rightEval[soc].Native == nil {
		t.Fatalf("right evaluation %v %q", err, rightReason)
	}
	left.observeNamespace(leftFrame)
	right.observeNamespace(rightFrame)
	gotLeft := readReading(t, leftReg, soc)
	gotRight := readReading(t, rightReg, soc)
	if gotLeft.Value == nil || *gotLeft.Value != quantityText(t, leftEval[soc].Native.Value) || gotLeft.Unit != leftEval[soc].Native.Unit || gotLeft.Unit != "native.percent" || gotLeft.Validity != "good" || gotLeft.ReceivedAt != strconv.FormatInt(leftAt.Nanoseconds(), 10) {
		t.Fatalf("left %+v", gotLeft)
	}
	if gotRight.Value == nil || *gotRight.Value != quantityText(t, rightEval[soc].Native.Value) || *gotRight.Value == *gotLeft.Value || gotRight.ReceivedAt == gotLeft.ReceivedAt {
		t.Fatalf("instances share a reading: %+v %+v", gotLeft, gotRight)
	}
	if leftReg.find(soc).spec.Scope != "instance" || leftReg.find(soc).spec.Units != "native.percent" || leftReg.find(soc).spec.Mode != "read" {
		t.Fatalf("spec %+v", leftReg.find(soc).spec)
	}
	// A later limits frame must not replace the retained state-of-charge reading or its receive time.
	left.observeNamespace(nsEvidence(t, "can0", 1, 5, 5*time.Millisecond, 0x311, false, []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	kept := readReading(t, leftReg, soc)
	if kept.Value == nil || *kept.Value != *gotLeft.Value || kept.ReceivedAt != gotLeft.ReceivedAt || kept.Validity != "good" {
		t.Fatalf("last good replaced: %+v", kept)
	}
	// A rejected state-of-charge byte is not a raw record and does not replace the retained reading.
	left.observeNamespace(nsEvidence(t, "can0", 1, 6, 6*time.Millisecond, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 101, 0}))
	kept = readReading(t, leftReg, soc)
	var docs []json.RawMessage
	readJSON(t, leftReg, "raw.documents", &docs)
	if kept.Value == nil || *kept.Value != *gotLeft.Value || kept.ReceivedAt != gotLeft.ReceivedAt || len(docs) != 2 {
		t.Fatalf("rejected frame changed state %+v documents %d", kept, len(docs))
	}
}

func TestNamespaceRawFieldsFollowThePage(t *testing.T) {
	// Each page field is asserted from its own evidence input.
	// interface, source: a reader that copies the instance name fails on the second record.
	// sequence, monotonic_nanos, identifier, extended, dlc, raw_dlc, data: a constant or a neighbour copy fails.
	// rtr: an extended record stays false, so copying extended into rtr fails.
	inst, reg := registerPlanned(t, "inst-raw", "can0", commonOptions(""))
	first := nsEvidence(t, "alpha", 1, 3, 4*time.Millisecond, 0x100, false, []byte{1, 2})
	second := nsRecord(t, "beta", 2, 9, 8*time.Millisecond, 0x100001, true, 9, []byte{8, 7, 6, 5, 4, 3, 2, 1})
	inst.observeNamespace(first)
	inst.observeNamespace(second)
	var docs []json.RawMessage
	readJSON(t, reg, "raw.documents", &docs)
	if len(docs) != 2 {
		t.Fatalf("documents %d", len(docs))
	}
	type rawPage struct {
		Interface      string  `json:"interface"`
		Sequence       string  `json:"sequence"`
		MonotonicNanos string  `json:"monotonic_nanos"`
		Identifier     uint32  `json:"identifier"`
		Extended       bool    `json:"extended"`
		Rtr            bool    `json:"rtr"`
		Dlc            uint8   `json:"dlc"`
		RawDlc         uint8   `json:"raw_dlc"`
		Data           []int   `json:"data"`
		Source         *string `json:"source"`
	}
	var pages [2]rawPage
	for i, doc := range docs {
		if err := json.Unmarshal(doc, &pages[i]); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(doc) || strings.Contains(string(doc), `"data":"`) {
			t.Fatalf("data is not a number array: %s", doc)
		}
	}
	if pages[0].Interface != "alpha" || pages[1].Interface != "beta" || pages[0].Source == nil || *pages[0].Source != "alpha" || pages[1].Source == nil || *pages[1].Source != "beta" {
		t.Fatalf("identity %+v %+v", pages[0], pages[1])
	}
	if pages[0].Sequence != "3" || pages[1].Sequence != "9" || pages[0].MonotonicNanos != strconv.FormatInt((4*time.Millisecond).Nanoseconds(), 10) || pages[1].MonotonicNanos != strconv.FormatInt((8*time.Millisecond).Nanoseconds(), 10) {
		t.Fatalf("time %+v %+v", pages[0], pages[1])
	}
	if pages[0].Identifier != 0x100 || pages[1].Identifier != 0x100001 || pages[0].Extended || !pages[1].Extended || pages[0].Rtr || pages[1].Rtr {
		t.Fatalf("identifier %+v %+v", pages[0], pages[1])
	}
	if pages[0].Dlc != 2 || pages[1].Dlc != 8 || pages[0].RawDlc != 2 || pages[1].RawDlc != 9 || !reflect.DeepEqual(pages[0].Data, []int{1, 2}) || !reflect.DeepEqual(pages[1].Data, []int{8, 7, 6, 5, 4, 3, 2, 1}) {
		t.Fatalf("payload %+v %+v", pages[0], pages[1])
	}
	var iface, source []string
	var seq []string
	var ident []uint32
	var extended, remote []bool
	var dlc, rawDlc []uint8
	readJSON(t, reg, "raw.interface", &iface)
	readJSON(t, reg, "raw.sequence", &seq)
	readJSON(t, reg, "raw.identifier", &ident)
	readJSON(t, reg, "raw.extended", &extended)
	readJSON(t, reg, "raw.rtr", &remote)
	readJSON(t, reg, "raw.dlc", &dlc)
	readJSON(t, reg, "raw.raw_dlc", &rawDlc)
	readJSON(t, reg, "raw.source", &source)
	if !reflect.DeepEqual(iface, []string{"alpha", "beta"}) || !reflect.DeepEqual(source, []string{"alpha", "beta"}) || !reflect.DeepEqual(seq, []string{"3", "9"}) || !reflect.DeepEqual(ident, []uint32{0x100, 0x100001}) {
		t.Fatalf("columns %v %v %v %v", iface, source, seq, ident)
	}
	if !reflect.DeepEqual(extended, []bool{false, true}) || !reflect.DeepEqual(remote, []bool{false, false}) || !reflect.DeepEqual(dlc, []uint8{2, 8}) || !reflect.DeepEqual(rawDlc, []uint8{2, 9}) {
		t.Fatalf("flags %v %v %v %v", extended, remote, dlc, rawDlc)
	}
	if len(reg.view.Records) != 2 || reg.view.Root != "raw_evidence" {
		t.Fatalf("view %+v", reg.view)
	}
	wantFields := []string{"interface", "sequence", "monotonic_nanos", "identifier", "extended", "rtr", "dlc", "raw_dlc", "data", "source"}
	var gotFields []string
	for _, field := range reg.view.Records[1].Fields {
		gotFields = append(gotFields, field.Name)
		if field.Member.Namespace != namespaceName || field.Member.Instance != inst.key || field.Member.Node == "native" || string(field.Member.Node) == "" {
			t.Fatalf("field %s key %+v", field.Name, field.Member)
		}
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("field order %v", gotFields)
	}
	charge := rowBySuffix(t, bms.CommonTable(bms.ScopeMember).Native, ".charge_voltage_limit")
	if reg.find(charge).read != nil {
		t.Fatal("unknown scope published a limit row")
	}
	status := readReading(t, reg, "diagnostic.status")
	if status.Value == nil || *status.Value != "raw" {
		t.Fatalf("status %+v", status)
	}
}

func TestNamespaceRevisionRawOmitsUnnamedSource(t *testing.T) {
	inst, reg := registerPlanned(t, "inst-revision", "can0", `{"profile":"growatt-bms-can-v104"}`)
	inst.observeNamespace(nsEvidence(t, "can0", 1, 1, time.Millisecond, 0x311, false, []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	var docs []json.RawMessage
	readJSON(t, reg, "raw.documents", &docs)
	if len(docs) != 1 {
		t.Fatalf("documents %d", len(docs))
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(docs[0], &keys); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"interface", "sequence", "monotonic_nanos", "identifier", "extended", "rtr", "dlc", "raw_dlc", "data"} {
		if _, ok := keys[name]; !ok {
			t.Fatalf("missing %s in %s", name, docs[0])
		}
	}
	if _, ok := keys["source"]; ok {
		t.Fatalf("revision raw named source: %s", docs[0])
	}
	var extended, remote []bool
	readJSON(t, reg, "raw.extended", &extended)
	readJSON(t, reg, "raw.rtr", &remote)
	if !reflect.DeepEqual(extended, []bool{false}) || !reflect.DeepEqual(remote, []bool{false}) {
		t.Fatalf("flags %v %v", extended, remote)
	}
	names := make([]string, 0, len(reg.view.Records[1].Fields))
	for _, field := range reg.view.Records[1].Fields {
		names = append(names, field.Name)
	}
	if !reflect.DeepEqual(names, []string{"interface", "sequence", "monotonic_nanos", "identifier", "extended", "rtr", "dlc", "raw_dlc", "data"}) {
		t.Fatalf("field order %v", names)
	}
}

func TestNamespaceCommonScopeStaysSingular(t *testing.T) {
	soc := rowBySuffix(t, bms.CommonTable(bms.ScopeMember).Native, ".soc")
	for _, scope := range []string{"local", "aggregate"} {
		_, reg := registerPlanned(t, "inst-"+scope, "can0", commonOptions(scope))
		member := reg.find(soc)
		if member.read == nil {
			t.Fatalf("%s did not register %s", scope, soc)
		}
		want := bms.ScopeMember
		if scope == "aggregate" {
			want = bms.ScopeAggregate
		}
		if string(member.spec.Scope) != want {
			t.Fatalf("%s scope %s", scope, member.spec.Scope)
		}
		seen := 0
		for _, other := range reg.members {
			if string(other.spec.Local) == soc {
				seen++
			}
		}
		if seen != 1 {
			t.Fatalf("%s registered %s %d times", scope, soc, seen)
		}
	}
	frame := nsEvidence(t, "can0", 1, 2, 2*time.Millisecond, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 50, 0})
	inst, reg := registerPlanned(t, "inst-member", "can0", commonOptions("local"))
	eval, reason, err := bms.EvaluateCommonFrame(bms.ScopeMember, frame.Frame, inst.acquired(frame, bms.CommonTable(bms.ScopeMember).Bindings))
	if err != nil || reason != "" || eval[soc].Native == nil {
		t.Fatalf("evaluation %v %q", err, reason)
	}
	inst.observeNamespace(frame)
	got := readReading(t, reg, soc)
	if got.Value == nil || *got.Value != quantityText(t, eval[soc].Native.Value) || got.Unit != "native.percent" || got.Validity != "good" {
		t.Fatalf("member %+v", got)
	}
	inst.observeNamespace(nsEvidence(t, "can0", 1, 3, 3*time.Millisecond, 0x100, false, []byte{9}))
	kept := readReading(t, reg, soc)
	if kept.Value == nil || *kept.Value != *got.Value || kept.Validity != "good" {
		t.Fatalf("short frame cleared the limit %+v", kept)
	}
	var docs []json.RawMessage
	readJSON(t, reg, "raw.documents", &docs)
	if len(docs) != 2 {
		t.Fatalf("raw records %d", len(docs))
	}
}

func TestNamespaceProfilesRegisterRows(t *testing.T) {
	stateRow := rowBySuffix(t, bms.V104Table().Native, ".soc")
	laterRow := bms.V108Table().Native[0].ID
	monitor := bms.MonitorTable(0x04010000).Native[0].ID
	_, revisionReg := registerPlanned(t, "inst-revision-rows", "can0", `{"profile":"growatt-bms-can-v104"}`)
	_, laterReg := registerPlanned(t, "inst-later", "can1", `{"profile":"growatt-bms-can-v108"}`)
	_, monitorReg := registerPlanned(t, "inst-monitor", "can2", `{"profile":"growatt-bms-can-monitor"}`)
	if revisionReg.find(stateRow).read == nil || laterReg.find(laterRow).read == nil || monitorReg.find(monitor).read == nil {
		t.Fatal("profile row missing")
	}
	if laterReg.viewed || monitorReg.viewed || !revisionReg.viewed {
		t.Fatal("raw view registered outside the pages that name it")
	}
	if laterReg.find(stateRow).read != nil || monitorReg.find(stateRow).read != nil {
		t.Fatal("another profile's row was registered")
	}
	inst, reg := registerPlanned(t, "inst-monitor-read", "can3", `{"profile":"growatt-bms-can-monitor"}`)
	frame := nsEvidence(t, "can3", 3, 1, time.Millisecond, 0x04010000, true, []byte{0, 10, 0, 20, 0, 30, 0, 40})
	eval, reason, err := bms.EvaluateMonitorFrame(frame.Frame, inst.acquired(frame, bms.MonitorTable(frame.Frame.ID().Value()).Bindings))
	if err != nil || reason != "" || eval[monitor].Native == nil {
		t.Fatalf("monitor %v %q", err, reason)
	}
	inst.observeNamespace(frame)
	got := readReading(t, reg, monitor)
	if got.Value == nil || *got.Value != quantityText(t, eval[monitor].Native.Value) || got.Unit != eval[monitor].Native.Unit || got.Validity != "good" || got.ReceivedAt != strconv.FormatInt(time.Millisecond.Nanoseconds(), 10) {
		t.Fatalf("monitor member %+v native %+v", got, eval[monitor].Native)
	}
}

func TestNamespaceProjectKeyVaries(t *testing.T) {
	// provider, bank, slot, module and field each select a different member.
	// Copying the first record into every key, or treating an absent slot as 0, returns the wrong value.
	inst, reg := registerPlanned(t, "inst-project", "can0", `{"profile":"growatt-bms-can-v104"}`)
	notes := []projectNote{
		{Provider: "pcs", Bank: "ordinary", Field: "soc", Value: "1", Unit: "native.percent", Validity: "good", Scope: "member", Received: time.Millisecond},
		{Provider: "link", Bank: "ordinary", Field: "soc", Value: "2", Validity: "good", Scope: "member"},
		{Provider: "pcs", Bank: "parallel", Field: "soc", Value: "3", Validity: "good", Scope: "aggregate"},
		{Provider: "pcs", Bank: "ordinary", Slot: "0", Field: "soc", Value: "4", Validity: "good", Scope: "member"},
		{Provider: "pcs", Bank: "ordinary", Slot: "1", Field: "soc", Value: "5", Validity: "good", Scope: "member"},
		{Provider: "pcs", Bank: "ordinary", Module: "01", Field: "soc", Value: "6", Validity: "good", Scope: "member"},
		{Provider: "pcs", Bank: "ordinary", Field: "soh", Value: "7", Validity: "invalid", Scope: "member"},
	}
	for _, note := range notes {
		if err := inst.noteProjectRecord(note); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"record.pcs.ordinary.absent.absent.soc":  "1",
		"record.link.ordinary.absent.absent.soc": "2",
		"record.pcs.parallel.absent.absent.soc":  "3",
		"record.pcs.ordinary.0.absent.soc":       "4",
		"record.pcs.ordinary.1.absent.soc":       "5",
		"record.pcs.ordinary.absent.01.soc":      "6",
		"record.pcs.ordinary.absent.absent.soh":  "7",
	}
	if len(want) != len(notes) {
		t.Fatalf("key collision in the fixture: %d", len(want))
	}
	for local, value := range want {
		var body projectBody
		readJSON(t, reg, local, &body)
		if body.Value == nil || *body.Value != value || body.Provider == "" || body.Bank == "" || body.Slot == "" || body.Module == "" || body.Field == "" {
			t.Fatalf("%s %+v", local, body)
		}
		if local == "record.pcs.ordinary.absent.absent.soc" && (body.Slot != absentToken || body.Module != absentToken || body.Scope != "member" || body.Unit != "native.percent" || body.ReceivedAt == "") {
			t.Fatalf("absent identity %+v", body)
		}
		if local == "record.pcs.ordinary.0.absent.soc" && body.Slot != "0" {
			t.Fatalf("slot zero %+v", body)
		}
		if local == "record.pcs.parallel.absent.absent.soc" && body.Scope != "aggregate" {
			t.Fatalf("scope %+v", body)
		}
		if local == "record.pcs.ordinary.absent.absent.soh" && body.Validity != "invalid" {
			t.Fatalf("validity %+v", body)
		}
	}
	if reg.find("record.pcs.ordinary.absent.absent.soc").spec.Scope != "member" {
		t.Fatal("member scope was not kept")
	}
	if !reg.hasMethod(pcs.Commands()[0].ID) || !reg.hasMethod(link.Commands()[0].ID) {
		t.Fatal("project command row missing")
	}
}

func (m *memRegistry) hasMethod(local string) bool {
	for _, spec := range m.methods {
		if string(spec.Local) == local {
			return true
		}
	}
	return false
}

func TestNamespaceTypedPointMarkers(t *testing.T) {
	labelRow, ok := canbusreg.LookupGreeVRFDictionary(0x3d, 0x06, canbusreg.GreeVRFDictionaryBit)
	if !ok || !labelRow.LabelOnly || labelRow.Conflict != "" {
		t.Fatalf("label row %+v %v", labelRow, ok)
	}
	conflictRow, ok := canbusreg.LookupGreeVRFDictionary(0x20, 0x5d, canbusreg.GreeVRFDictionaryByte)
	if !ok || conflictRow.Conflict == "" || conflictRow.LabelOnly {
		t.Fatalf("conflict row %+v %v", conflictRow, ok)
	}
	inst, reg := registerPlanned(t, "inst-point", "can0", `{"profile":"growatt-bms-can-v104"}`)
	indoor := typedPoint(canbusreg.GreeVRFIndoorUnit, 0x20, labelRow, 1, time.Millisecond, nil)
	outdoor := typedPoint(canbusreg.GreeVRFOutdoorUnit, 0x08, labelRow, 1, 2*time.Millisecond, nil)
	otherUnit := typedPoint(canbusreg.GreeVRFIndoorUnit, 0x21, labelRow, 1, 3*time.Millisecond, nil)
	conflict := typedPoint(canbusreg.GreeVRFIndoorUnit, 0x20, conflictRow, 7, 4*time.Millisecond, []byte{7})
	for _, note := range []typedPointNote{indoor, outdoor, otherUnit, conflict} {
		if err := inst.noteTypedPoint(note); err != nil {
			t.Fatal(err)
		}
	}
	var indoorBody, outdoorBody, unitBody, conflictBody pointBody
	readJSON(t, reg, pointLocal(indoor), &indoorBody)
	readJSON(t, reg, pointLocal(outdoor), &outdoorBody)
	readJSON(t, reg, pointLocal(otherUnit), &unitBody)
	readJSON(t, reg, pointLocal(conflict), &conflictBody)
	if !indoorBody.LabelOnly || indoorBody.Conflict != "" || indoorBody.Validity != "label_only" || indoorBody.DeviceClass != "indoor" || indoorBody.Enumeration != "" {
		t.Fatalf("indoor %+v", indoorBody)
	}
	if outdoorBody.DeviceClass != "outdoor" || !outdoorBody.LabelOnly || unitBody.DeviceClass != "indoor" {
		t.Fatalf("class or unit collapsed %+v %+v", outdoorBody, unitBody)
	}
	if conflictBody.Validity != "conflict" || conflictBody.Conflict != conflictRow.Conflict || conflictBody.LabelOnly || conflictBody.Value == nil {
		t.Fatalf("conflict %+v", conflictBody)
	}
	if reg.find(pointLocal(indoor)).spec.Schema != "canbusreg.native.point" {
		t.Fatal("point schema")
	}
}

func TestNamespacePresentationStaysMarked(t *testing.T) {
	row, ok := canbusreg.LookupGreeVRFDictionary(0x20, 0x51, canbusreg.GreeVRFDictionaryByte)
	if !ok || row.PresentationConverter != "CeVskPa" {
		t.Fatalf("saturation row %+v %v", row, ok)
	}
	other, ok := canbusreg.LookupGreeVRFDictionary(0x20, 0x52, canbusreg.GreeVRFDictionaryByte)
	if !ok || other.PresentationConverter != "CeVskPa" {
		t.Fatalf("second saturation row %+v %v", other, ok)
	}
	inst, reg := registerPlanned(t, "inst-presentation", "can0", `{"profile":"growatt-bms-can-v104"}`)
	native := canbusreg.GreeVRFValue{Status: canbusreg.GreeVRFValueDecoded, Numerator: -70, Denominator: 1, Raw: []byte{30}}
	tableNote := typedPoint(canbusreg.GreeVRFIndoorUnit, 0x20, row, 0, 5*time.Millisecond, native.Raw)
	tableNote.Point.Value = native
	tableNote.Options = canbusreg.GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R410A"}
	plainNote := typedPoint(canbusreg.GreeVRFIndoorUnit, 0x21, other, 0, 6*time.Millisecond, native.Raw)
	plainNote.Point.Value = native
	unknownNote := typedPoint(canbusreg.GreeVRFOutdoorUnit, 0x08, row, 0, 7*time.Millisecond, native.Raw)
	unknownNote.Point.Value = native
	unknownNote.Options = canbusreg.GreeVRFPresentationOptions{Pressure: true, Refrigerant: "unknown"}
	for _, note := range []typedPointNote{tableNote, plainNote, unknownNote} {
		if err := inst.noteTypedPoint(note); err != nil {
			t.Fatal(err)
		}
	}
	if native.Numerator != -70 || native.Denominator != 1 {
		t.Fatal("notation mutated the caller value")
	}
	var nativeBody pointBody
	var marked, plain, unknown markedBody
	readJSON(t, reg, pointLocal(tableNote), &nativeBody)
	readJSON(t, reg, "presentation."+pointLocal(tableNote), &marked)
	readJSON(t, reg, "presentation."+pointLocal(plainNote), &plain)
	readJSON(t, reg, "presentation."+pointLocal(unknownNote), &unknown)
	if nativeBody.Value == nil || *nativeBody.Value != "-70" || nativeBody.Unit != row.Unit || nativeBody.Unit == "kPa" || strings.Contains(mustJSON(t, nativeBody), `"marked"`) {
		t.Fatalf("native point %+v", nativeBody)
	}
	if marked.Marked != "presentation" || marked.Value == nil || *marked.Value != "36" || marked.Unit != "kPa" || marked.Validity != "good" {
		t.Fatalf("table result %+v", marked)
	}
	if plain.Marked != "presentation" || plain.Unit == "kPa" || plain.Unit == nativeBody.Unit || plain.Validity != "good" {
		t.Fatalf("display result %+v", plain)
	}
	if unknown.Marked != "presentation" || unknown.Value != nil || unknown.Validity != "unknown" || unknown.Unit == "kPa" {
		t.Fatalf("unknown refrigerant %+v", unknown)
	}
	if reg.find("presentation."+pointLocal(tableNote)).spec.Schema != "canbusreg.presentation.result" {
		t.Fatal("presentation schema")
	}
}

func TestNamespaceOpaqueCellPerUnit(t *testing.T) {
	// class, unit and cell each select a different member. A shared cell index alone collides.
	inst, reg := registerPlanned(t, "inst-cell", "can0", `{"profile":"growatt-bms-can-v104"}`)
	if err := inst.noteOpaqueCells(opaqueNote{Class: canbusreg.GreeVRFIndoorUnit, Unit: 0x20, Received: time.Millisecond, Cells: []canbusreg.GreeVRFCandidateStateCell{{Cell: 1, Value: 3}, {Cell: 2, Value: 4}}}); err != nil {
		t.Fatal(err)
	}
	if err := inst.noteOpaqueCells(opaqueNote{Class: canbusreg.GreeVRFIndoorUnit, Unit: 0x21, Received: 2 * time.Millisecond, Cells: []canbusreg.GreeVRFCandidateStateCell{{Cell: 1, Value: 8}}}); err != nil {
		t.Fatal(err)
	}
	if err := inst.noteOpaqueCells(opaqueNote{Class: canbusreg.GreeVRFOutdoorUnit, Unit: 0x08, Received: 3 * time.Millisecond, Cells: []canbusreg.GreeVRFCandidateStateCell{{Cell: 1, Value: 5}}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"cell.indoor.20.01":  "3",
		"cell.indoor.20.02":  "4",
		"cell.indoor.21.01":  "8",
		"cell.outdoor.08.01": "5",
	}
	for local, value := range want {
		var body cellBody
		readJSON(t, reg, local, &body)
		if body.Value != value || body.Validity != "good" || body.DeviceClass == "" || body.Unit == "" || body.Cell == "" {
			t.Fatalf("%s %+v", local, body)
		}
	}
}

func TestNamespaceCommandSendsNothing(t *testing.T) {
	inst, reg := registerPlanned(t, "inst-command", "can0", `{"profile":"growatt-bms-can-v104"}`)
	heartbeat := growattlowvoltage.V104InverterHeartbeatRowID
	if !reg.hasMethod(heartbeat) {
		t.Fatal("heartbeat method missing")
	}
	calls := 0
	namespaceInvoke = func(key semdriver.InstanceKey, name string, arg []byte) ([]byte, error) {
		calls++
		if key != inst.key || name != heartbeat {
			t.Fatalf("invoke %s %s", key, name)
		}
		return []byte(`{"accepted":true}`), nil
	}
	t.Cleanup(func() { namespaceInvoke = nil })
	_ = readReading(t, reg, "diagnostic.received")
	if calls != 0 {
		t.Fatal("a snapshot read invoked a command")
	}
	buf, err := reg.call(heartbeat, []byte(`{}`))
	if err != nil || calls != 1 || string(buf) != `{"accepted":true}` {
		t.Fatalf("seam %s %v calls %d", buf, err, calls)
	}
	namespaceInvoke = nil
	if _, err := reg.call(heartbeat, nil); !errors.Is(err, errNamespaceUnbound) {
		t.Fatalf("unbound seam %v", err)
	}
	_, monitorReg := registerPlanned(t, "inst-no-command", "can1", `{"profile":"growatt-bms-can-monitor"}`)
	if monitorReg.hasMethod(heartbeat) || monitorReg.hasMethod(growattlowvoltage.ClockEventRowID) {
		t.Fatal("monitor registered a command")
	}
	_, commandReg := registerPlanned(t, "inst-commands", "can2", `{"profile":"growatt-bms-can-v108"}`)
	for _, id := range []string{growattlowvoltage.ClockEventRowID, growattlowvoltage.SerialQueryRowID, growattlowvoltage.HistoryQueryRowID} {
		if !commandReg.hasMethod(id) {
			t.Fatalf("missing %s", id)
		}
	}
	if commandReg.hasMethod(heartbeat) {
		t.Fatal("revision command registered on another profile")
	}
	before := len(reg.methods)
	row, ok := canbusreg.LookupGreeVRFDictionary(0x3d, 0x06, canbusreg.GreeVRFDictionaryBit)
	if !ok {
		t.Fatal("label row")
	}
	if err := inst.noteTypedPoint(typedPoint(canbusreg.GreeVRFIndoorUnit, 0x20, row, 0, time.Millisecond, nil)); err != nil {
		t.Fatal(err)
	}
	if len(reg.methods) <= before || !reg.hasMethod(gree.CommandRows()[0].Name) {
		t.Fatal("gree command rows were not registered")
	}
}

func TestNamespaceOrderStoppedAndStale(t *testing.T) {
	inst, reg := registerPlanned(t, "inst-order", "can0", `{"profile":"growatt-bms-can-v104"}`)
	var ids []string
	for _, member := range reg.members {
		if member.spec.Schema == "canbusreg.native.row" {
			ids = append(ids, string(member.spec.Local))
		}
	}
	sortedIDs := append([]string(nil), ids...)
	sort.Strings(sortedIDs)
	if len(ids) == 0 || !reflect.DeepEqual(ids, sortedIDs) {
		t.Fatal("row members are not in stable order")
	}
	recv := newFakeReceiver(t, "can2", 2)
	running := startedInstance(t, factoryWith(recv, nil), config("inst-stop", `{"interface":"can2"}`, `{"profile":"growatt-bms-can-v104"}`))
	stopReg := &memRegistry{}
	if err := running.registerNamespace(stopReg); err != nil {
		t.Fatal(err)
	}
	frame := nsEvidence(t, "can2", 2, 1, time.Millisecond, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 50, 0})
	running.observeNamespace(frame)
	soc := rowBySuffix(t, bms.V104Table().Native, ".soc")
	live := readReading(t, stopReg, soc)
	recv.push(2, 0x313, false, 0, 0, 0, 0, 0, 0, 40, 0)
	waitFor(t, "receive", func() bool {
		got, _, _ := running.counts()
		return got == 1
	})
	counted := readReading(t, stopReg, "diagnostic.received")
	if counted.Value == nil || *counted.Value != "1" || counted.Validity != "good" {
		t.Fatalf("counter %+v", counted)
	}
	calls := 0
	namespaceInvoke = func(semdriver.InstanceKey, string, []byte) ([]byte, error) {
		calls++
		return nil, nil
	}
	t.Cleanup(func() { namespaceInvoke = nil })
	if report := running.Stop(context.Background()); !report.Confirmed && report.Err != nil {
		t.Fatal(report.Err)
	}
	if _, err := stopReg.find(soc).read(context.Background()); !errors.Is(err, errNamespaceStopped) {
		t.Fatalf("stopped read %v", err)
	}
	if _, err := stopReg.call(growattlowvoltage.V104InverterHeartbeatRowID, nil); !errors.Is(err, errNamespaceStopped) || calls != 0 {
		t.Fatalf("stopped command calls %d", calls)
	}
	if live.Validity != "good" || live.Value == nil {
		t.Fatalf("live %+v", live)
	}
	inst.observeNamespace(nsEvidence(t, "can0", 1, 1, 2*time.Millisecond, 0x313, false, []byte{0, 0, 0, 0, 0, 0, 50, 0}))
	inst.FenceWithdrawal()
	stale := readReading(t, reg, soc)
	if stale.Validity != "stale" || stale.Value == nil || *stale.Value != *live.Value {
		t.Fatalf("fenced %+v live %+v", stale, live)
	}
}

func TestNamespaceViewBindsRegisteredMembers(t *testing.T) {
	reg, err := tree.NewRegistry(tree.RegistryConfig{Installation: "synthetic", Bridge: "bridge", Incarnation: "one", Lifecycle: quietLife{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Close)
	inst := plannedNamespace(t, "inst-view", "can0", `{"profile":"growatt-bms-can-v104"}`)
	if err := reg.Reserve(semdriver.Namespace{Owner: Kind, Key: namespaceName}, []semdriver.InstanceKey{inst.key}); err != nil {
		t.Fatal(err)
	}
	bound, err := reg.Bind(namespaceName, inst.key, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.registerNamespace(bound); err != nil {
		t.Fatal(err)
	}
}

func TestRunningProfileWithoutRegistryDropsRaw(t *testing.T) {
	recv := newFakeReceiver(t, "can0", 1)
	inst := startedInstance(t, factoryWith(recv, nil), config("v104-raw", `{"interface":"can0"}`, `{"profile":"growatt-bms-can-v104"}`))
	for n := uint64(1); n <= 4; n++ {
		recv.push(n, 0x313, false, 0, 0, 0, 0, 0, 0, 50, 0)
	}
	waitFor(t, "frames", func() bool {
		got, _, _ := inst.counts()
		return got == 4
	})
	v, ok := namespaceBooks.Load(inst)
	if !ok {
		return
	}
	b := v.(*nsBook)
	b.mu.Lock()
	n := len(b.raw)
	b.mu.Unlock()
	if n != 0 {
		t.Fatalf("raw documents %d", n)
	}
}

func typedPoint(class canbusreg.GreeVRFDeviceClass, unit uint8, row canbusreg.GreeVRFDictionaryRow, raw byte, at time.Duration, data []byte) typedPointNote {
	if data == nil && raw != 0 {
		data = []byte{raw}
	}
	return typedPointNote{
		Class: class,
		Point: canbusreg.GreeVRFTypedPoint{
			Key: canbusreg.GreeVRFRegisterKey{Unit: unit, Type: row.Type}, Row: row, Present: true, Received: at,
			Value: canbusreg.GreeVRFValue{Status: canbusreg.GreeVRFValueDecoded, Raw: data, Numerator: int64(raw), Denominator: 1},
		},
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	buf, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}
