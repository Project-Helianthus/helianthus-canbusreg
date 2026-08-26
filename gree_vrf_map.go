package canbusreg

// GreeVRFMapProfile selects an already-qualified offline state-map family.
// It is not a vendor admission or device-identification result.
type GreeVRFMapProfile uint8

const (
	GreeVRFMapM94 GreeVRFMapProfile = iota + 1
	GreeVRFMapM115
)

// GreeVRFMapState is the bounded state used by the offline map codec.
// Cells and SlotFlags have no HVAC meaning outside the selected map profile.
type GreeVRFMapState struct {
	Cells               map[uint8]uint8
	SlotFlags           [100]uint8
	AggregateErrorLatch bool
	ModeLatch           bool
}

// GreeVRFMapDecoder evaluates one explicit map profile. ProductID is used only
// for the documented M115 post-decode cleanup, not as an admission signal.
type GreeVRFMapDecoder struct {
	profile   GreeVRFMapProfile
	productID uint16
}

func NewGreeVRFMapDecoder(profile GreeVRFMapProfile, productID uint16) GreeVRFMapDecoder {
	return GreeVRFMapDecoder{profile: profile, productID: productID}
}

type greeVRFSourceKind uint8

const (
	greeVRFSourceBit greeVRFSourceKind = iota + 1
	greeVRFSourceU8
	greeVRFSourceU16LEByteStream
)

type greeVRFTransform uint8

const (
	greeVRFTransformNone greeVRFTransform = iota
	greeVRFTransformAggregateErrorLatchFinalize
	greeVRFTransformAggregateErrorLatchPart
	greeVRFTransformBooleanToMagicAa55
	greeVRFTransformCoupledStateRemap
	greeVRFTransformGlobalModeLatchProjection
	greeVRFTransformGlobalModeLatchUpdate
	greeVRFTransformModeRangeToBoolean
	greeVRFTransformModeUpdatesAll100SlotFlags
	greeVRFTransformModeValueRemap
	greeVRFTransformNonzeroBooleanWithOneCleared
	greeVRFTransformRunModeCoupledRemap
	greeVRFTransformSwingValueRemap
)

type greeVRFMapEntry struct {
	index           int
	sourceKey       uint16
	destinationCell uint8
	destinationBit  uint8
	sourceKind      greeVRFSourceKind
	transform       greeVRFTransform
}

// Apply evaluates a single table-driven frame. It is entirely in-memory and
// returns false without changing state when the frame fails a structural gate.
func (d GreeVRFMapDecoder) Apply(initial GreeVRFMapState, opcode uint8, data []byte) (GreeVRFMapState, []int, bool) {
	entries, ok := d.entries()
	if !ok || len(data) == 0 || len(data) > 8 {
		return initial, nil, false
	}

	state := cloneGreeVRFMapState(initial)
	start := data[0]
	if uint16(opcode)<<8|uint16(start) == 0x3d00 {
		state.AggregateErrorLatch = false
	}

	matched := make([]int, 0)
	for _, entry := range entries {
		if entry.sourceKey>>8 != uint16(opcode) {
			continue
		}
		raw, covered, valid := greeVRFMapRaw(entry, start, data)
		if !valid {
			return initial, nil, false
		}
		if !covered {
			continue
		}
		matched = append(matched, entry.index)
		greeVRFApplyEntry(&state, entry, raw)
	}

	if d.profile == GreeVRFMapM115 && (d.productID == 0x6098 || d.productID == 0x60a4) {
		state.Cells[0x06] = 0
		state.Cells[0x11] = 0
		state.Cells[0x42] &= 0xfd
		state.Cells[0x44] = 0
	}
	return state, matched, true
}

func (d GreeVRFMapDecoder) entries() ([]greeVRFMapEntry, bool) {
	switch d.profile {
	case GreeVRFMapM94:
		return greeVRFMapM94Entries, true
	case GreeVRFMapM115:
		return greeVRFMapM115Entries, true
	default:
		return nil, false
	}
}

func cloneGreeVRFMapState(initial GreeVRFMapState) GreeVRFMapState {
	state := initial
	state.Cells = make(map[uint8]uint8, len(initial.Cells))
	for key, value := range initial.Cells {
		state.Cells[key] = value
	}
	return state
}

