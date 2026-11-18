package canbusreg

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// GreeVRFProviderMaxUnits bounds the per-unit entries a GreeVRFProvider
// retains. Admitting a further unit evicts the least recently received one.
const GreeVRFProviderMaxUnits = 32

// GreeVRFObservation binds value-owned native evidence to the caller's source generation.
type GreeVRFObservation struct {
	Generation uint64
	Evidence   Evidence
}

// GreeVRFReceiveSource must unblock Receive when its context is canceled.
// The caller owns acquisition; the interface grants no transmit,
// acknowledgement, probing or configuration capability.
type GreeVRFReceiveSource interface {
	Receive(context.Context) (GreeVRFObservation, error)
}

// GreeVRFUnitSnapshot is the opaque state of one native unit key. Candidate
// retains the last admitted native evidence and its one-byte candidate cells;
// State holds the selected map's opaque cells accumulated within Generation;
// Received is the transport monotonic time of the last admitted evidence.
// None of these establishes a device identity or an HVAC meaning.
type GreeVRFUnitSnapshot struct {
	Generation  uint64
	Received    time.Duration
	Candidate   GreeVRFCandidateProjection
	State       GreeVRFMapState
	MatchedRows []int
}

// GreeVRFProviderSnapshot separates lifecycle state from retained history.
// Units is keyed by the native Opaque7 identifier field of the admitted
// candidate (Class8 and Unit7 are fixed by admission) and holds at most
// GreeVRFProviderMaxUnits entries; an entry whose Generation differs from
// Generation is historical. Raw is the latest observation that passed the
// identity fences, admitted or not. Counters accumulate over the provider's
// lifetime: Admitted observations, Rejected by the identity fences,
// Unsupported by the candidate classifier or the selected map, and
// EvictedUnits by the bound.
type GreeVRFProviderSnapshot struct {
	State                                         string
	Generation                                    uint64
	MapProfile                                    GreeVRFMapProfile
	ProductID                                     uint16
	Raw                                           Evidence
	HasRaw                                        bool
	RawGeneration                                 uint64
	Units                                         map[uint8]GreeVRFUnitSnapshot
	Admitted, Rejected, Unsupported, EvictedUnits uint64
}

