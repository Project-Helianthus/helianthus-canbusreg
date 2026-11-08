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
type CX17Observation struct {
	Generation uint64
	Input      CX16ReplayInput
}

// CX17ReceiveSource transfers exclusive ownership of each returned input until
// the next receive call. Receive must unblock on context cancellation. It grants
// no transmit, acknowledgement, probing or configuration capability.
type CX17ReceiveSource interface {
	CX17Receive(context.Context) (CX17Observation, error)
}

type CX17Raw struct {
	Generation   uint64
	Input        CX01LINKInput
	NativeRecord []byte
	Status       string
}

type CX17Snapshot struct {
	Selection                                             CXSelection
	State                                                 string
	Generation                                            uint64
	Current, LastGood                                     map[string]CXNativeField
	LastGoodGeneration                                    map[string]uint64
	Raw                                                   []CX17Raw
	Rejected, EvictedRaw, EvictedFields, TruncatedRecords uint64
	Promotable, PhysicalQualified                         bool
}

// CX17Provider has exactly one configured source/selection, one identity
// assembly and one candidate group; no fragments or input queue are retained.
// Both field dictionaries and raw history are bounded to 256 entries. Field
// eviction uses insertion order with lexicographically ordered batch insertion.
// Start and Stop join the canceled predecessor; sources must honor cancellation.
type CX17Provider struct {
	lifecycle         sync.Mutex
	mu                sync.Mutex
	selection         CXSelection
	snapshot          CX17Snapshot
	running, conflict bool
	sequence          uint64
	monotonic         time.Duration
	cancel            context.CancelFunc
	done              chan struct{}
	order             []string
	identity          map[string]int64
	member            CXSelection
	hasMember, seen   bool
	association       cx17Association
	// assembler is the single LINK identity assembly of the active generation; it retains the completed serial so
	// that a later different serial under the same project, release and route raises a sticky conflict.
	assembler *CX01LINKIdentityAssembler
}

// cx17Association is the native slot and group association supplied with a PCS record. It is part of the
// identity fence like the member: the same wire bytes decode into another slot's or group's fields when it changes.
type cx17Association struct {
	slotValid, groupValid bool
	slot, group           uint8
}

