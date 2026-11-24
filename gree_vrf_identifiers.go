package canbusreg

import "github.com/Project-Helianthus/helianthus-canbus"

// GreeVRFIdentifierFamily distinguishes the native layouts, not the sender.
type GreeVRFIdentifierFamily uint8

// The documented identifier families and the separate clock layout.
const (
	GreeVRFData GreeVRFIdentifierFamily = iota + 1
	GreeVRFAddressedData
	GreeVRFAddressedWrite
	GreeVRFClock
)

// GreeVRFIdentifier keeps the identifier fields as received. Unit names the
// destination in addressed traffic and the register owner in data traffic.
type GreeVRFIdentifier struct {
	Family                    GreeVRFIdentifierFamily
	Class, Opaque, Unit, Type uint8
}

// ClassifyGreeVRFIdentifier identifies a documented identifier family. It
// does not validate the payload or assign meaning to the opaque field.
func ClassifyGreeVRFIdentifier(id canbus.Identifier) (GreeVRFIdentifier, bool) {
	if !id.Extended() {
		return GreeVRFIdentifier{}, false
	}
	raw := id.Value()
	out := GreeVRFIdentifier{
		Class: uint8(raw >> 21), Opaque: uint8(raw >> 14 & 0x7f),
		Unit: uint8(raw >> 7 & 0x7f), Type: uint8(raw & 0x7f),
	}
	if raw == 0x04820084 || raw == 0x04820000 {
		out.Family = GreeVRFClock
		return out, true
	}
	if out.Opaque != 0x7f {
		return GreeVRFIdentifier{}, false
	}
	switch out.Class {
	case 0xf7:
		out.Family = GreeVRFData
	case 0xe7:
		out.Family = GreeVRFAddressedData
	case 0xe5:
		out.Family = GreeVRFAddressedWrite
	default:
		return GreeVRFIdentifier{}, false
	}
	return out, true
}
