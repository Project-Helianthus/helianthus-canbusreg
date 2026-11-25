package bms

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// rowName returns the native row name without the profile prefix.
func rowName(id string) string { return strings.TrimPrefix(id, Profile+".") }

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
	bigEndian = "big-endian"
	bitSlice  = "bit-slice"
	packRole  = "pack"
	// packSlot names the coordinate slot that carries the configured pack
	// resource. The fact key takes its dimension from this slot, never from
	// the role name, so two configured packs give two keys.
	packSlot      = "pack-resource"
	packDimension = "storage.dimension.pack"
	sohValidBit   = 63

	// sohValidityHook is the validity-bit gate of the SOH binding, the same
	// hook the EX03 exemplar signs. Frame admission is FrameDisposition, not a
	// row hook.
	sohValidityHook = "soh-validity"
	// voltageHook and the SOH bound of sohValidity keep a candidate-only
	// domain: the storage catalog bounds storage.pack.voltage below at zero and
	// storage.state.soh above at one. No signed row pattern expresses a
	// candidate-only bound, so these two hooks are an open stop item (see the
	// report of this package); the native rows keep the signed values.
	voltageHook = "pack-voltage-nonnegative"
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
		Read: &roweval.ReadRow{Fact: storageRef(fact), Unit: unit,
			Dimensions: map[semreg.DefinitionID]string{packDimension: packSlot},
			ToRegistry: roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: divisor}, Hook: hook}}}
}

// withPolicy returns the table with every read row bound to the freshness
// policy of the caller's template, the source policy the host applies. The
// table itself names no policy.
func withPolicy(t roweval.Table[Selector], policy semreg.PolicyID) roweval.Table[Selector] {
	bindings := make([]roweval.BindingRow, len(t.Bindings))
	for i, b := range t.Bindings {
		if b.Read != nil {
			r := *b.Read
			r.Policy = policy
			b.Read = &r
		}
		bindings[i] = b
	}
	t.Bindings = bindings
	return t
}

// withoutEmptyPackSlot removes a pack coordinate that is present with an empty
// value, so the evaluator reports the pack as unresolved instead of emitting a
// fact key with an empty pack dimension.
func withoutEmptyPackSlot(c map[string]string) map[string]string {
	if c[packSlot] != "" {
		return c
	}
	out := make(map[string]string, len(c))
	for k, v := range c {
		if k != packSlot {
			out[k] = v
		}
	}
	return out
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
	return roweval.Table[Selector]{
		Native: rows,
		Bindings: []roweval.BindingRow{
			packRead("pack_current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10, ""),
			packRead("pack_voltage", "CAN 0x313", "storage.pack.voltage", "unit.volt", 100, voltageHook),
			packRead("soc", "CAN 0x313", "storage.state.soc", "unit.ratio", 100, ""),
			packRead("soh", "CAN 0x313", "storage.state.soh", "unit.ratio", 100, sohValidityHook),
		},
		Hooks: map[string]roweval.RowHook{
			sohValidityHook: sohValidity,
			voltageHook:     nonNegativeVoltage,
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
	table = withPolicy(table, a.Template.FreshnessPolicy.PolicyID)
	a.Coordinates = withoutEmptyPackSlot(a.Coordinates)
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
	// Each row of a frame may produce its own candidate; the candidate identity
	// is the template's identity extended by the row, so the candidates of one
	// frame are distinct within a batch.
	a.Template.CandidateID = semreg.CandidateID(string(a.Template.CandidateID) + "." + rowName(row.ID))
	a.Selector = Selector{Identifier: frame.ID().Value(), Extended: frame.ID().Extended(), DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
	a.Payload = frame.Data()
	a.Descriptors = V104Descriptors()
	ev, err := roweval.Evaluate(table, a)
	var inputErr *roweval.InputError
	if errors.As(err, &inputErr) {
		// An unresolved role or pack slot withholds only the candidate. The
		// native evaluation the evaluator computed is kept with the reason.
		switch inputErr.Kind {
		case roweval.InputUnresolvedRole:
			ev.Reason = roweval.ReasonUnresolvedRole
		case roweval.InputUnresolvedSlot:
			ev.Reason = unresolvedSlot
		default:
			return ev, err
		}
		ev.Candidate, ev.Subject = nil, nil
		ev.Detail = inputErr.Detail
		return ev, nil
	}
	return ev, err
}

// unresolvedSlot is the reason of a binding whose pack resource slot is not
// supplied by the caller.
const unresolvedSlot roweval.Reason = "unresolved_slot"

// nonNegativeVoltage withholds a negative pack voltage from the storage
// candidate. The catalog bound of storage.pack.voltage is zero; the native
// signed row is unchanged.
func nonNegativeVoltage(_ roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	if value.Quantity != nil && strings.HasPrefix(value.Quantity.Number.Coefficient, "-") {
		return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
	}
	return roweval.DerivedValue{Value: value}, ""
}

// sohValidity admits the SOH candidate only when the frame's validity bit (B7
// bit 7) is set and the SOH value is within the storage ratio bound of 1
// (SOH 0 through 100). The native SOH row is kept regardless.
func sohValidity(inputs roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	payload := inputs.Inputs()[0].Payload
	if payload[sohValidBit/8]&(1<<(sohValidBit%8)) == 0 {
		return roweval.DerivedValue{}, "not valid"
	}
	if payload[7]&0x7f > 100 {
		return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
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