func CX17NewPCSProvider(s CXSelection) (*CX17Provider, error) {
	if s.CX01Spec().Flavor == "link" {
		return nil, errors.New("PCS selection required")
	}
	return cx17New(s)
}
func CX17NewLINKProvider(s CXSelection) (*CX17Provider, error) {
	if s.CX01Spec().Flavor != "link" {
		return nil, errors.New("LINK selection required")
	}
	return cx17New(s)
}
func cx17New(s CXSelection) (*CX17Provider, error) {
	v, err := NewCXSelection(s.CX01Spec())
	if err != nil || v != s || s.CX01Group() == "" {
		return nil, errors.New("explicit supported selection required")
	}
	return &CX17Provider{selection: s, snapshot: CX17Snapshot{Selection: s, State: "stopped", Current: make(map[string]CXNativeField), LastGood: make(map[string]CXNativeField), LastGoodGeneration: make(map[string]uint64)}}, nil
}
func (p *CX17Provider) Start(source CX17ReceiveSource, generation uint64) error {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	invalid := source == nil || generation == 0 || generation <= p.snapshot.Generation
	p.mu.Unlock()
	if invalid {
		return errors.New("source and strictly increasing positive generation required")
	}
	p.cx17Stop()
	p.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.cancel, p.done = cancel, done
	p.running = true
	p.conflict = false
	p.identity = make(map[string]int64)
	p.member, p.hasMember, p.seen = CXSelection{}, false, false
	p.assembler = nil
	if p.selection.CX01Spec().Flavor == "link" {
		p.assembler = CX01NewLINKIdentityAssembler(p.selection)
	}
	p.sequence = 0
	p.monotonic = 0
	p.snapshot.Generation = generation
	p.snapshot.State = "awaiting"
	p.mu.Unlock()
	go p.cx17Receive(ctx, source, generation, done)
	return nil
}
func (p *CX17Provider) Stop() { p.lifecycle.Lock(); defer p.lifecycle.Unlock(); p.cx17Stop() }
func (p *CX17Provider) cx17Withhold(state string) {
	p.snapshot.State = state
	p.snapshot.Promotable = false
	p.snapshot.Current = make(map[string]CXNativeField)
}
func (p *CX17Provider) cx17Stop() {
	p.mu.Lock()
	p.running = false
	p.cx17Withhold("stopped")
	cancel, done := p.cancel, p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (p *CX17Provider) cx17Receive(ctx context.Context, source CX17ReceiveSource, generation uint64, done chan struct{}) {
	defer close(done)
	for {
		o, err := source.CX17Receive(ctx)
		p.mu.Lock()
		if !p.running || p.snapshot.Generation != generation || ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		if err != nil {
			p.running = false
			p.cx17Withhold("degraded")
			p.mu.Unlock()
			return
		}
		p.cx17Apply(o)
		p.mu.Unlock()
	}
}

// cx17ExtendedKind normalizes the coordinator's aggregate 0x1910..0x1919 records to the 0x1900..0x1909 kinds the
// decoder interprets them as, so identity fields fence identically on both routes. Gateway-state 0x1910 keeps its kind.
func cx17ExtendedKind(c CX01SelectionSpec, kind uint32) uint32 {
	if kind >= 0x1910 && kind <= 0x1919 && c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2 {
		return kind - 0x10
	}
	return kind
}

// cx17IdentityKey names the fence entry of a decoded identity or role field. Opaque fields, such as the LINK 0x1800
// flags byte whose unspecified bits cannot establish a role change, are never fenced; withheld fields carry no value.
func cx17IdentityKey(c CX01SelectionSpec, prefix string, id uint32, name string, f CXNativeField) (string, bool) {
	if cx17Withheld(f.Status) {
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
		// Only an accepted family is identity; a raw family on an unlabelled selection stays uninterpreted.
		if id == 0x321 && name == "family" && f.Status == "accepted" {
			return "identity/0321/family", true
		}
	case "extended":
		switch kind := cx17ExtendedKind(c, id>>16); {
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
func (p *CX17Provider) cx17Raw(o CX17Observation, status string) {
	raw := o.Input.PCS.NativeRecord
	if len(raw) > 72 {
		raw = raw[:72]
		p.snapshot.TruncatedRecords++
	}
	r := CX17Raw{Generation: o.Generation, Input: o.Input.PCS.CX01LINKInput, NativeRecord: append([]byte(nil), raw...), Status: status}
	if len(p.snapshot.Raw) == 256 {
		copy(p.snapshot.Raw, p.snapshot.Raw[1:])
		p.snapshot.Raw = p.snapshot.Raw[:255]
		p.snapshot.EvictedRaw++
	}
	p.snapshot.Raw = append(p.snapshot.Raw, r)
}
func (p *CX17Provider) cx17Apply(o CX17Observation) {
	in := o.Input
	i := in.PCS.CX01LINKInput
	e := i.Evidence
	c := p.selection.CX01Spec()
	if o.Generation != p.snapshot.Generation || e.Interface != c.Interface || e.Sequence == 0 || e.Sequence <= p.sequence || e.Monotonic < 0 || e.Monotonic < p.monotonic {
		p.snapshot.Rejected++
		p.cx17Raw(o, "rejected")
		return
	}
	p.sequence, p.monotonic = e.Sequence, e.Monotonic
	// A changed association cannot silently select a successor identity. History
	// remains diagnostic; only an explicitly started generation can reset the fence.
	identityChanged := in.Selection != p.selection || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot
	if (c.Flavor == "pcs-standard" || c.Flavor == "pcs-parallel") && !in.PCS.PublisherResolved {
		identityChanged = true
	}
	// The reporting-member association is part of the identity fence: a change
	// within a generation would reinterpret member-derived fields.
	hasMember := in.PCS.Member != nil
	var member CXSelection
	if hasMember {
		member = *in.PCS.Member
	}
	if p.seen && (hasMember != p.hasMember || member != p.member) {
		identityChanged = true
	}
	association := cx17Association{in.PCS.SlotValid, in.PCS.GroupValid, in.PCS.NativeSlot, in.PCS.NativeGroup}
	if p.seen && association != p.association {
		identityChanged = true
	}
	if identityChanged {
		p.conflict = true
	}
	if p.conflict {
		p.cx17Withhold("conflict")
		p.cx17Raw(o, "conflict")
		return
	}
	if !p.seen {
		p.seen, p.hasMember, p.member, p.association = true, hasMember, member, association
	}
	profile, err := NewCX16SelectedProfile(p.selection, []CX16ReplayInput{in})
	if err != nil {
		p.snapshot.Rejected++
		p.cx17Raw(o, "invalid")
		return
	}
	classification := profile.Classify(e)
	projection, ok := classification.Projection.(CX16Projection)
	if !ok {
		p.snapshot.Rejected++
		p.cx17Raw(o, "unsupported")
		return
	}
	if projection.Current.Status == "conflict" {
		p.conflict = true
		p.cx17Withhold("conflict")
		p.cx17Raw(o, "conflict")
		return
	}
	// The serial is assembled across observations; a completed serial that differs from the retained one is a
	// sticky conflict of this generation.
	if p.assembler != nil && projection.Current.Status == "descriptive" && p.assembler.CX01Decode(i).Status == "conflict" {
		p.conflict = true
	}
	// Compare only identity/role fields supplied by the accepted native API.
	id := e.Frame.ID().Value()
	for prefix, part := range projection.Native {
		for name, f := range part.Fields {
			key, identityField := cx17IdentityKey(c, prefix, id, name, f)
			if !identityField {
				continue
			}
			if old, seen := p.identity[key]; seen && old != f.Numerator {
				p.conflict = true
			}
			p.identity[key] = f.Numerator
		}
	}
	if p.conflict {
		p.cx17Withhold("conflict")
		p.cx17Raw(o, "conflict")
		return
	}
	p.cx17Raw(o, projection.Current.Status)
	fields := make(map[string]CXNativeField)
	for prefix, part := range projection.Native {
		key := cx16RetentionKey(prefix, e, part)
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
		key := cx16RetentionKey(prefix, e, part)
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
	if c.Flavor == "link" {
		p.cx17Reconcile("cells/cell_", 16, in.CellCount, in.CellCountValid, fields)
		p.cx17Reconcile("sensors/sensor_", 4, in.SensorCount, in.SensorCountValid, fields)
		// A later 0x1801 whose gate, hardware age or count withholds enables must not leave earlier enables current.
		if part, ok := projection.Native["balance"]; ok && part.Status == "accepted" {
			_, enabled := part.Fields["enable_1"]
			p.cx17Reconcile("balance/enable_", 16, in.CellCount, enabled, fields)
		}
	}
	p.snapshot.State = "receiving"
}

// cx17Withheld reports field states that carry no value for identity comparison.
func cx17Withheld(status string) bool {
	switch status {
	case "absent", "invalid", "unsupported", "stale", "conflict", "unassigned":
		return true
	}
	return false
}

// cx17Reconcile demotes retained current vector fields that the latest count
// no longer configures. Historical last-good values are left untouched.
func (p *CX17Provider) cx17Reconcile(prefix string, max, count int, valid bool, fresh map[string]CXNativeField) {
	valid = valid && count >= 1 && count <= max
	for index := 1; index <= max; index++ {
		name := fmt.Sprintf("%s%d", prefix, index)
		if _, ok := fresh[name]; ok {
			continue
		}
		f, ok := p.snapshot.Current[name]
		if !ok || (valid && index <= count) {
			continue
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
}

// CX17Snapshot uses caller logical time and an inclusive maximum age. Each
// field ages from its own evidence and gate age; other frames cannot renew it.
// LastGood is historical, with its origin generation explicitly retained.
func (p *CX17Provider) CX17Snapshot(now, maxAge time.Duration) CX17Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.snapshot
	s.Current = make(map[string]CXNativeField)
	s.LastGood = make(map[string]CXNativeField)
	s.LastGoodGeneration = make(map[string]uint64)
	for name, f := range p.snapshot.Current {
		aged, valid := cx12Age(f, now)
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
		s.LastGood[name], _ = cx12Age(f, now)
		s.LastGoodGeneration[name] = p.snapshot.LastGoodGeneration[name]
	}
	s.Raw = make([]CX17Raw, len(p.snapshot.Raw))
	for n, r := range p.snapshot.Raw {
		r.NativeRecord = append([]byte(nil), r.NativeRecord...)
		s.Raw[n] = r
	}
	return s
}
