package canbusreg

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Native consumer contracts (candidate grades never establish qualification):
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Data validity and consumer obligations.
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Cell and sensor counts; Identify the peer before interpreting version-dependent fields.
type Observation struct {
	Generation uint64
	Input      ReplayInput
}

// ReceiveSource transfers exclusive ownership of each returned input until
// the next receive call. Receive must unblock on context cancellation. It grants
// no transmit, acknowledgement, probing or configuration capability.
type ReceiveSource interface {
	ProjectProviderReceive(context.Context) (Observation, error)
}

type ProjectProviderRaw struct {
	Generation   uint64
	Input        LINKInput
	NativeRecord []byte
	Status       string
}

type ProjectProviderSnapshot struct {
	Selection                                             CXSelection
	State                                                 string
	Generation                                            uint64
	Current, LastGood                                     map[string]CXNativeField
	LastGoodGeneration                                    map[string]uint64
	Raw                                                   []ProjectProviderRaw
	Rejected, EvictedRaw, EvictedFields, TruncatedRecords uint64
	Promotable, PhysicalQualified                         bool
}

// Provider has exactly one configured source/selection, one identity
// assembly per identity kind (per native module for PCS-parallel serials, at
// most 16) and one candidate group; only the pending serial fragments of those
// assemblies are retained and there is no input queue.
// Both field dictionaries and raw history are bounded to 256 entries. Field
// eviction uses insertion order with lexicographically ordered batch insertion.
// Start and Stop join the canceled predecessor; sources must honor cancellation.
type Provider struct {
	lifecycle         sync.Mutex
	mu                sync.Mutex
	selection         CXSelection
	snapshot          ProjectProviderSnapshot
	running, conflict bool
	sequence          uint64
	monotonic         time.Duration
	cancel            context.CancelFunc
	done              chan struct{}
	order             []string
	identity          map[string]int64
	member            CXSelection
	hasMember, seen   bool
	association       projectProviderAssociation
	// assembler is the single LINK identity assembly of the active generation; it retains the completed serial so
	// that a later different serial under the same project, release and route raises a sticky conflict.
	assembler *LINKIdentityAssembler
	// linkSerial assembles 0x18fb serial announcements of the configured LINK selection whether or not a 0x1802
	// record was received, so two different complete serials under one selection always raise a sticky conflict.
	linkSerial projectProviderLINKSerial
	// serial is the single PCS-standard 0x324 serial assembly of the active generation; serialSlot and
	// serialValue retain the completed serial and its native slot so a later different one raises a sticky conflict.
	serial      *PCSIdentitySerialAssembler
	serialSlot  uint8
	serialValue [16]byte
	hasSerial   bool
	// modules holds the PCS-parallel 0x6C0 serial assembly of each module identity named by the native module
	// nibble (at most 16, allocated on first use); each retains only its own module's completed serial.
	modules [16]*ParallelIdentityAssembler
}

// cx17Association is the native slot, group and extrema index association supplied with a PCS record. It is part of
// the identity fence like the member: the same wire bytes decode into another slot's or group's fields, or into
// indexes accepted under another scope or other inclusive bounds, when it changes.
type projectProviderAssociation struct {
	slotValid, groupValid, serialSlotValid bool
	slot, group, serialSlot                uint8
	indexScope                             string
	indexMinimum, indexMaximum             uint16
}

// cx17LINKSerial is the provider's bounded 0x18fb serial assembly: one pending in-order fragment sequence and the
// completed serial retained for the generation.
type projectProviderLINKSerial struct {
	assembling, has bool
	next            uint8
	fragments       [16]byte
	serial          [16]byte
}

