package canbusreg

import (
	"context"
	"errors"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type bmsResult struct {
	bmsObservation BMSObservation
	bmsErr         error
}
type bmsSource struct {
	bmsEntered chan struct{}
	bmsResults chan bmsResult
	bmsLate    *BMSObservation
}

func (bmsS *bmsSource) Receive(bmsCtx context.Context) (BMSObservation, error) {
	select {
	case bmsS.bmsEntered <- struct{}{}:
	case <-bmsCtx.Done():
		return BMSObservation{}, bmsCtx.Err()
	}
	select {
	case bmsR := <-bmsS.bmsResults:
		return bmsR.bmsObservation, bmsR.bmsErr
	case <-bmsCtx.Done():
		if bmsS.bmsLate != nil {
			return *bmsS.bmsLate, nil
		}
		return BMSObservation{}, bmsCtx.Err()
	}
}
func bmsNewSource() *bmsSource {
	return &bmsSource{bmsEntered: make(chan struct{}), bmsResults: make(chan bmsResult)}
}
func bmsIdentity(bmsT *testing.T, bmsName string, bmsIndex int) canbus.InterfaceIdentity {
	bmsT.Helper()
	bmsID, bmsErr := canbus.NewInterfaceIdentity(bmsName, bmsIndex)
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	return bmsID
}
func bmsEvidence(bmsT *testing.T, bmsID canbus.InterfaceIdentity, bmsSeq uint64, bmsFrame uint32) Evidence {
	bmsT.Helper()
	bmsBytes := []byte{0, 1, 0, 2, 2, 0, 50, 16}
	bmsNativeID, bmsErr := canbus.NewStandardID(bmsFrame)
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	bmsNativeFrame, bmsErr := canbus.NewFrame(bmsNativeID, bmsBytes)
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	return Evidence{Interface: bmsID, Sequence: bmsSeq, Frame: bmsNativeFrame, RawRecord: [canbus.SocketCANRecordSize]byte{1, 2, 3}}
}
func bmsProvider(bmsT *testing.T) *GrowattBMSProvider {
	bmsT.Helper()
	bmsP, bmsErr := NewGrowattBMSProvider(bmsIdentity(bmsT, "o08can", 1), BMSRevisionV104)
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	bmsT.Cleanup(bmsP.Stop)
	return bmsP
}
func bmsFeed(bmsP *GrowattBMSProvider, bmsE Evidence) {
	bmsP.bmsMu.Lock()
	defer bmsP.bmsMu.Unlock()
	bmsP.bmsApply(BMSObservation{Generation: 1, Evidence: bmsE})
}
func bmsTuple(bmsT *testing.T, bmsP *GrowattBMSProvider) {
	bmsT.Helper()
	for bmsI, bmsFrame := range []uint32{0x311, 0x312, 0x313} {
		bmsFeed(bmsP, bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI+1), bmsFrame))
	}
	if bmsP.Snapshot().Projection == nil {
		bmsT.Fatal("valid control tuple rejected")
	}
}
func bmsStart(bmsT *testing.T, bmsP *GrowattBMSProvider, bmsS *bmsSource, bmsGeneration uint64) {
	bmsT.Helper()
	if bmsErr := bmsP.Start(bmsS, bmsGeneration); bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	<-bmsS.bmsEntered
}
func TestO08IdentityFences(bmsT *testing.T) {
	for _, bmsCase := range []struct {
		bmsName                    string
		bmsID                      canbus.InterfaceIdentity
		bmsGeneration, bmsSequence uint64
	}{
		{"O08name", bmsIdentity(bmsT, "o08other", 1), 1, 4},
		{"O08index", bmsIdentity(bmsT, "o08can", 2), 1, 4},
		{"O08generation", bmsIdentity(bmsT, "o08can", 1), 2, 4},
		{"O08sequence", bmsIdentity(bmsT, "o08can", 1), 1, 3},
		{"O08olderSequence", bmsIdentity(bmsT, "o08can", 1), 1, 2},
	} {
		bmsT.Run(bmsCase.bmsName, func(bmsT *testing.T) {
			bmsP := bmsProvider(bmsT)
			bmsStart(bmsT, bmsP, bmsNewSource(), 1)
			bmsTuple(bmsT, bmsP)
			bmsBefore := bmsP.Snapshot()
			bmsCandidate := bmsEvidence(bmsT, bmsCase.bmsID, bmsCase.bmsSequence, 0x313)
			bmsCandidate.RawRecord[0] = 9
			bmsP.bmsMu.Lock()
			bmsP.bmsApply(BMSObservation{Generation: bmsCase.bmsGeneration, Evidence: bmsCandidate})
			bmsP.bmsMu.Unlock()
			if bmsP.Snapshot().Raw != bmsBefore.Raw {
				bmsT.Fatal("invalid identity/key changed raw evidence")
			}
			bmsCandidate.Interface = bmsP.bmsInterface
			bmsCandidate.Sequence = 4
			bmsFeed(bmsP, bmsCandidate)
			if bmsP.Snapshot().Projection.MeasurementsEvidence.Sequence != 4 || bmsP.Snapshot().Raw.RawRecord[0] != 9 {
				bmsT.Fatal("valid successor rejected")
			}
		})
	}
}
func TestO08RevisionSelection(bmsT *testing.T) {
	for _, bmsRevision := range []string{"", "V1.07", "V1.08"} {
		if _, bmsErr := NewGrowattBMSProvider(bmsIdentity(bmsT, "o08can", 1), bmsRevision); bmsErr == nil {
			bmsT.Fatal("unsupported revision admitted")
		}
	}
	_ = bmsProvider(bmsT)
	for _, bmsID := range []canbus.InterfaceIdentity{{}} {
		if _, bmsErr := NewGrowattBMSProvider(bmsID, BMSRevisionV104); bmsErr == nil {
			bmsT.Fatal("invalid interface admitted")
		}
	}
}
func TestO08LossRecoveryAndDetachedHistory(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsS := bmsNewSource()
	bmsStart(bmsT, bmsP, bmsS, 1)
	for bmsI, bmsFrame := range []uint32{0x311, 0x312, 0x313} {
		bmsS.bmsResults <- bmsResult{bmsObservation: BMSObservation{Generation: 1, Evidence: bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI+1), bmsFrame)}}
		<-bmsS.bmsEntered
	}
	bmsSnapshot := bmsP.Snapshot()
	if bmsSnapshot.State != "receiving" || bmsSnapshot.Projection == nil {
		bmsT.Fatal("missing current tuple")
	}
	bmsSnapshot.Projection.LimitsEvidence.RawRecord[0] = 99
	bmsSnapshot.Projection.Measurements.SOCPercent = 99
	bmsSnapshot.Raw.RawRecord[0] = 99
	if bmsP.Snapshot().Projection.LimitsEvidence.RawRecord[0] != 1 || bmsP.Snapshot().Projection.Measurements.SOCPercent != 50 || bmsP.Snapshot().Raw.RawRecord[0] != 1 {
		bmsT.Fatal("snapshot aliases provider")
	}
	bmsS.bmsResults <- bmsResult{bmsErr: errors.New("O08 synthetic source loss")}
	<-bmsP.bmsDone
	if bmsP.Snapshot().State != "degraded" || bmsP.Snapshot().Projection == nil {
		bmsT.Fatal("loss discarded history/status")
	}
	if bmsP.Start(bmsNewSource(), 1) == nil {
		bmsT.Fatal("generation reused")
	}
	bmsSuccessor := bmsNewSource()
	bmsStart(bmsT, bmsP, bmsSuccessor, 2)
	bmsFeed(bmsP, bmsEvidence(bmsT, bmsP.bmsInterface, 4, 0x313)) // late predecessor
	if bmsP.Snapshot().ProjectionGeneration != 1 || bmsP.Snapshot().RawGeneration != 1 {
		bmsT.Fatal("late predecessor republished")
	}
	for bmsI, bmsFrame := range []uint32{0x313, 0x311, 0x312} {
		bmsSuccessor.bmsResults <- bmsResult{bmsObservation: BMSObservation{Generation: 2, Evidence: bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI+1), bmsFrame)}}
		<-bmsSuccessor.bmsEntered
		bmsGot := bmsP.Snapshot()
		if bmsI < 2 && (bmsGot.ProjectionGeneration != 1 || bmsGot.State != "awaiting") {
			bmsT.Fatal("successor mixed historical tuple")
		}
	}
	if bmsP.Snapshot().ProjectionGeneration != 2 || bmsP.Snapshot().RawGeneration != 2 {
		bmsT.Fatal("successor failed to publish")
	}
	bmsP.Stop()
	if bmsP.Snapshot().State != "stopped" {
		bmsT.Fatal("stop status")
	}
}
func TestO08StopCancelsJoinsAndFencesLateReceive(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsS := bmsNewSource()
	bmsStart(bmsT, bmsP, bmsS, 1)
	bmsTuple(bmsT, bmsP)
	bmsLate := BMSObservation{Generation: 1, Evidence: bmsEvidence(bmsT, bmsP.bmsInterface, 4, 0x313)}
	bmsS.bmsLate = &bmsLate
	bmsP.Stop()
	select {
	case <-bmsP.bmsDone:
	default:
		bmsT.Fatal("receiver not joined")
	}
	if bmsP.Snapshot().Projection.MeasurementsEvidence.Sequence != 3 {
		bmsT.Fatal("post-stop receive admitted")
	}
	bmsP.Stop()
}

