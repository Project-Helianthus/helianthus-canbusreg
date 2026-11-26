package pcs

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the native rows of the extended PCS frames 0x1900 to 0x1904
// that DecodePCSExtended emits: the product and version of 0x1900, the
// configuration of 0x1901, the pack voltage, current, port voltage and state of
// charge of 0x1902, the limits and permissions of 0x1903 and the capacities and
// cycle count of 0x1904. The identifiers are extended 29-bit identifiers with
// DLC 8 and raw DLC 8. The decoder admits the frame by its route: the
// destination and source bytes are the selection's, so a row declares the frame
// kind (the upper 13 bits) and the evaluator presents the frame's kind with the
// route bytes zero. An aggregate coordinator frame sent as 0x1910 to 0x1914 is
// the same frame kind as 0x1900 to 0x1904, as the decoder folds it.
//
// Frame 0x1902 carries two voltages: the pack voltage (B0-B1) and the port
// voltage (B4-B5). They are different quantities and stay two rows.

// extendedRowPrefix is the identifier prefix of every extended PCS native row.
const extendedRowPrefix = "growatt.bms.pcs.extended19."

// Gate qualifiers of the extended frames, each "true" or "false".
const (
	// gateExtendedLocalUnits is the decoder's localUnits: a local QAAA0006 source.
	gateExtendedLocalUnits = "extended-local-units"
	// gateExtendedCycleUnits is the decoder's cycle-count unit: QAAA0006, either scope.
	gateExtendedCycleUnits = "extended-cycle-units"
	// qExtendedInvalidPrefix prefixes the qualifier that is "true" when the decoder
	// marks the field invalid; the row identifier follows.
	qExtendedInvalidPrefix = "extended-invalid:"
)

type extendedSpec struct {
	frame         uint32
	field         string
	slice         signalSlice
	unit          string
	divisor       int64
	offset        int64
	gate          string
	raw           bool
	padding       bool
	productAbsent bool
}

func extendedID(frame uint32, field string) string {
	return fmt.Sprintf("%s%X.%s", extendedRowPrefix, frame, field)
}

func extendedRaw(frame uint32, field string, slice signalSlice) extendedSpec {
	return extendedSpec{frame: frame, field: field, slice: slice, divisor: 1, raw: true}
}

func extendedKnown(frame uint32, field string, slice signalSlice) extendedSpec {
	return extendedSpec{frame: frame, field: field, slice: slice, divisor: 1}
}

func extendedScaled(frame uint32, field string, slice signalSlice, unit string, divisor, offset int64, gate string) extendedSpec {
	return extendedSpec{frame: frame, field: field, slice: slice, unit: unit, divisor: divisor, offset: offset, gate: gate}
}

func extendedPadding(frame uint32, at uint16) extendedSpec {
	return extendedSpec{frame: frame, field: fmt.Sprintf("padding_%d", at/8), slice: byteAt(at), divisor: 1, raw: true, padding: true}
}

// extendedSpecs returns the row specifications of frames 0x1900 to 0x1904.
func extendedSpecs() []extendedSpec {
	return []extendedSpec{
		{frame: 0x1900, field: "product", slice: littleWord(0), divisor: 1, productAbsent: true},
		extendedKnown(0x1900, "hardware", byteAt(16)),
		extendedKnown(0x1900, "version", littleWord(24)),
		extendedPadding(0x1900, 40), extendedPadding(0x1900, 48), extendedPadding(0x1900, 56),

		extendedKnown(0x1901, "count", byteAt(0)),
		extendedRaw(0x1901, "configuration_1", byteAt(8)),
		extendedRaw(0x1901, "configuration_2", byteAt(16)),
		extendedKnown(0x1901, "state", byteAt(24)),
		extendedRaw(0x1901, "configuration_4", littleWord(32)),
		extendedRaw(0x1901, "configuration_6", littleWord(48)),

		extendedScaled(0x1902, "pack_voltage", littleWord(0), "V", 10, 0, gateExtendedLocalUnits),
		extendedScaled(0x1902, "current", littleWord(16), "A", 10, -1000, gateExtendedLocalUnits),
		extendedScaled(0x1902, "port_voltage", littleWord(32), "V", 10, 0, gateExtendedLocalUnits),
		extendedScaled(0x1902, "soc", littleWord(48), "percent", 10, 0, gateExtendedLocalUnits),

		extendedRaw(0x1903, "limit_0", littleWord(0)),
		extendedRaw(0x1903, "limit_2", littleWord(16)),
		extendedRaw(0x1903, "permission_4", byteAt(32)),
		extendedRaw(0x1903, "permission_5", byteAt(40)),
		extendedKnown(0x1903, "state", byteAt(48)),
		extendedKnown(0x1903, "role", byteAt(56)),

		extendedScaled(0x1904, "full_capacity", littleWord(0), "Ah", 10, 0, gateExtendedLocalUnits),
		extendedScaled(0x1904, "remaining_capacity", littleWord(16), "Ah", 10, 0, gateExtendedLocalUnits),
		extendedScaled(0x1904, "cycles", littleWord(32), "cycles", 1, 0, gateExtendedCycleUnits),
		extendedPadding(0x1904, 48), extendedPadding(0x1904, 56),
	}
}

