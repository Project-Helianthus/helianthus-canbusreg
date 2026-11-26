package pcs

import (
	"fmt"
	"math/big"
	"math/bits"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// The identity, version, serial-fragment and history frames of the standard PCS
// profile: 0x320, 0x321, 0x323, 0x324 and 0x325, as DecodePCSIdentity emits them.
// Each field of the decoder is one native row. The serial fragments of 0x324 are
// the byte rows of that frame, each kept as its own raw row; no row assembles a
// serial. Selection, publisher resolution and PCSIdentitySerialAssembler stay in
// the decoder.

// qPlaceholderPath is the selection qualifier that is "true" when the decoder
// takes its placeholder path for the frame: the aggregate gateway for 0x321,
// 0x324 and 0x325, and the two projects that name 0x324 a placeholder.
const qPlaceholderPath = "placeholder-path"

// qGatewayHigh is the qualifier that carries byte 3 of 0x323, the high byte of the
// aggregate gateway configuration count (B0 + 256 x B3, as DecodePCSIdentity reads it).
const qGatewayHigh = "identity-byte-3"

// gateIdentityPrefix prefixes the gate qualifier of one identity row; the row
// identifier follows.
const gateIdentityPrefix = "identity-gate:"

// identityCtx is the selection and payload the decoder's gates read.
type identityCtx struct {
	id                                    string
	group                                 string
	selected, local, gateway              bool
	payload                               [8]byte
	measured, thresholdKnown, alternative bool
}

func newIdentityCtx(s canbusreg.Selection, in canbusreg.PCSIdentityPCSInput) identityCtx {
	c := s.SelectionSpecValue()
	g := s.SelectionGroup()
	var p [8]byte
	copy(p[:], in.Evidence.Frame.Data())
	id := c.ProjectCode + c.Release
	return identityCtx{
		id:             id,
		group:          g,
		selected:       g != "" && c.Flavor == "pcs-standard",
		local:          c.Scope == "local",
		gateway:        id == "QCAA0001" && c.Scope == "aggregate",
		payload:        p,
		measured:       in.MeasurementValid,
		thresholdKnown: in.ThresholdStateKnown,
		alternative:    in.AlternativeEligible,
	}
}

// capable is the decoder's contract test for the family bytes of 0x321.
func (c identityCtx) capable() bool {
	switch c.group {
	case "L4", "L5", "L6", "L7", "L8":
		return true
	}
	switch c.id {
	case "QAAA0008", "QAAa6008", "QAAa6009":
		return true
	}
	return false
}

// asciiFamily reports whether the four family bytes of 0x321 are printable ASCII.
func (c identityCtx) asciiFamily() bool {
	for _, b := range c.payload[1:5] {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

// omitted reports whether the six bytes the family field reads from are zero.
func (c identityCtx) omitted() bool {
	return c.payload[0] == 0 && c.payload[1] == 0 && c.payload[2] == 0 && c.payload[3] == 0 && c.payload[4] == 0 && c.payload[5] == 0
}

// familyOmittedApplies is the decoder's gate of the unlabelled family marker.
func (c identityCtx) familyOmittedApplies() bool {
	if !c.selected || !c.local || !c.omitted() {
		return false
	}
	if c.capable() {
		return true
	}
	switch c.id {
	case "QAaT8252", "QAAA0002", "QAAA0005", "QAAA0006", "QAAA0007", "QAAa7004", "QAAa8008":
		return true
	}
	return false
}

// placeholderPath reports whether the decoder emits the placeholder layout of a
// frame instead of its field switch.
func (c identityCtx) placeholderPath(frame uint32) bool {
	switch frame {
	case 0x321, 0x325:
		return c.gateway
	case 0x324:
		return c.gateway || (c.selected && c.local && (c.id == "QAaT8252" || c.id == "QAAA0002"))
	}
	return false
}

// identitySpec is one native row of an identity frame before it is built.
type identitySpec struct {
	frame         uint32
	layout, field string
	slice         signalSlice
	unit          string
	raw, zero     bool
	gate          func(identityCtx) bool
	cond          []string
	// sum marks the aggregate count of 0x323: B0 + 256 x B3, the high byte read
	// from the qualifier qGatewayHigh.
	sum bool
}

// bigDWord is the big-endian 32-bit word of four bytes from a bit offset.
func bigDWord(at uint16) signalSlice { return signalSlice{offset: at, width: 32, order: bigEndian} }

func identitySpecs() []identitySpec {
	sel := func(c identityCtx) bool { return c.selected }
	zeroPayload := func(c identityCtx) bool { return c.payload == [8]byte{} }
	byteRows := func(frame uint32, layout string, cond []string) []identitySpec {
		out := make([]identitySpec, 0, 8)
		for at := uint16(0); at < 8; at++ {
			out = append(out, identitySpec{frame: frame, layout: layout, field: fmt.Sprintf("byte_%d", at), slice: byteAt(at * 8), raw: true, cond: cond})
		}
		return out
	}
	placeholderPath := []string{qPlaceholderPath, "true"}
	fieldPath := []string{qPlaceholderPath, "false"}
	var specs []identitySpec

	// 0x320: identity and version, selected standard contract only.
	specs = append(specs,
		identitySpec{frame: 0x320, field: "product_0", slice: byteAt(0), gate: sel},
		identitySpec{frame: 0x320, field: "product_1", slice: byteAt(8), gate: sel},
		identitySpec{frame: 0x320, field: "hardware", slice: byteAt(16), gate: sel},
		identitySpec{frame: 0x320, field: "version_3", slice: littleWord(24), gate: sel},
		identitySpec{frame: 0x320, field: "version_5", slice: littleWord(40), gate: sel},
		identitySpec{frame: 0x320, field: "reserved_7", slice: byteAt(56), gate: func(c identityCtx) bool { return c.selected && c.payload[7] == 0 }},
	)

	// 0x321: family and tail on the field path; the bytes only on the placeholder path.
	specs = append(specs,
		identitySpec{frame: 0x321, field: "family", slice: bigDWord(8), gate: func(c identityCtx) bool {
			return c.selected && c.capable() && c.payload[0] == 0 && c.payload[5] == 0 && c.asciiFamily()
		}, cond: fieldPath},
		identitySpec{frame: 0x321, field: "family_omitted", slice: byteAt(0), zero: true, gate: func(c identityCtx) bool { return c.familyOmittedApplies() }, cond: fieldPath},
		identitySpec{frame: 0x321, field: "reserved_0", slice: byteAt(0), raw: true, cond: fieldPath},
		identitySpec{frame: 0x321, field: "reserved_5", slice: byteAt(40), raw: true, cond: fieldPath},
		identitySpec{frame: 0x321, field: "tail", slice: bigWord(48), gate: sel, cond: fieldPath},
	)
	specs = append(specs, byteRows(0x321, "placeholder", placeholderPath)...)
	specs = append(specs, identitySpec{frame: 0x321, layout: "placeholder", field: "placeholder", slice: byteAt(0), zero: true, gate: zeroPayload, cond: placeholderPath})

	// 0x323: configuration count, parameter, threshold and synthesized bytes.
	specs = append(specs,
		identitySpec{frame: 0x323, field: "count", slice: byteAt(0), gate: sel, cond: []string{"gateway", "false"}},
		identitySpec{frame: 0x323, layout: "gateway", field: "count", slice: byteAt(0), sum: true, gate: func(c identityCtx) bool { return c.gateway }, cond: []string{"gateway", "true"}},
		identitySpec{frame: 0x323, field: "parameter", slice: bigWord(8), raw: true},
		identitySpec{frame: 0x323, field: "separate_3", slice: byteAt(24), raw: true},
		identitySpec{frame: 0x323, field: "synthesized_4", slice: byteAt(32), gate: sel},
		identitySpec{frame: 0x323, field: "synthesized_5", slice: byteAt(40), gate: sel},
		identitySpec{frame: 0x323, field: "synthesized_6", slice: byteAt(48), gate: sel},
		identitySpec{frame: 0x323, field: "synthesized_7", slice: byteAt(56), gate: sel},
		identitySpec{frame: 0x323, field: "threshold", slice: bigWord(8), unit: "mV", gate: func(c identityCtx) bool {
			return c.selected && c.local && c.group == "L8" && c.measured && c.thresholdKnown && !c.alternative
		}},
	)

	// 0x324: the serial fragments, each byte a raw row; the placeholder path adds the zero row.
	specs = append(specs, byteRows(0x324, "", nil)...)
	specs = append(specs, identitySpec{frame: 0x324, layout: "placeholder", field: "placeholder", slice: byteAt(0), zero: true, gate: zeroPayload, cond: placeholderPath})

	// 0x325: the history bytes; the placeholder path adds the zero row.
	specs = append(specs, byteRows(0x325, "", nil)...)
	specs = append(specs, identitySpec{frame: 0x325, layout: "placeholder", field: "placeholder", slice: byteAt(0), zero: true, gate: zeroPayload, cond: placeholderPath})
	return specs
}

// identityRowID is the row identifier of an identity spec:
// growatt.bms.pcs.standard.<frame>[.<layout>].<field>.
func identityRowID(sp identitySpec) string {
	layout := ""
	if sp.layout != "" {
		layout = sp.layout + "."
	}
	return fmt.Sprintf("%s%X.%s%s", rowPrefix, sp.frame, layout, sp.field)
}

// identityGateKey is the qualifier key of the gate of one identity row, or
// empty when the row has no gate.
func identityGateKey(sp identitySpec) string {
	if sp.gate == nil {
		return ""
	}
	return gateIdentityPrefix + identityRowID(sp)
}

// gatewayCountHook adds the high byte of the aggregate gateway count to the
// value of its low byte, as DecodePCSIdentity does. The base hook withholds the
// value under the same rules as every other identity row. The low byte is read as
// the exact integer its decimal denotes, so a normalized value such as 1 x 10^1
// for B0 = 10 is summed the same as 10, and the sum is written in normalized form.
func gatewayCountHook(base roweval.RowHook) roweval.RowHook {
	return func(in roweval.DetachedInputs, id roweval.RowID, dir roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		dv, reason := base(in, id, dir, v)
		if reason != "" {
			return dv, reason
		}
		high, err := strconv.ParseUint(in.Qualifiers().Values[qGatewayHigh], 10, 8)
		if err != nil || v.Kind != semreg.ValueQuantity || v.Quantity == nil {
			return roweval.DerivedValue{}, roweval.ReasonConversion
		}
		low, ok := wholeDecimalOf(v.Quantity.Number)
		if !ok {
			return roweval.DerivedValue{}, roweval.ReasonConversion
		}
		sum := normalizedDecimal(new(big.Int).Add(low, big.NewInt(256*int64(high))))
		value := semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: sum, Unit: v.Quantity.Unit}}
		return roweval.DerivedValue{Value: value, Citations: dv.Citations}, ""
	}
}

// wholeDecimalOf is the exact non-negative integer a semreg decimal of a byte
// denotes, or false when it is negative or not a whole number.
func wholeDecimalOf(d semreg.Decimal) (*big.Int, bool) {
	n, ok := new(big.Int).SetString(d.Coefficient, 10)
	if !ok || n.Sign() < 0 {
		return nil, false
	}
	switch {
	case d.Exponent10 >= 0:
		return n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.Exponent10)), nil)), true
	default:
		q, rem := new(big.Int).QuoRem(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-d.Exponent10)), nil), new(big.Int))
		return q, rem.Sign() == 0
	}
}

