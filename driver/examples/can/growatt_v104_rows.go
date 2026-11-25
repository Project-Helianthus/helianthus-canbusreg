package can

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Selector identifies a field within a CAN data frame, not a transport record.
type Selector struct {
	Identifier  uint32
	Extended    bool
	DLC, RawDLC uint8
	Multiplexor *uint64
	Slice       signalSlice
}

type signalSlice struct {
	offset, width uint16
	order         string
	signed        bool
}

const (
	bigEndian     = "big-endian"
	littleEndian  = "little-endian"
	bitSlice      = "bit-slice"
	packRole      = "pack"
	packDimension = "storage.dimension.pack"
	receivePolicy = "policy.can.example"
)

func field(id uint32, extended bool, slice signalSlice, name, unit string) roweval.NativeRow[Selector] {
	codec := "unsigned"
	if slice.signed {
		codec = "signed"
	}
	return roweval.NativeRow[Selector]{ID: name, Unit: unit, SpecRef: fmt.Sprintf("CAN %#x", id),
		At:   Selector{Identifier: id, Extended: extended, DLC: 8, RawDLC: 8, Slice: slice},
		Wire: roweval.Layout{BitOffset: slice.offset, Width: slice.width, Codec: codec, ByteOrder: slice.order, BitOrder: "lsb0", WordOrder: "single"}}
}

func storageRef(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}, ID: id}
}

func packRead(row, ref string, fact, unit semreg.DefinitionID, divisor int64) roweval.BindingRow {
	return roweval.BindingRow{NativeID: row, SubjectRole: packRole, SpecRef: ref,
		Service: storageRef("storage.service.pack"), Capability: storageRef("storage.capability.read.pack"),
		Read: &roweval.ReadRow{Fact: storageRef(fact), Unit: unit, Policy: receivePolicy,
			Dimensions: map[semreg.DefinitionID]string{packDimension: packRole}, ToRegistry: scale(divisor)}}
}

func scale(divisor int64) roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: divisor}}
}

func v104Rows(descriptors roweval.Descriptors[Selector]) roweval.Table[Selector] {
	soh := field(0x313, false, signalSlice{56, 7, bitSlice, false}, "state_of_health", "native.percent")
	soh.Decode.Hook = "soh-validity"
	return roweval.Table[Selector]{
		Native: []roweval.NativeRow[Selector]{
			field(0x311, false, signalSlice{0, 16, bigEndian, false}, "charge_voltage_limit", "native.decivolt"),
			field(0x311, false, signalSlice{16, 16, bigEndian, false}, "charge_current_limit", "native.deciampere"),
			field(0x311, false, signalSlice{32, 16, bigEndian, false}, "discharge_current_limit", "native.deciampere"),
			field(0x311, false, signalSlice{48, 16, bigEndian, false}, "status_word", "native.raw"),
			field(0x312, false, signalSlice{32, 8, bitSlice, false}, "pack_count", "native.count"),
			field(0x312, false, signalSlice{56, 8, bitSlice, false}, "total_cell_count", "native.count"),
			field(0x313, false, signalSlice{0, 16, bigEndian, true}, "voltage", "native.centivolt"),
			field(0x313, false, signalSlice{16, 16, bigEndian, true}, "current", "native.deciampere"),
			field(0x313, false, signalSlice{32, 16, bigEndian, true}, "maximum_cell_temperature", "native.decicelsius"),
			field(0x313, false, signalSlice{48, 8, bitSlice, false}, "state_of_charge", "native.percent"),
			soh,
		},
		Bindings: []roweval.BindingRow{
			// The 0x311 rows have no binding in either direction.
			packRead("current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10),
			packRead("state_of_charge", "CAN 0x313", "storage.state.soc", "unit.ratio", 100),
			packRead("state_of_health", "CAN 0x313", "storage.state.soh", "unit.ratio", 100),
		},
		Hooks: map[string]roweval.RowHook{"soh-validity": sohValidity(descriptors)},
	}
}

func sohValidity(descriptors roweval.Descriptors[Selector]) roweval.RowHook {
	validity := field(0x313, false, signalSlice{63, 1, bitSlice, false}, "state_of_health_valid", "native.raw")
	validity.Wire.Sentinels = map[uint64]string{0: "not valid"}
	table := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{validity}}
	return func(inputs roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, value roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		ev, err := roweval.Evaluate(table, roweval.Acquired[Selector]{
			Selector: validity.At, Payload: inputs.Inputs()[0].Payload, Descriptors: descriptors,
		})
		if err != nil || ev.Native == nil {
			return roweval.DerivedValue{}, roweval.ReasonDecode
		}
		if ev.Native.Reason == roweval.ReasonSentinel {
			return roweval.DerivedValue{}, roweval.Reason(ev.Native.Disposition)
		}
		if ev.Native.Reason != "" {
			return roweval.DerivedValue{}, ev.Native.Reason
		}
		return roweval.DerivedValue{Value: value}, ""
	}
}

func canDescriptors() roweval.Descriptors[Selector] {
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
			bigEndian: {Decode: swap16, Encode: swap16}, littleEndian: {Decode: identity, Encode: identity}, bitSlice: {Decode: identity, Encode: identity},
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
				return 0, errors.New("CAN integer cannot encode a fraction")
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

func decodeRecord(record receiver.Record) (canbusreg.Evidence, error) {
	frame, err := canbus.DecodeSocketCANRecord(record.Raw)
	e := canbusreg.Evidence{Interface: record.Interface, Sequence: record.Sequence, Monotonic: record.Monotonic, Frame: frame}
	copy(e.RawRecord[:], record.Raw)
	return e, err
}

func evaluateSlice(table roweval.Table[Selector], frame canbus.Frame, slice signalSlice, a roweval.Acquired[Selector]) (roweval.Evaluation, error) {
	a.Selector = Selector{Identifier: frame.ID().Value(), Extended: frame.ID().Extended(), DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: slice}
	a.Payload = frame.Data()
	a.Descriptors = canDescriptors()
	return roweval.Evaluate(table, a)
}

func v104Disposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	p := frame.Bytes()
	switch frame.ID().Value() {
	case 0x311:
	case 0x312:
		if p[4] == 0 || p[4] == 255 || p[7] == 0 || p[7] == 255 {
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

func evaluateV104(record receiver.Record, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	e, err := decodeRecord(record)
	if err != nil {
		return nil, roweval.ReasonNoMatch, nil
	}
	if reason := v104Disposition(e.Frame); reason != "" {
		return nil, reason, nil
	}
	table := v104Rows(canDescriptors())
	out := make(map[string]roweval.Evaluation)
	for _, row := range table.Native {
		if row.At.Identifier != e.Frame.ID().Value() {
			continue
		}
		result, err := evaluateSlice(table, e.Frame, row.At.Slice, a)
		if err != nil {
			return nil, "", err
		}
		out[row.ID] = result
	}
	return out, "", nil
}
