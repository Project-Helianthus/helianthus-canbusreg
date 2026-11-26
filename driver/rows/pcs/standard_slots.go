package pcs

import (
	"fmt"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the native rows of the slot frames 0x326, 0x327, 0x333 and
// 0x334 that DecodePCSSlots emits, with the index companions of the 0x333
// dictionary words. Every field exists once per bank: the ordinary bank is the
// flavor pcs-standard and the parallel bank is the flavor pcs-parallel, so the
// row identifier carries the flavor token, the frame and the field. The 0x326
// current is bank-dependent: its offset is 4000 raw counts on the ordinary bank
// and 10000 on the parallel bank, which the rows apply as a whole-ampere offset
// after the divisor of 10.

// Gate qualifiers of the slot frames. Each one is the gate the frozen
// DecodePCSSlots applies to the field of the same name, computed by
// slotQualifiers from the frame bytes and the slot input.
const (
	gateSlot326        = "slot-326"
	gateMeasured326    = "measured-326"
	gateSlot327        = "slot-327"
	gateMeasured327    = "measured-327"
	gateGroup333       = "group-333"
	gateState334       = "state-334"
	gateIndependent334 = "independent-334"

	// qDTCAccepted prefixes the accepted state of a 0x333 dictionary word;
	// the word field key follows.
	qDTCAccepted = "dtc-accepted:"
	// qIndexState and qIndex prefix the state and the dictionary index of a
	// 0x333 index companion; the companion field key follows.
	qIndexState = "dtc-index-state:"
	qIndex      = "dtc-index:"
)

// slotBank is one bank of the slot frames: the flavor and bank the selection
// pairs, the row identifier token and the raw offset of the 0x326 current.
type slotBank struct {
	token, flavor, bank string
	// currentBase is the raw count subtracted from the 0x326 current word.
	currentBase int64
}

var slotBanks = []slotBank{
	{token: "standard", flavor: "pcs-standard", bank: "ordinary", currentBase: 4000},
	{token: "parallel", flavor: "pcs-parallel", bank: "parallel", currentBase: 10000},
}

// slotSpec describes one native row of a slot frame for one bank.
type slotSpec struct {
	frame   uint32
	field   string
	slice   signalSlice
	unit    string
	divisor int64
	// offset is a whole-unit addend applied after the divisor.
	offset int64
	gate   string
	// indexOf names the 0x333 word field whose dictionary index this row
	// states; empty for every other row.
	indexOf string
}

// slotSpecs returns the rows of the slot frames for one bank, in frame order.
func slotSpecs(b slotBank) []slotSpec {
	return []slotSpec{
		{frame: 0x326, field: "slot", slice: byteAt(0), divisor: 1, gate: gateSlot326},
		{frame: 0x326, field: "voltage", slice: bigWord(8), unit: "V", divisor: 100, gate: gateMeasured326},
		{frame: 0x326, field: "current", slice: bigWord(24), unit: "A", divisor: 10, offset: -b.currentBase / 10, gate: gateMeasured326},
		{frame: 0x326, field: "soc_low", slice: byteAt(40), divisor: 1, gate: gateSlot326},
		{frame: 0x327, field: "slot", slice: byteAt(0), divisor: 1, gate: gateSlot327},
		{frame: 0x327, field: "maximum_cell", slice: bigWord(8), unit: "mV", divisor: 1, gate: gateMeasured327},
		{frame: 0x327, field: "minimum_cell", slice: bigWord(24), unit: "mV", divisor: 1, gate: gateMeasured327},
		{frame: 0x327, field: "maximum_index", slice: byteAt(40), divisor: 1, gate: gateSlot327},
		{frame: 0x327, field: "minimum_index", slice: byteAt(48), divisor: 1, gate: gateSlot327},
		{frame: 0x333, field: "group", slice: byteAt(0), divisor: 1, gate: gateGroup333},
		{frame: 0x333, field: "slot", slice: byteAt(8), divisor: 1, gate: gateGroup333},
		{frame: 0x333, field: "dtc_a", slice: bigWord(16), divisor: 1, gate: qDTCAccepted + "dtc_a"},
		{frame: 0x333, field: "dtc_b", slice: bigWord(32), divisor: 1, gate: qDTCAccepted + "dtc_b"},
		{frame: 0x333, field: "dtc_a_index", slice: bigWord(16), divisor: 1, indexOf: "dtc_a"},
		{frame: 0x333, field: "dtc_b_index", slice: bigWord(32), divisor: 1, indexOf: "dtc_b"},
		{frame: 0x334, field: "state", slice: byteAt(0), divisor: 1, gate: gateState334},
		{frame: 0x334, field: "independent", slice: byteAt(8), divisor: 1, gate: gateIndependent334},
	}
}

// slotNativeRow builds one native row of the slot table for one bank. The
// selector is the frame's identifier with DLC 8 and raw DLC 8. The conversion
// is the exact affine (raw / divisor + offset); the row hook carries the gate,
// the field disposition and, for a dictionary index, the index state.
func slotNativeRow(b slotBank, r slotSpec) roweval.NativeRow[Selector] {
	codec := "unsigned"
	row := roweval.NativeRow[Selector]{
		ID:      fmt.Sprintf("growatt.bms.pcs.%s.%X.%s", b.token, r.frame, r.field),
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %#x", r.frame),
		At:      Selector{Identifier: r.frame, DLC: 8, RawDLC: 8, Slice: r.slice},
		When:    roweval.When{Equal: map[string]string{qFlavor: b.flavor, qBank: b.bank}},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: codec, ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor}},
	}
	if r.offset != 0 {
		row.Decode.Numeric.Add = wholeDecimal(r.offset)
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

// wholeDecimal is the canonical decimal of a whole number: trailing zeros move
// into the exponent, so the coefficient has none.
func wholeDecimal(n int64) semreg.Decimal {
	d := semreg.Decimal{}
	for n != 0 && n%10 == 0 {
		n /= 10
		d.Exponent10++
	}
	d.Coefficient = strconv.FormatInt(n, 10)
	return d
}

// slotIndexHook is the hook of a 0x333 index companion. The index is not a
// field of the wire word; it is the position of the word in the selected
// member dictionary, which slotQualifiers supplies. An accepted word gives that
// index; a zero word under a populated dictionary gives the decoder's empty
// status; any other word leaves the companion absent, as the decoder does.
func slotIndexHook(field string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, _ roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		if q[qFieldAgeInvalid+field] == "true" {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		if state, ok := q[qFieldState+field]; ok {
			return roweval.DerivedValue{}, dispositionReason(state)
		}
		switch q[qIndexState+field] {
		case "accepted":
			number := semreg.Decimal{Coefficient: q[qIndex+field]}
			return roweval.DerivedValue{Value: semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: number}}}, ""
		case "empty":
			return roweval.DerivedValue{}, gateWithheld
		}
		return roweval.DerivedValue{}, roweval.Reason("absent")
	}
}

