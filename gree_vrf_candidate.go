package canbusreg

import "github.com/Project-Helianthus/helianthus-canbus"

const (
	GreeVRFCandidateProfile = "gree.vrf.canbus.candidate.v1"

	greeVRFCandidateMask     = uint32(0x1fe0007f)
	greeVRFCandidateClass8   = uint8(0xf7)
	greeVRFCandidateUnit7    = uint8(8)
	greeVRFCandidateOpcode10 = uint8(0x10)
	greeVRFCandidateOpcode11 = uint8(0x11)
	greeVRFCandidateOpcode52 = uint8(0x52)
	greeVRFCandidateOpcode58 = uint8(0x58)
	greeVRFCandidateCell0F   = uint8(0x0f)
	greeVRFCandidateCell10   = uint8(0x10)
	greeVRFCandidateCell11   = uint8(0x11)
	greeVRFCandidateCell12   = uint8(0x12)
	greeVRFCandidateCell13   = uint8(0x13)
	greeVRFCandidateCell14   = uint8(0x14)
	greeVRFCandidateCell15   = uint8(0x15)
	greeVRFCandidateCell16   = uint8(0x16)
	greeVRFCandidateCell17   = uint8(0x17)
	greeVRFCandidateCell18   = uint8(0x18)
	greeVRFCandidateCell19   = uint8(0x19)
	greeVRFCandidateCell1A   = uint8(0x1a)
	greeVRFCandidateCell1B   = uint8(0x1b)
)

// GreeVRFCandidateStateCell is an opaque one-byte candidate state update.
type GreeVRFCandidateStateCell struct {
	Cell  uint8
	Value uint8
}

// GreeVRFCandidateProjection retains only the admitted source frame,
// identifier fields, and opaque state-cell updates.
type GreeVRFCandidateProjection struct {
	Evidence Evidence
	Class8   uint8
	Opaque7  uint8
	Unit7    uint8
	Opcode7  uint8
	Updates  []GreeVRFCandidateStateCell
}

type greeVRFCandidate struct{}

// GreeVRFCandidate returns the receive-only candidate profile. It is active
// only when a caller explicitly adds it to a registry.
func GreeVRFCandidate() Profile { return greeVRFCandidate{} }

func (greeVRFCandidate) Classify(e Evidence) Classification {
	if e.Interface.Name() == "" || e.Interface.Index() <= 0 {
		return Classification{}
	}

	frame := e.Frame
	if !frame.ID().Extended() || frame.DLC() == 0 || frame.RawDLC() != frame.DLC() {
		return Classification{}
	}

	identifier := frame.ID().Value()
	if !greeVRFCandidateIdentifier(identifier) {
		return Classification{}
	}

	data := frame.Data()
	updates, ok := greeVRFCandidateUpdates(identifier&0x7f, data)
	if !ok || len(updates) == 0 {
		return Classification{}
	}

	return Classification{
		Profile: GreeVRFCandidateProfile,
		Projection: GreeVRFCandidateProjection{
			Evidence: e,
			Class8:   uint8((identifier >> 21) & 0xff),
			Opaque7:  uint8((identifier >> 14) & 0x7f),
			Unit7:    uint8((identifier >> 7) & 0x7f),
			Opcode7:  uint8(identifier & 0x7f),
			Updates:  updates,
		},
	}
}

func greeVRFCandidateIdentifier(identifier uint32) bool {
	if uint8((identifier>>7)&0x7f) != greeVRFCandidateUnit7 {
		return false
	}
	switch identifier & greeVRFCandidateMask {
	case 0x1ee00010, 0x1ee00011, 0x1ee00052, 0x1ee00058:
		return true
	default:
		return false
	}
}

