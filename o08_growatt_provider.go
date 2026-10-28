package canbusreg

import (
	"context"
	"errors"
	"sync"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const O08RevisionV104 = "V1.04"

// O08Observation binds value-owned native evidence to the caller's source generation.
type O08Observation struct {
	O08Generation uint64
	O08Evidence   Evidence
}

// O08ReceiveSource must unblock O08Receive when its context is canceled.
// The caller owns acquisition; this interface grants no send or configuration access.
type O08ReceiveSource interface {
	O08Receive(context.Context) (O08Observation, error)
}

// O08GrowattSnapshot retains at most one raw observation and one accepted tuple.
// A historical projection's generation can differ from the current source generation.
type O08GrowattSnapshot struct {
	O08State                string
	O08Generation           uint64
	O08Raw                  Evidence
	O08HasRaw               bool
	O08RawGeneration        uint64
	O08ProjectionGeneration uint64
	O08Projection           *GrowattLowVoltageBMSV104Projection
}

// O08GrowattProvider wraps only the explicitly selected V1.04 replay.
// Construct it with O08NewGrowattProvider; its zero value is not usable.
type O08GrowattProvider struct {
	o08Lifecycle   sync.Mutex
	o08Mu          sync.Mutex
	o08Interface   canbus.InterfaceIdentity
	o08Replay      *GrowattLowVoltageBMSV104QualificationReplay
	o08Snapshot    O08GrowattSnapshot
	o08Running     bool
	o08Sequence    uint64
	o08HasSequence bool
	o08Cancel      context.CancelFunc
	o08Done        chan struct{}
}

func O08NewGrowattProvider(o08Interface canbus.InterfaceIdentity, o08Revision string) (*O08GrowattProvider, error) {
	if o08Revision != O08RevisionV104 {
		return nil, errors.New("O08 requires explicit V1.04 selection")
	}
	o08Replay, o08OK := NewGrowattLowVoltageBMSV104QualificationReplay(o08Interface)
	if !o08OK {
		return nil, errors.New("O08 requires a valid interface")
	}
	return &O08GrowattProvider{o08Interface: o08Interface, o08Replay: o08Replay, o08Snapshot: O08GrowattSnapshot{O08State: "stopped"}}, nil
}

// O08Start cancels and joins any predecessor before installing a fresh replay.
// Generations must be positive and strictly increasing, including after loss/stop.
func (o08P *O08GrowattProvider) O08Start(o08Source O08ReceiveSource, o08Generation uint64) error {
	o08P.o08Lifecycle.Lock()
	defer o08P.o08Lifecycle.Unlock()
	o08P.o08Mu.Lock()
	o08Invalid := o08Source == nil || o08Generation == 0 || o08Generation <= o08P.o08Snapshot.O08Generation
	o08P.o08Mu.Unlock()
	if o08Invalid {
		return errors.New("O08 requires a source and a successor generation")
	}
	o08P.o08Stop()
	o08P.o08Mu.Lock()
	o08P.o08Replay, _ = NewGrowattLowVoltageBMSV104QualificationReplay(o08P.o08Interface)
	o08Ctx, o08Cancel := context.WithCancel(context.Background())
	o08Done := make(chan struct{})
	o08P.o08Cancel, o08P.o08Done = o08Cancel, o08Done
	o08P.o08Running = true
	o08P.o08Sequence, o08P.o08HasSequence = 0, false
	o08P.o08Snapshot.O08Generation = o08Generation
	o08P.o08Snapshot.O08State = "awaiting"
	o08P.o08Mu.Unlock()
	go o08P.o08Receive(o08Ctx, o08Source, o08Generation, o08Done)
	return nil
}

// O08Stop fences publication before cancellation and waits for receive to exit.
// It retains detached historical evidence and is idempotent.
func (o08P *O08GrowattProvider) O08Stop() {
	o08P.o08Lifecycle.Lock()
	defer o08P.o08Lifecycle.Unlock()
	o08P.o08Stop()
}

func (o08P *O08GrowattProvider) o08Stop() {
	o08P.o08Mu.Lock()
	o08P.o08Running = false
	o08P.o08Snapshot.O08State = "stopped"
	o08Cancel, o08Done := o08P.o08Cancel, o08P.o08Done
	o08P.o08Mu.Unlock()
	if o08Cancel != nil {
		o08Cancel()
		<-o08Done
	}
}

func (o08P *O08GrowattProvider) o08Receive(o08Ctx context.Context, o08Source O08ReceiveSource, o08Generation uint64, o08Done chan struct{}) {
	defer close(o08Done)
	for {
		o08Observation, o08Err := o08Source.O08Receive(o08Ctx)
		o08P.o08Mu.Lock()
		if o08Err != nil {
			if o08P.o08Running && o08P.o08Snapshot.O08Generation == o08Generation {
				o08P.o08Running = false
				o08P.o08Snapshot.O08State = "degraded"
			}
			o08P.o08Mu.Unlock()
			return
		}
		o08P.o08Apply(o08Observation)
		o08P.o08Mu.Unlock()
		if o08Ctx.Err() != nil {
			return
		}
	}
}

// o08Apply runs under o08Mu. Rejected identities never enter replay or raw history.
func (o08P *O08GrowattProvider) o08Apply(o08Observation O08Observation) {
	o08Evidence := o08Observation.O08Evidence
	if !o08P.o08Running {
		return
	}
	if o08Observation.O08Generation != o08P.o08Snapshot.O08Generation {
		return
	}
	if o08Evidence.Interface.Name() != o08P.o08Interface.Name() || o08Evidence.Interface.Index() != o08P.o08Interface.Index() {
		return
	}
	if o08P.o08HasSequence && o08Evidence.Sequence <= o08P.o08Sequence {
		return
	}
	o08P.o08Sequence, o08P.o08HasSequence = o08Evidence.Sequence, true
	// Evidence and Frame own fixed-width arrays; assignment copies all raw bytes.
	o08P.o08Snapshot.O08Raw, o08P.o08Snapshot.O08HasRaw = o08Evidence, true
	o08P.o08Snapshot.O08RawGeneration = o08Observation.O08Generation
	o08Projection, o08OK := o08P.o08Replay.Apply(o08Evidence)
	if !o08OK {
		o08P.o08Snapshot.O08State = "awaiting"
		return
	}
	o08P.o08Snapshot.O08Projection = &o08Projection
	o08P.o08Snapshot.O08ProjectionGeneration = o08Observation.O08Generation
	o08P.o08Snapshot.O08State = "receiving"
}

func (o08P *O08GrowattProvider) O08Snapshot() O08GrowattSnapshot {
	o08P.o08Mu.Lock()
	defer o08P.o08Mu.Unlock()
	o08Snapshot := o08P.o08Snapshot
	if o08Snapshot.O08Projection != nil {
		o08Projection := *o08Snapshot.O08Projection
		o08Snapshot.O08Projection = &o08Projection
	}
	return o08Snapshot
}
