package canbusreg

import (
	"context"
	"errors"
	"sync"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// BMSRevisionV104 is the explicit revision selector accepted by the BMS provider.
const BMSRevisionV104 = "V1.04"

// BMSObservation binds value-owned native evidence to the caller's source generation.
type BMSObservation struct {
	Generation uint64
	Evidence   Evidence
}

// BMSReceiveSource must unblock Receive when its context is canceled.
// The caller owns acquisition; this interface grants no send or configuration access.
type BMSReceiveSource interface {
	Receive(context.Context) (BMSObservation, error)
}

// GrowattBMSSnapshot retains at most one raw observation and one accepted tuple.
// A historical projection's generation can differ from the current source generation.
type GrowattBMSSnapshot struct {
	State                string
	Generation           uint64
	Raw                  Evidence
	HasRaw               bool
	RawGeneration        uint64
	ProjectionGeneration uint64
	Projection           *GrowattLowVoltageBMSV104Projection
}

// GrowattBMSProvider wraps only the explicitly selected V1.04 replay.
// Construct it with NewGrowattBMSProvider; its zero value is not usable.
type GrowattBMSProvider struct {
	bmsLifecycle   sync.Mutex
	bmsMu          sync.Mutex
	bmsInterface   canbus.InterfaceIdentity
	bmsReplay      *GrowattLowVoltageBMSV104QualificationReplay
	bmsSnapshot    GrowattBMSSnapshot
	bmsRunning     bool
	bmsSequence    uint64
	bmsHasSequence bool
	bmsCancel      context.CancelFunc
	bmsDone        chan struct{}
}

// NewGrowattBMSProvider creates a stopped provider; unsupported revisions or invalid interfaces return an error.
func NewGrowattBMSProvider(bmsInterface canbus.InterfaceIdentity, bmsRevision string) (*GrowattBMSProvider, error) {
	if bmsRevision != BMSRevisionV104 {
		return nil, errors.New("growatt BMS provider requires explicit V1.04 selection")
	}
	bmsReplay, bmsOK := NewGrowattLowVoltageBMSV104QualificationReplay(bmsInterface)
	if !bmsOK {
		return nil, errors.New("growatt BMS provider requires a valid interface")
	}
	return &GrowattBMSProvider{bmsInterface: bmsInterface, bmsReplay: bmsReplay, bmsSnapshot: GrowattBMSSnapshot{State: "stopped"}}, nil
}

// Start cancels and joins any predecessor before installing a fresh replay.
// Generations must be positive and strictly increasing, including after loss/stop.
func (bmsP *GrowattBMSProvider) Start(bmsSource BMSReceiveSource, bmsGeneration uint64) error {
	bmsP.bmsLifecycle.Lock()
	defer bmsP.bmsLifecycle.Unlock()
	bmsP.bmsMu.Lock()
	bmsInvalid := bmsSource == nil || bmsGeneration == 0 || bmsGeneration <= bmsP.bmsSnapshot.Generation
	bmsP.bmsMu.Unlock()
	if bmsInvalid {
		return errors.New("growatt BMS provider requires a source and a successor generation")
	}
	bmsP.bmsStop()
	bmsP.bmsMu.Lock()
	bmsP.bmsReplay, _ = NewGrowattLowVoltageBMSV104QualificationReplay(bmsP.bmsInterface)
	bmsCtx, bmsCancel := context.WithCancel(context.Background())
	bmsDone := make(chan struct{})
	bmsP.bmsCancel, bmsP.bmsDone = bmsCancel, bmsDone
	bmsP.bmsRunning = true
	bmsP.bmsSequence, bmsP.bmsHasSequence = 0, false
	bmsP.bmsSnapshot.Generation = bmsGeneration
	bmsP.bmsSnapshot.State = "awaiting"
	bmsP.bmsMu.Unlock()
	go bmsP.bmsReceive(bmsCtx, bmsSource, bmsGeneration, bmsDone)
	return nil
}

// Stop fences publication before cancellation and waits for receive to exit.
// It retains detached historical evidence and is idempotent.
func (bmsP *GrowattBMSProvider) Stop() {
	bmsP.bmsLifecycle.Lock()
	defer bmsP.bmsLifecycle.Unlock()
	bmsP.bmsStop()
}

func (bmsP *GrowattBMSProvider) bmsStop() {
	bmsP.bmsMu.Lock()
	bmsP.bmsRunning = false
	bmsP.bmsSnapshot.State = "stopped"
	bmsCancel, bmsDone := bmsP.bmsCancel, bmsP.bmsDone
	bmsP.bmsMu.Unlock()
	if bmsCancel != nil {
		bmsCancel()
		<-bmsDone
	}
}

func (bmsP *GrowattBMSProvider) bmsReceive(bmsCtx context.Context, bmsSource BMSReceiveSource, bmsGeneration uint64, bmsDone chan struct{}) {
	defer close(bmsDone)
	for {
		bmsObservation, bmsErr := bmsSource.Receive(bmsCtx)
		bmsP.bmsMu.Lock()
		if bmsErr != nil {
			if bmsP.bmsRunning && bmsP.bmsSnapshot.Generation == bmsGeneration {
				bmsP.bmsRunning = false
				bmsP.bmsSnapshot.State = "degraded"
			}
			bmsP.bmsMu.Unlock()
			return
		}
		bmsP.bmsApply(bmsObservation)
		bmsP.bmsMu.Unlock()
		if bmsCtx.Err() != nil {
			return
		}
	}
}

// bmsApply runs under bmsMu. Rejected identities never enter replay or raw history.
func (bmsP *GrowattBMSProvider) bmsApply(bmsObservation BMSObservation) {
	bmsEvidence := bmsObservation.Evidence
	if !bmsP.bmsRunning {
		return
	}
	if bmsObservation.Generation != bmsP.bmsSnapshot.Generation {
		return
	}
	if bmsEvidence.Interface.Name() != bmsP.bmsInterface.Name() || bmsEvidence.Interface.Index() != bmsP.bmsInterface.Index() {
		return
	}
	if bmsP.bmsHasSequence && bmsEvidence.Sequence <= bmsP.bmsSequence {
		return
	}
	bmsP.bmsSequence, bmsP.bmsHasSequence = bmsEvidence.Sequence, true
	// Evidence and Frame own fixed-width arrays; assignment copies all raw bytes.
	bmsP.bmsSnapshot.Raw, bmsP.bmsSnapshot.HasRaw = bmsEvidence, true
	bmsP.bmsSnapshot.RawGeneration = bmsObservation.Generation
	bmsProjection, bmsOK := bmsP.bmsReplay.Apply(bmsEvidence)
	if !bmsOK {
		bmsP.bmsSnapshot.State = "awaiting"
		return
	}
	bmsP.bmsSnapshot.Projection = &bmsProjection
	bmsP.bmsSnapshot.ProjectionGeneration = bmsObservation.Generation
	bmsP.bmsSnapshot.State = "receiving"
}

// Snapshot returns a synchronized copy with a detached projection.
func (bmsP *GrowattBMSProvider) Snapshot() GrowattBMSSnapshot {
	bmsP.bmsMu.Lock()
	defer bmsP.bmsMu.Unlock()
	bmsSnapshot := bmsP.bmsSnapshot
	if bmsSnapshot.Projection != nil {
		bmsProjection := *bmsSnapshot.Projection
		bmsSnapshot.Projection = &bmsProjection
	}
	return bmsSnapshot
}
