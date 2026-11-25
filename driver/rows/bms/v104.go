package bms

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Profile is the profile identifier of the V1.04 low-voltage BMS CAN profile.
const Profile = "growatt.bms.low_voltage.can.v1_04"

// Selector identifies a field within a CAN data frame.
type Selector struct {
	Identifier  uint32
	Extended    bool
	DLC, RawDLC uint8
	Slice       signalSlice
}

type signalSlice struct {
	offset, width uint16
	order         string
	signed        bool
}

const (
	bigEndian     = "big-endian"
	bitSlice      = "bit-slice"
	packRole      = "pack"
	packDimension = "storage.dimension.pack"
	receivePolicy = "policy.can.example"
	sohValidBit   = 63

	// packFrameHook and socFrameHook name the per-row admission hooks of
	// frames 0x312 and 0x313. FrameDisposition gates the whole frame before any
	// row is evaluated; the hooks are a second check on the same rule.
	packFrameHook = "pack-frame-admission"
	socFrameHook  = "soc-frame-admission"
)

func nativeRow(id uint32, name string, slice signalSlice, unit string) roweval.NativeRow[Selector] {
	codec := "unsigned"
	if slice.signed {
		codec = "signed"
	}
	return roweval.NativeRow[Selector]{
		ID:      Profile + "." + name,
		Unit:    unit,
		SpecRef: fmt.Sprintf("CAN %#x", id),
		At:      Selector{Identifier: id, DLC: 8, RawDLC: 8, Slice: slice},
		Wire:    roweval.Layout{BitOffset: slice.offset, Width: slice.width, Codec: codec, ByteOrder: slice.order, BitOrder: "lsb0", WordOrder: "single"},
	}
}

func storageRef(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}, ID: id}
}

func packRead(name, ref string, fact, unit semreg.DefinitionID, divisor int64, hook string) roweval.BindingRow {
	return roweval.BindingRow{NativeID: Profile + "." + name, SubjectRole: packRole, SpecRef: ref,
		Service: storageRef("storage.service.pack"), Capability: storageRef("storage.capability.read.pack"),
		Read: &roweval.ReadRow{Fact: storageRef(fact), Unit: unit, Policy: receivePolicy,
			Dimensions: map[semreg.DefinitionID]string{packDimension: packRole},
			ToRegistry: roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: divisor}, Hook: hook}}}
}

// V104Table returns the native rows of frames 0x311 to 0x313 and the bindings
// that feed storage elements from them. The table enforces frame admission
// itself: a 0x312 frame whose count bytes are outside 1 through 254, or a 0x313
// frame whose SOC is above 100, yields its native rows with a reason and no
// value, and no candidate.
func V104Table() roweval.Table[Selector] {
	rows := []roweval.NativeRow[Selector]{
		nativeRow(0x311, "charge_voltage_limit", signalSlice{0, 16, bigEndian, false}, "native.decivolt"),
		nativeRow(0x311, "charge_current_limit", signalSlice{16, 16, bigEndian, false}, "native.deciampere"),
		nativeRow(0x311, "discharge_current_limit", signalSlice{32, 16, bigEndian, false}, "native.deciampere"),
		nativeRow(0x311, "status_word", signalSlice{48, 16, bigEndian, false}, "native.raw"),
		nativeRow(0x311, "operating_state", signalSlice{56, 2, bitSlice, false}, "native.state"),
		nativeRow(0x312, "protection_flags", signalSlice{0, 16, bigEndian, false}, "native.raw"),
		nativeRow(0x312, "warning_flags", signalSlice{16, 16, bigEndian, false}, "native.raw"),
		nativeRow(0x312, "pack_count", signalSlice{32, 8, bitSlice, false}, "native.count"),
		nativeRow(0x312, "manufacturer", signalSlice{40, 16, bigEndian, false}, "native.raw"),
		nativeRow(0x312, "total_cell_count", signalSlice{56, 8, bitSlice, false}, "native.count"),
		nativeRow(0x313, "pack_voltage", signalSlice{0, 16, bigEndian, true}, "native.centivolt"),
		nativeRow(0x313, "pack_current", signalSlice{16, 16, bigEndian, true}, "native.deciampere"),
		nativeRow(0x313, "maximum_cell_temperature", signalSlice{32, 16, bigEndian, true}, "native.decicelsius"),
		nativeRow(0x313, "soc", signalSlice{48, 8, bitSlice, false}, "native.percent"),
		nativeRow(0x313, "soh", signalSlice{56, 7, bitSlice, false}, "native.percent"),
		nativeRow(0x313, "soh_valid", signalSlice{63, 1, bitSlice, false}, "native.flag"),
	}
	for i := range rows {
		switch rows[i].At.Identifier {
		case 0x312:
			rows[i].Decode.Hook = packFrameHook
		case 0x313:
			rows[i].Decode.Hook = socFrameHook
		}
	}
	return roweval.Table[Selector]{
		Native: rows,
		Bindings: []roweval.BindingRow{
			packRead("pack_current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10, ""),
			packRead("pack_voltage", "CAN 0x313", "storage.pack.voltage", "unit.volt", 100, ""),
			packRead("soc", "CAN 0x313", "storage.state.soc", "unit.ratio", 100, ""),
			packRead("soh", "CAN 0x313", "storage.state.soh", "unit.ratio", 100, "soh-validity"),
		},
		Hooks: map[string]roweval.RowHook{
			"soh-validity": sohValidity,
			packFrameHook:  admitPackFrame,
			socFrameHook:   admitSOCFrame,
		},
	}
}

// FrameDisposition is the frame-level admission of the V1.04 profile. It runs
// before any native row of the frame is evaluated, so a rejected frame gives no
// extracted field, no value and no candidate. A frame that matches no row gives
// roweval.ReasonNoMatch; a frame of a listed identifier that fails the frozen
// growattV104Valid rule gives "not valid".
func FrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	p := frame.Bytes()
	switch frame.ID().Value() {
	case 0x311:
	case 0x312:
		if p[4] < 1 || p[4] > 254 || p[7] < 1 || p[7] > 254 {
			return "not valid"
		}
	case 0x313:
		if p[6] > 100 {
			return "not valid"
		}
	default:
		return roweval.ReasonNoMatch
	}
	return ""
}

