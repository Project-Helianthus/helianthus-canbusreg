package pcs

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the native rows of the extended PCS frames 0x1905 to 0x1909
// and of the gateway state record 0x1910 that DecodePCSExtended emits: the cell
// extrema of 0x1905 and 0x1906, the two fault blocks 0x1907 and 0x1908 (one
// byte row and eight bit rows per fault byte), the limits of 0x1909 and the
// gateway state. The identifiers are extended 29-bit identifiers with DLC 8 and
// raw DLC 8. The decoder admits a frame by its route, so a row declares the
// frame kind (the upper 13 bits) and the evaluator presents the folded kind of
// the frame with the route bytes zero. An aggregate coordinator frame sent as
// 0x1915 to 0x1919 is the same frame kind as 0x1905 to 0x1909, as the decoder
// folds it, so it has no row of its own. The kinds 0x1910 to 0x1914 folded onto
// 0x1900 to 0x1904 belong to the rows of those frames.
//
// A fault bit carries no condition label here: the label is the decoder's
// dictionary. Under the selections the extended flavor admits the decoder names
// no bit, so every bit row is raw evidence; the row follows the dictionary the
// selection carries, through EncodeDTC, as the decoder does.

const (
	extendedFaultRowPrefix = "growatt.bms.pcs.extended19."

	extendedFaultFlavor = "pcs-extended19"

	// gateFaultLocalUnits is the decoder's localUnits: a local QAAA0006 source.
	gateFaultLocalUnits = "extended-fault-local-units"
	// qFaultGateway is "true" for the gateway selection (QCAA0001).
	qFaultGateway = "extended-fault-gateway"
	// qFaultGatewayRoute is "true" when the selection's route is the gateway
	// state route: destination 0xff, source 0xf4.
	qFaultGatewayRoute = "extended-fault-gateway-route"
	// qFaultBitKnownPrefix is followed by the bit index; "true" when the
	// selection's dictionary names that bit.
	qFaultBitKnownPrefix = "extended-fault-bit-known:"
	// qFaultInvalidPrefix is followed by the row identifier; "true" when the
	// decoder marks the field invalid.
	qFaultInvalidPrefix = "extended-fault-invalid:"

	gatewayStateKind = 0x1910
)

type extendedFaultSpec struct {
	frame   uint32
	field   string
	slice   signalSlice
	unit    string
	divisor int64
	gate    string
	// raw marks a field the decoder never interprets.
	raw bool
	// acceptedWhen, when set, names the qualifier that must be "true" for the
	// decoder to accept the field; otherwise the field is raw.
	acceptedWhen string
	gateway      bool
}

func extendedFaultID(frame uint32, field string) string {
	return fmt.Sprintf("%s%X.%s", extendedFaultRowPrefix, frame, field)
}

func faultRaw(frame uint32, field string, slice signalSlice) extendedFaultSpec {
	return extendedFaultSpec{frame: frame, field: field, slice: slice, divisor: 1, raw: true}
}

func faultKnown(frame uint32, field string, slice signalSlice) extendedFaultSpec {
	return extendedFaultSpec{frame: frame, field: field, slice: slice, divisor: 1}
}

func faultMillivolt(frame uint32, field string, slice signalSlice) extendedFaultSpec {
	return extendedFaultSpec{frame: frame, field: field, slice: slice, unit: "mV", divisor: 1, gate: gateFaultLocalUnits}
}

func faultGatewayPadding(at uint16) extendedFaultSpec {
	field := fmt.Sprintf("padding_%d", at/8)
	return extendedFaultSpec{frame: gatewayStateKind, field: field, slice: byteAt(at), divisor: 1, raw: true, gateway: true}
}

