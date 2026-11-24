package canbusreg

import (
	"errors"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
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

// NewGrowattBMSProfile registers one report layout for one explicitly configured interface.
// Use GrowattLowVoltageBMSV104 separately for the legacy tuple-only classifier.
func NewGrowattBMSProfile(source canbus.InterfaceIdentity, revision string) (Profile, error) {
	name, err := growattBMSRevisionProfile(revision)
	if err != nil {
		return nil, err
	}
	if source.Name() == "" || source.Index() <= 0 {
		return nil, errors.New("growatt BMS profile requires a valid interface")
	}
	return growattBMSReportProfile{source: source, revision: revision, name: name}, nil
}

type growattBMSReportProfile struct {
	source         canbus.InterfaceIdentity
	revision, name string
}

func (p growattBMSReportProfile) Classify(e Evidence) Classification {
	if !growattV104SameInterface(p.source, e.Interface) {
		return Classification{}
	}
	report, ok := decodeGrowattBMSReport(e, p.revision)
	if !ok {
		return Classification{}
	}
	return Classification{Profile: p.name, Projection: report}
}
