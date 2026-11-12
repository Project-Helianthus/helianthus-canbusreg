package canbusreg

import (
	"fmt"
	"sync"
)

type CapabilityState string

const (
	ReceiverCapabilityUnknown     CapabilityState = "unknown"
	ReceiverCapabilityObserved    CapabilityState = "observed"
	ReceiverCapabilityQualified   CapabilityState = "qualified"
	ReceiverCapabilityUnsupported CapabilityState = "unsupported"
)

// Capability keys include the receiver identity and direction. A caller's
// independent qualification assertion is never physical qualification.
type CapabilityKey struct {
	Selection CXSelection
	Context   ReceiverContext
	PID       uint32
}

type receiverCapabilityCapability struct {
	key   CapabilityKey
	state CapabilityState
}
type receiverCapabilityRetained struct {
	selection CXSelection
	result    ReceiverResult
}

// CX15OpaqueStore is a bounded, receive-only evidence journal. Rejections enter
// the journal but cannot replace an independently admitted last-known-good value.
// Native records and their ages are copied verbatim; no timer units are inferred.
type OpaqueStore struct {
	mu           sync.Mutex
	limit        int
	journal      []ReceiverResult
	good         []receiverCapabilityRetained
	capabilities []receiverCapabilityCapability
}

func NewCX15OpaqueStore(limit int) (*OpaqueStore, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("raw retention bound must be 1..256")
	}
	return &OpaqueStore{limit: limit}, nil
}

func (s *OpaqueStore) Capability(key CapabilityKey) CapabilityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.capabilities {
		if v.key == key {
			return v.state
		}
	}
	return ReceiverCapabilityUnknown
}

// AssertCapability records only external capability evidence. Classification and
// ordinary output observations never create a qualified capability assertion.
func (s *OpaqueStore) AssertCapability(key CapabilityKey, state CapabilityState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit < 1 {
		return fmt.Errorf("raw store requires a retention bound")
	}
	if state != ReceiverCapabilityUnknown && state != ReceiverCapabilityObserved && state != ReceiverCapabilityQualified && state != ReceiverCapabilityUnsupported {
		return fmt.Errorf("invalid capability state")
	}
	s.setCapability(key, state)
	return nil
}
func (s *OpaqueStore) setCapability(key CapabilityKey, state CapabilityState) {
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
	s.capabilities = append(s.capabilities, receiverCapabilityCapability{key, state})
}
func receiverCapabilitySameStream(a receiverCapabilityRetained, selection CXSelection, r ReceiverResult) bool {
	return a.selection == selection && a.result.Context.Direction == r.Context.Direction && a.result.Context.Role == r.Context.Role && a.result.Context.Profile == r.Context.Profile && a.result.Context.Form == r.Context.Form && a.result.Input.Evidence.Frame.ID() == r.Input.Evidence.Frame.ID()
}

func (s *OpaqueStore) Observe(selection CXSelection, c ReceiverContext, i LINKInput) ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := ClassifyReceiver(selection, c, i)
	if s.limit < 1 {
		r.Admitted = false
		r.Status = "invalid"
		return r
	}
	pid := i.Evidence.Frame.ID().Value()
	if i.Evidence.Frame.ID().Extended() {
		pid >>= 16
	}
	key := CapabilityKey{selection, c, pid}
	for _, v := range s.capabilities {
		if v.key == key && v.state == ReceiverCapabilityUnsupported {
			r.Admitted = false
			r.Status = "unsupported"
			break
		}
	}
	index := -1
	for n, v := range s.good {
		if receiverCapabilitySameStream(v, selection, r) {
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
		value := receiverCapabilityRetained{selection, r}
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
			s.setCapability(key, ReceiverCapabilityObserved)
		}
	}
	return r
}
func (s *OpaqueStore) RawSnapshot() []ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ReceiverResult(nil), s.journal...)
}
func (s *OpaqueStore) LastKnownGood() []ReceiverResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ReceiverResult, len(s.good))
	for n, v := range s.good {
		out[n] = v.result
	}
	return out
}