// normalizedDecimal is the whole number n as a decimal with no trailing zero in
// its coefficient: 520 is 52 x 10^1.
func normalizedDecimal(n *big.Int) semreg.Decimal {
	if n.Sign() == 0 {
		return semreg.Decimal{Coefficient: "0"}
	}
	c := new(big.Int).Set(n)
	ten := big.NewInt(10)
	var exp int32
	for {
		q, rem := new(big.Int).QuoRem(c, ten, new(big.Int))
		if rem.Sign() != 0 {
			break
		}
		c, exp = q, exp+1
	}
	return semreg.Decimal{Coefficient: c.String(), Exponent10: exp}
}

// identityRow builds one native row of an identity frame: the frame's identifier
// with DLC 8 and raw DLC 8, the slice of the field, the layout of its selection and
// the exact conversion. A zero row reads the field's byte and scales it by zero;
// its value is the constant zero the decoder states, under its gate.
func identityRow(sp identitySpec) roweval.NativeRow[Selector] {
	num := int64(1)
	if sp.zero {
		num = 0
	}
	row := roweval.NativeRow[Selector]{
		ID:      identityRowID(sp),
		Unit:    sp.unit,
		SpecRef: fmt.Sprintf("CAN %#x", sp.frame),
		At:      Selector{Identifier: sp.frame, DLC: 8, RawDLC: 8, Slice: sp.slice},
		When:    roweval.When{Equal: when(sp.cond...)},
		Wire:    roweval.Layout{BitOffset: sp.slice.offset, Width: sp.slice.width, Codec: "unsigned", ByteOrder: sp.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: num, Den: 1}},
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

// identityRows returns the native rows of the identity frames, the row hooks
// they name, and the identifiers of the rows the decoder never interprets.
func identityRows() ([]roweval.NativeRow[Selector], map[string]roweval.RowHook, map[string]bool) {
	specs := identitySpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	raw := make(map[string]bool)
	for _, sp := range specs {
		row := identityRow(sp)
		rows = append(rows, row)
		hook := rowHook(sp.field, identityGateKey(sp))
		if sp.sum {
			hook = gatewayCountHook(hook)
		}
		hooks[row.Decode.Hook] = hook
		if sp.raw {
			raw[row.ID] = true
		}
	}
	return rows, hooks, raw
}

// identityDescriptors are the standard descriptors with a 32-bit big-endian
// order, which the family word of 0x321 needs.
func identityDescriptors() roweval.Descriptors[Selector] {
	d := Descriptors()
	beWord := func(raw uint64, width uint16) (uint64, error) {
		switch width {
		case 16:
			return uint64(bits.ReverseBytes16(uint16(raw))), nil
		case 32:
			return uint64(bits.ReverseBytes32(uint32(raw))), nil
		}
		return 0, fmt.Errorf("big-endian CAN word: width %d", width)
	}
	orders := make(map[string]roweval.Order, len(d.ByteOrders))
	for k, v := range d.ByteOrders {
		orders[k] = v
	}
	orders[bigEndian] = roweval.Order{Decode: beWord, Encode: beWord}
	d.ByteOrders = orders
	return d
}

// identityFrameDisposition admits only the standard identifiers, DLC 8 and raw
// DLC 8 of the identity frames the table names.
func identityFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x320, 0x321, 0x323, 0x324, 0x325:
		return ""
	}
	return roweval.ReasonNoMatch
}