// cx17Consume feeds one admitted descriptive LINK record. Another admitted record kind, or a malformed or reordered
// fragment, only discards the pending assembly. A completed serial that differs from the retained one is a conflict.
func (a *projectProviderLINKSerial) projectProviderConsume(s CXSelection, i LINKInput) bool {
	if !lINKAdmission(s, i) {
		return false
	}
	if i.Evidence.Frame.ID().Value()>>16 != 0x18fb {
		a.assembling = false
		return false
	}
	p := i.Evidence.Frame.Bytes()
	length, fragment := (p[0]&31)+1, (p[0]>>5)&3
	if p[0]&0x80 != 0 || length != 16 || fragment > 2 || (!a.assembling && fragment != 0) || (a.assembling && fragment != a.next) {
		a.assembling = false
		return false
	}
	if fragment == 0 {
		a.fragments, a.assembling = [16]byte{}, true
	}
	if fragment == 2 {
		for _, b := range p[3:] {
			if b != 0 {
				a.assembling = false
				return false
			}
		}
		copy(a.fragments[14:], p[1:3])
		a.assembling = false
		if a.has && a.serial != a.fragments {
			return true
		}
		a.serial, a.has = a.fragments, true
		return false
	}
	copy(a.fragments[int(fragment)*7:], p[1:])
	a.next = fragment + 1
	return false
}

