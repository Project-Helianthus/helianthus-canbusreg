package growattlowvoltage

import (
	"errors"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	historyQueryIdentifier    uint32 = 0x212
	historyResponseIdentifier uint32 = 0x325

	// HistoryQueryRowID is the native write row of the V1.08 0x212 history query.
	HistoryQueryRowID = Profile + ".command.212.history"
	// HistoryResponseRowID is the native row of the V1.08 0x325 history response.
	HistoryResponseRowID = Profile + ".response.325.history"

	historyQueryBit uint8 = 0x02
)

var historyQueryRow = row{
	id:         HistoryQueryRowID,
	identifier: historyQueryIdentifier,
	fields: []field{
		{name: "query_select", index: 0, min: historyQueryBit, max: historyQueryBit},
		{name: "battery_id", index: 1, min: 0, max: 255},
	},
}

// EncodeHistoryQuery encodes the V1.08 0x212 history query: B0 bit 1 set, B1
// the target battery ID, and the reserved bytes B2 to B7 left zero and
// uninterpreted. It is a standard 11-bit data frame of DLC 8.
func EncodeHistoryQuery(batteryID uint8) (canbus.Frame, error) {
	return historyQueryRow.encode(map[string]uint8{
		"query_select": historyQueryBit,
		"battery_id":   batteryID,
	})
}

// HistoryExchange is one on-demand history query and its response evidence.
type HistoryExchange struct {
	batteryID uint8
	received  bool
	raw       [8]byte
}

// BatteryID is the target of the query.
func (e *HistoryExchange) BatteryID() uint8 { return e.batteryID }

// Completed reports whether a 0x325 frame with B0 = 0 naming the queried
// battery ID was accepted.
func (e *HistoryExchange) Completed() bool { return e != nil && e.received }

// Raw returns the accepted response exactly as received, and whether one was
// accepted.
func (e *HistoryExchange) Raw() ([8]byte, bool) {
	if e == nil || !e.received {
		return [8]byte{}, false
	}
	return e.raw, true
}

// BodyState is the state of the history body: B2 to B7 stay raw and
// unresolved, and no interpreted history is reported.
func (e *HistoryExchange) BodyState() string { return "pending/raw" }

// Body returns B2 to B7 of the accepted response, uninterpreted.
func (e *HistoryExchange) Body() []byte {
	if e == nil || !e.received {
		return nil
	}
	return append([]byte(nil), e.raw[2:]...)
}

// ErrNotHistoryResponse reports a frame that is not the response of the
// outstanding history query.
var ErrNotHistoryResponse = errors.New("frame is not the response of the outstanding history query")

// Offer takes one received frame. A standard 0x325 data frame of DLC 8 with
// B0 = 0 and B1 the queried battery ID is the response; any other frame is
// refused and changes nothing.
func (e *HistoryExchange) Offer(f canbus.Frame) error {
	if e == nil || f.ID().Extended() || f.ID().Value() != historyResponseIdentifier || f.DLC() != 8 || f.RawDLC() != 8 {
		return ErrNotHistoryResponse
	}
	d := f.Data()
	if d[0] != 0 || d[1] != e.batteryID || e.received {
		return ErrNotHistoryResponse
	}
	copy(e.raw[:], d)
	e.received = true
	return nil
}

// HistoryQuerier sends the V1.08 0x212 history query on a caller's request.
// Constructing it sends nothing.
type HistoryQuerier struct {
	send Sender
}

// NewHistoryQuerier returns a querier for a V1.08 selection, and refuses any
// other selection, a read-only one or a missing sender.
func NewHistoryQuerier(sel Selection, send Sender) (*HistoryQuerier, error) {
	if sel.Profile != Profile || sel.ReadOnly {
		return nil, ErrNotV108
	}
	if send == nil {
		return nil, errors.New("sender is required")
	}
	return &HistoryQuerier{send: send}, nil
}

// Query sends the query for one battery ID exactly once and returns the
// exchange that recognizes its response. A send error is returned with no
// retry and no exchange.
func (q *HistoryQuerier) Query(batteryID uint8) (*HistoryExchange, error) {
	if q == nil || q.send == nil {
		return nil, ErrNotV108
	}
	frame, err := EncodeHistoryQuery(batteryID)
	if err != nil {
		return nil, err
	}
	if err := q.send(frame); err != nil {
		return nil, fmt.Errorf("history query: %w", err)
	}
	return &HistoryExchange{batteryID: batteryID}, nil
}
