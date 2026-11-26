package growattlowvoltage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	clockEventIdentifier uint32 = 0x211

	// EventRepeats is the number of consecutive sends of one triggered 0x211.
	EventRepeats = 5
	// EventPeriod is the period between those sends.
	EventPeriod = time.Second

	// ClockEventRowID is the native write row of the V1.08 0x211 frame.
	ClockEventRowID = Profile + ".command.211"
)

var clockEventRow = row{
	id:         ClockEventRowID,
	identifier: clockEventIdentifier,
	fields: []field{
		{name: "frequency_enable", index: 0, min: 0, max: 1},
		{name: "year", index: 1, min: 20, max: 250, unit: "year"},
		{name: "month", index: 2, min: 1, max: 12, unit: "month"},
		{name: "day", index: 3, min: 1, max: 31, unit: "day"},
		{name: "hour", index: 4, min: 0, max: 23, unit: "hour"},
		{name: "minute", index: 5, min: 0, max: 59, unit: "minute"},
		{name: "second", index: 6, min: 0, max: 59, unit: "second"},
		{name: "clear_fault_enable", index: 7, min: 0, max: 1},
	},
}

// ClockEvent is the argument of the V1.08 0x211 frame. FrequencyEnable is B0,
// Year to Second are B1 to B6 as the documented domains state them, and
// ClearFaultEnable is B7. A false enable is the documented value 0.
type ClockEvent struct {
	FrequencyEnable  bool
	Year             uint8
	Month, Day       uint8
	Hour, Minute     uint8
	Second           uint8
	ClearFaultEnable bool
}

func flag(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// EncodeClockEvent encodes the V1.08 0x211 frame as a standard 11-bit data
// frame of DLC 8. An argument outside the documented domain is refused.
func EncodeClockEvent(in ClockEvent) (canbus.Frame, error) {
	return clockEventRow.encode(map[string]uint8{
		"frequency_enable":   flag(in.FrequencyEnable),
		"year":               in.Year,
		"month":              in.Month,
		"day":                in.Day,
		"hour":               in.Hour,
		"minute":             in.Minute,
		"second":             in.Second,
		"clear_fault_enable": flag(in.ClearFaultEnable),
	})
}

// Selection is the explicit choice of a profile. Only the V1.08 profile that is
// not read-only can dispatch the V1.08 rows.
type Selection struct {
	Profile  string
	ReadOnly bool
}

// ErrNotV108 reports a selection that cannot dispatch the V1.08 rows.
var ErrNotV108 = errors.New("selection is not an explicit writable V1.08 choice")

// Sender hands one frame to the publication API of the transport.
type Sender func(canbus.Frame) error

// Wait blocks for the duration or until the context ends.
type Wait func(context.Context, time.Duration) error

// Dispatcher sends the V1.08 0x211 event on a caller's trigger. Constructing it
// sends nothing.
type Dispatcher struct {
	send Sender
	wait Wait
}

// NewDispatcher returns a dispatcher for a V1.08 selection, and refuses any
// other selection, a read-only one, a missing sender or a missing wait.
func NewDispatcher(sel Selection, send Sender, wait Wait) (*Dispatcher, error) {
	if sel.Profile != Profile || sel.ReadOnly {
		return nil, ErrNotV108
	}
	if send == nil || wait == nil {
		return nil, errors.New("sender and wait are required")
	}
	return &Dispatcher{send: send, wait: wait}, nil
}

// Trigger sends the encoded 0x211 frame five times, one second apart. An
// argument outside the documented domain sends nothing. A send or wait error
// ends the dispatch with no retry.
func (d *Dispatcher) Trigger(ctx context.Context, in ClockEvent) error {
	if d == nil || d.send == nil || d.wait == nil {
		return ErrNotV108
	}
	frame, err := EncodeClockEvent(in)
	if err != nil {
		return err
	}
	for i := 0; i < EventRepeats; i++ {
		if i > 0 {
			if err := d.wait(ctx, EventPeriod); err != nil {
				return fmt.Errorf("event send %d: %w", i+1, err)
			}
		}
		if err := d.send(frame); err != nil {
			return fmt.Errorf("event send %d: %w", i+1, err)
		}
	}
	return nil
}

// ClockSyncDue reports whether the clock-setting content is triggered between
// two observations: now is in a later calendar hour than previous.
func ClockSyncDue(previous, now time.Time) bool {
	return hourStart(now).After(hourStart(previous))
}

func hourStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}