func greeVRFCandidateUpdates(opcode uint32, data []byte) ([]GreeVRFCandidateStateCell, bool) {
	if len(data) == 0 || len(data) > canbus.MaxClassicDataLength {
		return nil, false
	}

	switch uint8(opcode) {
	case greeVRFCandidateOpcode10:
		if !greeVRFCandidateByteSpanValid(data) {
			return nil, false
		}
		return greeVRFCandidateOpcode10Updates(data), true
	case greeVRFCandidateOpcode52:
		if !greeVRFCandidateBitSpanValid(data) {
			return nil, false
		}
		return greeVRFCandidateOpcode52Updates(data), true
	case greeVRFCandidateOpcode58:
		if !greeVRFCandidateByteSpanValid(data) {
			return nil, false
		}
		return greeVRFCandidateOpcode58Updates(data), true
	case greeVRFCandidateOpcode11:
		if !greeVRFCandidateByteSpanValid(data) {
			return nil, false
		}
		return greeVRFCandidateSingleByteUpdate(data, 0x09, greeVRFCandidateCell1B), true
	default:
		return nil, false
	}
}

func greeVRFCandidateOpcode10Updates(data []byte) []GreeVRFCandidateStateCell {
	updates := greeVRFCandidateBytePairUpdate(data, 0x05, greeVRFCandidateCell0F, greeVRFCandidateCell10)
	updates = append(updates, greeVRFCandidateSingleByteUpdate(data, 0x20, greeVRFCandidateCell11)...)
	return updates
}

func greeVRFCandidateOpcode52Updates(data []byte) []GreeVRFCandidateStateCell {
	value, ok := greeVRFCandidatePackedBit(data, 0x59)
	if !ok {
		return nil
	}
	return []GreeVRFCandidateStateCell{{Cell: greeVRFCandidateCell12, Value: value}}
}

func greeVRFCandidateOpcode58Updates(data []byte) []GreeVRFCandidateStateCell {
	updates := greeVRFCandidateBytePairUpdate(data, 0x5d, greeVRFCandidateCell13, greeVRFCandidateCell14)
	updates = append(updates, greeVRFCandidateBytePairUpdate(data, 0x5f, greeVRFCandidateCell15, greeVRFCandidateCell16)...)
	updates = append(updates, greeVRFCandidateBytePairUpdate(data, 0x59, greeVRFCandidateCell17, greeVRFCandidateCell18)...)
	updates = append(updates, greeVRFCandidateBytePairUpdate(data, 0x5b, greeVRFCandidateCell19, greeVRFCandidateCell1A)...)
	return updates
}

func greeVRFCandidateByteSpanValid(data []byte) bool {
	return int(data[0])+len(data)-1 <= 0xff
}

func greeVRFCandidateBitSpanValid(data []byte) bool {
	return int(data[0])+8*(len(data)-1) <= 0xff
}

func greeVRFCandidateBytePairUpdate(data []byte, coordinate, firstCell, secondCell uint8) []GreeVRFCandidateStateCell {
	first, firstOK := greeVRFCandidateByte(data, coordinate)
	second, secondOK := greeVRFCandidateByte(data, coordinate+1)
	if !firstOK || !secondOK {
		return nil
	}
	return []GreeVRFCandidateStateCell{{Cell: firstCell, Value: first}, {Cell: secondCell, Value: second}}
}

func greeVRFCandidateSingleByteUpdate(data []byte, coordinate, cell uint8) []GreeVRFCandidateStateCell {
	value, ok := greeVRFCandidateByte(data, coordinate)
	if !ok {
		return nil
	}
	return []GreeVRFCandidateStateCell{{Cell: cell, Value: value}}
}

func greeVRFCandidateByte(data []byte, coordinate uint8) (uint8, bool) {
	start := data[0]
	if coordinate < start {
		return 0, false
	}
	index := int(coordinate-start) + 1
	if index >= len(data) {
		return 0, false
	}
	return data[index], true
}

func greeVRFCandidatePackedBit(data []byte, coordinate uint8) (uint8, bool) {
	start := data[0]
	if coordinate < start {
		return 0, false
	}
	bit := int(coordinate - start)
	index := bit/8 + 1
	if index >= len(data) {
		return 0, false
	}
	return (data[index] >> (bit % 8)) & 1, true
}