func TestO08MalformedPartialRetainsHistory(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsStart(bmsT, bmsP, bmsNewSource(), 1)
	bmsTuple(bmsT, bmsP)
	bmsBad := bmsEvidence(bmsT, bmsP.bmsInterface, 4, 0x312)
	bmsID, bmsErr := canbus.NewStandardID(0x312)
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	bmsBad.Frame, bmsErr = canbus.NewFrame(bmsID, []byte{1})
	if bmsErr != nil {
		bmsT.Fatal(bmsErr)
	}
	bmsFeed(bmsP, bmsBad)
	bmsGot := bmsP.Snapshot()
	if bmsGot.State != "awaiting" || bmsGot.Raw.Sequence != 4 || bmsGot.Projection.MeasurementsEvidence.Sequence != 3 {
		bmsT.Fatal("malformed partial changed last accepted tuple or lost raw")
	}
	bmsFeed(bmsP, bmsEvidence(bmsT, bmsP.bmsInterface, 5, 0x313))
	if bmsP.Snapshot().State != "awaiting" {
		bmsT.Fatal("malformed tuple was not reset")
	}
	for bmsI, bmsFrame := range []uint32{0x311, 0x312} {
		bmsFeed(bmsP, bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI+6), bmsFrame))
	}
	if bmsP.Snapshot().State != "receiving" || bmsP.Snapshot().Projection.MeasurementsEvidence.Sequence != 5 {
		bmsT.Fatal("valid recovery control failed")
	}
}

