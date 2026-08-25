package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"time"
)

type Evidence struct {
	Frame     canbus.Frame
	Interface canbus.InterfaceIdentity
	Sequence  uint64
	Monotonic time.Duration
	RawRecord [canbus.SocketCANRecordSize]byte
}

func NewEvidence(o canbus.Observation) Evidence {
	return Evidence{o.Frame(), o.Interface(), o.Sequence(), o.MonotonicTimestamp(), o.RawRecord()}
}

type Classification struct {
	Profile    string
	Projection any
}
type Profile interface{ Classify(Evidence) Classification }
type Registry struct{ profiles []Profile }

func NewRegistry(profiles ...Profile) Registry { return Registry{append([]Profile(nil), profiles...)} }

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
