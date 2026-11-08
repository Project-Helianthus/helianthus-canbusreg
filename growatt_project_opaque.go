package canbusreg

import (
	"fmt"
	"sync"
)

type CX15CapabilityState string

const (
	CX15Unknown     CX15CapabilityState = "unknown"
	CX15Observed    CX15CapabilityState = "observed"
	CX15Qualified   CX15CapabilityState = "qualified"
	CX15Unsupported CX15CapabilityState = "unsupported"
)

// Capability keys include the receiver identity and direction. A caller's
// independent qualification assertion is never physical qualification.
type CX15CapabilityKey struct {
	Selection CXSelection
	Context   CX15ReceiverContext
	PID       uint32
}

type cx15Capability struct {
	key   CX15CapabilityKey
	state CX15CapabilityState
}
type cx15Retained struct {
	selection CXSelection
	result    CX15ReceiverResult
}

// CX15OpaqueStore is a bounded, receive-only evidence journal. Rejections enter
// the journal but cannot replace an independently admitted last-known-good value.
// Native records and their ages are copied verbatim; no timer units are inferred.
type CX15OpaqueStore struct {
	mu           sync.Mutex
	limit        int
	journal      []CX15ReceiverResult
	good         []cx15Retained
	capabilities []cx15Capability
}

func NewCX15OpaqueStore(limit int) (*CX15OpaqueStore, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("raw retention bound must be 1..256")
	}
	return &CX15OpaqueStore{limit: limit}, nil
}

func (s *CX15OpaqueStore) Capability(key CX15CapabilityKey) CX15CapabilityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.capabilities {
		if v.key == key {
			return v.state
		}
	}
	return CX15Unknown
}

// AssertCapability records only external capability evidence. Classification and
// ordinary output observations never create a qualified capability assertion.
func (s *CX15OpaqueStore) AssertCapability(key CX15CapabilityKey, state CX15CapabilityState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit < 1 {
		return fmt.Errorf("raw store requires a retention bound")
	}
	if state != CX15Unknown && state != CX15Observed && state != CX15Qualified && state != CX15Unsupported {
		return fmt.Errorf("invalid capability state")
	}
	s.setCapability(key, state)
	return nil
}
func (s *CX15OpaqueStore) setCapability(key CX15CapabilityKey, state CX15CapabilityState) {
	for n, v := range s.capabilities {
		if v.key == key {
			s.capabilities[n].state = state
			return
		}
	}
	if len(s.capabilities) == s.limit {
		copy(s.capabilities, s.capabilities[1:])
		s.capabilities = s.capabilities[:s.limit-1]
	}
	s.capabilities = append(s.capabilities, cx15Capability{key, state})
}
func cx15SameStream(a cx15Retained, selection CXSelection, r CX15ReceiverResult) bool {
	return a.selection == selection && a.result.Context.Direction == r.Context.Direction && a.result.Context.Role == r.Context.Role && a.result.Context.Profile == r.Context.Profile && a.result.Context.Form == r.Context.Form && a.result.Input.Evidence.Frame.ID() == r.Input.Evidence.Frame.ID()
}

func (s *CX15OpaqueStore) Observe(selection CXSelection, c CX15ReceiverContext, i CX01LINKInput) CX15ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := CX15ClassifyReceiver(selection, c, i)
	if s.limit < 1 {
		r.Admitted = false
		r.Status = "invalid"
		return r
	}
	pid := i.Evidence.Frame.ID().Value()
	if i.Evidence.Frame.ID().Extended() {
		pid >>= 16
	}
	key := CX15CapabilityKey{selection, c, pid}
	for _, v := range s.capabilities {
		if v.key == key && v.state == CX15Unsupported {
			r.Admitted = false
			r.Status = "unsupported"
			break
		}
	}
	index := -1
	for n, v := range s.good {
		if cx15SameStream(v, selection, r) {
			index = n
			break
		}
	}
	if r.Admitted && index >= 0 {
		old := s.good[index].result.Input.Evidence
		if i.Evidence.Sequence <= old.Sequence || i.Evidence.Monotonic < old.Monotonic {
			r.Admitted = false
			r.Status = "stale"
		}
	}
	if len(s.journal) == s.limit {
		copy(s.journal, s.journal[1:])
		s.journal = s.journal[:s.limit-1]
	}
	s.journal = append(s.journal, r)
	if r.Admitted {
		value := cx15Retained{selection, r}
		if index >= 0 {
			s.good[index] = value
		} else {
			if len(s.good) == s.limit {
				copy(s.good, s.good[1:])
				s.good = s.good[:s.limit-1]
			}
			s.good = append(s.good, value)
		}
		found := false
		for _, v := range s.capabilities {
			if v.key == key {
				found = true
				break
			}
		}
		if !found {
			s.setCapability(key, CX15Observed)
		}
	}
	return r
}
func (s *CX15OpaqueStore) RawSnapshot() []CX15ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CX15ReceiverResult(nil), s.journal...)
}
func (s *CX15OpaqueStore) LastKnownGood() []CX15ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CX15ReceiverResult, len(s.good))
	for n, v := range s.good {
		out[n] = v.result
	}
	return out
}