func greeVRFMapRaw(entry greeVRFMapEntry, start uint8, data []byte) (uint8, bool, bool) {
	coordinate := uint8(entry.sourceKey)
	if entry.sourceKind == greeVRFSourceU16LEByteStream && start&1 != 0 {
		return 0, false, false
	}
	if coordinate < start {
		return 0, false, true
	}
	delta := int(coordinate - start)
	switch entry.sourceKind {
	case greeVRFSourceBit:
		if delta >= 8*(len(data)-1) {
			return 0, false, true
		}
		byteIndex := 1 + delta/8
		return (data[byteIndex] >> (delta % 8)) & 1, true, true
	case greeVRFSourceU8, greeVRFSourceU16LEByteStream:
		if delta >= len(data)-1 {
			return 0, false, true
		}
		return data[1+delta], true, true
	default:
		return 0, false, false
	}
}

func greeVRFApplyEntry(state *GreeVRFMapState, entry greeVRFMapEntry, raw uint8) {
	if entry.sourceKind == greeVRFSourceBit {
		mask := uint8(1 << entry.destinationBit)
		if raw == 0 {
			state.Cells[entry.destinationCell] &^= mask
		} else {
			state.Cells[entry.destinationCell] |= mask
		}
	} else {
		state.Cells[entry.destinationCell] = raw
	}

	switch entry.transform {
	case greeVRFTransformNone:
	case greeVRFTransformBooleanToMagicAa55:
		if raw == 1 {
			state.Cells[entry.destinationCell] = 0xaa
		} else {
			state.Cells[entry.destinationCell] = 0x55
		}
	case greeVRFTransformSwingValueRemap:
		if raw == 1 {
			state.Cells[entry.destinationCell] = 0
		} else if raw == 2 {
			state.Cells[entry.destinationCell] = 1
		}
	case greeVRFTransformModeRangeToBoolean:
		if raw == 1 {
			state.Cells[entry.destinationCell] = 0
		} else if raw >= 2 && raw <= 4 {
			state.Cells[entry.destinationCell] = 1
		}
	case greeVRFTransformNonzeroBooleanWithOneCleared:
		if raw > 1 {
			state.Cells[entry.destinationCell] = 1
		} else {
			state.Cells[entry.destinationCell] = 0
		}
	case greeVRFTransformAggregateErrorLatchPart:
		if raw == 1 {
			state.AggregateErrorLatch = true
		}
	case greeVRFTransformAggregateErrorLatchFinalize:
		if raw != 0 {
			state.AggregateErrorLatch = false
		} else if state.AggregateErrorLatch {
			state.Cells[entry.destinationCell]++
		}
	case greeVRFTransformRunModeCoupledRemap:
		if greeVRFCell(state, int(entry.destinationCell)+3) != 0 {
			state.Cells[entry.destinationCell] = 5
		}
		if raw == 5 || raw == 6 {
			greeVRFSetCell(state, int(entry.destinationCell)+3, 1)
		}
	case greeVRFTransformGlobalModeLatchUpdate:
		if raw == 1 {
			state.Cells[entry.destinationCell] = 7
			state.ModeLatch = true
		} else {
			state.ModeLatch = false
		}
	case greeVRFTransformModeUpdatesAll100SlotFlags:
		if greeVRFCell(state, int(entry.destinationCell)+19) != 0 {
			greeVRFSetCell(state, int(entry.destinationCell)+17, 2)
		} else if greeVRFCell(state, int(entry.destinationCell)-4) != 0 {
			if raw == 1 || raw == 2 || raw == 5 {
				state.SlotFlags = [100]uint8{}
			} else if raw == 4 || raw == 6 {
				for index := range state.SlotFlags {
					state.SlotFlags[index] = 1
				}
			}
		}
	case greeVRFTransformGlobalModeLatchProjection:
		if state.ModeLatch {
			state.Cells[entry.destinationCell] = 7
		}
	case greeVRFTransformCoupledStateRemap:
		if state.Cells[entry.destinationCell] == 1 {
			greeVRFSetCell(state, int(entry.destinationCell)+3, 6)
		}
	case greeVRFTransformModeValueRemap:
		if greeVRFCell(state, int(entry.destinationCell)-3) != 0 {
			state.Cells[entry.destinationCell] = 6
		} else if raw == 1 {
			state.Cells[entry.destinationCell] = 7
		} else if raw >= 2 && raw <= 6 {
			state.Cells[entry.destinationCell] = raw - 1
		}
	}
}

func greeVRFCell(state *GreeVRFMapState, cell int) uint8 {
	if cell < 0 || cell > 0xff {
		return 0
	}
	return state.Cells[uint8(cell)]
}

func greeVRFSetCell(state *GreeVRFMapState, cell int, value uint8) {
	if cell >= 0 && cell <= 0xff {
		state.Cells[uint8(cell)] = value
	}
}