func extendedNativeRow(r extendedSpec) roweval.NativeRow[Selector] {
	row := roweval.NativeRow[Selector]{
		ID:      extendedID(r.frame, r.field),
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %#x", r.frame),
		At:      Selector{Identifier: r.frame << 16, Extended: true, DLC: 8, RawDLC: 8, Slice: r.slice},
		When:    roweval.When{Equal: map[string]string{qFlavor: "pcs-extended19"}},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: "unsigned", ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor}},
	}
	if r.offset != 0 {
		row.Decode.Numeric.Add = wholeDecimal(r.offset)
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

// extendedHook withholds a field the decoder marks invalid (outside its domain)
// and a field whose unit gate is not met (raw word kept).
func extendedHook(id, gate string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		if q[qExtendedInvalidPrefix+id] == "true" {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		if gate != "" && q[gate] != "true" {
			return roweval.DerivedValue{}, gateWithheld
		}
		return roweval.DerivedValue{Value: v}, ""
	}
}

// ExtendedTable returns the native rows of the extended PCS frames 0x1900 to
// 0x1904 and the row hooks those rows name.
func ExtendedTable() roweval.Table[Selector] {
	specs := extendedSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := extendedNativeRow(s)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = extendedHook(row.ID, s.gate)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// extendedFrameKind returns the frame kind the rows of this file cover for the
// frame under the selection, as DecodePCSExtended folds it, and false when the
// frame is the gateway state record or a kind outside 0x1900 to 0x1904.
func extendedFrameKind(s canbusreg.Selection, frame canbus.Frame) (uint32, bool) {
	c := s.SelectionSpecValue()
	kind := frame.ID().Value() >> 16
	if kind == 0x1910 && c.Destination == 0xff && c.Source == 0xf4 {
		return 0, false
	}
	if kind >= 0x1910 && kind <= 0x1919 && c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2 {
		kind -= 0x10
	}
	return kind, kind >= 0x1900 && kind <= 0x1904
}

// ExtendedQualifiers returns the selection vocabulary of one extended
// observation: the flavor, the scope, bank, endpoint, group and slot of the
// selection, the two unit gates and the invalid marks of the decoder.
func ExtendedQualifiers(s canbusreg.Selection, in canbusreg.LINKInput, kind uint32) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	p := in.Evidence.Frame.Bytes()
	le := func(at int) int { return int(p[at]) | int(p[at+1])<<8 }
	values := map[string]string{
		qFlavor:                c.Flavor,
		qBank:                  c.Bank,
		qScope:                 c.Scope,
		qEndpoint:              c.Endpoint,
		qGroup:                 c.Group,
		qSlot:                  c.Slot,
		gateExtendedLocalUnits: boolText(c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"),
		gateExtendedCycleUnits: boolText(c.ProjectCode == "QAAA" && c.Release == "0006"),
	}
	for _, sp := range extendedSpecs() {
		if sp.frame != kind {
			continue
		}
		id := extendedID(sp.frame, sp.field)
		switch {
		case sp.padding:
			values[qExtendedInvalidPrefix+id] = boolText(p[sp.slice.offset/8] != 0)
		case sp.productAbsent:
			values[qExtendedInvalidPrefix+id] = boolText(c.Scope == "aggregate" && le(0) == 65535)
		}
	}
	return roweval.Qualifiers{Values: values}
}

// ExtendedResult is the evaluation of one extended observation: the evidence it
// carries, its attribution (an absent group or slot stays absent), the evidence
// grade the decoder gives each field keyed by row identifier, and the
// evaluation of every row of the frame kind.
type ExtendedResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
	Grades      map[string]string
}

// EvaluateExtended evaluates the native rows of one extended observation under
// the selection it was received with. A frame DecodePCSExtended refuses gives
// the decoder's status as the reason and no rows; a frame it accepts that is
// not 0x1900 to 0x1904 (or the gateway state record) gives roweval.ReasonNoMatch.
func EvaluateExtended(s canbusreg.Selection, in canbusreg.LINKInput) (ExtendedResult, roweval.Reason, error) {
	res := ExtendedResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSExtended(s, in)
	if decoded.Status != "accepted" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	kind, ok := extendedFrameKind(s, frame)
	if !ok {
		return res, roweval.ReasonNoMatch, nil
	}
	table := ExtendedTable()
	q := ExtendedQualifiers(s, in, kind)
	raw := make(map[string]bool)
	field := make(map[string]string)
	for _, sp := range extendedSpecs() {
		raw[extendedID(sp.frame, sp.field)] = sp.raw
		field[extendedID(sp.frame, sp.field)] = sp.field
	}
	res.Rows = make(map[string]roweval.Evaluation)
	res.Grades = make(map[string]string)
	for _, row := range table.Native {
		if row.At.Identifier != kind<<16 {
			continue
		}
		one := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: table.Hooks}
		sel := Selector{Identifier: kind << 16, Extended: true, DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
		a := roweval.Acquired[Selector]{Selector: sel, Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(one, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil {
			if f, ok := decoded.Fields[field[row.ID]]; ok {
				res.Grades[row.ID] = f.Grade
			}
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			ev.Native.Disposition = disposition(ev.Native, raw[row.ID])
		}
		res.Rows[row.ID] = ev
	}
	return res, "", nil
}
