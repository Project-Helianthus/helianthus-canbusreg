package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/bms"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const namespaceName semdriver.NamespaceID = "canbusreg"
const absentToken = "absent"

var (
	errNamespaceStopped = errors.New("namespace member unavailable")
	errNamespaceMissing = errors.New("namespace member is not registered")
	errNamespaceUnbound = errors.New("command sends nothing")
	namespaceInvoke     func(semdriver.InstanceKey, string, []byte) ([]byte, error)
	namespaceBooks      sync.Map
)
var monitorBases = []uint32{0x04010000, 0x04020000, 0x04030000, 0x04040000, 0x04050000, 0x04060000, 0x04070000, 0x040a0000, 0x040b0000, 0x040c0000, 0x040d0000, 0x04100000, 0x04110000, 0x04120000, 0x04130000, 0x04140000, 0x04150000, 0x04160000, 0x04170000, 0x08000000, 0x08010000, 0x08020000, 0x08030000, 0x10000000, 0x10010000, 0x14000000}

type nsBook struct {
	mu          sync.Mutex
	inst        *instance
	reg         semdriver.NativeRegistry
	transport   semdriver.NodeRef
	device      semdriver.NodeRef
	rows        map[string]nsRow
	raw         []nsRaw
	status      string
	projects    map[string]projectBody
	points      map[string]pointBody
	marked      map[string]markedBody
	cells       map[string]cellBody
	bound       map[string]bool
	methods     map[string]bool
	greeMethods bool
}
type nsRow struct {
	value                    *string
	unit, validity, received string
	seen                     bool
}
type readingBody struct {
	Value      *string `json:"value,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Validity   string  `json:"validity"`
	ReceivedAt string  `json:"received_at,omitempty"`
}
type nsRaw struct {
	Interface  string
	Sequence   string
	Monotonic  string
	Identifier uint32
	Extended   bool
	DLC        uint8
	RawDLC     uint8
	Data       []int
	Source     string
	common     bool
}
type projectNote struct {
	Provider, Bank, Slot, Module, Field string
	Value, Unit, Validity, Scope        string
	Received                            time.Duration
}
type projectBody struct {
	Provider   string  `json:"provider"`
	Bank       string  `json:"bank"`
	Slot       string  `json:"slot"`
	Module     string  `json:"module"`
	Field      string  `json:"field"`
	Value      *string `json:"value,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Validity   string  `json:"validity"`
	ReceivedAt string  `json:"received_at,omitempty"`
	Scope      string  `json:"scope"`
}
type typedPointNote struct {
	Class   canbusreg.GreeVRFDeviceClass
	Point   canbusreg.GreeVRFTypedPoint
	Options canbusreg.GreeVRFPresentationOptions
}
type pointBody struct {
	DeviceClass string  `json:"device_class"`
	Raw         []int   `json:"raw"`
	Value       *string `json:"value,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	Enumeration string  `json:"enumeration"`
	ReceivedAt  string  `json:"received_at,omitempty"`
	LabelOnly   bool    `json:"label_only"`
	Conflict    string  `json:"conflict,omitempty"`
	Validity    string  `json:"validity"`
}
type markedBody struct {
	Marked     string  `json:"marked"`
	Value      *string `json:"value,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Validity   string  `json:"validity"`
	ReceivedAt string  `json:"received_at,omitempty"`
}
type opaqueNote struct {
	Class    canbusreg.GreeVRFDeviceClass
	Unit     uint8
	Received time.Duration
	Cells    []canbusreg.GreeVRFCandidateStateCell
}
type cellBody struct {
	DeviceClass string `json:"device_class"`
	Unit        string `json:"unit"`
	Cell        string `json:"cell"`
	Value       string `json:"value"`
	Validity    string `json:"validity"`
	ReceivedAt  string `json:"received_at,omitempty"`
}
type staleReader interface{ staleCopy(bool) any }