// SlotTable returns the native rows of the slot frames 0x326, 0x327, 0x333 and
// 0x334, for the ordinary and the parallel bank. It is separate from Table:
// the standard PCS table keeps the frames 0x311 to 0x314 only.
func SlotTable() roweval.Table[Selector] {
	var rows []roweval.NativeRow[Selector]
	hooks := make(map[string]roweval.RowHook)
	for _, b := range slotBanks {
		for _, r := range slotSpecs(b) {
			row := slotNativeRow(b, r)
			rows = append(rows, row)
			if r.indexOf != "" {
				hooks[row.Decode.Hook] = slotIndexHook(r.field)
			} else {
				hooks[row.Decode.Hook] = rowHook(r.field, r.gate)
			}
		}
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// slotFrameDisposition admits only the standard identifier with DLC 8 and raw
// DLC 8 of the slot frames the table names.
func slotFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x326, 0x327, 0x333, 0x334:
		return ""
	}
	return roweval.ReasonNoMatch
}

// slotRelease11 is the release-11 contract test DecodePCSSlots applies.
func slotRelease11(s canbusreg.Selection) bool {
	c := s.SelectionSpecValue()
	return s.SelectionGroup() == "L8" && (c.ProjectCode == "QAAA" && c.Release == "0011" || c.ProjectCode == "QAAa" && c.Release == "9011" && c.Choice == "release11")
}

// slotSameMember reports whether the member selection names the same reporting
// member context as the selection, as DecodePCSSlots requires for a dictionary.
func slotSameMember(s, m canbusreg.Selection) bool {
	c, x := s.SelectionSpecValue(), m.SelectionSpecValue()
	return x.Direction == c.Direction && x.Flavor == c.Flavor && x.Interface == c.Interface && x.Epoch == c.Epoch && x.Scope == c.Scope && x.Bank == c.Bank && x.Endpoint == c.Endpoint && x.Group == c.Group && x.Slot == c.Slot
}

// slotMemberCount is the number of dictionary indices of the member: the
// indices EncodeDTC maps, counted from zero.
func slotMemberCount(m canbusreg.Selection) int {
	for n := 0; ; n++ {
		if _, ok := canbusreg.EncodeDTC(m, n); !ok {
			return n
		}
	}
}

