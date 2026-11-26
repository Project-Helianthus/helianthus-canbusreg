package link

import (
	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	commandClockFamily uint32 = 0x188A

	commandClockRowID = "growatt.bms.link.command.188A"
)

// Command is one native write row: the frame a caller can have encoded and
// the page layout of its payload. It is bound to no SemReg operation.
type Command struct {
	ID     string
	Family uint32
	Layout string
}

// Commands returns the native write rows of this package. Nothing is sent by
// listing or by encoding; a caller chooses what to send.
func Commands() []Command {
	return []Command{
		{
			ID:     commandClockRowID,
			Family: commandClockFamily,
			Layout: "extended identifier 18 8A DD SS with DLC 8; B0-B1 little-endian year; B2 month; B3 day; B4 hour; B5 minute; B6 second; B7 extra",
		},
	}
}

// UncoveredFamily is a class C or Q family, or a block range, that the page
// leaves without a command payload layout. It has no command row.
type UncoveredFamily struct {
	Family string
	Class  string
	Gap    string
}

// UncoveredFamilies lists every class C and Q family of the LINK page that gets
// no command row, each with the gap.
func UncoveredFamilies() []UncoveredFamily {
	const noLayout = "the page documents no command payload layout for this family"
	return []UncoveredFamily{
		{"1800DFF4", "C", noLayout},
		{"1805DFF4", "C", noLayout},
		{"180ADFF4", "C", "the page calls it a wake and coordination frame and gives no payload layout"},
		{"1860DFF4", "C", "a coordination input with a synchronization role; the page gives no command layout, so it has no row"},
		{"18FEDFF4", "C", noLayout},
		{"18B0DFF4", "C", "update coordination; the page leaves the control data raw and ties it to an active update session"},
		{"18B0FFF5", "C", "service update route; the page keeps the payload raw without the matching service contract"},
		{"18B1DFF4", "C", noLayout},
		{"1869DFF4", "C", noLayout},
		{"18F0", "C", "discovery and addressing on a matched route; the page gives no payload layout and states no automatic discovery transmission"},
		{"1870-1889", "Q", "configuration blocks: the page defines no register addresses, units or editable fields and forbids transferring a layout across feature groups"},
		{"188B", "Q", "only the report form is laid out; the page marks it maintenance-only"},
		{"188C", "Q", "identity fragments; the page marks it maintenance-only"},
		{"1892", "Q", "encoding is profile-dependent and the page marks it not a date query"},
		{"1894", "Q", "the page defines no generic meaning"},
		{"1899", "Q", "the page gives a configuration layout without a stated write route or conditions"},
		{"18A0", "Q", "selector-dependent data; transmit and receive selector meanings are not symmetric"},
		{"18DF", "Q", "session-dependent bytes; the page defines no layout"},
		{"18FC", "Q", "the report is laid out; the page gives no layout for a transmitted input beyond the legacy L4 case"},
		{"18FD", "Q", "B3-B7 are further control fields the page does not lay out"},
	}
}

// ClockInput is the argument of the addressed clock-setting frame 188A.
// Destination and Source are the two address bytes of the identifier.
type ClockInput struct {
	Destination, Source                     uint8
	Year                                    uint16
	Month, Day, Hour, Minute, Second, Extra uint8
}

// EncodeClockInput encodes the input as the extended identifier 188ADDSS with
// the page's payload: year little-endian in B0-B1, then month, day, hour,
// minute, second and the extra byte. The page states no range for any field,
// so none is applied.
func EncodeClockInput(in ClockInput) (canbus.Frame, error) {
	id, err := canbus.NewExtendedID(commandClockFamily<<16 | uint32(in.Destination)<<8 | uint32(in.Source))
	if err != nil {
		return canbus.Frame{}, err
	}
	data := [8]byte{byte(in.Year), byte(in.Year >> 8), in.Month, in.Day, in.Hour, in.Minute, in.Second, in.Extra}
	return canbus.NewFrame(id, data[:])
}