// GreeVRFProvider is the receive-only lifecycle around the candidate
// classifier and one explicitly selected offline map. It holds no command
// encoder and no transmit path. Construct it with NewGreeVRFProvider; its
// zero value is not usable. Start and Stop join the canceled predecessor;
// sources must honor cancellation.
type GreeVRFProvider struct {
	lifecycle sync.Mutex
	mu        sync.Mutex
	source    canbus.InterfaceIdentity
	decoder   GreeVRFMapDecoder
	snapshot  GreeVRFProviderSnapshot
	running   bool
	sequence  uint64
	monotonic time.Duration
	order     []uint8
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewGreeVRFProvider creates a stopped provider for one interface and one
// explicit map selection; an invalid interface or unknown map returns an
// error. The selection is not a device-identification or qualification result.
func NewGreeVRFProvider(source canbus.InterfaceIdentity, mapProfile GreeVRFMapProfile, productID uint16) (*GreeVRFProvider, error) {
	if _, ok := NewGreeVRFQualificationReplay(source, mapProfile, productID); !ok {
		return nil, errors.New("gree VRF provider requires a valid interface and an explicit map selection")
	}
	return &GreeVRFProvider{
		source:  source,
		decoder: NewGreeVRFMapDecoder(mapProfile, productID),
		snapshot: GreeVRFProviderSnapshot{
			State:      "stopped",
			MapProfile: mapProfile,
			ProductID:  productID,
			Units:      make(map[uint8]GreeVRFUnitSnapshot),
		},
	}, nil
}

// Start joins the predecessor and starts receiving with a strictly increasing
// positive generation; a nil source or a reused generation returns an error.
// Map state never carries over from a predecessor generation.
func (p *GreeVRFProvider) Start(source GreeVRFReceiveSource, generation uint64) error {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	invalid := source == nil || generation == 0 || generation <= p.snapshot.Generation
	p.mu.Unlock()
	if invalid {
		return errors.New("gree VRF provider requires a source and a successor generation")
	}
	p.greeVRFProviderStop()
	p.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.cancel, p.done = cancel, done
	p.running = true
	p.sequence, p.monotonic = 0, 0
	p.snapshot.Generation = generation
	p.snapshot.State = "awaiting"
	p.mu.Unlock()
	go p.greeVRFProviderReceive(ctx, source, generation, done)
	return nil
}

// Stop fences publication before cancellation and waits for the receive loop
// to exit. It retains detached historical evidence and is idempotent.
func (p *GreeVRFProvider) Stop() {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.greeVRFProviderStop()
}

func (p *GreeVRFProvider) greeVRFProviderStop() {
	p.mu.Lock()
	p.running = false
	p.snapshot.State = "stopped"
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (p *GreeVRFProvider) greeVRFProviderReceive(ctx context.Context, source GreeVRFReceiveSource, generation uint64, done chan struct{}) {
	defer close(done)
	for {
		observation, err := source.Receive(ctx)
		p.mu.Lock()
		if !p.running || p.snapshot.Generation != generation || ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		if err != nil {
			p.running = false
			p.snapshot.State = "degraded"
			p.mu.Unlock()
			return
		}
		p.greeVRFProviderApply(observation)
		p.mu.Unlock()
	}
}

// greeVRFProviderApply runs under mu. Fenced observations never reach raw
// history; the fences advance only for observations that pass them. Start
// resets both fences to zero, so a zero sequence or a negative monotonic time
// is always fenced.
func (p *GreeVRFProvider) greeVRFProviderApply(observation GreeVRFObservation) {
	evidence := observation.Evidence
	if observation.Generation != p.snapshot.Generation || !greeVRFSameInterface(p.source, evidence.Interface) ||
		evidence.Sequence <= p.sequence || evidence.Monotonic < p.monotonic {
		p.snapshot.Rejected++
		return
	}
	p.sequence, p.monotonic = evidence.Sequence, evidence.Monotonic
	// Evidence and Frame own fixed-width arrays; assignment copies all raw bytes.
	p.snapshot.Raw, p.snapshot.HasRaw, p.snapshot.RawGeneration = evidence, true, observation.Generation

	classification := NewRegistry(GreeVRFCandidate()).Classify(evidence)
	candidate, ok := classification.Projection.(GreeVRFCandidateProjection)
	if classification.Profile != GreeVRFCandidateProfile || !ok {
		p.snapshot.Unsupported++
		return
	}
	var initial GreeVRFMapState
	if unit, found := p.snapshot.Units[candidate.Opaque7]; found && unit.Generation == observation.Generation {
		initial = unit.State
	}
	state, rows, ok := p.decoder.Apply(initial, candidate.Opcode7, evidence.Frame.Data())
	if !ok {
		p.snapshot.Unsupported++
		return
	}
	p.greeVRFProviderRetain(candidate.Opaque7, GreeVRFUnitSnapshot{
		Generation:  observation.Generation,
		Received:    evidence.Monotonic,
		Candidate:   candidate,
		State:       state,
		MatchedRows: rows,
	})
	p.snapshot.Admitted++
	p.snapshot.State = "receiving"
}

// greeVRFProviderRetain stores one unit entry as the most recently received
// and evicts the least recently received entry beyond the bound.
func (p *GreeVRFProvider) greeVRFProviderRetain(key uint8, unit GreeVRFUnitSnapshot) {
	for index, known := range p.order {
		if known == key {
			p.order = append(p.order[:index], p.order[index+1:]...)
			break
		}
	}
	if len(p.order) == GreeVRFProviderMaxUnits {
		delete(p.snapshot.Units, p.order[0])
		copy(p.order, p.order[1:])
		p.order = p.order[:len(p.order)-1]
		p.snapshot.EvictedUnits++
	}
	p.order = append(p.order, key)
	p.snapshot.Units[key] = unit
}

// Snapshot returns a synchronized copy whose maps and slices are detached
// from the provider.
func (p *GreeVRFProvider) Snapshot() GreeVRFProviderSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.snapshot
	snapshot.Units = make(map[uint8]GreeVRFUnitSnapshot, len(p.snapshot.Units))
	for key, unit := range p.snapshot.Units {
		unit.State = cloneGreeVRFMapState(unit.State)
		unit.Candidate.Updates = append([]GreeVRFCandidateStateCell(nil), unit.Candidate.Updates...)
		unit.MatchedRows = append([]int(nil), unit.MatchedRows...)
		snapshot.Units[key] = unit
	}
	return snapshot
}
