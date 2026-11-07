package canbusreg

import (
	"fmt"
	"sync"
	"time"
)

// CX01LINKIdentitySnapshot is detached candidate evidence, not authentication.
// It carries a single explicitly selected source; allocation is bounded.
type CX01LINKIdentitySnapshot struct {
	Selection                    CXSelection
	Grade                        string
	ProjectCode, Release, Status string
	Serial                       [16]byte
	HasIdentity, HasSerial       bool
	IdentityEvidence             Evidence
	SerialEvidence               [3]Evidence
	Fields                       map[string]CXNativeField
	PhysicalQualified            bool
}
type CX01LINKIdentityAssembler struct {
	mu            sync.Mutex
	selection     CXSelection
	snapshot      CX01LINKIdentitySnapshot
	next          uint8
	assembling    bool
	fragments     [16]byte
	evidence      [3]Evidence
	lastSequence  uint64
	lastMonotonic time.Duration
}

func CX01NewLINKIdentityAssembler(s CXSelection) *CX01LINKIdentityAssembler {
	return &CX01LINKIdentityAssembler{selection: s, snapshot: CX01LINKIdentitySnapshot{Selection: s, Grade: "Unknown", Status: "absent", Fields: make(map[string]CXNativeField)}}
}
func (a *CX01LINKIdentityAssembler) CX01Snapshot() CX01LINKIdentitySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cx01Snapshot()
}
func (a *CX01LINKIdentityAssembler) cx01Snapshot() CX01LINKIdentitySnapshot {
	s := a.snapshot
	s.Fields = make(map[string]CXNativeField, len(a.snapshot.Fields))
	for k, v := range a.snapshot.Fields {
		s.Fields[k] = v
	}
	return s
}
func (a *CX01LINKIdentityAssembler) cx01Invalidate() {
	a.snapshot = CX01LINKIdentitySnapshot{Selection: a.selection, Grade: "Unknown", Status: "conflict", Fields: make(map[string]CXNativeField)}
	a.assembling = false
	a.fragments = [16]byte{}
	a.evidence = [3]Evidence{}
}

// Decode validates identity continuity and retains independent last-known-good fields.
// A conflict is sticky: a changed role, identity or epoch needs a new selection/assembler.
func (a *CX01LINKIdentityAssembler) CX01Decode(i CX01LINKInput) CXNativeResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := cx01Result(a.selection, i, "invalid")
	c := a.selection.spec
	f := i.Evidence.Frame
	id := f.ID().Value()
	routed := c.Flavor == "link" && c.RouteSet && f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 && id>>24 == 0x18 && uint8(id>>8) == c.Destination && uint8(id) == c.Source
	if routed && i.Evidence.Interface == c.Interface && (i.Epoch != c.Epoch || i.Direction != c.Direction || i.Endpoint != c.Endpoint || i.Bank != c.Bank || i.Group != c.Group || i.Slot != c.Slot || i.Scope != c.Scope) {
		a.cx01Invalidate()
	}
	if a.snapshot.Status == "conflict" {
		r.Status = "conflict"
		return r
	}
	if !cx01LINKAdmission(a.selection, i) {
		// Unrelated traffic must not disturb an in-progress serial assembly.
		return r
	}
	if i.Evidence.Sequence <= a.lastSequence || i.Evidence.Monotonic < a.lastMonotonic {
		r.Status = "stale"
		a.assembling = false
		return r
	}
	a.lastSequence = i.Evidence.Sequence
	a.lastMonotonic = i.Evidence.Monotonic
	kind := uint16(i.Evidence.Frame.ID().Value() >> 16)
	p := r.Raw
	if kind != 0x18fb && a.assembling {
		a.assembling = false
	}
	if kind == 0x1802 {
		if !cx01ASCII(p[:]) {
			r.Status = "invalid"
			return r
		}
		code, release := string(p[:4]), string(p[4:])
		if code != c.ProjectCode || release != c.Release || (a.snapshot.HasIdentity && (code != a.snapshot.ProjectCode || release != a.snapshot.Release)) {
			a.cx01Invalidate()
			r.Status = "conflict"
			return r
		}
		a.snapshot.ProjectCode = code
		a.snapshot.Release = release
		a.snapshot.HasIdentity = true
		a.snapshot.IdentityEvidence = i.Evidence
		a.snapshot.Status = "accepted"
		a.snapshot.Grade = "Proven"
	}
	if kind == 0x18fb {
		if a.selection.group == "" || a.selection.group == "L0" || !a.snapshot.HasIdentity {
			r.Status = "unsupported"
			return r
		}
		length := (p[0] & 31) + 1
		fragment := (p[0] >> 5) & 3
		if p[0]&0x80 != 0 || length != 16 || fragment > 2 || (!a.assembling && fragment != 0) || (a.assembling && fragment != a.next) {
			a.assembling = false
			r.Status = "invalid"
			return r
		}
		if fragment == 0 {
			a.fragments = [16]byte{}
			a.evidence = [3]Evidence{}
			a.assembling = true
		}
		if fragment == 2 {
			for _, b := range p[3:] {
				if b != 0 {
					a.assembling = false
					r.Status = "invalid"
					return r
				}
			}
			copy(a.fragments[14:], p[1:3])
		} else {
			copy(a.fragments[int(fragment)*7:], p[1:])
		}
		a.evidence[fragment] = i.Evidence
		a.next = fragment + 1
		if fragment == 2 {
			if a.snapshot.HasSerial && a.snapshot.Serial != a.fragments {
				a.cx01Invalidate()
				r.Status = "conflict"
				return r
			}
			a.snapshot.Serial = a.fragments
			a.snapshot.SerialEvidence = a.evidence
			a.snapshot.HasSerial = true
			a.assembling = false
		}
		r.Status = "accepted"
		return r
	}
	if kind == 0x1800 && a.snapshot.HasIdentity {
		if old, ok := a.snapshot.Fields["1800/role_flags"]; ok && old.Numerator != int64(p[1]) {
			a.cx01Invalidate()
			r.Status = "conflict"
			return r
		}
	}
	r = CXDecodeLINKTelemetry(a.selection, i)
	if r.Status == "conflict" {
		a.cx01Invalidate()
		return r
	}
	if !a.snapshot.HasIdentity && kind != 0x1802 {
		for name, f := range r.Fields {
			if f.Status == "accepted" {
				f.Status = "absent"
				f.Meaning = ""
				f.Unit = ""
				r.Fields[name] = f
			}
		}
	}
	if r.Status == "accepted" {
		for name, f := range r.Fields {
			if f.Status != "invalid" && f.Status != "absent" {
				a.snapshot.Fields[fmt.Sprintf("%04x/%s", kind, name)] = f
			}
		}
	}
	return r
}