// EvaluateFrame gates the frame with FrameDisposition and, only when it is
// admitted, evaluates every native row of the frame and its bindings. A
// rejected frame returns the rejection and no evaluation: no sibling slice is
// extracted from it.
func EvaluateFrame(table roweval.Table[Selector], frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	if reason := FrameDisposition(frame); reason != "" {
		return nil, reason, nil
	}
	out := make(map[string]roweval.Evaluation)
	for _, row := range table.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		ev, err := evaluateRow(table, frame, row, a)
		if err != nil {
			return nil, "", err
		}
		out[row.ID] = ev
	}
	return out, "", nil
}

func evaluateRow(table roweval.Table[Selector], frame canbus.Frame, row roweval.NativeRow[Selector], a roweval.Acquired[Selector]) (roweval.Evaluation, error) {
	a.Selector = Selector{Identifier: frame.ID().Value(), Extended: frame.ID().Extended(), DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
	a.Payload = frame.Data()
	a.Descriptors = V104Descriptors()
	return roweval.Evaluate(table, a)
}

// admitPackFrame is the frozen growattV104Valid rule for 0x312: both count
// bytes (B4 and B7) in 1 through 254.
func admitPackFrame(inputs roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	p := inputs.Inputs()[0].Payload
	if len(p) < 8 || p[4] < 1 || p[4] > 254 || p[7] < 1 || p[7] > 254 {
		return roweval.DerivedValue{}, "not valid"
	}
	return roweval.DerivedValue{Value: value}, ""
}

// admitSOCFrame is the frozen growattV104Valid rule for 0x313: SOC (B6) at
// most 100.
func admitSOCFrame(inputs roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	p := inputs.Inputs()[0].Payload
	if len(p) < 8 || p[6] > 100 {
		return roweval.DerivedValue{}, "not valid"
	}
	return roweval.DerivedValue{Value: value}, ""
}

// sohValidity admits the SOH candidate only when the frame's validity bit (B7
// bit 7) is set. The native SOH row is kept regardless.
func sohValidity(inputs roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	payload := inputs.Inputs()[0].Payload
	if payload[sohValidBit/8]&(1<<(sohValidBit%8)) == 0 {
		return roweval.DerivedValue{}, "not valid"
	}
	return roweval.DerivedValue{Value: value}, ""
}

// V104Descriptors returns the codecs and byte, bit and word orders the V1.04
// table names.
func V104Descriptors() roweval.Descriptors[Selector] {
	identity := func(raw uint64, _ uint16) (uint64, error) { return raw, nil }
	swap16 := func(raw uint64, width uint16) (uint64, error) {
		if width != 16 {
			return 0, fmt.Errorf("big-endian CAN word: width %d", width)
		}
		return uint64(bits.ReverseBytes16(uint16(raw))), nil
	}
	return roweval.Descriptors[Selector]{
		Codecs: map[string]roweval.Codec{"signed": integerCodec(true), "unsigned": integerCodec(false)},
		ByteOrders: map[string]roweval.Order{
			bigEndian: {Decode: swap16, Encode: swap16}, bitSlice: {Decode: identity, Encode: identity},
		},
		BitOrders:  map[string]roweval.Order{"lsb0": {Decode: identity, Encode: identity}},
		WordOrders: map[string]roweval.Order{"single": {Decode: identity, Encode: identity}},
		Extent:     func(at Selector) (int, bool) { return int(at.DLC), true },
	}
}

func integerCodec(signed bool) roweval.Codec {
	return roweval.Codec{
		Decode: func(raw uint64, width uint16) (semreg.Decimal, error) {
			n := int64(raw)
			if signed && raw&(1<<(width-1)) != 0 {
				n -= 1 << width
			}
			d := semreg.Decimal{}
			for n != 0 && n%10 == 0 {
				n /= 10
				d.Exponent10++
			}
			d.Coefficient = strconv.FormatInt(n, 10)
			return d, nil
		},
		Encode: func(d semreg.Decimal, width uint16) (uint64, error) {
			if err := d.Validate(); err != nil {
				return 0, err
			}
			if d.Exponent10 < 0 {
				return 0, fmt.Errorf("CAN integer cannot encode a fraction")
			}
			s := d.Coefficient + strings.Repeat("0", int(d.Exponent10))
			if signed {
				n, err := strconv.ParseInt(s, 10, int(width))
				return uint64(n) & ((1 << width) - 1), err
			}
			return strconv.ParseUint(s, 10, int(width))
		},
	}
}
