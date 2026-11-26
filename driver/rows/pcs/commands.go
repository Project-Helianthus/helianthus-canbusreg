package pcs

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Command identifiers of the host inputs the PCS page documents with a
// complete payload layout (211 and 6F0). Each is a standard 11-bit data frame of DLC 8.
const (
	commandClockFrame     uint32 = 0x211
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
			Identifier: 0x37F,
			Gap:        "the page leaves B1-B6 profile-specific, defines no flag meaning for B0 or the configuration byte, says the input changes the active profile, role or transmit state, and defines no generic encoder for unknown flags",
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

// ExtendedContract names the native 19xx contract a caller states for the
// stack endpoint a frame is meant for. The page's addressing and eligibility
// table admits the legacy extended-19 input/output contract and gives the
// release-11 standard/parallel contract no legacy 19xx input.
type ExtendedContract string

const (
	// ContractLegacyExtended19 is the legacy extended-19 contract (QAAa4010,
	// QAAa4011 and the legacy contract of QAAa9011).
	ContractLegacyExtended19 ExtendedContract = "legacy-extended-19"
	// ContractRelease11 is the release-11 standard/parallel contract (QAAB0006,
	// QAAA0011 and the release-11 contract of QAAa9011).
	ContractRelease11 ExtendedContract = "release-11"
)

// Extended command identifiers: the complete identifiers the page lists as
// typical for the coordinator-to-endpoint direction (kind in the upper 13 bits,
// then DF and F2). The role and address bytes are protocol fields and are not
// arguments.
const (
	extendedTimeFrame      uint32 = 0x190BDFF2
	extendedStackFrame     uint32 = 0x191ADFF2
	extendedSecondaryFrame uint32 = 0x191CDFF2
	extendedUpdateFrame    uint32 = 0x191EDFF2
)

// ExtendedCommand is one native write row of an extended 19xx input: the
// complete frame identifier, the contracts the section admits and the page
// layout of its payload. It is bound to no SemReg operation, and it is not the
// way back of any bound element.
type ExtendedCommand struct {
	ID         string
	Identifier uint32
	Profiles   []ExtendedContract
	Layout     string
}

// ExtendedCommands returns the native write rows of the extended 19xx inputs
// whose payload the page gives in full, in identifier order. Nothing is sent
// by listing or by encoding.
func ExtendedCommands() []ExtendedCommand {
	admitted := []ExtendedContract{ContractLegacyExtended19}
	return []ExtendedCommand{
		{
			ID:         commandRowPrefix + "190B",
			Identifier: extendedTimeFrame,
			Profiles:   admitted,
			Layout:     "B0 year offset from 2000; B1 month; B2 day; B3 hour; B4 minute; B5 second; B6 extension/reserved sent zero; B7 zero; binary integers",
		},
		{
			ID:         commandRowPrefix + "191A",
			Identifier: extendedStackFrame,
			Profiles:   admitted,
			Layout:     "LE16(B2) aggregate current code; LE16(B4) aggregate state of charge; the other bytes are not setter fields and are sent zero",
		},
		{
			ID:         commandRowPrefix + "191C",
			Identifier: extendedSecondaryFrame,
			Profiles:   admitted,
			Layout:     "LE16(B2) secondary percentage quantity; B4 setter byte; the other bytes are not setter fields and are sent zero",
		},
		{
			ID:         commandRowPrefix + "191E",
			Identifier: extendedUpdateFrame,
			Profiles:   admitted,
			Layout:     "B0 update-session coordination parameter; B1 to B7 zero",
		},
	}
}

// IncompleteExtendedInputs lists the extended 19xx frames of the page that get
// no command row, each with the exact gap. Identifiers are the frame kinds.
func IncompleteExtendedInputs() []IncompleteInput {
	return []IncompleteInput{
		{Identifier: 0x190A, Gap: "B0 control flags have no stated bit meanings and B1 to B7 are coordinator-specific; the page defines no generic encoder for an unknown profile"},
		{Identifier: 0x190C, Gap: "the payload is eight aggregate fault bytes of block 0 with no field layout; several profiles accept it as a no-op and the page lists no accepted-input contract"},
		{Identifier: 0x190D, Gap: "the payload is eight aggregate fault bytes of block 1 with no field layout; output support does not establish a battery input handler"},
		{Identifier: 0x190E, Gap: "a gateway publication; battery receive semantics are profile-specific or unknown and no accepted-input contract is listed"},
		{Identifier: 0x190F, Gap: "a gateway publication, not an input; no accepted-input contract is listed"},
		{Identifier: 0x191B, Gap: "a gateway publication; the page says not to assume a stack input handler"},
		{Identifier: 0x191D, Gap: "gateway identity output; the page implies no ordinary battery command"},
		{Identifier: 0x1918, Gap: "the fragment-header bit encoding is not defined for a generic writer; the input is mutative and needs the separately qualified maintenance profile"},
		{Identifier: 0x1919, Gap: "the page states no payload layout, only that it is not a generic register selection; it needs the separately qualified maintenance profile"},
	}
}

func admitExtended(contract ExtendedContract, frame string) error {
	if contract != ContractLegacyExtended19 {
		return fmt.Errorf("extended input %s refused for contract %q: the section admits only %q", frame, contract, ContractLegacyExtended19)
	}
	return nil
}

// ExtendedTimeInput is the argument of the extended time input 0x190B. Year
// is the offset from 2000.
type ExtendedTimeInput struct {
	YearOffset, Month, Day, Hour, Minute, Second uint8
}

// EncodeExtendedTimeInput encodes the input as the extended identifier
// 0x190BDFF2. The byte positions differ from 0x211: the year offset is B0 and
// the second B5. The calendar ranges are those of the clock input. B6 and B7
// are sent zero.
func EncodeExtendedTimeInput(contract ExtendedContract, in ExtendedTimeInput) (canbus.Frame, error) {
	if err := admitExtended(contract, "190B"); err != nil {
		return canbus.Frame{}, err
	}
	switch {
	case in.Month < 1 || in.Month > 12:
		return canbus.Frame{}, fmt.Errorf("extended time input month %d outside 1..12", in.Month)
	case in.Day < 1 || in.Day > 31:
		return canbus.Frame{}, fmt.Errorf("extended time input day %d outside 1..31", in.Day)
	case in.Hour > 23:
		return canbus.Frame{}, fmt.Errorf("extended time input hour %d outside 0..23", in.Hour)
	case in.Minute > 59:
		return canbus.Frame{}, fmt.Errorf("extended time input minute %d outside 0..59", in.Minute)
	case in.Second > 59:
		return canbus.Frame{}, fmt.Errorf("extended time input second %d outside 0..59", in.Second)
	}
	return commandExtendedFrame(extendedTimeFrame, [8]byte{in.YearOffset, in.Month, in.Day, in.Hour, in.Minute, in.Second, 0, 0})
}

// StackStateInput is the argument of the extended input 0x191A: the
// aggregate current code and the aggregate state of charge, both little-endian
// 16-bit words. The page states no range for either.
type StackStateInput struct {
	CurrentCode, StateOfCharge uint16
}

// EncodeStackStateInput encodes the input as the extended identifier
// 0x191ADFF2 with LE16 at B2 and B4 and every other byte zero.
func EncodeStackStateInput(contract ExtendedContract, in StackStateInput) (canbus.Frame, error) {
	if err := admitExtended(contract, "191A"); err != nil {
		return canbus.Frame{}, err
	}
	return commandExtendedFrame(extendedStackFrame, [8]byte{
		2: byte(in.CurrentCode), 3: byte(in.CurrentCode >> 8),
		4: byte(in.StateOfCharge), 5: byte(in.StateOfCharge >> 8),
	})
}

// SecondaryQuantityInput is the argument of the extended input 0x191C: the
// secondary percentage quantity (little-endian 16-bit, B2) and the byte B4.
// The page states no range for either.
type SecondaryQuantityInput struct {
	Percentage uint16
	ByteFour   uint8
}

// EncodeSecondaryQuantityInput encodes the input as the extended identifier
// 0x191CDFF2 with every byte other than B2, B3 and B4 zero.
func EncodeSecondaryQuantityInput(contract ExtendedContract, in SecondaryQuantityInput) (canbus.Frame, error) {
	if err := admitExtended(contract, "191C"); err != nil {
		return canbus.Frame{}, err
	}
	return commandExtendedFrame(extendedSecondaryFrame, [8]byte{
		2: byte(in.Percentage), 3: byte(in.Percentage >> 8), 4: in.ByteFour,
	})
}

// UpdateCoordinationInput is the argument of the extended input 0x191E: B0
// the update-session coordination parameter.
type UpdateCoordinationInput struct {
	Parameter uint8
}

// EncodeUpdateCoordinationInput encodes the input as the extended identifier
// 0x191EDFF2 with B1 to B7 zero.
func EncodeUpdateCoordinationInput(contract ExtendedContract, in UpdateCoordinationInput) (canbus.Frame, error) {
	if err := admitExtended(contract, "191E"); err != nil {
		return canbus.Frame{}, err
	}
	return commandExtendedFrame(extendedUpdateFrame, [8]byte{in.Parameter})
}

func commandExtendedFrame(identifier uint32, data [8]byte) (canbus.Frame, error) {
	id, err := canbus.NewExtendedID(identifier)
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(id, data[:])
}
