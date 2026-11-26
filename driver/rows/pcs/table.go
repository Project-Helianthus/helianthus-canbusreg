package pcs

import (
	"fmt"
	"math/bits"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

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
	bigEndian    = "big-endian"
	littleEndian = "little-endian"
	bitSlice     = "bit-slice"

	// rowPrefix is the identifier prefix of every standard PCS native row.
	rowPrefix = "growatt.bms.pcs.standard."

	// Selection qualifiers. The When conditions and the gate hooks read them.
	qFlavor          = "flavor"
	qBank            = "bank"
	qScope           = "scope"
	qEndpoint        = "endpoint"
	qLayout          = "layout"
	qContractGroup   = "contract-group"
	qGroup           = "group"
	qSlot            = "slot"
	qRelease11       = "release-11"
	qLocal           = "local"
	qGateway         = "gateway"
	qMeasurementOK   = "measurement-valid"
	qLocalMeasured   = "local-measured"
	qMeasured        = "measured"
	qTemperatureGate = "temperature"

	// qFieldState and qFieldAgeInvalid prefix the per-field dispositions the
	// decoder input supplies: an explicit field state, and a negative field age
	// that the decoder reads as invalid. Each is followed by the field key.
	qFieldState      = "field-state:"
	qFieldAgeInvalid = "field-age-invalid:"

	// rowHookPrefix prefixes the hook name of a native row; the row identifier follows.
	rowHookPrefix = "row:"

	// gateWithheld is the native reason of a field whose selection gate is
	// not met. The native value is withheld and the raw word is kept.
	gateWithheld roweval.Reason = "gate_withheld"
)

// Gate qualifiers, each a boolean the selection supplies as "true" or "false".
// A native row with a gate is withheld, with its raw word kept, unless the
// gate qualifier is "true".
const (
	gateLocalMeasured = qLocalMeasured
	gateMeasured      = qMeasured
	gateLocal         = qLocal
	gateTemperature   = qTemperatureGate
)

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Qualifiers returns the selection vocabulary of one standard PCS selection and
// its input: the When conditions the rows test, the gate qualifiers the gate
// hooks test and the per-field dispositions the decoder input supplies. The
// gates follow the frozen DecodePCSStandard rules: a local QAAA0006 source, the
// release-11 contract (contract group L8), a gateway aggregate and a
// measurement or temperature validity assertion.
func Qualifiers(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	release11 := s.SelectionGroup() == "L8"
	local := c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"
	gateway := c.ProjectCode == "QCAA" && c.Release == "0001" && c.Scope == "aggregate"
	measured := in.MeasurementValid && (local || release11)
	temperature := measured && in.TemperatureValid && (!release11 || in.CountValid && in.Count > 0)
	values := map[string]string{
		qFlavor:          c.Flavor,
		qBank:            c.Bank,
		qScope:           c.Scope,
		qEndpoint:        c.Endpoint,
		qLayout:          s.SelectionLayout(),
		qContractGroup:   s.SelectionGroup(),
		qGroup:           c.Group,
		qSlot:            c.Slot,
		qRelease11:       boolText(release11),
		qLocal:           boolText(local),
		qGateway:         boolText(gateway),
		qMeasurementOK:   boolText(in.MeasurementValid),
		qLocalMeasured:   boolText(local && in.MeasurementValid),
		qMeasured:        boolText(measured),
		qTemperatureGate: boolText(temperature),
	}
	for field, state := range in.FieldStates {
		values[qFieldState+field] = state
	}
	for field, age := range in.FieldAges {
		if age < 0 {
			values[qFieldAgeInvalid+field] = "true"
		}
	}
	return roweval.Qualifiers{Values: values}
}

// dispositionReason is the reason of a field the decoder input states as not
// accepted. The decoder keeps these states as the field status, so the reason
// is the state itself; any other state is invalid, as the decoder reads it.
func dispositionReason(state string) roweval.Reason {
	switch state {
	case "absent", "stale", "unsupported", "conflict", "unassigned":
		return roweval.Reason(state)
	}
	return roweval.ReasonOutsideDomain
}

// rowHook is the hook of one native row. It withholds the value when the
// decoder input gives the field an invalid age (the decoder's invalid status),
// when it states an explicit disposition for the field, or when the row's gate
// qualifier is not "true". Each withheld value keeps its raw word.
func rowHook(field, gate string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		if q[qFieldAgeInvalid+field] == "true" {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		if state, ok := q[qFieldState+field]; ok {
			return roweval.DerivedValue{}, dispositionReason(state)
		}
		if gate != "" && q[gate] != "true" {
			return roweval.DerivedValue{}, gateWithheld
		}
		return roweval.DerivedValue{Value: v}, ""
	}
}

