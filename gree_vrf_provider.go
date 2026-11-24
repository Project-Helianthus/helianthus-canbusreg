package canbusreg

import (
	"context"
	"errors"
	"maps"
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

// GreeVRFUnitKey separates register families and native unit addresses.
type GreeVRFUnitKey struct {
	Family GreeVRFIdentifierFamily
	Unit   uint8
}

// GreeVRFUnitSnapshot holds native images, the selected device class and
// the independent map and specialised projection for a single generation.
type GreeVRFUnitSnapshot struct {
	Generation  uint64
	Received    time.Duration
	Identifier  GreeVRFIdentifier
	Evidence    Evidence
	Identity    GreeVRFDeviceIdentity
	Images      map[uint8]*GreeVRFRegisterImage
	Writes      map[uint8]GreeVRFTypedPoint
	Clock       *GreeVRFClockPoint
	Candidate   GreeVRFCandidateProjection
	State       GreeVRFMapState
	MatchedRows []int
}

// GreeVRFProviderSnapshot separates active publication from historical units.
// Raw is the latest observation passing the interface and generation fences.
type GreeVRFProviderSnapshot struct {
	State                                         string
	Generation                                    uint64
	MapProfile                                    GreeVRFMapProfile
	ProductID                                     uint16
	Raw                                           Evidence
	HasRaw                                        bool
	RawGeneration                                 uint64
	Units                                         map[GreeVRFUnitKey]GreeVRFUnitSnapshot
	Admitted, Rejected, Unsupported, EvictedUnits uint64
}

// GreeVRFProvider receives register families and runs one explicitly selected map. It holds no command
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
	order     []GreeVRFUnitKey
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
			Units:      make(map[GreeVRFUnitKey]GreeVRFUnitSnapshot),
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

	id, classified := ClassifyGreeVRFIdentifier(evidence.Frame.ID())
	if !classified {
		// The specialised projection defines its own opaque identifier variants.
		// They are not interpreted as images or fed to the maps.
		candidate, ok := GreeVRFCandidate().Classify(evidence).Projection.(GreeVRFCandidateProjection)
		if !ok {
			p.snapshot.Unsupported++
			return
		}
		id = GreeVRFIdentifier{GreeVRFData, candidate.Class8, candidate.Opaque7, candidate.Unit7, candidate.Opcode7}
		key, unit := p.greeVRFProviderUnit(id, observation)
		unit.Candidate = candidate
		p.greeVRFProviderPublish(key, unit)
		return
	}
	key, unit := p.greeVRFProviderUnit(id, observation)
	switch id.Family {
	case GreeVRFClock:
		point, ok := DecodeGreeVRFClock(evidence)
		if !ok {
			p.snapshot.Unsupported++
			return
		}
		unit.Clock = &point
	case GreeVRFAddressedWrite:
		point, ok := DecodeGreeVRFAddressedWrite(evidence)
		if !ok {
			p.snapshot.Unsupported++
			return
		}
		if unit.Writes == nil {
			unit.Writes = make(map[uint8]GreeVRFTypedPoint)
		}
		unit.Writes[id.Type] = point
	case GreeVRFData, GreeVRFAddressedData:
		if !p.greeVRFProviderApplyRegister(&unit, id, evidence) {
			p.snapshot.Unsupported++
			return
		}
		if candidate, ok := GreeVRFCandidate().Classify(evidence).Projection.(GreeVRFCandidateProjection); ok {
			unit.Candidate = candidate
		}
	}
	p.greeVRFProviderPublish(key, unit)
}

func (p *GreeVRFProvider) greeVRFProviderUnit(id GreeVRFIdentifier, observation GreeVRFObservation) (GreeVRFUnitKey, GreeVRFUnitSnapshot) {
	key := GreeVRFUnitKey{id.Family, id.Unit}
	unit := p.snapshot.Units[key]
	if unit.Generation != observation.Generation {
		unit = GreeVRFUnitSnapshot{Generation: observation.Generation, Images: make(map[uint8]*GreeVRFRegisterImage)}
	}
	unit.Identifier, unit.Evidence, unit.Received = id, observation.Evidence, observation.Evidence.Monotonic
	return key, unit
}

func (p *GreeVRFProvider) greeVRFProviderApplyRegister(unit *GreeVRFUnitSnapshot, id GreeVRFIdentifier, evidence Evidence) bool {
	image := unit.Images[id.Type]
	if image == nil {
		image = NewGreeVRFRegisterImage(GreeVRFRegisterKey{evidence.Interface, id.Family, id.Unit, id.Type})
	}
	rows := greeVRFDictionary[id.Type]
	bits := len(rows) != 0 && rows[0].Kind != GreeVRFDictionaryByte
	if !image.Apply(evidence, bits) {
		return false
	}
	unit.Images[id.Type] = image
	if id.Type == 0x10 {
		unit.Identity = image.DeviceIdentity()
	}
	if state, rows, ok := p.decoder.ApplyRegister(unit.State, image); ok {
		unit.State, unit.MatchedRows = state, rows
	}
	return true
}

func (p *GreeVRFProvider) greeVRFProviderPublish(key GreeVRFUnitKey, unit GreeVRFUnitSnapshot) {
	p.greeVRFProviderRetain(key, unit)
	p.snapshot.Admitted++
	p.snapshot.State = "receiving"
}

// greeVRFProviderRetain stores one unit entry as the most recently received
// and evicts the least recently received entry beyond the bound.
func (p *GreeVRFProvider) greeVRFProviderRetain(key GreeVRFUnitKey, unit GreeVRFUnitSnapshot) {
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
	snapshot.Units = make(map[GreeVRFUnitKey]GreeVRFUnitSnapshot, len(p.snapshot.Units))
	for key, unit := range p.snapshot.Units {
		if unit.State.Cells != nil {
			unit.State = cloneGreeVRFMapState(unit.State)
		}
		unit.Images = maps.Clone(unit.Images)
		for typ, image := range unit.Images {
			clone := *image
			clone.fragments = maps.Clone(image.fragments)
			unit.Images[typ] = &clone
		}
		if unit.Clock != nil {
			clock := *unit.Clock
			unit.Clock = &clock
		}
		unit.Writes = maps.Clone(unit.Writes)
		for typ, point := range unit.Writes {
			point.Row = point.Row.detached()
			point.Value.Raw = append([]byte(nil), point.Value.Raw...)
			point.Value.Bits = maps.Clone(point.Value.Bits)
			unit.Writes[typ] = point
		}
		unit.Candidate.Updates = append([]GreeVRFCandidateStateCell(nil), unit.Candidate.Updates...)
		unit.MatchedRows = append([]int(nil), unit.MatchedRows...)
		snapshot.Units[key] = unit
	}
	return snapshot
}
