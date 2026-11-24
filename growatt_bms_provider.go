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

// GrowattBMSObservedReport records the generation of a retained native report.
type GrowattBMSObservedReport struct {
	Generation uint64
	Report     GrowattBMSReport
}

// GrowattBMSSnapshot retains the latest raw observation, the last complete V1.04
// tuple and the last valid report of each observed identifier. ProjectionCurrent
// distinguishes a currently complete tuple from historical last-known-good data.
// A historical projection's generation can differ from the current source generation.
type GrowattBMSSnapshot struct {
	State                string
	Generation           uint64
	Raw                  Evidence
	HasRaw               bool
	RawGeneration        uint64
	ProjectionGeneration uint64
	ProjectionCurrent    bool
	Projection           *GrowattLowVoltageBMSV104Projection
	Profile              string
	Reports              map[uint32]GrowattBMSObservedReport
}

// GrowattBMSProvider publishes reports of the explicitly selected revision.
// Construct it with NewGrowattBMSProvider; its zero value is not usable.
type GrowattBMSProvider struct {
	bmsLifecycle   sync.Mutex
	bmsMu          sync.Mutex
	bmsInterface   canbus.InterfaceIdentity
	bmsReplay      *GrowattLowVoltageBMSV104QualificationReplay
	bmsProfile     Profile
	bmsSnapshot    GrowattBMSSnapshot
	bmsRunning     bool
	bmsSequence    uint64
	bmsHasSequence bool
	bmsCancel      context.CancelFunc
	bmsDone        chan struct{}
}

// NewGrowattBMSProvider creates a stopped provider; unsupported revisions or invalid interfaces return an error.
func NewGrowattBMSProvider(bmsInterface canbus.InterfaceIdentity, bmsRevision string) (*GrowattBMSProvider, error) {
	profile, err := NewGrowattBMSProfile(bmsInterface, bmsRevision)
	if err != nil {
		return nil, err
	}
	name, _ := growattBMSRevisionProfile(bmsRevision)
	return &GrowattBMSProvider{bmsInterface: bmsInterface, bmsProfile: profile, bmsSnapshot: GrowattBMSSnapshot{State: "stopped", Profile: name, Reports: make(map[uint32]GrowattBMSObservedReport)}}, nil
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
	if bmsP.bmsSnapshot.Profile == growattLowVoltageBMSV104Profile {
		bmsP.bmsReplay, _ = NewGrowattLowVoltageBMSV104QualificationReplay(bmsP.bmsInterface)
	}
	bmsCtx, bmsCancel := context.WithCancel(context.Background())
	bmsDone := make(chan struct{})
	bmsP.bmsCancel, bmsP.bmsDone = bmsCancel, bmsDone
	bmsP.bmsRunning = true
	bmsP.bmsSequence, bmsP.bmsHasSequence = 0, false
	bmsP.bmsSnapshot.ProjectionCurrent = false
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
	bmsP.bmsSnapshot.ProjectionCurrent = false
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
				bmsP.bmsSnapshot.ProjectionCurrent = false
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
	bmsP.bmsSnapshot.State = "awaiting"
	bmsP.bmsSnapshot.ProjectionCurrent = false
	if bmsP.bmsReplay != nil {
		if projection, ok := bmsP.bmsReplay.Apply(bmsEvidence); ok {
			bmsP.bmsSnapshot.Projection = &projection
			bmsP.bmsSnapshot.ProjectionCurrent = true
			bmsP.bmsSnapshot.ProjectionGeneration = bmsObservation.Generation
			bmsP.bmsSnapshot.State = "receiving"
		}
	}
	if report, ok := bmsP.bmsProfile.Classify(bmsEvidence).Projection.(GrowattBMSReport); ok {
		bmsP.bmsSnapshot.Reports[bmsEvidence.Frame.ID().Value()] = GrowattBMSObservedReport{Generation: bmsObservation.Generation, Report: report}
		bmsP.bmsSnapshot.State = "receiving"
	}
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
	bmsSnapshot.Reports = make(map[uint32]GrowattBMSObservedReport, len(bmsP.bmsSnapshot.Reports))
	for id, observed := range bmsP.bmsSnapshot.Reports {
		fields := make(growattReportFields, len(observed.Report.Fields))
		for name, field := range observed.Report.Fields {
			field.Raw = append([]byte(nil), field.Raw...)
			fields[name] = field
		}
		observed.Report.Fields = fields
		bmsSnapshot.Reports[id] = observed
	}
	return bmsSnapshot
}