// extendedFaultSpecs returns the row specifications of frames 0x1905 to 0x1909
// and of the gateway state record.
func extendedFaultSpecs() []extendedFaultSpec {
	specs := []extendedFaultSpec{
		faultKnown(0x1905, "maximum_index", littleWord(0)),
		faultMillivolt(0x1905, "maximum_voltage", littleWord(16)),
		faultKnown(0x1905, "minimum_index", littleWord(32)),
		faultMillivolt(0x1905, "minimum_voltage", littleWord(48)),

		faultKnown(0x1906, "maximum_index", littleWord(0)),
		faultRaw(0x1906, "maximum_temperature_code", byteAt(16)),
		faultKnown(0x1906, "minimum_index", littleWord(24)),
		faultRaw(0x1906, "minimum_temperature_code", byteAt(40)),
		faultRaw(0x1906, "extra", byteAt(48)),
		faultRaw(0x1906, "soh_like", byteAt(56)),

		faultRaw(0x1909, "limit_0", littleWord(0)),
		faultRaw(0x1909, "limit_2", littleWord(16)),
		faultRaw(0x1909, "derived", littleWord(32)),
		faultRaw(0x1909, "flag_6", byteAt(48)),
		faultRaw(0x1909, "flag_7", byteAt(56)),
	}
	for _, frame := range []uint32{0x1907, 0x1908} {
		start := int(frame-0x1907) * 8
		for at := 0; at < 8; at++ {
			n := start + at
			specs = append(specs, faultKnown(frame, fmt.Sprintf("current_fault_%d", n), byteAt(uint16(8*at))))
			for bit := 0; bit < 8; bit++ {
				index := 8*n + bit
				spec := faultRaw(frame, fmt.Sprintf("current_fault_bit_%d", index), bitsAt(uint16(8*at+bit), 1))
				spec.raw = false
				spec.acceptedWhen = fmt.Sprintf("%s%d", qFaultBitKnownPrefix, index)
				specs = append(specs, spec)
			}
		}
	}
	state := faultRaw(gatewayStateKind, "state", byteAt(0))
	state.gateway, state.raw, state.acceptedWhen = true, false, qFaultGateway
	flag := faultRaw(gatewayStateKind, "flag", byteAt(8))
	flag.gateway = true
	address := faultRaw(gatewayStateKind, "downstream_address", byteAt(56))
	address.gateway, address.raw, address.acceptedWhen = true, false, qFaultGateway
	specs = append(specs, state, flag)
	for at := uint16(16); at <= 48; at += 8 {
		specs = append(specs, faultGatewayPadding(at))
	}
	return append(specs, address)
}

func extendedFaultNativeRow(r extendedFaultSpec) roweval.NativeRow[Selector] {
	equal := map[string]string{qFlavor: extendedFaultFlavor}
	if r.gateway {
		equal[qFaultGatewayRoute] = "true"
	}
	row := roweval.NativeRow[Selector]{
		ID:      extendedFaultID(r.frame, r.field),
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %#x", r.frame),
		At:      Selector{Identifier: r.frame << 16, Extended: true, DLC: 8, RawDLC: 8, Slice: r.slice},
		When:    roweval.When{Equal: equal},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: "unsigned", ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor}},
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

// extendedFaultHook withholds a field the decoder marks invalid (outside its
// domain) and a field whose unit gate is not met (raw word kept).
func extendedFaultHook(id, gate string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		if q[qFaultInvalidPrefix+id] == "true" {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		if gate != "" && q[gate] != "true" {
			return roweval.DerivedValue{}, gateWithheld
		}
		return roweval.DerivedValue{Value: v}, ""
	}
}

