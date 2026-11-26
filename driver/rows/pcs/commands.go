package pcs

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Command identifiers of the host inputs the PCS page documents with a
// complete payload layout. Each is a standard 11-bit data frame of DLC 8.
const (
	commandClockFrame     uint32 = 0x211
	commandParallelFrame  uint32 = 0x37F
	commandDiscoveryFrame uint32 = 0x6F0

	commandRowPrefix = "growatt.bms.pcs.command."
)

// Command is one native write row: the frame a caller can have encoded and
// the page layout of its payload. It is bound to no SemReg operation.
type Command struct {
	ID         string
	Identifier uint32
	Layout     string
}

// Commands returns the native write rows of this package, in identifier
// order. Nothing is sent by listing or by encoding; a caller chooses what to
// send.
func Commands() []Command {
	return []Command{
		{
			ID:         commandRowPrefix + "211",
			Identifier: commandClockFrame,
			Layout:     "B0 unused; B1 year offset from 2000; B2 month; B3 day; B4 hour; B5 minute; B6 second; B7 unused; binary integers",
		},
		{
			ID:         commandRowPrefix + "37F",
			Identifier: commandParallelFrame,
			Layout:     "B0 control flags; B1-B6 profile-specific; B7 parallel configuration",
		},
		{
			ID:         commandRowPrefix + "6F0",
			Identifier: commandDiscoveryFrame,
			Layout:     "B0 phase/control; B1 coordination parameter; B2-B7 zero",
		},
	}
}

// IncompleteInput is a host input the page documents without a payload an
// encoder can state. It has no command row.
type IncompleteInput struct {
	Identifier uint32
	Gap        string
}

// IncompleteInputs lists the host inputs of the standard PCS page that get no
// command row, each with the exact gap.
func IncompleteInputs() []IncompleteInput {
	return []IncompleteInput{
		{
			Identifier: 0x212,
			Gap:        "the page defines no operation for this input in the profiles it covers: its payload has no defined serial or history query, and the page marks it unsupported for query purposes",
		},
		{
			Identifier: 0x301,
			Gap:        "the page describes a communications heartbeat whose host-state payload is profile-specific; B2 is retained by some profiles, later profiles ignore the payload bytes, release-11 B7 follows a feature table, and the page gives no common byte layout",
		},
	}
}

// ClockInput is the argument of the time-synchronization input 0x211. Year is
// the offset from 2000.
type ClockInput struct {
	YearOffset, Month, Day, Hour, Minute, Second uint8
}

// EncodeClockInput encodes the clock input as the standard identifier 0x211
// with the page's calendar layout. The year offset is one binary byte; the
// month is 1 to 12, the day 1 to 31, the hour 0 to 23 and the minute and
// second 0 to 59, the ranges of the calendar fields the page names. B0 and B7
// are unused and are sent as zero.
func EncodeClockInput(in ClockInput) (canbus.Frame, error) {
	switch {
	case in.Month < 1 || in.Month > 12:
		return canbus.Frame{}, fmt.Errorf("clock input month %d outside 1..12", in.Month)
	case in.Day < 1 || in.Day > 31:
		return canbus.Frame{}, fmt.Errorf("clock input day %d outside 1..31", in.Day)
	case in.Hour > 23:
		return canbus.Frame{}, fmt.Errorf("clock input hour %d outside 0..23", in.Hour)
	case in.Minute > 59:
		return canbus.Frame{}, fmt.Errorf("clock input minute %d outside 0..59", in.Minute)
	case in.Second > 59:
		return canbus.Frame{}, fmt.Errorf("clock input second %d outside 0..59", in.Second)
	}
	return commandFrame(commandClockFrame, [8]byte{0, in.YearOffset, in.Month, in.Day, in.Hour, in.Minute, in.Second, 0})
}

// ParallelControlInput is the argument of the profile control input 0x37F.
// The page names B0 as control flags and B7 as the parallel configuration and
// leaves B1 to B6 profile-specific; each byte is sent as given and the page
// defines no flag or profile meaning that this row interprets.
type ParallelControlInput struct {
	Flags         uint8
	Profile       [6]uint8
	Configuration uint8
}

// EncodeParallelControlInput encodes the input as the standard identifier
// 0x37F: B0 the control flags, B1 to B6 the profile bytes, B7 the parallel
// configuration.
func EncodeParallelControlInput(in ParallelControlInput) (canbus.Frame, error) {
	return commandFrame(commandParallelFrame, [8]byte{
		in.Flags, in.Profile[0], in.Profile[1], in.Profile[2], in.Profile[3], in.Profile[4], in.Profile[5], in.Configuration,
	})
}

// DiscoveryControlInput is the argument of the coordinator input 0x6F0:
// B0 the phase or control byte and B1 the coordination parameter.
type DiscoveryControlInput struct {
	Phase, Parameter uint8
}

// EncodeDiscoveryControlInput encodes the input as the standard identifier
// 0x6F0 with B2 to B7 zero, the defined form of the page.
func EncodeDiscoveryControlInput(in DiscoveryControlInput) (canbus.Frame, error) {
	return commandFrame(commandDiscoveryFrame, [8]byte{in.Phase, in.Parameter})
}

func commandFrame(identifier uint32, data [8]byte) (canbus.Frame, error) {
	id, err := canbus.NewStandardID(identifier)
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(id, data[:])
}
