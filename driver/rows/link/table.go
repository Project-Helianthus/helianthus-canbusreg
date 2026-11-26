package link

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Selector identifies a field within a LINK frame family. Family is the
// identifier shifted right by sixteen bits (0x18 and the frame family byte);
// the destination and source address bytes are selection conditions.
type Selector struct {
	Family      uint16
	Extended    bool
	DLC, RawDLC uint8
	Field       string
	Slice       signalSlice
}

type signalSlice struct {
	offset, width uint16
	order         string
}

const (
	bigEndian    = "big-endian"
	littleEndian = "little-endian"
	bitSlice     = "bit-slice"

	rowPrefix     = "growatt.bms.link."
	rowHookPrefix = "row:"
)

const (
	qFlavor        = "flavor"
	qDirection     = "direction"
	qScope         = "scope"
	qBank          = "bank"
	qEndpoint      = "endpoint"
	qDestination   = "destination"
	qSource        = "source"
	qContractGroup = "contract-group"
	qLayout        = "layout"
	qGroup         = "group"
	qSlot          = "slot"
	qRelease11     = "release-11"
	qLocalUnits    = "local-units"
	qGroupTwo      = "group-two"
	qGroupEight    = "group-eight"
	qConfigWord    = "configuration-word"
	qIdentityASCII = "identity-ascii"
)

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func identity(raw uint64, _ uint16) (uint64, error) { return raw, nil }

func swapBytes(raw uint64, width uint16) (uint64, error) {
	switch width {
	case 16:
		return uint64(bits.ReverseBytes16(uint16(raw))), nil
	case 32:
		return uint64(bits.ReverseBytes32(uint32(raw))), nil
	}
	return 0, fmt.Errorf("big-endian CAN word: width %d", width)
}

// Descriptors returns the codecs and the byte, bit and word orders the table names.
func Descriptors() roweval.Descriptors[Selector] {
	return roweval.Descriptors[Selector]{
		Codecs: map[string]roweval.Codec{"unsigned": unsignedCodec()},
		ByteOrders: map[string]roweval.Order{
			bigEndian:    {Decode: swapBytes, Encode: swapBytes},
			littleEndian: {Decode: identity, Encode: identity},
			bitSlice:     {Decode: identity, Encode: identity},
		},
		BitOrders:  map[string]roweval.Order{"lsb0": {Decode: identity, Encode: identity}},
		WordOrders: map[string]roweval.Order{"single": {Decode: identity, Encode: identity}},
		Extent:     func(at Selector) (int, bool) { return int(at.DLC), true },
	}
}

func unsignedCodec() roweval.Codec {
	return roweval.Codec{
		Decode: func(raw uint64, _ uint16) (semreg.Decimal, error) {
			d := semreg.Decimal{}
			for raw != 0 && raw%10 == 0 {
				raw /= 10
				d.Exponent10++
			}
			d.Coefficient = strconv.FormatUint(raw, 10)
			return d, nil
		},
		Encode: func(number semreg.Decimal, width uint16) (uint64, error) {
			if number.Exponent10 < 0 || width == 0 || width > 64 {
				return 0, fmt.Errorf("unsigned integer width %d", width)
			}
			n, err := strconv.ParseUint(number.Coefficient, 10, 64)
			if err != nil {
				return 0, err
			}
			for i := int32(0); i < int32(number.Exponent10); i++ {
				if n > ^uint64(0)/10 {
					return 0, fmt.Errorf("integer outside %d-bit range", width)
				}
				n *= 10
			}
			if width < 64 && n>>width != 0 {
				return 0, fmt.Errorf("integer outside %d-bit range", width)
			}
			return n, nil
		},
	}
}

// Qualifiers returns the selection vocabulary of one LINK selection and its
// input: the conditions the rows test. The destination and source are the two
// address bytes of the identifier as two uppercase hexadecimal digits.
func Qualifiers(s canbusreg.Selection, in canbusreg.LINKInput) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	group := s.SelectionGroup()
	payload := in.Evidence.Frame.Data()
	values := map[string]string{
		qFlavor:        c.Flavor,
		qDirection:     c.Direction,
		qScope:         c.Scope,
		qBank:          c.Bank,
		qEndpoint:      c.Endpoint,
		qDestination:   fmt.Sprintf("%02X", c.Destination),
		qSource:        fmt.Sprintf("%02X", c.Source),
		qContractGroup: group,
		qLayout:        s.SelectionLayout(),
		qGroup:         c.Group,
		qSlot:          c.Slot,
		qRelease11:     boolText(group == "L8"),
		qLocalUnits:    boolText(group == "L2" || group == "L8"),
		qGroupTwo:      boolText(group == "L2"),
		qGroupEight:    boolText(group == "L8"),
		qConfigWord:    boolText(group == "L8"),
		qIdentityASCII: boolText(asciiIdentity(payload)),
	}
	return roweval.Qualifiers{Values: values}
}

func asciiIdentity(p []byte) bool {
	for _, b := range p {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return len(p) == 8
}

// rowHook withholds a value the decoder does not accept, keeping its raw word:
// a 1802 identity with a non-ASCII byte and a 1803 configuration byte that is
// not the constant 1 under QAAA0006.
func rowHook(spec rowSpec) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		switch spec.check {
		case checkASCII:
			if q[qIdentityASCII] != "true" {
				return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
			}
		case checkConstantOne:
			one := v.Quantity != nil && v.Quantity.Number == semreg.Decimal{Coefficient: "1"}
			if q[qGroupTwo] == "true" && !one {
				return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
			}
		}
		return roweval.DerivedValue{Value: v}, ""
	}
}