func bookFor(i *instance) *nsBook {
	if v, ok := namespaceBooks.Load(i); ok {
		return v.(*nsBook)
	}
	b := &nsBook{
		inst: i, rows: map[string]nsRow{}, projects: map[string]projectBody{},
		points: map[string]pointBody{}, marked: map[string]markedBody{}, cells: map[string]cellBody{},
		bound: map[string]bool{}, methods: map[string]bool{},
	}
	actual, _ := namespaceBooks.LoadOrStore(i, b)
	return actual.(*nsBook)
}
func (i *instance) registerNamespace(reg semdriver.NativeRegistry) error {
	if reg == nil {
		return errors.New("namespace registry is required")
	}
	return bookFor(i).install(reg)
}
func (i *instance) namespaceState() (closed bool, received, skipped uint64, stale bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.started && (i.recv == nil || i.cancel == nil), i.received, i.skipped, i.fenced
}
func (b *nsBook) install(reg semdriver.NativeRegistry) error {
	b.mu.Lock()
	if b.reg != nil {
		b.mu.Unlock()
		return errors.New("namespace already registered")
	}
	b.mu.Unlock()
	local := b.inst.config.Interface
	if local == "" {
		local = "can"
	}
	transport, err := reg.Node(semdriver.NodeSpec{
		Kind: "resource.interface", Local: semdriver.NodeID(local), Contract: "canbusreg.interface",
	})
	if err != nil {
		return err
	}
	device, err := reg.Node(semdriver.NodeSpec{
		Parent: &transport, Kind: "resource.equipment", Local: "native", Contract: "canbusreg.equipment",
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.reg, b.transport, b.device = reg, transport, device
	b.mu.Unlock()
	if err := b.bindDiagnostics(device); err != nil {
		return err
	}
	if err := b.bindInstanceRows(device); err != nil {
		return err
	}
	if err := b.bindRaw(device); err != nil {
		return err
	}
	return b.bindStored(device)
}
func (i *instance) observeNamespace(e canbusreg.Evidence) {
	v, ok := namespaceBooks.Load(i)
	if !ok {
		return
	}
	b := v.(*nsBook)
	b.mu.Lock()
	registered := b.reg != nil
	b.mu.Unlock()
	if !registered {
		return
	}
	ev, reason, err := i.profileEvaluation(e)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.status = "rejected"
		return
	}
	decoded := reason == "" && len(ev) > 0
	keep, status := rawAdmission(i.config.Options.Profile, e, decoded)
	if keep {
		b.raw = append(b.raw, rawDocument(e, i.config.Options.Profile == ProfileCommon))
	}
	if decoded {
		for id, one := range ev {
			if one.Native == nil {
				continue
			}
			b.rows[id] = rowFrom(one.Native, e.Monotonic)
		}
	}
	b.status = status
}
func (i *instance) noteProjectRecord(note projectNote) error {
	if note.Provider == "" || note.Bank == "" || note.Field == "" {
		return errors.New("project record requires provider, bank and field")
	}
	body := projectBody{
		Provider: note.Provider, Bank: note.Bank, Field: note.Field, Unit: note.Unit,
		Slot: nonempty(note.Slot, absentToken), Module: nonempty(note.Module, absentToken),
		Scope: nonempty(note.Scope, "unknown"), Validity: nonempty(note.Validity, "unknown"),
	}
	if note.Value != "" {
		text := note.Value
		body.Value = &text
	}
	if note.Received != 0 {
		body.ReceivedAt = strconv.FormatInt(note.Received.Nanoseconds(), 10)
	}
	local := strings.Join([]string{"record", body.Provider, body.Bank, body.Slot, body.Module, body.Field}, ".")
	b := bookFor(i)
	b.mu.Lock()
	b.projects[local] = body
	node := b.device
	registered := b.reg != nil
	b.mu.Unlock()
	if !registered {
		return nil
	}
	if err := b.bind(node, readingSpec(local, "canbusreg.project.field", body.Unit, body.Scope)); err != nil {
		return err
	}
	return b.bindMethodList(providerCommandIDs(note.Provider))
}
func (i *instance) noteTypedPoint(note typedPointNote) error {
	body := pointFrom(note)
	local := pointLocal(note)
	b := bookFor(i)
	b.mu.Lock()
	b.points[local] = body
	var marked *markedBody
	markedLocal := "presentation." + local
	if note.Point.Row.PresentationConverter != "" {
		m := presentationFrom(note)
		marked = &m
		b.marked[markedLocal] = m
	}
	node := b.device
	registered := b.reg != nil
	b.mu.Unlock()
	if !registered {
		return nil
	}
	if err := b.bind(node, readingSpec(local, "canbusreg.native.point", "", "instance")); err != nil {
		return err
	}
	if marked != nil {
		if err := b.bind(node, readingSpec(markedLocal, "canbusreg.presentation.result", "", "instance")); err != nil {
			return err
		}
	}
	b.mu.Lock()
	first := !b.greeMethods
	b.greeMethods = true
	b.mu.Unlock()
	if !first {
		return nil
	}
	return b.bindMethodList(greeCommandIDs())
}
func (i *instance) noteOpaqueCells(note opaqueNote) error {
	type item struct {
		local string
		body  cellBody
	}
	items := make([]item, 0, len(note.Cells))
	received := ""
	if note.Received != 0 {
		received = strconv.FormatInt(note.Received.Nanoseconds(), 10)
	}
	class := classLabel(note.Class)
	unit := fmt.Sprintf("%02x", note.Unit)
	for _, cell := range note.Cells {
		body := cellBody{
			DeviceClass: class, Unit: unit, Cell: fmt.Sprintf("%02x", cell.Cell),
			Value: strconv.FormatUint(uint64(cell.Value), 10), Validity: "good", ReceivedAt: received,
		}
		items = append(items, item{local: "cell." + class + "." + unit + "." + body.Cell, body: body})
	}
	b := bookFor(i)
	b.mu.Lock()
	for _, it := range items {
		b.cells[it.local] = it.body
	}
	node := b.device
	registered := b.reg != nil
	b.mu.Unlock()
	if !registered {
		return nil
	}
	for _, it := range items {
		if err := b.bind(node, readingSpec(it.local, "canbusreg.opaque.cell", "", "instance")); err != nil {
			return err
		}
	}
	return nil
}
func profileRows(i *instance) ([]roweval.NativeRow[bms.Selector], string) {
	switch i.config.Options.Profile {
	case ProfileV104:
		return bms.V104Table().Native, "instance"
	case ProfileV108:
		return bms.V108Table().Native, "instance"
	case ProfileCommon:
		scope := i.commonScope()
		if scope == "" {
			return nil, ""
		}
		return bms.CommonTable(scope).Native, scope
	case ProfileMonitor:
		return monitorRows(), "instance"
	default:
		return nil, "instance"
	}
}
func monitorRows() []roweval.NativeRow[bms.Selector] {
	var rows []roweval.NativeRow[bms.Selector]
	seen := map[string]bool{}
	for _, base := range monitorBases {
		for _, row := range bms.MonitorTable(base).Native {
			if row.ID == "" || seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			rows = append(rows, row)
		}
	}
	return rows
}
func (i *instance) profileEvaluation(e canbusreg.Evidence) (map[string]roweval.Evaluation, roweval.Reason, error) {
	frame := e.Frame
	switch i.config.Options.Profile {
	case ProfileV104:
		table := bms.V104Table()
		return bms.EvaluateFrame(table, frame, i.acquired(e, table.Bindings))
	case ProfileV108:
		table := bms.V108Table()
		return bms.EvaluateV108Frame(frame, i.acquired(e, table.Bindings))
	case ProfileCommon:
		scope := i.commonScope()
		table := bms.CommonTable(scope)
		return bms.EvaluateCommonFrame(scope, frame, i.acquired(e, table.Bindings))
	case ProfileMonitor:
		table := bms.MonitorTable(frame.ID().Value())
		return bms.EvaluateMonitorFrame(frame, i.acquired(e, table.Bindings))
	default:
		return i.projectProfileEvaluation(e)
	}
}
func rawAdmission(profile Profile, e canbusreg.Evidence, decoded bool) (bool, string) {
	switch profile {
	case ProfileV104:
		if bms.FrameDisposition(e.Frame) != "" {
			return false, "rejected"
		}
		return true, "decoded"
	case ProfileCommon:
		if !decoded {
			return true, "raw"
		}
		return true, "decoded"
	default:
		if decoded {
			return false, "decoded"
		}
		return false, "rejected"
	}
}
func rawDocument(e canbusreg.Evidence, common bool) nsRaw {
	data := e.Frame.Data()
	nums := make([]int, len(data))
	for i, b := range data {
		nums[i] = int(b)
	}
	doc := nsRaw{
		Interface: e.Interface.Name(), Sequence: strconv.FormatUint(e.Sequence, 10),
		Monotonic: strconv.FormatInt(e.Monotonic.Nanoseconds(), 10), Identifier: e.Frame.ID().Value(),
		Extended: e.Frame.ID().Extended(), DLC: e.Frame.DLC(), RawDLC: e.Frame.RawDLC(), Data: nums, common: common,
	}
	if common {
		doc.Source = e.Interface.Name()
	}
	return doc
}
func rowFrom(n *roweval.Native, at time.Duration) nsRow {
	text, ok := nativeText(n.Value)
	validity := "good"
	if n.Reason != "" {
		validity = "invalid"
	} else if !ok {
		validity = "absent"
	}
	row := nsRow{unit: n.Unit, validity: validity, received: strconv.FormatInt(at.Nanoseconds(), 10), seen: true}
	if ok {
		row.value = &text
	}
	return row
}
func nativeText(v *semreg.Value) (string, bool) {
	if v == nil {
		return "", false
	}
	switch v.Kind {
	case semreg.ValueQuantity:
		if v.Quantity == nil {
			return "", false
		}
		d := v.Quantity.Number
		if d.Exponent10 == 0 {
			return d.Coefficient, true
		}
		return d.Coefficient + "e" + strconv.FormatInt(int64(d.Exponent10), 10), true
	case semreg.ValueText:
		if v.Text == nil {
			return "", false
		}
		return *v.Text, true
	case semreg.ValueBoolean:
		if v.Boolean == nil {
			return "", false
		}
		return strconv.FormatBool(*v.Boolean), true
	case semreg.ValueSymbol:
		if v.Symbol == nil {
			return "", false
		}
		return v.Symbol.Token, true
	default:
		return "", false
	}
}
func commandIDs(profile Profile) []string {
	switch profile {
	case ProfileV104:
		return []string{growattlowvoltage.V104InverterHeartbeatRowID}
	case ProfileV108:
		return []string{growattlowvoltage.ClockEventRowID, growattlowvoltage.SerialQueryRowID, growattlowvoltage.HistoryQueryRowID}
	default:
		return projectCommandIDs(profile)
	}
}
func providerCommandIDs(provider string) []string {
	var ids []string
	switch provider {
	case "pcs":
		for _, c := range pcs.Commands() {
			ids = append(ids, c.ID)
		}
		for _, c := range pcs.ExtendedCommands() {
			ids = append(ids, c.ID)
		}
	case "link":
		for _, c := range link.Commands() {
			ids = append(ids, c.ID)
		}
	}
	return ids
}
func greeCommandIDs() []string {
	rows := gree.CommandRows()
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Name != "" {
			ids = append(ids, row.Name)
		}
	}
	return ids
}
func (b *nsBook) bindDiagnostics(node semdriver.NodeRef) error {
	for _, local := range []string{"diagnostic.received", "diagnostic.skipped", "diagnostic.status"} {
		unit := ""
		if local != "diagnostic.status" {
			unit = "native.count"
		}
		if err := b.bind(node, readingSpec(local, "canbusreg.diagnostic", unit, "instance")); err != nil {
			return err
		}
	}
	return nil
}
func (b *nsBook) bindRows(node semdriver.NodeRef) error {
	rows, scope := profileRows(b.inst)
	ids := make([]string, 0, len(rows))
	units := map[string]string{}
	for _, row := range rows {
		if row.ID == "" {
			continue
		}
		if _, ok := units[row.ID]; ok {
			continue
		}
		units[row.ID] = row.Unit
		ids = append(ids, row.ID)
	}
	sort.Strings(ids)
	b.mu.Lock()
	for _, id := range ids {
		if _, ok := b.rows[id]; !ok {
			b.rows[id] = nsRow{unit: units[id], validity: "absent"}
		}
	}
	b.mu.Unlock()
	if scope == "" {
		scope = "instance"
	}
	for _, id := range ids {
		if err := b.bind(node, readingSpec(id, "canbusreg.native.row", units[id], scope)); err != nil {
			return err
		}
	}
	return nil
}
func (b *nsBook) bindRaw(node semdriver.NodeRef) error {
	fields := rawFieldNames(b.inst.config.Options.Profile)
	if len(fields) == 0 {
		return nil
	}
	for _, field := range fields {
		if err := b.bind(node, readingSpec("raw."+field, "canbusreg.raw.field", "", "instance")); err != nil {
			return err
		}
	}
	if err := b.bind(node, readingSpec("raw.documents", "canbusreg.raw.field", "", "instance")); err != nil {
		return err
	}
	return b.bindView(node, fields)
}
func (b *nsBook) bindStored(node semdriver.NodeRef) error {
	b.mu.Lock()
	projects := mapKeys(b.projects)
	points := mapKeys(b.points)
	marked := mapKeys(b.marked)
	cells := mapKeys(b.cells)
	providers := map[string]struct{}{}
	for _, rec := range b.projects {
		providers[rec.Provider] = struct{}{}
	}
	greePoints := len(b.points) > 0
	if greePoints {
		b.greeMethods = true
	}
	b.mu.Unlock()
	sort.Strings(projects)
	sort.Strings(points)
	sort.Strings(marked)
	sort.Strings(cells)
	for _, local := range projects {
		b.mu.Lock()
		rec := b.projects[local]
		b.mu.Unlock()
		if err := b.bind(node, readingSpec(local, "canbusreg.project.field", rec.Unit, rec.Scope)); err != nil {
			return err
		}
	}
	for _, local := range points {
		if err := b.bind(node, readingSpec(local, "canbusreg.native.point", "", "instance")); err != nil {
			return err
		}
	}
	for _, local := range marked {
		if err := b.bind(node, readingSpec(local, "canbusreg.presentation.result", "", "instance")); err != nil {
			return err
		}
	}
	for _, local := range cells {
		if err := b.bind(node, readingSpec(local, "canbusreg.opaque.cell", "", "instance")); err != nil {
			return err
		}
	}
	names := commandIDs(b.inst.config.Options.Profile)
	if greePoints {
		names = append(names, greeCommandIDs()...)
	}
	for provider := range providers {
		names = append(names, providerCommandIDs(provider)...)
	}
	return b.bindMethodList(names)
}
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
func (b *nsBook) bind(node semdriver.NodeRef, spec semdriver.MemberSpec) error {
	local := string(spec.Local)
	b.mu.Lock()
	reg := b.reg
	done := b.bound[local]
	b.mu.Unlock()
	if done {
		return nil
	}
	if reg == nil {
		return errors.New("namespace is not registered")
	}
	if err := reg.ReadMember(node, spec, func(context.Context) ([]byte, error) {
		return b.read(local)
	}); err != nil {
		return err
	}
	b.mu.Lock()
	b.bound[local] = true
	b.mu.Unlock()
	return nil
}
func (b *nsBook) bindMethod(name string) error {
	b.mu.Lock()
	reg, node, done := b.reg, b.transport, b.methods[name]
	b.mu.Unlock()
	if reg == nil || done || name == "" {
		return nil
	}
	spec := semdriver.MethodSpec{Local: semdriver.MemberID(name), Schema: "canbusreg.command", Scope: "instance"}
	if err := reg.Method(node, spec, func(_ context.Context, arg []byte) ([]byte, error) {
		return b.invoke(name, arg)
	}); err != nil {
		return err
	}
	b.mu.Lock()
	b.methods[name] = true
	b.mu.Unlock()
	return nil
}
func (b *nsBook) bindMethodList(names []string) error {
	sort.Strings(names)
	prev := ""
	for _, name := range names {
		if name == "" || name == prev {
			continue
		}
		prev = name
		if err := b.bindMethod(name); err != nil {
			return err
		}
	}
	return nil
}
func (b *nsBook) bindView(node semdriver.NodeRef, fields []string) error {
	record := make([]semdriver.ViewField, len(fields))
	for i, name := range fields {
		record[i] = semdriver.ViewField{
			Name: name, Type: rawViewType(name), Member: b.memberKey(node, "raw."+name),
		}
	}
	return b.reg.View(semdriver.ViewSpec{
		Root: "raw_evidence",
		Records: []semdriver.ViewRecord{
			{Name: "raw_evidence", Fields: []semdriver.ViewField{{
				Name: "records", Type: semdriver.ViewType{Name: "raw_record", Record: true, List: true},
				Member: b.memberKey(node, "raw.documents"),
			}}},
			{Name: "raw_record", Fields: record},
		},
	})
}
func (b *nsBook) memberKey(node semdriver.NodeRef, local string) semdriver.MemberKey {
	return semdriver.MemberKey{
		Namespace: namespaceName, Instance: b.inst.key, Node: semdriver.NodeID(node.ID), Local: semdriver.MemberID(local),
	}
}
func rawFieldNames(profile Profile) []string {
	fields := []string{"interface", "sequence", "monotonic_nanos", "identifier", "extended", "rtr", "dlc", "raw_dlc", "data"}
	switch profile {
	case ProfileV104:
		return fields
	case ProfileCommon:
		return append(append([]string{}, fields...), "source")
	default:
		return nil
	}
}
func rawViewType(name string) semdriver.ViewType {
	switch name {
	case "extended", "rtr":
		return semdriver.ViewType{Name: "boolean", List: true}
	case "identifier", "dlc", "raw_dlc", "data":
		return semdriver.ViewType{Name: "integer", List: true}
	default:
		return semdriver.ViewType{Name: "text", List: true}
	}
}
func readingSpec(local, schema, unit, scope string) semdriver.MemberSpec {
	if scope == "" {
		scope = "instance"
	}
	spec := semdriver.MemberSpec{
		Local: semdriver.MemberID(local), Schema: semdriver.SchemaRef(schema),
		Mode: "read", Scope: semdriver.ScopeID(scope),
	}
	if unit != "" {
		id := semreg.DefinitionID(unit)
		if id.Validate() == nil {
			spec.Units = id
		}
	}
	return spec
}
func (b *nsBook) read(local string) ([]byte, error) {
	closed, received, skipped, stale := b.inst.namespaceState()
	if closed {
		return nil, errNamespaceStopped
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.payload(local, stale, received, skipped)
}
func (b *nsBook) payload(local string, stale bool, received, skipped uint64) ([]byte, error) {
	switch {
	case strings.HasPrefix(local, "diagnostic."):
		return diagnosticPayload(local, b.status, stale, received, skipped)
	case strings.HasPrefix(local, "raw."):
		return b.rawPayload(local)
	case strings.HasPrefix(local, "record."):
		return stamped(b.projects, local, stale)
	case strings.HasPrefix(local, "presentation."):
		return stamped(b.marked, local, stale)
	case strings.HasPrefix(local, "point."):
		return stamped(b.points, local, stale)
	case strings.HasPrefix(local, "cell."):
		return stamped(b.cells, local, stale)
	default:
		return b.rowPayload(local, stale)
	}
}
func diagnosticPayload(local, status string, stale bool, received, skipped uint64) ([]byte, error) {
	validity := "good"
	if stale {
		validity = "stale"
	}
	switch local {
	case "diagnostic.received":
		text := strconv.FormatUint(received, 10)
		return json.Marshal(readingBody{Value: &text, Unit: "native.count", Validity: validity})
	case "diagnostic.skipped":
		text := strconv.FormatUint(skipped, 10)
		return json.Marshal(readingBody{Value: &text, Unit: "native.count", Validity: validity})
	case "diagnostic.status":
		if status == "" {
			status = "absent"
		}
		if status == "absent" && !stale {
			validity = "absent"
		}
		return json.Marshal(readingBody{Value: &status, Validity: validity})
	default:
		return nil, errNamespaceMissing
	}
}
func (b *nsBook) rowPayload(local string, stale bool) ([]byte, error) {
	row, ok := b.rows[local]
	if !ok {
		return nil, errNamespaceMissing
	}
	validity := row.validity
	if stale && row.seen {
		validity = "stale"
	}
	return json.Marshal(readingBody{Value: row.value, Unit: row.unit, Validity: validity, ReceivedAt: row.received})
}
func (b *nsBook) rawPayload(local string) ([]byte, error) {
	field := strings.TrimPrefix(local, "raw.")
	if field == "documents" {
		docs := make([]json.RawMessage, len(b.raw))
		for i, doc := range b.raw {
			encoded, err := doc.marshal()
			if err != nil {
				return nil, err
			}
			docs[i] = encoded
		}
		return json.Marshal(docs)
	}
	column := rawColumn(b.raw, field)
	if column == nil {
		return nil, errNamespaceMissing
	}
	return json.Marshal(column)
}
func rawColumn(docs []nsRaw, field string) any {
	switch field {
	case "interface", "sequence", "monotonic_nanos", "source":
		out := make([]string, len(docs))
		for i, doc := range docs {
			out[i] = map[string]string{"interface": doc.Interface, "sequence": doc.Sequence, "monotonic_nanos": doc.Monotonic, "source": doc.Source}[field]
		}
		return out
	case "identifier":
		out := make([]uint32, len(docs))
		for i := range docs {
			out[i] = docs[i].Identifier
		}
		return out
	case "extended", "rtr":
		out := make([]bool, len(docs))
		for i := range docs {
			out[i] = field == "extended" && docs[i].Extended
		}
		return out
	case "dlc", "raw_dlc":
		out := make([]uint8, len(docs))
		for i := range docs {
			out[i] = docs[i].DLC
			if field == "raw_dlc" {
				out[i] = docs[i].RawDLC
			}
		}
		return out
	case "data":
		out := make([][]int, len(docs))
		for i := range docs {
			out[i] = docs[i].Data
			if out[i] == nil {
				out[i] = []int{}
			}
		}
		return out
	default:
		return nil
	}
}
func (d nsRaw) marshal() ([]byte, error) {
	data := d.Data
	if data == nil {
		data = []int{}
	}
	doc := map[string]any{
		"interface": d.Interface, "sequence": d.Sequence, "monotonic_nanos": d.Monotonic,
		"identifier": d.Identifier, "extended": d.Extended, "rtr": false,
		"dlc": d.DLC, "raw_dlc": d.RawDLC, "data": data,
	}
	if d.common {
		doc["source"] = d.Source
	}
	return json.Marshal(doc)
}
func markStale(validity string, stale, keepUnknown bool) string {
	if stale && validity != "absent" && (!keepUnknown || validity != "unknown") {
		return "stale"
	}
	return validity
}
func (b projectBody) staleCopy(stale bool) any {
	b.Validity = markStale(b.Validity, stale, false)
	return b
}
func (b pointBody) staleCopy(stale bool) any {
	b.Validity = markStale(b.Validity, stale, false)
	if b.Raw == nil {
		b.Raw = []int{}
	}
	return b
}
func (b markedBody) staleCopy(stale bool) any {
	b.Validity = markStale(b.Validity, stale, true)
	return b
}
func (b cellBody) staleCopy(stale bool) any {
	b.Validity = markStale(b.Validity, stale, false)
	return b
}
func stamped[T staleReader](m map[string]T, local string, stale bool) ([]byte, error) {
	body, ok := m[local]
	if !ok {
		return nil, errNamespaceMissing
	}
	return json.Marshal(body.staleCopy(stale))
}
func pointFrom(note typedPointNote) pointBody {
	raw := note.Point.Value.Raw
	nums := make([]int, len(raw))
	for i, b := range raw {
		nums[i] = int(b)
	}
	body := pointBody{
		DeviceClass: classLabel(note.Class), Raw: nums, Unit: note.Point.Row.Unit,
		Enumeration: note.Point.EnumerationLabel, LabelOnly: note.Point.Row.LabelOnly,
		Conflict: note.Point.Row.Conflict, Validity: pointValidity(note.Point),
	}
	if note.Point.Received != 0 {
		body.ReceivedAt = strconv.FormatInt(note.Point.Received.Nanoseconds(), 10)
	}
	if text, ok := ratioText(note.Point.Value.Numerator, note.Point.Value.Denominator); ok && note.Point.Value.Status == canbusreg.GreeVRFValueDecoded {
		body.Value = &text
	}
	return body
}
func pointValidity(p canbusreg.GreeVRFTypedPoint) string {
	if !p.Present {
		return "absent"
	}
	if p.Row.Conflict != "" {
		return "conflict"
	}
	if p.Row.LabelOnly {
		return "label_only"
	}
	if p.Value.Status != canbusreg.GreeVRFValueDecoded || p.Value.Denominator == 0 {
		return "invalid"
	}
	return "good"
}
func presentationFrom(note typedPointNote) markedBody {
	in := note.Point.Value
	out := markedBody{Marked: "presentation", Validity: "unknown"}
	if note.Point.Received != 0 {
		out.ReceivedAt = strconv.FormatInt(note.Point.Received.Nanoseconds(), 10)
	}
	converted, ok := canbusreg.ConvertGreeVRFPresentation(note.Point.Row.PresentationConverter, in, note.Options)
	if !ok || converted.Value == nil {
		return out
	}
	text := ratText(converted.Value)
	out.Value = &text
	out.Unit = converted.Unit
	out.Validity = "good"
	return out
}
func ratText(v *big.Rat) string {
	if v == nil {
		return ""
	}
	if v.IsInt() {
		return v.Num().String()
	}
	text := strings.TrimRight(v.FloatString(8), "0")
	return strings.TrimRight(text, ".")
}
func ratioText(n, d int64) (string, bool) {
	if d == 0 {
		return "", false
	}
	if n%d == 0 {
		return strconv.FormatInt(n/d, 10), true
	}
	return strconv.FormatInt(n, 10) + "/" + strconv.FormatInt(d, 10), true
}
func pointLocal(note typedPointNote) string {
	return fmt.Sprintf("point.%s.%02x.%02x.%04x.%s", classLabel(note.Class), note.Point.Key.Unit, note.Point.Row.Type, note.Point.Row.Coordinate, kindLabel(note.Point.Row.Kind))
}
func classLabel(class canbusreg.GreeVRFDeviceClass) string {
	label := map[canbusreg.GreeVRFDeviceClass]string{
		canbusreg.GreeVRFOutdoorUnit: "outdoor", canbusreg.GreeVRFIndoorUnit: "indoor",
		canbusreg.GreeVRFHotWaterUnit: "hot_water", canbusreg.GreeVRFWaterTank: "water_tank",
	}[class]
	if label == "" {
		return "unknown"
	}
	return label
}
func kindLabel(kind canbusreg.GreeVRFDictionaryKind) string {
	label := map[canbusreg.GreeVRFDictionaryKind]string{
		canbusreg.GreeVRFDictionaryByte: "byte", canbusreg.GreeVRFDictionaryBit: "bit", canbusreg.GreeVRFDictionaryContainer: "container",
	}[kind]
	if label == "" {
		return "unknown"
	}
	return label
}
func nonempty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
