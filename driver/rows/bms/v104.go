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

	// storageDefinitionVersion is the version of the storage pack definitions
	// the pinned SemReg module carries. Every definition reference the table
	// names carries it, as the catalog's own references do.
	storageDefinitionVersion = "1.1.0"

	// sohValidityHook is the validity-bit gate of the SOH binding, the same
	// hook the EX03 exemplar signs. Frame admission is FrameDisposition, not a
	// row hook.
	sohValidityHook = "soh-validity"
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

// bitRow is the native row of one bit: bit of the byte at byteIdx.
func bitRow(id uint32, byteIdx, bit int, name string) roweval.NativeRow[Selector] {
	return nativeRow(id, name, signalSlice{offset: uint16(8*byteIdx + bit), width: 1, order: bitSlice}, "native.flag")
}

// bitRows returns one native row per named bit of the byte at byteIdx, bit
// index i being names[i]. An empty name is a reserved or unnamed bit: it has
// no row and stays inside the raw word rows.
func bitRows(id uint32, byteIdx int, names ...string) []roweval.NativeRow[Selector] {
	var rows []roweval.NativeRow[Selector]
	for bit, name := range names {
		if name != "" {
			rows = append(rows, bitRow(id, byteIdx, bit, name))
		}
	}
	return rows
}

func storageRef(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: semreg.PackRef{ID: "helianthus.pack.storage", Version: storageDefinitionVersion}, ID: id, Version: storageDefinitionVersion}
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

// V104Table returns the native rows of the V1.04 frames 0x311 to 0x321 and the
// bindings that feed storage elements from frames 0x311 to 0x313. The table
// enforces frame admission itself: a 0x312 frame whose count bytes are outside
// 1 through 254, or a 0x313 frame whose SOC is above 100, yields no rows.
//
// The bindings are the signed EX03 patterns and their pack-voltage sibling.
// Values with no signed pattern are not bound here and are listed in the
// package report as stop items: the protection, warning and interlock symbols,
// the operating-state symbols, the device limits, the power product, the cell
// extrema and the resource and relation rows.
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
	rows = append(rows, v104NamedRows()...)
	return roweval.Table[Selector]{
		Native: rows,
		Bindings: []roweval.BindingRow{
			packRead("pack_current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10, ""),
			packRead("pack_voltage", "CAN 0x313", "storage.pack.voltage", "unit.volt", 100, ""),
			packRead("soc", "CAN 0x313", "storage.state.soc", "unit.ratio", 100, ""),
			packRead("soh", "CAN 0x313", "storage.state.soh", "unit.ratio", 100, sohValidityHook),
		},
		Hooks: map[string]roweval.RowHook{
			sohValidityHook: sohValidity,
		},
	}
}

