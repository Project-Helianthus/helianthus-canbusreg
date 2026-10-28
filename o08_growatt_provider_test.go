package canbusreg

import (
	"context"
	"errors"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type o08Result struct {
	o08Observation O08Observation
	o08Err         error
}
type o08Source struct {
	o08Entered chan struct{}
	o08Results chan o08Result
	o08Late    *O08Observation
}

func (o08S *o08Source) O08Receive(o08Ctx context.Context) (O08Observation, error) {
	select {
	case o08S.o08Entered <- struct{}{}:
	case <-o08Ctx.Done():
		return O08Observation{}, o08Ctx.Err()
	}
	select {
	case o08R := <-o08S.o08Results:
		return o08R.o08Observation, o08R.o08Err
	case <-o08Ctx.Done():
		if o08S.o08Late != nil {
			return *o08S.o08Late, nil
		}
		return O08Observation{}, o08Ctx.Err()
	}
}
func o08NewSource() *o08Source {
	return &o08Source{o08Entered: make(chan struct{}), o08Results: make(chan o08Result)}
}
func o08Identity(o08T *testing.T, o08Name string, o08Index int) canbus.InterfaceIdentity {
	o08T.Helper()
	o08ID, o08Err := canbus.NewInterfaceIdentity(o08Name, o08Index)
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	return o08ID
}
func o08Evidence(o08T *testing.T, o08ID canbus.InterfaceIdentity, o08Seq uint64, o08Frame uint32) Evidence {
	o08T.Helper()
	o08Bytes := []byte{0, 1, 0, 2, 2, 0, 50, 16}
	o08NativeID, o08Err := canbus.NewStandardID(o08Frame)
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	o08NativeFrame, o08Err := canbus.NewFrame(o08NativeID, o08Bytes)
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	return Evidence{Interface: o08ID, Sequence: o08Seq, Frame: o08NativeFrame, RawRecord: [canbus.SocketCANRecordSize]byte{1, 2, 3}}
}
func o08Provider(o08T *testing.T) *O08GrowattProvider {
	o08T.Helper()
	o08P, o08Err := O08NewGrowattProvider(o08Identity(o08T, "o08can", 1), O08RevisionV104)
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	o08T.Cleanup(o08P.O08Stop)
	return o08P
}
func o08Feed(o08P *O08GrowattProvider, o08E Evidence) {
	o08P.o08Mu.Lock()
	defer o08P.o08Mu.Unlock()
	o08P.o08Apply(O08Observation{O08Generation: 1, O08Evidence: o08E})
}
func o08Tuple(o08T *testing.T, o08P *O08GrowattProvider) {
	o08T.Helper()
	for o08I, o08Frame := range []uint32{0x311, 0x312, 0x313} {
		o08Feed(o08P, o08Evidence(o08T, o08P.o08Interface, uint64(o08I+1), o08Frame))
	}
	if o08P.O08Snapshot().O08Projection == nil {
		o08T.Fatal("valid control tuple rejected")
	}
}
func o08Start(o08T *testing.T, o08P *O08GrowattProvider, o08S *o08Source, o08Generation uint64) {
	o08T.Helper()
	if o08Err := o08P.O08Start(o08S, o08Generation); o08Err != nil {
		o08T.Fatal(o08Err)
	}
	<-o08S.o08Entered
}
func TestO08IdentityFences(o08T *testing.T) {
	for _, o08Case := range []struct {
		o08Name                    string
		o08ID                      canbus.InterfaceIdentity
		o08Generation, o08Sequence uint64
	}{
		{"O08name", o08Identity(o08T, "o08other", 1), 1, 4},
		{"O08index", o08Identity(o08T, "o08can", 2), 1, 4},
		{"O08generation", o08Identity(o08T, "o08can", 1), 2, 4},
		{"O08sequence", o08Identity(o08T, "o08can", 1), 1, 3},
		{"O08olderSequence", o08Identity(o08T, "o08can", 1), 1, 2},
	} {
		o08T.Run(o08Case.o08Name, func(o08T *testing.T) {
			o08P := o08Provider(o08T)
			o08Start(o08T, o08P, o08NewSource(), 1)
			o08Tuple(o08T, o08P)
			o08Before := o08P.O08Snapshot()
			o08Candidate := o08Evidence(o08T, o08Case.o08ID, o08Case.o08Sequence, 0x313)
			o08Candidate.RawRecord[0] = 9
			o08P.o08Mu.Lock()
			o08P.o08Apply(O08Observation{O08Generation: o08Case.o08Generation, O08Evidence: o08Candidate})
			o08P.o08Mu.Unlock()
			if o08P.O08Snapshot().O08Raw != o08Before.O08Raw {
				o08T.Fatal("invalid identity/key changed raw evidence")
			}
			o08Candidate.Interface = o08P.o08Interface
			o08Candidate.Sequence = 4
			o08Feed(o08P, o08Candidate)
			if o08P.O08Snapshot().O08Projection.MeasurementsEvidence.Sequence != 4 || o08P.O08Snapshot().O08Raw.RawRecord[0] != 9 {
				o08T.Fatal("valid successor rejected")
			}
		})
	}
}
func TestO08RevisionSelection(o08T *testing.T) {
	for _, o08Revision := range []string{"", "V1.07", "V1.08"} {
		if _, o08Err := O08NewGrowattProvider(o08Identity(o08T, "o08can", 1), o08Revision); o08Err == nil {
			o08T.Fatal("unsupported revision admitted")
		}
	}
	_ = o08Provider(o08T)
	for _, o08ID := range []canbus.InterfaceIdentity{{}} {
		if _, o08Err := O08NewGrowattProvider(o08ID, O08RevisionV104); o08Err == nil {
			o08T.Fatal("invalid interface admitted")
		}
	}
}
func TestO08LossRecoveryAndDetachedHistory(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08S := o08NewSource()
	o08Start(o08T, o08P, o08S, 1)
	for o08I, o08Frame := range []uint32{0x311, 0x312, 0x313} {
		o08S.o08Results <- o08Result{o08Observation: O08Observation{O08Generation: 1, O08Evidence: o08Evidence(o08T, o08P.o08Interface, uint64(o08I+1), o08Frame)}}
		<-o08S.o08Entered
	}
	o08Snapshot := o08P.O08Snapshot()
	if o08Snapshot.O08State != "receiving" || o08Snapshot.O08Projection == nil {
		o08T.Fatal("missing current tuple")
	}
	o08Snapshot.O08Projection.LimitsEvidence.RawRecord[0] = 99
	o08Snapshot.O08Projection.Measurements.SOCPercent = 99
	o08Snapshot.O08Raw.RawRecord[0] = 99
	if o08P.O08Snapshot().O08Projection.LimitsEvidence.RawRecord[0] != 1 || o08P.O08Snapshot().O08Projection.Measurements.SOCPercent != 50 || o08P.O08Snapshot().O08Raw.RawRecord[0] != 1 {
		o08T.Fatal("snapshot aliases provider")
	}
	o08S.o08Results <- o08Result{o08Err: errors.New("O08 synthetic source loss")}
	<-o08P.o08Done
	if o08P.O08Snapshot().O08State != "degraded" || o08P.O08Snapshot().O08Projection == nil {
		o08T.Fatal("loss discarded history/status")
	}
	if o08P.O08Start(o08NewSource(), 1) == nil {
		o08T.Fatal("generation reused")
	}
	o08Successor := o08NewSource()
	o08Start(o08T, o08P, o08Successor, 2)
	o08Feed(o08P, o08Evidence(o08T, o08P.o08Interface, 4, 0x313)) // late predecessor
	if o08P.O08Snapshot().O08ProjectionGeneration != 1 || o08P.O08Snapshot().O08RawGeneration != 1 {
		o08T.Fatal("late predecessor republished")
	}
	for o08I, o08Frame := range []uint32{0x313, 0x311, 0x312} {
		o08Successor.o08Results <- o08Result{o08Observation: O08Observation{O08Generation: 2, O08Evidence: o08Evidence(o08T, o08P.o08Interface, uint64(o08I+1), o08Frame)}}
		<-o08Successor.o08Entered
		o08Got := o08P.O08Snapshot()
		if o08I < 2 && (o08Got.O08ProjectionGeneration != 1 || o08Got.O08State != "awaiting") {
			o08T.Fatal("successor mixed historical tuple")
		}
	}
	if o08P.O08Snapshot().O08ProjectionGeneration != 2 || o08P.O08Snapshot().O08RawGeneration != 2 {
		o08T.Fatal("successor failed to publish")
	}
	o08P.O08Stop()
	if o08P.O08Snapshot().O08State != "stopped" {
		o08T.Fatal("stop status")
	}
}
func TestO08StopCancelsJoinsAndFencesLateReceive(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08S := o08NewSource()
	o08Start(o08T, o08P, o08S, 1)
	o08Tuple(o08T, o08P)
	o08Late := O08Observation{O08Generation: 1, O08Evidence: o08Evidence(o08T, o08P.o08Interface, 4, 0x313)}
	o08S.o08Late = &o08Late
	o08P.O08Stop()
	select {
	case <-o08P.o08Done:
	default:
		o08T.Fatal("receiver not joined")
	}
	if o08P.O08Snapshot().O08Projection.MeasurementsEvidence.Sequence != 3 {
		o08T.Fatal("post-stop receive admitted")
	}
	o08P.O08Stop()
}

func TestO08MalformedPartialRetainsHistory(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08Start(o08T, o08P, o08NewSource(), 1)
	o08Tuple(o08T, o08P)
	o08Bad := o08Evidence(o08T, o08P.o08Interface, 4, 0x312)
	o08ID, o08Err := canbus.NewStandardID(0x312)
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	o08Bad.Frame, o08Err = canbus.NewFrame(o08ID, []byte{1})
	if o08Err != nil {
		o08T.Fatal(o08Err)
	}
	o08Feed(o08P, o08Bad)
	o08Got := o08P.O08Snapshot()
	if o08Got.O08State != "awaiting" || o08Got.O08Raw.Sequence != 4 || o08Got.O08Projection.MeasurementsEvidence.Sequence != 3 {
		o08T.Fatal("malformed partial changed last accepted tuple or lost raw")
	}
	o08Feed(o08P, o08Evidence(o08T, o08P.o08Interface, 5, 0x313))
	if o08P.O08Snapshot().O08State != "awaiting" {
		o08T.Fatal("malformed tuple was not reset")
	}
	for o08I, o08Frame := range []uint32{0x311, 0x312} {
		o08Feed(o08P, o08Evidence(o08T, o08P.o08Interface, uint64(o08I+6), o08Frame))
	}
	if o08P.O08Snapshot().O08State != "receiving" || o08P.O08Snapshot().O08Projection.MeasurementsEvidence.Sequence != 5 {
		o08T.Fatal("valid recovery control failed")
	}
}

func TestO08ActiveSuccessorJoinsAndResetsSequence(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08S := o08NewSource()
	o08Start(o08T, o08P, o08S, 1)
	o08Tuple(o08T, o08P)
	o08Late := O08Observation{O08Generation: 1, O08Evidence: o08Evidence(o08T, o08P.o08Interface, 4, 0x313)}
	o08S.o08Late = &o08Late
	o08PreviousDone := o08P.o08Done
	o08Start(o08T, o08P, o08NewSource(), 2)
	select {
	case <-o08PreviousDone:
	default:
		o08T.Fatal("predecessor not joined")
	}
	if o08P.O08Snapshot().O08Projection.MeasurementsEvidence.Sequence != 3 {
		o08T.Fatal("late predecessor published during replacement")
	}
	o08P.o08Mu.Lock()
	for o08I, o08Frame := range []uint32{0x311, 0x312, 0x313} {
		o08P.o08Apply(O08Observation{O08Generation: 2, O08Evidence: o08Evidence(o08T, o08P.o08Interface, uint64(o08I+1), o08Frame)})
	}
	o08P.o08Mu.Unlock()
	if o08P.O08Snapshot().O08ProjectionGeneration != 2 || o08P.O08Snapshot().O08RawGeneration != 2 {
		o08T.Fatal("sequence did not reset with successor generation")
	}
}

func TestO08InvalidStartPreservesRunningSource(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08Start(o08T, o08P, o08NewSource(), 1)
	o08Tuple(o08T, o08P)
	if o08P.O08Start(nil, 2) == nil || o08P.O08Start(o08NewSource(), 0) == nil || o08P.O08Start(o08NewSource(), 1) == nil {
		o08T.Fatal("invalid start admitted")
	}
	if o08P.O08Snapshot().O08State != "receiving" || o08P.O08Snapshot().O08Generation != 1 {
		o08T.Fatal("invalid start disrupted current source")
	}
}

func TestO08ZeroFirstSequence(o08T *testing.T) {
	o08P := o08Provider(o08T)
	o08Start(o08T, o08P, o08NewSource(), 1)
	for o08I, o08Frame := range []uint32{0x311, 0x312, 0x313} {
		o08Feed(o08P, o08Evidence(o08T, o08P.o08Interface, uint64(o08I), o08Frame))
	}
	o08Got := o08P.O08Snapshot()
	if o08Got.O08Projection == nil || o08Got.O08Projection.LimitsEvidence.Sequence != 0 || !o08Got.O08HasRaw {
		o08T.Fatal("zero first sequence was rejected")
	}
}