// slotDTC mirrors the dictionary path of DecodePCSSlots for one 0x333 word. It
// returns the state of the word's index companion and the index: accepted for
// a nonzero word the dictionary maps, empty for a zero word under a populated
// dictionary, absent otherwise (no member, a member that does not match, or a
// word the dictionary does not map).
func slotDTC(in canbusreg.PCSSlotPCSInput, s canbusreg.Selection, gate bool, word uint16) (string, int) {
	if !gate || in.Member == nil || !slotSameMember(s, *in.Member) {
		return "absent", 0
	}
	count := slotMemberCount(*in.Member)
	if count > 0 && word == 0 {
		return "empty", 0
	}
	for index := 0; index < count; index++ {
		if expected, _ := canbusreg.EncodeDTC(*in.Member, index); expected == word {
			return "accepted", index
		}
	}
	return "absent", 0
}

// slotQualifiers computes the selection vocabulary of one slot observation: the
// flavor and bank the rows pair, the gate of each field, the field dispositions
// the input supplies and the dictionary state of each 0x333 word. Every value
// follows the gate of the frozen DecodePCSSlots field of the same name.
func slotQualifiers(s canbusreg.Selection, in canbusreg.PCSSlotPCSInput) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	p := in.Evidence.Frame.Bytes()
	selected := slotRelease11(s)
	modern := c.ProjectCode == "QAAa"
	slot := selected && in.SlotValid && p[0] == in.NativeSlot
	pad326 := p[6] == 0 && p[7] == 0
	pad334 := p[2] == 0 && p[3] == 0 && p[4] == 0 && p[5] == 0 && p[6] == 0 && p[7] == 0
	group := selected && in.GroupValid && p[0] == in.NativeGroup && in.SlotValid && p[1] == in.NativeSlot && p[6] == 0 && p[7] == 0
	values := map[string]string{
		qFlavor:            c.Flavor,
		qBank:              c.Bank,
		gateSlot326:        boolText(slot && pad326),
		gateMeasured326:    boolText(slot && pad326 && in.MeasurementValid),
		gateSlot327:        boolText(slot && p[7] == 0),
		gateMeasured327:    boolText(slot && p[7] == 0 && in.MeasurementValid),
		gateGroup333:       boolText(group),
		gateState334:       boolText(selected && pad334 && (modern || p[1] == 0)),
		gateIndependent334: boolText(selected && modern && pad334),
	}
	for field, state := range in.FieldStates {
		values[qFieldState+field] = state
	}
	for field, age := range in.FieldAges {
		if age < 0 {
			values[qFieldAgeInvalid+field] = "true"
		}
	}
	for _, dtc := range []struct {
		name string
		at   int
	}{{"dtc_a", 2}, {"dtc_b", 4}} {
		word := uint16(p[dtc.at])<<8 | uint16(p[dtc.at+1])
		state, index := slotDTC(in, s, group, word)
		values[qDTCAccepted+dtc.name] = boolText(state == "accepted")
		values[qIndexState+dtc.name+"_index"] = state
		values[qIndex+dtc.name+"_index"] = strconv.Itoa(index)
	}
	return roweval.Qualifiers{Values: values}
}

// SlotResult is the evaluation of one slot observation: the evidence it carries,
// its attribution (an absent group or slot stays absent) and the evaluation of
// every applicable row, keyed by row identifier. A row that does not apply to
// the selection is not in Rows.
type SlotResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
}

// EvaluateSlots evaluates the native slot rows of one observation under the
// selection and the slot input it was received with. A frame DecodePCSSlots
// refuses gives the decoder's status as the reason and no rows.
func EvaluateSlots(s canbusreg.Selection, in canbusreg.PCSSlotPCSInput) (SlotResult, roweval.Reason, error) {
	res := SlotResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSSlots(s, in)
	if decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	if reason := slotFrameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	rows, err := evaluateSlotRows(SlotTable(), frame, slotQualifiers(s, in))
	if err != nil {
		return res, "", err
	}
	res.Rows = rows
	return res, "", nil
}

// evaluateSlotRows evaluates every native row of the table whose identifier is
// the frame's, under the qualifiers. A row whose selection pairing does not
// apply gives roweval.ReasonNoMatch and is not returned.
func evaluateSlotRows(t roweval.Table[Selector], frame canbus.Frame, q roweval.Qualifiers) (map[string]roweval.Evaluation, error) {
	out := make(map[string]roweval.Evaluation)
	for _, row := range t.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		// Each row is evaluated against its own selector and conditions. A
		// dictionary companion reads the word of its field, so it shares the
		// word row's selector; evaluating the table as a whole would find both.
		one := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: t.Hooks}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At.Slice), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(one, a)
		if err != nil {
			return nil, err
		}
		if ev.Native != nil {
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			ev.Native.Disposition = disposition(ev.Native, false)
			out[ev.Native.RowID] = ev
		} else if ev.Reason != roweval.ReasonNoMatch {
			out[row.ID] = ev
		}
	}
	return out, nil
}