func NewPCSProvider(s CXSelection) (*Provider, error) {
	if s.SelectionSpecValue().Flavor == "link" {
		return nil, errors.New("PCS selection required")
	}
	return projectProviderNew(s)
}
func NewLINKProvider(s CXSelection) (*Provider, error) {
	if s.SelectionSpecValue().Flavor != "link" {
		return nil, errors.New("LINK selection required")
	}
	return projectProviderNew(s)
}
func projectProviderNew(s CXSelection) (*Provider, error) {
	v, err := NewCXSelection(s.SelectionSpecValue())
	if err != nil || v != s || s.SelectionGroup() == "" {
		return nil, errors.New("explicit supported selection required")
	}
	return &Provider{selection: s, snapshot: ProjectProviderSnapshot{Selection: s, State: "stopped", Current: make(map[string]CXNativeField), LastGood: make(map[string]CXNativeField), LastGoodGeneration: make(map[string]uint64)}}, nil
}
func (p *Provider) Start(source ReceiveSource, generation uint64) error {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	invalid := source == nil || generation == 0 || generation <= p.snapshot.Generation
	p.mu.Unlock()
	if invalid {
		return errors.New("source and strictly increasing positive generation required")
	}
	p.projectProviderStop()
	p.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.cancel, p.done = cancel, done
	p.running = true
	p.conflict = false
	p.identity = make(map[string]int64)
	p.member, p.hasMember, p.seen = CXSelection{}, false, false
	p.assembler, p.linkSerial = nil, projectProviderLINKSerial{}
	if p.selection.SelectionSpecValue().Flavor == "link" {
		p.assembler = NewLINKIdentityAssembler(p.selection)
	}
	p.serial, p.serialSlot, p.serialValue, p.hasSerial = nil, 0, [16]byte{}, false
	if p.selection.SelectionSpecValue().Flavor == "pcs-standard" {
		p.serial = &PCSIdentitySerialAssembler{}
	}
	p.modules = [16]*ParallelIdentityAssembler{}
	p.sequence = 0
	p.monotonic = 0
	p.snapshot.Generation = generation
	p.snapshot.State = "awaiting"
	p.mu.Unlock()
	go p.projectProviderReceive(ctx, source, generation, done)
	return nil
}
func (p *Provider) Stop() { p.lifecycle.Lock(); defer p.lifecycle.Unlock(); p.projectProviderStop() }
func (p *Provider) projectProviderWithhold(state string) {
	p.snapshot.State = state
	p.snapshot.Promotable = false
	p.snapshot.Current = make(map[string]CXNativeField)
}
func (p *Provider) projectProviderStop() {
	p.mu.Lock()
	p.running = false
	p.projectProviderWithhold("stopped")
	cancel, done := p.cancel, p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (p *Provider) projectProviderReceive(ctx context.Context, source ReceiveSource, generation uint64, done chan struct{}) {
	defer close(done)
	for {
		o, err := source.ProjectProviderReceive(ctx)
		p.mu.Lock()
		if !p.running || p.snapshot.Generation != generation || ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		if err != nil {
			p.running = false
			p.projectProviderWithhold("degraded")
			p.mu.Unlock()
			return
		}
		p.projectProviderApply(o)
		p.mu.Unlock()
	}
}

// cx17ExtendedKind normalizes the coordinator's aggregate 0x1910..0x1919 records to the 0x1900..0x1909 kinds the
// decoder interprets them as, so identity fields fence identically on both routes. Gateway-state 0x1910 keeps its kind.
func projectProviderExtendedKind(c SelectionSpec, kind uint32) uint32 {
	if kind >= 0x1910 && kind <= 0x1919 && c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2 {
		return kind - 0x10
	}
	return kind
}

// cx17IdentityKey names the fence entry of a decoded identity or role field. Opaque fields, such as the LINK 0x1800
// flags byte whose unspecified bits cannot establish a role change, are never fenced; withheld fields carry no value.
func projectProviderIdentityKey(c SelectionSpec, prefix string, id uint32, name string, f CXNativeField) (string, bool) {
	if projectProviderWithheld(f.Status) {
		return "", false
	}
	switch prefix {
	case "parallel":
		if id&^15 == 0x600 && (name == "product" || name == "hardware" || name == "version_mux") {
			return prefix + "/" + name, true
		}
	case "identity":
		if id == 0x320 && (name == "product_0" || name == "product_1" || name == "hardware" || name == "version_3" || name == "version_5") {
			return prefix + "/" + name, true
		}
		// Only an accepted family, or an accepted all-zero family_omitted announcement, is identity: both share one
		// fence entry, so switching between a labelled and an omitted family conflicts in either direction. A raw
		// family on an unlabelled selection stays uninterpreted.
		if id == 0x321 && (name == "family" || name == "family_omitted") && f.Status == "accepted" {
			return "identity/0321/family", true
		}
	case "extended":
		// The gateway-state 0x1910 downstream address is not fenced: the gateway-state selection has no native group,
		// so the constructors refuse it, and every other selection decodes that field only as raw, never accepted.
		switch kind := projectProviderExtendedKind(c, id>>16); {
		case kind == 0x1900 && (name == "product" || name == "hardware" || name == "version"):
			return prefix + "/" + name, true
		case kind == 0x1903 && name == "role":
			return "extended/1903/role", true
		}
	case "link":
		switch kind := id >> 16; {
		case kind == 0x1802 && (name == "project_code" || name == "release"):
			return prefix + "/" + name, true
		case kind == 0x1801 && name == "version":
			return "link/1801/version", true
		}
	}
	return "", false
}
func (p *Provider) projectProviderRaw(o Observation, status string) {
	raw := o.Input.PCS.NativeRecord
	if len(raw) > 72 {
		raw = raw[:72]
		p.snapshot.TruncatedRecords++
	}
	r := ProjectProviderRaw{Generation: o.Generation, Input: o.Input.PCS.LINKInput, NativeRecord: append([]byte(nil), raw...), Status: status}
	if len(p.snapshot.Raw) == 256 {
		copy(p.snapshot.Raw, p.snapshot.Raw[1:])
		p.snapshot.Raw = p.snapshot.Raw[:255]
		p.snapshot.EvictedRaw++
	}
	p.snapshot.Raw = append(p.snapshot.Raw, r)
}
func (p *Provider) projectProviderApply(o Observation) {
	in := o.Input
	i := in.PCS.LINKInput
	e := i.Evidence
	c := p.selection.SelectionSpecValue()
	if o.Generation != p.snapshot.Generation || e.Interface != c.Interface || e.Sequence == 0 || e.Sequence <= p.sequence || e.Monotonic < 0 || e.Monotonic < p.monotonic {
		p.snapshot.Rejected++
		p.projectProviderRaw(o, "rejected")
		return
	}
	p.sequence, p.monotonic = e.Sequence, e.Monotonic
	// A changed association cannot silently select a successor identity. History
	// remains diagnostic; only an explicitly started generation can reset the fence.
	identityChanged := in.Selection != p.selection || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot
	if (c.Flavor == "pcs-standard" || c.Flavor == "pcs-parallel") && !in.PCS.PublisherResolved {
		identityChanged = true
	}
	// The reporting-member, native slot/group and serial slot associations are part of the identity fence: a change
	// within a generation would reinterpret member-, slot- or serial-derived fields. The baseline is taken from the
	// first admitted observation only, so a malformed observation cannot fix the generation's associations.
	hasMember := in.PCS.Member != nil
	var member CXSelection
	if hasMember {
		member = *in.PCS.Member
	}
	association := projectProviderAssociation{in.PCS.SlotValid, in.PCS.GroupValid, in.PCS.SerialSlotValid, in.PCS.NativeSlot, in.PCS.NativeGroup, in.PCS.SerialSlot, in.PCS.IndexScope, in.PCS.IndexMinimum, in.PCS.IndexMaximum}
	if identityChanged {
		p.conflict = true
	}
	if p.conflict {
		p.projectProviderWithhold("conflict")
		p.projectProviderRaw(o, "conflict")
		return
	}
	profile, err := NewCX16SelectedProfile(p.selection, []ReplayInput{in})
	if err != nil {
		p.snapshot.Rejected++
		p.projectProviderRaw(o, "invalid")
		return
	}
	classification := profile.Classify(e)
	projection, ok := classification.Projection.(ReplayProjection)
	if !ok {
		p.snapshot.Rejected++
		p.projectProviderRaw(o, "unsupported")
		return
	}
	if projection.Current.Status == "conflict" {
		p.conflict = true
		p.projectProviderWithhold("conflict")
		p.projectProviderRaw(o, "conflict")
		return
	}
	if !p.seen && projection.Current.Status == "descriptive" {
		p.seen, p.hasMember, p.member, p.association = true, hasMember, member, association
	}
	// Associations are compared only for admitted observations: rejected evidence (a malformed record) must
	// neither fix nor break the generation's associations.
	if p.seen && projection.Current.Status == "descriptive" && (hasMember != p.hasMember || member != p.member || association != p.association) {
		p.conflict = true
	}
	// The serial is assembled across observations; a completed serial that differs from the retained one is a
	// sticky conflict of this generation.
	if p.assembler != nil && projection.Current.Status == "descriptive" && p.assembler.DecodeLINKIdentity(i).Status == "conflict" {
		p.conflict = true
	}
	if c.Flavor == "link" && projection.Current.Status == "descriptive" && p.linkSerial.projectProviderConsume(p.selection, i) {
		p.conflict = true
	}
	if projection.Current.Status == "descriptive" && p.projectProviderSerialConflict(in) {
		p.conflict = true
	}
	// Compare only identity/role fields supplied by the accepted native API.
	id := e.Frame.ID().Value()
	for prefix, part := range projection.Native {
		for name, f := range part.Fields {
			key, identityField := projectProviderIdentityKey(c, prefix, id, name, f)
			if !identityField {
				continue
			}
			value := f.Numerator
			if name == "family_omitted" {
				// An accepted family is four printable bytes; the omitted state gets a value no family can carry.
				value = -1
			}
			if old, seen := p.identity[key]; seen && old != value {
				p.conflict = true
			}
			p.identity[key] = value
		}
	}
	if p.conflict {
		p.projectProviderWithhold("conflict")
		p.projectProviderRaw(o, "conflict")
		return
	}
	p.projectProviderRaw(o, projection.Current.Status)
	fields := make(map[string]CXNativeField)
	for prefix, part := range projection.Native {
		key := replayRetentionKey(prefix, e, part)
		for name, f := range part.Fields {
			fields[key+"/"+name] = f
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := fields[name]
		known := false
		for _, key := range p.order {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			if len(p.order) == 256 {
				old := p.order[0]
				p.order = p.order[1:]
				delete(p.snapshot.Current, old)
				delete(p.snapshot.LastGood, old)
				delete(p.snapshot.LastGoodGeneration, old)
				p.snapshot.EvictedFields++
			}
			p.order = append(p.order, name)
		}
		p.snapshot.Current[name] = f
		if f.Status == "accepted" || f.Status == "empty" {
			p.snapshot.LastGood[name] = f
			p.snapshot.LastGoodGeneration[name] = o.Generation
		}
	}
	// A replacement standard or release-11 parallel DTC record that omits a derived index must not leave the
	// previous index current; LastGood stays untouched.
	for prefix, part := range projection.Native {
		if !(prefix == "slots" && e.Frame.ID().Value() == 0x333) && prefix != "release11" {
			continue
		}
		key := replayRetentionKey(prefix, e, part)
		for _, name := range []string{"dtc_a_index", "dtc_b_index"} {
			full := key + "/" + name
			if _, ok := fields[full]; ok {
				continue
			}
			if f, ok := p.snapshot.Current[full]; ok {
				f.Status, f.Unit, f.Meaning = "absent", "", ""
				p.snapshot.Current[full] = f
			}
		}
	}
	// Only an admitted observation reconciles: a wrong-route or malformed record carries rejected counts that must
	// not demote fields retained from accepted evidence.
	if c.Flavor == "link" && projection.Current.Status == "descriptive" {
		p.projectProviderReconcile("cells/cell_", 16, in.CellCount, in.CellCountValid, fields)
		p.projectProviderReconcile("sensors/sensor_", 4, in.SensorCount, in.SensorCountValid, fields)
		// A later 0x1801 whose gate, hardware age or count withholds enables must not leave earlier enables current.
		// Every other accepted record carries the configured cell count too, so a smaller or invalid count there
		// demotes the retained enables it no longer configures.
		if part, ok := projection.Native["balance"]; ok && part.Status == "accepted" {
			_, enabled := part.Fields["enable_1"]
			p.projectProviderReconcile("balance/enable_", 16, in.CellCount, enabled, fields)
		} else {
			p.projectProviderReconcile("balance/enable_", 16, in.CellCount, in.CellCountValid, fields)
		}
	}
	// The PCS count gates early-layout cells 5..8 and the release-11 fine average temperature; a later admitted
	// record whose count no longer qualifies them demotes the retained current values. LastGood stays untouched.
	if c.Flavor == "pcs-standard" && projection.Current.Status == "descriptive" {
		count, valid := int(in.PCS.Count), in.PCS.CountValid
		switch p.selection.SelectionGroup() {
		case "L0", "L1":
			for position := 5; position <= 8; position++ {
				p.projectProviderDemote(fmt.Sprintf("gauge/316/cell_%d", position), valid && count >= position, valid, fields)
			}
		case "L8":
			p.projectProviderDemote("core/temperature", valid && count > 0, valid, fields)
		}
	}
	p.snapshot.State = "receiving"
}

// cx17SerialConflict feeds a descriptive PCS record to the generation's serial assemblies. A completed PCS-standard
// 0x324 serial, or a completed PCS-parallel 0x6C0 serial of one module, that differs from the one retained earlier
// in this generation (including a changed native serial slot) is a conflict. Interrupted or reordered fragment
// sequences only discard the pending assembly.
func (p *Provider) projectProviderSerialConflict(in ReplayInput) bool {
	i := in.PCS.LINKInput
	id := i.Evidence.Frame.ID().Value()
	if p.serial != nil && id == 0x324 {
		r := p.serial.Consume(p.selection, in.PCS.PCSIdentityPCSInput)
		if r.Status != "complete" {
			return false
		}
		slot := in.PCS.SerialSlot
		if p.hasSerial && (slot != p.serialSlot || r.Serial != p.serialValue) {
			return true
		}
		p.serialSlot, p.serialValue, p.hasSerial = slot, r.Serial, true
		return false
	}
	if p.selection.SelectionSpecValue().Flavor != "pcs-parallel" || i.Evidence.Frame.ID().Extended() || id&^15 != 0x6c0 || i.Evidence.Frame.DLC() != 8 {
		return false
	}
	module := i.Evidence.Frame.Bytes()[0] >> 4
	a := p.modules[module]
	if a == nil {
		a = NewParallelIdentityAssembler(p.selection, module)
		p.modules[module] = a
	}
	// Only the final fragment of an in-order assembly can complete a serial; an out-of-order fragment also
	// reports conflict from the assembler but merely discards the pending assembly.
	completing := a.active && a.next == 2 && i.Evidence.Frame.Bytes()[0]&15 == 2
	before := a.SnapshotParallelIdentity()
	return a.DecodeParallelIdentity(i).Status == "conflict" && completing && before.HasSerial
}

// cx17Withheld reports field states that carry no value for identity comparison.
func projectProviderWithheld(status string) bool {
	switch status {
	case "absent", "invalid", "unsupported", "stale", "conflict", "unassigned":
		return true
	}
	return false
}

// cx17Reconcile demotes retained current vector fields that the latest count
// no longer configures. Historical last-good values are left untouched.
func (p *Provider) projectProviderReconcile(prefix string, max, count int, valid bool, fresh map[string]CXNativeField) {
	valid = valid && count >= 1 && count <= max
	for index := 1; index <= max; index++ {
		p.projectProviderDemote(fmt.Sprintf("%s%d", prefix, index), valid && index <= count, valid, fresh)
	}
}

// cx17Demote marks one retained current field that the latest admitted count no longer configures: unconfigured for
// a valid count, absent for an invalid one. A field decoded fresh in this observation keeps its own status.
func (p *Provider) projectProviderDemote(name string, configured, valid bool, fresh map[string]CXNativeField) {
	if _, ok := fresh[name]; ok {
		return
	}
	f, ok := p.snapshot.Current[name]
	if !ok || configured {
		return
	}
	if valid {
		f.Status = "unconfigured"
	} else {
		f.Status = "absent"
	}
	f.Unit = ""
	f.Meaning = ""
	p.snapshot.Current[name] = f
}

// ProjectProviderSnapshot uses caller logical time and an inclusive maximum age. Each
// field ages from its own evidence and gate age; other frames cannot renew it.
// LastGood is historical, with its origin generation explicitly retained.
func (p *Provider) ProjectProviderSnapshot(now, maxAge time.Duration) ProjectProviderSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.snapshot
	s.Current = make(map[string]CXNativeField)
	s.LastGood = make(map[string]CXNativeField)
	s.LastGoodGeneration = make(map[string]uint64)
	for name, f := range p.snapshot.Current {
		aged, valid := lINKVectorAge(f, now)
		f = aged
		if (f.Status == "accepted" || f.Status == "empty") && (!valid || maxAge < 0 || f.Age > maxAge) {
			f.Status = "stale"
			f.Unit = ""
			f.Meaning = ""
		}
		s.Current[name] = f
		if p.running && !p.conflict && f.Status == "accepted" {
			s.Promotable = true
		}
	}
	for name, f := range p.snapshot.LastGood {
		s.LastGood[name], _ = lINKVectorAge(f, now)
		s.LastGoodGeneration[name] = p.snapshot.LastGoodGeneration[name]
	}
	s.Raw = make([]ProjectProviderRaw, len(p.snapshot.Raw))
	for n, r := range p.snapshot.Raw {
		r.NativeRecord = append([]byte(nil), r.NativeRecord...)
		s.Raw[n] = r
	}
	return s
}