// ExtendedFaultTable returns the native rows of the extended PCS frames 0x1905
// to 0x1909 and of the gateway state record, and the row hooks those rows name.
func ExtendedFaultTable() roweval.Table[Selector] {
	specs := extendedFaultSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := extendedFaultNativeRow(s)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = extendedFaultHook(row.ID, s.gate)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// extendedFaultFrameKind returns the frame kind the rows of this file cover for
// the frame under the selection, as DecodePCSExtended folds it, and false for a
// kind this file has no rows for.
func extendedFaultFrameKind(s canbusreg.Selection, frame canbus.Frame) (uint32, bool) {
	c := s.SelectionSpecValue()
	kind := frame.ID().Value() >> 16
	if kind == gatewayStateKind && c.Destination == 0xff && c.Source == 0xf4 {
		return kind, true
	}
	if kind >= 0x1910 && kind <= 0x1919 && c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2 {
		kind -= 0x10
	}
	return kind, kind >= 0x1905 && kind <= 0x1909
}

// ExtendedFaultQualifiers returns the selection vocabulary of one extended
// observation of these frames: the flavor, the scope, bank, endpoint, group and
// slot of the selection, the unit gate, the gateway marks, the bits the
// selection's dictionary names and the invalid marks of the decoder.
func ExtendedFaultQualifiers(s canbusreg.Selection, in canbusreg.LINKInput, kind uint32) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	p := in.Evidence.Frame.Bytes()
	gateway := c.ProjectCode == "QCAA" && c.Release == "0001"
	values := map[string]string{
		qFlavor:             c.Flavor,
		qBank:               c.Bank,
		qScope:              c.Scope,
		qEndpoint:           c.Endpoint,
		qGroup:              c.Group,
		qSlot:               c.Slot,
		gateFaultLocalUnits: boolText(c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"),
		qFaultGateway:       boolText(gateway),
		qFaultGatewayRoute:  boolText(c.Destination == 0xff && c.Source == 0xf4),
	}
	for index := 0; index < 128; index++ {
		_, known := canbusreg.EncodeDTC(s, index)
		values[fmt.Sprintf("%s%d", qFaultBitKnownPrefix, index)] = boolText(known)
	}
	if kind == gatewayStateKind {
		for at := 2; at <= 6; at++ {
			values[qFaultInvalidPrefix+extendedFaultID(kind, fmt.Sprintf("padding_%d", at))] = boolText(p[at] != 0)
		}
		values[qFaultInvalidPrefix+extendedFaultID(kind, "downstream_address")] = boolText(gateway && (p[7] < 1 || p[7] > 8))
	}
	return roweval.Qualifiers{Values: values}
}

// ExtendedFaultResult is the evaluation of one extended observation: the
// evidence it carries, its attribution (an absent group or slot stays absent),
// the evidence grade the decoder gives each field keyed by row identifier, and
// the evaluation of every row of the frame kind.
type ExtendedFaultResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
	Grades      map[string]string
}

// EvaluateExtendedFaults evaluates the native rows of one extended observation
// of the frames 0x1905 to 0x1909 or the gateway state record under the
// selection it was received with. A frame DecodePCSExtended refuses gives the
// decoder's status as the reason and no rows; a frame it accepts that is not one
// of these kinds gives roweval.ReasonNoMatch.
func EvaluateExtendedFaults(s canbusreg.Selection, in canbusreg.LINKInput) (ExtendedFaultResult, roweval.Reason, error) {
	res := ExtendedFaultResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSExtended(s, in)
	if decoded.Status != "accepted" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	kind, ok := extendedFaultFrameKind(s, frame)
	if !ok {
		return res, roweval.ReasonNoMatch, nil
	}
	table := ExtendedFaultTable()
	q := ExtendedFaultQualifiers(s, in, kind)
	specs := make(map[string]extendedFaultSpec)
	for _, sp := range extendedFaultSpecs() {
		specs[extendedFaultID(sp.frame, sp.field)] = sp
	}
	res.Rows = make(map[string]roweval.Evaluation)
	res.Grades = make(map[string]string)
	for _, row := range table.Native {
		if row.At.Identifier != kind<<16 {
			continue
		}
		sp := specs[row.ID]
		one := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: table.Hooks}
		sel := Selector{Identifier: kind << 16, Extended: true, DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
		a := roweval.Acquired[Selector]{Selector: sel, Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(one, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil {
			if f, ok := decoded.Fields[sp.field]; ok {
				res.Grades[row.ID] = f.Grade
			}
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			rawRow := sp.raw
			if sp.acceptedWhen != "" {
				rawRow = q.Values[sp.acceptedWhen] != "true"
			}
			ev.Native.Disposition = disposition(ev.Native, rawRow)
		}
		res.Rows[row.ID] = ev
	}
	return res, "", nil
}
