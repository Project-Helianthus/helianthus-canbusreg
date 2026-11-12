package canbusreg

import (
	"sync"
	"time"
)

// CX10ParallelIdentitySnapshot preserves each fragment's native evidence.
// It proves neither inventory completeness nor simultaneity with telemetry.
type ParallelIdentitySnapshot struct {
	Serial            [16]byte
	Fragments         [3]LINKInput
	HasSerial         bool
	Grade, Status     string
	PhysicalQualified bool
}

type ParallelIdentityAssembler struct {
	lastSequence  uint64
	lastMonotonic time.Duration
	seen          bool
	mu            sync.Mutex
	selection     CXSelection
	module        uint8
	next          uint8
	active        bool
	pending       [16]byte
	fragments     [3]LINKInput
	snapshot      ParallelIdentitySnapshot
}

// CX10NewParallelIdentityAssembler binds one explicit bus/group/module/epoch.
func NewParallelIdentityAssembler(s CXSelection, module uint8) *ParallelIdentityAssembler {
	return &ParallelIdentityAssembler{selection: s, module: module, snapshot: ParallelIdentitySnapshot{Grade: "Unknown", Status: "absent"}}
}
func (a *ParallelIdentityAssembler) SnapshotParallelIdentity() ParallelIdentitySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot
}
func (a *ParallelIdentityAssembler) DecodeParallelIdentity(i LINKInput) CXNativeResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := selectionResult(a.selection, i, "invalid")
	if !parallelIdentityAdmission(a.selection, i) {
		return r
	}
	family, ok := parallelIdentityFamily(a.selection, i.Evidence.Frame.ID().Value())
	if !ok || family != 0x6c0 || a.module > 15 {
		r.Status = "unsupported"
		return r
	}
	p := r.Raw
	module, fragment := p[0]>>4, p[0]&15
	if module != a.module {
		return r
	}
	if a.seen && (i.Evidence.Sequence <= a.lastSequence || i.Evidence.Monotonic < a.lastMonotonic) {
		a.active = false
		r.Status = "stale"
		return r
	}
	a.seen = true
	a.lastSequence = i.Evidence.Sequence
	a.lastMonotonic = i.Evidence.Monotonic
	if fragment > 2 {
		a.active = false
		return r
	}
	if a.active {
		if fragment != a.next {
			a.active = false
			r.Status = "conflict"
			return r
		}
	} else if fragment != 0 {
		return r
	}
	if fragment == 0 {
		a.pending = [16]byte{}
		a.fragments = [3]LINKInput{}
		a.active = true
	}
	if fragment == 2 {
		copy(a.pending[14:], p[1:3])
	} else {
		copy(a.pending[int(fragment)*7:], p[1:])
	}
	a.fragments[fragment] = i
	a.next = fragment + 1
	if fragment == 2 {
		a.active = false
		if a.snapshot.HasSerial && a.snapshot.Serial != a.pending {
			r.Status = "conflict"
			return r
		}
		a.snapshot = ParallelIdentitySnapshot{Serial: a.pending, Fragments: a.fragments, HasSerial: true, Grade: "Hypothesis", Status: "accepted"}
	}
	r.Status = "accepted"
	return r
}
