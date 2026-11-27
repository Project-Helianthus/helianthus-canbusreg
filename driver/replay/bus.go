package replay

import (
	"errors"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/periodic"
)

// Bus is a simulated bus. Submit records every accepted frame in order and
// opens no platform interface.
type Bus struct {
	sim *periodic.Simulated
}

// NewBus returns a bus that stamps each receipt with now. now is required.
func NewBus(now func() time.Duration) (*Bus, error) {
	if now == nil {
		return nil, errors.New("replay bus requires a clock")
	}
	return &Bus{sim: periodic.NewSimulated(now)}, nil
}

// Submit records one frame.
func (b *Bus) Submit(frame canbus.Frame) (periodic.Submission, error) {
	if b == nil || b.sim == nil {
		return periodic.Submission{}, errors.New("nil replay bus")
	}
	return b.sim.Submit(frame)
}

// Frames returns the accepted frames in submission order.
func (b *Bus) Frames() []periodic.Sent {
	if b == nil || b.sim == nil {
		return nil
	}
	return b.sim.Frames()
}