func TestO08ActiveSuccessorJoinsAndResetsSequence(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsS := bmsNewSource()
	bmsStart(bmsT, bmsP, bmsS, 1)
	bmsTuple(bmsT, bmsP)
	bmsLate := BMSObservation{Generation: 1, Evidence: bmsEvidence(bmsT, bmsP.bmsInterface, 4, 0x313)}
	bmsS.bmsLate = &bmsLate
	bmsPreviousDone := bmsP.bmsDone
	bmsStart(bmsT, bmsP, bmsNewSource(), 2)
	select {
	case <-bmsPreviousDone:
	default:
		bmsT.Fatal("predecessor not joined")
	}
	if bmsP.Snapshot().Projection.MeasurementsEvidence.Sequence != 3 {
		bmsT.Fatal("late predecessor published during replacement")
	}
	bmsP.bmsMu.Lock()
	for bmsI, bmsFrame := range []uint32{0x311, 0x312, 0x313} {
		bmsP.bmsApply(BMSObservation{Generation: 2, Evidence: bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI+1), bmsFrame)})
	}
	bmsP.bmsMu.Unlock()
	if bmsP.Snapshot().ProjectionGeneration != 2 || bmsP.Snapshot().RawGeneration != 2 {
		bmsT.Fatal("sequence did not reset with successor generation")
	}
}

func TestO08InvalidStartPreservesRunningSource(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsStart(bmsT, bmsP, bmsNewSource(), 1)
	bmsTuple(bmsT, bmsP)
	if bmsP.Start(nil, 2) == nil || bmsP.Start(bmsNewSource(), 0) == nil || bmsP.Start(bmsNewSource(), 1) == nil {
		bmsT.Fatal("invalid start admitted")
	}
	if bmsP.Snapshot().State != "receiving" || bmsP.Snapshot().Generation != 1 {
		bmsT.Fatal("invalid start disrupted current source")
	}
}

func TestO08ZeroFirstSequence(bmsT *testing.T) {
	bmsP := bmsProvider(bmsT)
	bmsStart(bmsT, bmsP, bmsNewSource(), 1)
	for bmsI, bmsFrame := range []uint32{0x311, 0x312, 0x313} {
		bmsFeed(bmsP, bmsEvidence(bmsT, bmsP.bmsInterface, uint64(bmsI), bmsFrame))
	}
	bmsGot := bmsP.Snapshot()
	if bmsGot.Projection == nil || bmsGot.Projection.LimitsEvidence.Sequence != 0 || !bmsGot.HasRaw {
		bmsT.Fatal("zero first sequence was rejected")
	}
}