// identityQualifiers returns the standard qualifiers of the observation with the
// identity gates and the placeholder path of its frame added.
func identityQualifiers(s canbusreg.Selection, in canbusreg.PCSIdentityPCSInput) roweval.Qualifiers {
	q := Qualifiers(s, in.PCSStandardPCSInput)
	c := newIdentityCtx(s, in)
	q.Values[qPlaceholderPath] = boolText(c.placeholderPath(in.Evidence.Frame.ID().Value()))
	q.Values[qGatewayHigh] = strconv.Itoa(int(c.payload[3]))
	for _, sp := range identitySpecs() {
		if sp.gate != nil {
			q.Values[identityGateKey(sp)] = boolText(sp.gate(c))
		}
	}
	return q
}

// IdentityResult is the evaluation of one identity observation: its evidence, its
// selection attribution and the evaluation of every identity row of its
// identifier, keyed by row identifier. Rows that do not apply give
// roweval.ReasonNoMatch.
type IdentityResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
	// Grades is the evidence grade the decoder gives each field, keyed by row
	// identifier: Proven, Hypothesis or Unknown. It is independent of the
	// disposition of the row, so a Hypothesis value that is accepted stays
	// distinguishable from a Proven one.
	Grades map[string]string
}

// EvaluateIdentity evaluates the native rows of one identity observation under
// the selection and decoder input it was received with. A frame DecodePCSIdentity
// refuses gives the decoder's status as the reason and no rows. Each row is
// evaluated on its own, so one observation of a shared slice never makes two rows
// ambiguous.
func EvaluateIdentity(s canbusreg.Selection, in canbusreg.PCSIdentityPCSInput) (IdentityResult, roweval.Reason, error) {
	res := IdentityResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	frame := in.Evidence.Frame
	decoded := canbusreg.DecodePCSIdentity(s, in)
	if decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	if reason := identityFrameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	q := identityQualifiers(s, in)
	rows, hooks, rawRows := identityRows()
	res.Rows = make(map[string]roweval.Evaluation)
	res.Grades = make(map[string]string)
	for _, sp := range identitySpecs() {
		if sp.frame != frame.ID().Value() {
			continue
		}
		if f, ok := decoded.Fields[sp.field]; ok {
			res.Grades[identityRowID(sp)] = f.Grade
		}
	}
	for _, row := range rows {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		t := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: hooks}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At.Slice), Payload: frame.Data(), Qualifiers: q, Descriptors: identityDescriptors()}
		ev, err := roweval.Evaluate(t, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil {
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			ev.Native.Disposition = disposition(ev.Native, rawRows[row.ID])
		}
		res.Rows[row.ID] = ev
	}
	return res, "", nil
}
