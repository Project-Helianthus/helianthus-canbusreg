package replay

import (
	"context"
	"errors"
	"sync"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
)

// ErrClosed is returned by Receive after Close.
var ErrClosed = errors.New("replay source closed")

// Source is one playback of a script. It implements receiver.Receiver.
// Receive yields recorded records in order, then blocks until the context
// ends or Close is called. Two opens of one script do not share a cursor.
type Source struct {
	identity canbus.InterfaceIdentity
	records  []receiver.Record
	mu       sync.Mutex
	next     int
	closed   bool
	wake     chan struct{}
}

// Open copies script and returns a source positioned at its first record.
func Open(script Script) (*Source, error) {
	if err := script.validate(); err != nil {
		return nil, err
	}
	identity, err := canbus.NewInterfaceIdentity(script.Interface, script.Index)
	if err != nil {
		return nil, err
	}
	records := make([]receiver.Record, len(script.Frames))
	for i, frame := range script.Frames {
		raw, err := frame.bytes()
		if err != nil {
			return nil, err
		}
		seq := uint64(i + 1)
		if frame.HasSequence {
			seq = frame.Sequence
		}
		records[i] = receiver.Record{
			Interface: identity, Raw: raw, Sequence: seq, Monotonic: frame.Monotonic,
		}
	}
	return &Source{identity: identity, records: records, wake: make(chan struct{})}, nil
}

// Identity returns the interface this source was opened for.
func (s *Source) Identity() canbus.InterfaceIdentity {
	if s == nil {
		return canbus.InterfaceIdentity{}
	}
	return s.identity
}

// Receive returns the next recorded record, or blocks after the last one.
func (s *Source) Receive(ctx context.Context) (receiver.Record, error) {
	if s == nil {
		return receiver.Record{}, errors.New("nil replay source")
	}
	if err := ctx.Err(); err != nil {
		return receiver.Record{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return receiver.Record{}, ErrClosed
	}
	if s.next < len(s.records) {
		rec := s.records[s.next]
		s.next++
		s.mu.Unlock()
		return rec, nil
	}
	wake := s.wake
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return receiver.Record{}, ctx.Err()
	case <-wake:
		if err := ctx.Err(); err != nil {
			return receiver.Record{}, err
		}
		return receiver.Record{}, ErrClosed
	}
}

// Close unblocks Receive. A second close is a no-op.
func (s *Source) Close() error {
	if s == nil {
		return errors.New("nil replay source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.wake)
	return nil
}