// Table returns the native rows of the LINK module telemetry frames and the
// hooks those rows name.
func Table() roweval.Table[Selector] {
	specs := telemetrySpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = rowHook(s)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

func nativeRow(r rowSpec) roweval.NativeRow[Selector] {
	row := roweval.NativeRow[Selector]{
		ID:      r.rowID(),
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %04X", r.frame),
		At:      Selector{Family: r.frame, Extended: true, DLC: 8, RawDLC: 8, Field: r.field, Slice: r.slice},
		When:    roweval.When{Equal: r.conditions()},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: "unsigned", ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor, Add: r.add}},
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

// Attribution is the selection context an observation was evaluated under. A
// group or slot the selection does not carry is absent, never zero.
type Attribution struct {
	Scope, Bank, Endpoint string
	Group, Slot           string
	HasGroup, HasSlot     bool
}

// Result is the evaluation of one LINK observation: the evidence, the
// attribution, the decoder status the selection and bytes give, and every row
// of the frame family keyed by row id. A row that does not apply under the
// selection gives roweval.ReasonNoMatch.
type Result struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Status      string
	Rows        map[string]roweval.Evaluation
	Grades      map[string]string
}

// admitted reports the frame and route conditions of a module telemetry frame:
// the extended identifier 18kkF4SS with DLC 8 and raw DLC 8, the source address
// equal to the endpoint and never FF, and the asserted context equal to the
// selection.
func admitted(s canbusreg.Selection, in canbusreg.LINKInput) bool {
	c := s.SelectionSpecValue()
	f := in.Evidence.Frame
	id := f.ID().Value()
	return c.Flavor == "link" && c.RouteSet && f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 &&
		id>>24 == 0x18 && uint8(id>>8) == c.Destination && uint8(id) == c.Source &&
		c.Endpoint == fmt.Sprintf("%02X", uint8(id)) && in.Evidence.Interface == c.Interface &&
		in.Epoch == c.Epoch && in.Direction == c.Direction && in.Scope == c.Scope && in.Bank == c.Bank &&
		in.Endpoint == c.Endpoint && in.Group == c.Group && in.Slot == c.Slot && in.FieldAge >= 0 &&
		c.Direction == "module-to-coordinator" && c.Destination == 0xf4 && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local"
}

// Evaluate evaluates the native rows of one module telemetry observation. It
// admits only what the decoder admits: an observation outside the route, the
// frame geometry or the selection context gives the reason invalid and no
// rows, and a frame family outside 1800 to 1808 gives no rows.
func Evaluate(s canbusreg.Selection, in canbusreg.LINKInput) (Result, roweval.Reason, error) {
	c := s.SelectionSpecValue()
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	if !admitted(s, in) {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	if family < 0x1800 || family > 0x1808 {
		res.Status = "unsupported"
		return res, roweval.ReasonNoMatch, nil
	}
	q := Qualifiers(s, in)
	res.Status = "accepted"
	if family == 0x1802 {
		p := frame.Data()
		switch {
		case q.Values[qIdentityASCII] != "true":
			res.Status = "invalid"
		case string(p[:4]) != c.ProjectCode || string(p[4:]) != c.Release:
			res.Status = "conflict"
		}
	}
	rows, err := evaluateRows(Table(), frame, q)
	if err != nil {
		return res, "", err
	}
	res.Rows = rows
	decoded := canbusreg.DecodeLINKTelemetry(s, in)
	res.Grades = make(map[string]string)
	for id, ev := range rows {
		if f, ok := decoded.Fields[id[strings.LastIndex(id, ".")+1:]]; ok && ev.Reason != roweval.ReasonNoMatch {
			res.Grades[id] = f.Grade
		}
	}
	return res, "", nil
}

func frameSelector(frame canbus.Frame, at Selector) Selector {
	return Selector{
		Family:   uint16(frame.ID().Value() >> 16),
		Extended: frame.ID().Extended(),
		DLC:      frame.DLC(),
		RawDLC:   frame.RawDLC(),
		Field:    at.Field,
		Slice:    at.Slice,
	}
}

const (
	dispositionAccepted = "accepted"
	dispositionRaw      = "raw"
	dispositionInvalid  = "invalid"
)

// evaluateRows evaluates every row of the table whose family is the frame's.
// A native value with a unit is accepted; one without is raw evidence; a value
// the hook withholds is invalid and keeps its raw word.
func evaluateRows(t roweval.Table[Selector], frame canbus.Frame, q roweval.Qualifiers) (map[string]roweval.Evaluation, error) {
	out := make(map[string]roweval.Evaluation)
	family := uint16(frame.ID().Value() >> 16)
	for _, row := range t.Native {
		if row.At.Family != family {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(t, a)
		if err != nil {
			return nil, err
		}
		if ev.Native == nil {
			continue
		}
		switch {
		case ev.Native.Reason != "":
			ev.Native.Value, ev.Native.Unit, ev.Native.Disposition = nil, "", dispositionInvalid
		case ev.Native.Unit != "":
			ev.Native.Disposition = dispositionAccepted
		default:
			ev.Native.Disposition = dispositionRaw
		}
		out[ev.Native.RowID] = ev
	}
	for _, row := range t.Native {
		if row.At.Family == family {
			if _, ok := out[row.ID]; !ok {
				out[row.ID] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
			}
		}
	}
	return out, nil
}
