package growattlowvoltage

import (
	"errors"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	serialQueryIdentifier    uint32 = 0x212
	serialResponseIdentifier uint32 = 0x324

	// SerialQueryRowID is the native write row of the V1.08 0x212 serial query.
	SerialQueryRowID = Profile + ".command.212.serial"
	// SerialResponseRowID is the native row of the V1.08 0x324 serial response.
	SerialResponseRowID = Profile + ".response.324.serial"

	// MaxSerialBytes is the documented bound of the serial bytes of one response.
	MaxSerialBytes = 32

	serialQueryBit uint8 = 0x01
)

var serialQueryRow = row{
	id:         SerialQueryRowID,
	identifier: serialQueryIdentifier,
	fields: []field{
		{name: "query_select", index: 0, min: serialQueryBit, max: serialQueryBit},
		{name: "battery_id", index: 1, min: 0, max: 255},
	},
}

// EncodeSerialQuery encodes the V1.08 0x212 serial-number query: B0 bit 0 set,
// B1 the target battery ID, and the reserved bytes B2 to B7 left zero and
// uninterpreted. It is a standard 11-bit data frame of DLC 8.
func EncodeSerialQuery(batteryID uint8) (canbus.Frame, error) {
	return serialQueryRow.encode(map[string]uint8{
		"query_select": serialQueryBit,
		"battery_id":   batteryID,
	})
}

// SerialFragment is one received 0x324 frame, kept as received.
type SerialFragment struct {
	Raw [8]byte
}

// FrameNumber is B0 of the fragment.
func (f SerialFragment) FrameNumber() uint8 { return f.Raw[0] }

// SerialExchange is one on-demand serial query and its response evidence.
type SerialExchange struct {
	batteryID uint8
	started   bool
	fragments []SerialFragment
}

// BatteryID is the target of the query.
func (e *SerialExchange) BatteryID() uint8 { return e.batteryID }

// Fragments returns every accepted fragment in arrival order.
func (e *SerialExchange) Fragments() []SerialFragment {
	return append([]SerialFragment(nil), e.fragments...)
}

// Completed reports the documented completion: the battery sent the requested
// data as its single application response. It is true only after frame 0 of a
// 0x324 response naming the queried battery ID was accepted. Whether further
// serial frames follow, and how their end is signalled, the document does not
// state, so that is not reported.
func (e *SerialExchange) Completed() bool { return e != nil && e.started }

// SerialBytes returns the serial bytes received so far: B2 to B7 of frame 0
// and B1 to B7 of each later fragment, in arrival order, at most MaxSerialBytes.
// Bytes past the documented bound are not serial content and stay only in the
// raw fragments. Their order by frame number and the end of the serial are not
// stated by the document.
func (e *SerialExchange) SerialBytes() []byte {
	var out []byte
	for i, f := range e.fragments {
		if i == 0 {
			out = append(out, f.Raw[2:]...)
		} else {
			out = append(out, f.Raw[1:]...)
		}
	}
	if len(out) > MaxSerialBytes {
		out = out[:MaxSerialBytes]
	}
	return out
}

// ErrNotSerialResponse reports a frame that is not the response of the
// outstanding serial query.
var ErrNotSerialResponse = errors.New("frame is not the response of the outstanding serial query")

// Offer takes one received frame. A standard 0x324 data frame of DLC 8 is the
// response when it is frame 0 naming the queried battery ID, or a later frame
// after frame 0. Any other frame, a second frame 0, or any frame once 32 serial
// bytes are already held is refused and changes nothing.
func (e *SerialExchange) Offer(f canbus.Frame) error {
	if e == nil || f.ID().Extended() || f.ID().Value() != serialResponseIdentifier || f.DLC() != 8 || f.RawDLC() != 8 {
		return ErrNotSerialResponse
	}
	var raw [8]byte
	copy(raw[:], f.Data())
	if !e.started {
		if raw[0] != 0 || raw[1] != e.batteryID {
			return ErrNotSerialResponse
		}
		e.started = true
		e.fragments = append(e.fragments, SerialFragment{Raw: raw})
		return nil
	}
	if raw[0] == 0 {
		return ErrNotSerialResponse
	}
	if len(e.SerialBytes()) >= MaxSerialBytes {
		return ErrNotSerialResponse
	}
	e.fragments = append(e.fragments, SerialFragment{Raw: raw})
	return nil
}

// SerialQuerier sends the V1.08 0x212 serial query on a caller's request.
// Constructing it sends nothing.
type SerialQuerier struct {
	send Sender
}

// NewSerialQuerier returns a querier for a V1.08 selection, and refuses any
// other selection, a read-only one or a missing sender.
func NewSerialQuerier(sel Selection, send Sender) (*SerialQuerier, error) {
	if sel.Profile != Profile || sel.ReadOnly {
		return nil, ErrNotV108
	}
	if send == nil {
		return nil, errors.New("sender is required")
	}
	return &SerialQuerier{send: send}, nil
}

// Query sends the query for one battery ID exactly once and returns the
// exchange that recognizes its response. A send error is returned with no
// retry and no exchange.
func (q *SerialQuerier) Query(batteryID uint8) (*SerialExchange, error) {
	if q == nil || q.send == nil {
		return nil, ErrNotV108
	}
	frame, err := EncodeSerialQuery(batteryID)
	if err != nil {
		return nil, err
	}
	if err := q.send(frame); err != nil {
		return nil, fmt.Errorf("serial query: %w", err)
	}
	return &SerialExchange{batteryID: batteryID}, nil
}