// Descriptors returns the codecs and byte, bit and word orders the standard
// table names.
func Descriptors() roweval.Descriptors[Selector] {
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
		Encode: func(number semreg.Decimal, width uint16) (uint64, error) {
			if number.Exponent10 < 0 {
				return 0, fmt.Errorf("fractional integer %s", number.Coefficient)
			}
			n, err := strconv.ParseInt(number.Coefficient, 10, 64)
			if err != nil {
				return 0, err
			}
			for e := int32(0); e < number.Exponent10; e++ {
				n *= 10
			}
			return uint64(n) & (1<<width - 1), nil
		},
	}
}

// Table returns the native rows of the standard PCS frames and the row hooks
// those rows name. Each row has its own hook, which carries the row's field
// disposition and its gate. The table has no bindings: the bindings of these
// rows are added by the later slices of the package.
func Table() roweval.Table[Selector] {
	specs := standardSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = rowHook(s.field, s.gate)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// frameDisposition admits only the standard identifier with DLC 8 and raw DLC
// 8 of the standard frames the table names.
func frameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x311, 0x312, 0x313, 0x314:
		return ""
	}
	return roweval.ReasonNoMatch
}

// Result is the evaluation of one standard PCS observation: the evidence the
// observation carries and the evaluation of every row of its identifier, keyed
// by row identifier. Rows that do not apply under the selection give
// roweval.ReasonNoMatch.
type Result struct {
	Evidence canbusreg.Evidence
	Rows     map[string]roweval.Evaluation
}

// Evaluate evaluates the native rows of one standard PCS observation under the
// selection and decoder input it was received with. The evaluator admits only
// what DecodePCSStandard admits: a frame the decoder refuses gives the decoder's
// status as the reason and no rows, so an unresolved publisher, a direction or
// context mismatch or a frame outside the table is never evaluated as a value.
func Evaluate(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput) (Result, roweval.Reason, error) {
	res := Result{Evidence: in.Evidence}
	frame := in.Evidence.Frame
	if reason := frameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	if decoded := canbusreg.DecodePCSStandard(s, in); decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	rows, reason, err := evaluateRows(frame, Qualifiers(s, in))
	res.Rows = rows
	return res, reason, err
}

// evaluateRows evaluates every native row of the frame's identifier under the
// qualifiers. It does not check the decoder admission; Evaluate does.
func evaluateRows(frame canbus.Frame, q roweval.Qualifiers) (map[string]roweval.Evaluation, roweval.Reason, error) {
	table := Table()
	out := make(map[string]roweval.Evaluation)
	for _, row := range table.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: row.At, Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(table, a)
		if err != nil {
			return nil, "", err
		}
		switch {
		case ev.Native != nil:
			out[ev.Native.RowID] = ev
		case ev.Reason != roweval.ReasonNoMatch:
			out[row.ID] = ev
		}
	}
	for _, row := range table.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		if _, ok := out[row.ID]; !ok {
			out[row.ID] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
		}
	}
	return out, "", nil
}

// nativeRow builds one native row of the standard table. The selector is the
// standard identifier with DLC 8 and raw DLC 8. The conversion is the exact
// affine x / divisor, and the hook of the row carries its gate and its field
// disposition. The domain, when set, bounds the native value to [0, 100].
func nativeRow(r rowSpec) roweval.NativeRow[Selector] {
	codec := "unsigned"
	if r.slice.signed {
		codec = "signed"
	}
	row := roweval.NativeRow[Selector]{
		ID:      rowPrefix + r.frameToken() + "." + r.layoutToken() + r.field,
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %#x", r.frame),
		At:      Selector{Identifier: r.frame, DLC: 8, RawDLC: 8, Slice: r.slice},
		When:    roweval.When{Equal: r.when},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: codec, ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor}},
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	if r.percentDomain {
		zero, hundred := semreg.Decimal{Coefficient: "0"}, semreg.Decimal{Coefficient: "1", Exponent10: 2}
		row.Domain = roweval.Bounds{Min: &zero, Max: &hundred}
	}
	return row
}

// layoutToken is the selection layout between frame and field, empty when the
// field name is unique within its frame.
func (r rowSpec) layoutToken() string {
	if r.layout == "" {
		return ""
	}
	return r.layout + "."
}

func (r rowSpec) frameToken() string { return fmt.Sprintf("%X", r.frame) }