// v104NamedRows returns the native rows of the named status bits, the
// 0x314 to 0x321 fields and the raw spans of the V1.04 frames, with the slice
// and codec the frozen report decoder names for each (DD03, V1.04 report).
func v104NamedRows() []roweval.NativeRow[Selector] {
	var rows []roweval.NativeRow[Selector]
	// 0x311: byte 7 bits 0 and 1 are operating_state; bits 2 to 7 are named.
	rows = append(rows, bitRows(0x311, 7, "", "", "error_byte_valid", "cell_balance", "sleep", "discharge_output", "charge_output", "battery_terminal_open")...)
	rows = append(rows,
		nativeRow(0x311, "master_operation_mode", signalSlice{48, 2, bitSlice, false}, "native.state"),
		nativeRow(0x311, "storage_system_state", signalSlice{50, 2, bitSlice, false}, "native.state"),
	)
	// 0x312: the protection and warning bytes, one row per named bit. Bits the
	// page leaves reserved (byte 1 bits 0 and 1, byte 2 bits 0 and 5, byte 3
	// bit 3) have no row and stay in the raw word rows.
	rows = append(rows, bitRows(0x312, 0, "protection_soft_start_failure", "protection_module_undervoltage", "protection_module_overvoltage", "protection_cell_undervoltage", "protection_cell_overvoltage", "protection_short_circuit_discharge", "protection_charge_overcurrent", "protection_discharge_overcurrent")...)
	rows = append(rows, bitRows(0x312, 1, "", "", "protection_delta_voltage_failure", "protection_system_error", "protection_charge_undertemperature", "protection_discharge_undertemperature", "protection_charge_overtemperature", "protection_discharge_overtemperature")...)
	rows = append(rows, bitRows(0x312, 2, "", "warning_module_undervoltage", "warning_module_overvoltage", "warning_cell_undervoltage", "warning_cell_overvoltage", "", "warning_charge_overcurrent", "warning_discharge_overcurrent")...)
	rows = append(rows, bitRows(0x312, 3, "warning_internal_communication_failure", "warning_before_shutdown", "warning_delta_voltage_failure", "", "warning_charge_undertemperature", "warning_discharge_undertemperature", "warning_charge_overtemperature", "warning_discharge_overtemperature")...)
	// 0x314: capacities are counts of 10 mAh; the cell voltage spread is in mV.
	rows = append(rows,
		nativeRow(0x314, "remaining_capacity", signalSlice{0, 16, bigEndian, false}, "native.decamilliampere_hour"),
		nativeRow(0x314, "full_charge_capacity", signalSlice{16, 16, bigEndian, false}, "native.decamilliampere_hour"),
		nativeRow(0x314, "cell_voltage_spread", signalSlice{32, 16, bigEndian, false}, "native.millivolt"),
		nativeRow(0x314, "cycle_count", signalSlice{48, 16, bigEndian, false}, "native.count"),
	)
	// 0x315 to 0x318: cells 1 to 16, four big-endian words per frame.
	for id := uint32(0x315); id <= 0x318; id++ {
		for i := 0; i < 4; i++ {
			cell := 4*(id-0x315) + uint32(i) + 1
			rows = append(rows, nativeRow(id, fmt.Sprintf("cell_voltage_%d", cell), signalSlice{uint16(16 * i), 16, bigEndian, false}, "native.millivolt"))
		}
	}
	// 0x319: the request byte, the cell extrema and the protected pack.
	rows = append(rows,
		nativeRow(0x319, "battery_chemistry_code", signalSlice{0, 2, bitSlice, false}, "native.code"),
		bitRow(0x319, 0, 5, "force_charge_request"),
		bitRow(0x319, 0, 6, "discharge_enable"),
		bitRow(0x319, 0, 7, "charge_enable"),
		nativeRow(0x319, "maximum_cell_voltage", signalSlice{8, 16, bigEndian, false}, "native.millivolt"),
		nativeRow(0x319, "minimum_cell_voltage", signalSlice{24, 16, bigEndian, false}, "native.millivolt"),
		nativeRow(0x319, "maximum_cell_index", signalSlice{40, 8, bitSlice, false}, "native.index"),
		nativeRow(0x319, "minimum_cell_index", signalSlice{48, 8, bitSlice, false}, "native.index"),
		nativeRow(0x319, "protected_pack_id", signalSlice{56, 8, bitSlice, false}, "native.index"),
	)
	// 0x320: identity and version; the packed build date and time stay raw
	// because the page leaves their field positions and byte order unspecified.
	rows = append(rows,
		nativeRow(0x320, "manufacturer_identity", signalSlice{0, 16, bigEndian, false}, "native.raw"),
		nativeRow(0x320, "hardware_version", signalSlice{16, 8, bitSlice, false}, "native.version"),
		nativeRow(0x320, "software_version", signalSlice{24, 8, bitSlice, false}, "native.version"),
		nativeRow(0x320, "build_date_time", signalSlice{32, 32, bitSlice, false}, "native.raw"),
	)
	// 0x321: update state and progress; the bytes the page leaves unnamed stay raw.
	rows = append(rows,
		nativeRow(0x321, "update_state", signalSlice{0, 8, bitSlice, false}, "native.state"),
		nativeRow(0x321, "pack_progress", signalSlice{8, 8, bitSlice, false}, "native.raw"),
		nativeRow(0x321, "programming_pack_id", signalSlice{16, 8, bitSlice, false}, "native.index"),
		nativeRow(0x321, "successful_update_count", signalSlice{24, 8, bitSlice, false}, "native.count"),
		nativeRow(0x321, "reserved_bytes", signalSlice{32, 32, bitSlice, false}, "native.raw"),
	)
	return rows
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
	case 0x314, 0x315, 0x316, 0x317, 0x318, 0x319, 0x320, 0x321:
		// The frozen V1.04 rule admits these frames by geometry alone.
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

// sohValidity admits the SOH candidate only when the frame's validity bit (B7
// bit 7) is set. It is the signed EX03 gate and nothing more: a valid SOH above
// the catalog bound of storage.state.soh is not withheld here. The pinned
// storage validator refuses that fact at ingestion, and the report lists it.
// The native SOH row is kept regardless of the bit.
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
