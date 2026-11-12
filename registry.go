package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"time"
)

// Evidence preserves the native frame, interface, sequence, monotonic duration and raw record supplied by transport.
type Evidence struct {
	Frame     canbus.Frame
	Interface canbus.InterfaceIdentity
	Sequence  uint64
	Monotonic time.Duration
	RawRecord [canbus.SocketCANRecordSize]byte
}

// NewEvidence copies a transport observation without interpreting its native record.
func NewEvidence(o canbus.Observation) Evidence {
	return Evidence{o.Frame(), o.Interface(), o.Sequence(), o.MonotonicTimestamp(), o.RawRecord()}
}

// Classification pairs a profile identifier with its projection; an empty identifier or nil projection is not a match.
type Classification struct {
	Profile    string
	Projection any
}
// Profile classifies supplied evidence without owning transport I/O.
type Profile interface{ Classify(Evidence) Classification }
// Registry holds explicitly supplied profiles; overlapping matches fail closed.
type Registry struct{ profiles []Profile }

// NewRegistry copies the profile slice; profile instances remain caller-supplied and are not cloned.
func NewRegistry(profiles ...Profile) Registry { return Registry{append([]Profile(nil), profiles...)} }

// Classify returns the sole usable match, or an empty classification for no match or overlapping profiles.
func (r Registry) Classify(e Evidence) Classification {
	var match Classification
	for _, p := range r.profiles {
		if p == nil {
			continue
		}
		candidate := p.Classify(e)
		if candidate.Profile == "" || candidate.Projection == nil {
			continue
		}
		if match.Profile != "" {
			return Classification{}
		}
		match = candidate
	}
	return match
}
