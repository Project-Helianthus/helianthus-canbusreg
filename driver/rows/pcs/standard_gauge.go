package pcs

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Selection qualifiers of the gauge frames 0x315 to 0x318. The gauge decoder
// selects its fields by the contract group, the scope and the frame; the
// qualifiers name each of those selections and each gate the decoder tests.
const (
	// qGaugeBranch names the decoder branch the frame and the contract group
	// select: early (contract groups L0 and L1 on 0x315 and 0x316), standard,
	// l2 to l8 (the contract group of 0x316), l0 and l1 (0x317), other (every
	// group the branch does not name) or placeholder (the gateway aggregate).
	qGaugeBranch = "gauge-branch"

	// qGaugeValid is the decoder's valid gate: a selected standard contract
	// group, the local scope and an asserted measurement.
	qGaugeValid = "gauge-valid"

	// qGaugeValidRelease is the valid gate on the QAAA0006 project (315 capacity).
	qGaugeValidRelease = "gauge-valid-release"

	// qGaugeCell prefixes the count gates of the 316 cell positions 5 to 8.
	qGaugeCell = "gauge-cell-"

	// qGaugeInitial is the initial-accumulator gate: zero integration with the
	// initial accumulator word 5000 on the L2 contract group.
	qGaugeInitial = "gauge-initial"

	// qGaugeFailedPrecheck is the failed-precheck gate: state 100 on L2.
	qGaugeFailedPrecheck = "gauge-failed-precheck"

	// qGaugeSlot is the 317 slot gate: a selected group, and zero in the local scope.
	qGaugeSlot = "gauge-slot"

	// qGaugeSOHTruncated is the 317 truncated SOH gate.
	qGaugeSOHTruncated = "gauge-soh-truncated"

	// qGaugeZeroPayload is the gateway placeholder gate: an all-zero payload.
	qGaugeZeroPayload = "gauge-zero-payload"
)

// Branch names of the gauge frames.
const (
	branchEarly       = "early"
	branchStandard    = "standard"
	branchPlaceholder = "placeholder"
	branchOther       = "other"
)

// gaugeSpec is one native row of a gauge frame. A non-empty bitOf names the
// retained byte whose accepted disposition gates the retained fault bit. Each
// retained fault bit is published raw: its named condition label is the
// decoder's dictionary and is not reproduced by the row.
type gaugeSpec struct {
	rowSpec
	bitOf string
	// parallel marks a row of the pcs-parallel flavor on the parallel bank; its
	// identifier carries the parallel flavor token in place of the standard one.
	parallel bool
	// numerator marks a row whose raw word is the decoder's own numerator for
	// its field, not the payload slice the row reads (the placeholder).
	numerator bool
}

// gaugeNativeRow builds the native row of a gauge spec. A parallel-bank row
// takes the parallel flavor token in its identifier.
func gaugeNativeRow(s gaugeSpec) roweval.NativeRow[Selector] {
	row := nativeRow(s.rowSpec)
	if s.parallel {
		row.ID = "growatt.bms.pcs.parallel." + strings.TrimPrefix(row.ID, rowPrefix)
		row.Decode.Hook = rowHookPrefix + row.ID
	}
	return row
}

// gaugeParallelBankSpecs returns the parallel-bank twin of every standard gauge
// row that is not a retained fault bit and whose branch the pcs-parallel flavor
// can select. The frozen decoder admits the pcs-parallel flavor on every gauge
// frame and emits the same field keys as on the ordinary bank; its gate
// qualifiers are never met on that flavor (selected is false), so each gated
// field is raw and no retained fault bit is accepted. The twin keeps the branch,
// the gate and the raw evidence of its standard row, under the pcs-parallel
// flavor and the parallel bank. NewSelection drops the contract groups L0 to L4
// on that flavor, so the early, L0, L1, L2, L3 and L4 branches are never selected
// there and have no twin.
func gaugeParallelBankSpecs(standard []gaugeSpec) []gaugeSpec {
	var out []gaugeSpec
	for _, s := range standard {
		if s.bitOf != "" {
			continue
		}
		switch s.when[qGaugeBranch] {
		case branchEarly, "l0", "l1", "l2", "l3", "l4":
			continue
		}
		p := s
		p.parallel = true
		p.when = make(map[string]string, len(s.when))
		for k, v := range s.when {
			p.when[k] = v
		}
		p.when[qFlavor], p.when[qBank] = "pcs-parallel", "parallel"
		out = append(out, p)
	}
	return out
}

// gaugeBranch returns the decoder branch of the frame under the contract group.
// The gateway aggregate takes the placeholder branch on every gauge frame.
func gaugeBranch(frame uint32, group string, gateway bool) string {
	switch {
	case gateway:
		return branchPlaceholder
	case frame == 0x315:
		if group == "L0" || group == "L1" {
			return branchEarly
		}
		return branchStandard
	case frame == 0x316:
		switch group {
		case "L0", "L1":
			return branchEarly
		case "L2", "L3", "L4", "L5", "L6", "L7", "L8":
			return "l" + group[1:]
		}
		return branchOther
	case frame == 0x317:
		switch group {
		case "L0":
			return "l0"
		case "L1":
			return "l1"
		}
		return branchOther
	case frame == 0x318:
		if group == "L0" {
			return "l0"
		}
		return branchOther
	}
	return ""
}

// gaugeQualifiers returns the selection vocabulary of one gauge observation:
// the standard qualifiers of the observation plus the branch and the gates of
// the gauge decoder. Gates that depend on the payload are read from the frame
// the observation carries.
func gaugeQualifiers(s canbusreg.Selection, in canbusreg.PCSIdentityPCSInput) roweval.Qualifiers {
	q := Qualifiers(s, in.PCSStandardPCSInput)
	c := s.SelectionSpecValue()
	group := s.SelectionGroup()
	id := c.ProjectCode + c.Release
	p := in.Evidence.Frame.Bytes()
	local := c.Scope == "local"
	selected := c.Flavor == "pcs-standard" && group != ""
	valid := selected && local && in.MeasurementValid
	gateway := id == "QCAA0001" && c.Scope == "aggregate"
	// The decoder takes its placeholder branch on the gateway aggregate and, on
	// 0x315 only, on the parallel bank of the QAAB and QAAb projects.
	parallelPlaceholder := c.Flavor == "pcs-parallel" && (c.ProjectCode == "QAAB" || c.ProjectCode == "QAAb") && group != "" && in.Evidence.Frame.ID().Value() == 0x315
	v := q.Values
	v[qGaugeBranch] = gaugeBranch(in.Evidence.Frame.ID().Value(), group, gateway || parallelPlaceholder)
	v[qGaugeValid] = boolText(valid)
	v[qGaugeValidRelease] = boolText(valid && id == "QAAA0006")
	for pos := 5; pos <= 8; pos++ {
		v[qGaugeCell+strconv.Itoa(pos)] = boolText(valid && in.CountValid && in.Count >= uint16(pos))
	}
	initial := binary.BigEndian.Uint16(p[0:2]) == 5000
	v[qGaugeInitial] = boolText(valid && group == "L2" && in.ZeroIntegration && initial)
	v[qGaugeFailedPrecheck] = boolText(valid && group == "L2" && in.FailedPrecheck && p[6] == 100)
	v[qGaugeSlot] = boolText(selected && (!local || p[7] == 0))
	v[qGaugeSOHTruncated] = boolText(valid && (id == "QAAA0006" || group == "L8") && p[7] == 0 && p[6] <= 100)
	v[qGaugeZeroPayload] = boolText(p == [8]byte{})
	return q
}

// gaugeRow is a gated native row of a gauge frame under one branch.
func gaugeRow(frame uint32, layout, branch, field string, slice signalSlice, unit string, divisor int64, gate string) gaugeSpec {
	return gaugeSpec{rowSpec: rowSpec{frame: frame, layout: layout, field: field, slice: slice, unit: unit, divisor: divisor, gate: gate, when: when(qGaugeBranch, branch)}}
}

// gaugeRawRow is a native row the decoder publishes without a gate: its raw
// word is the native value and its disposition is raw.
func gaugeRawRow(frame uint32, layout, branch, field string, slice signalSlice) gaugeSpec {
	s := gaugeRow(frame, layout, branch, field, slice, "", 1, "")
	s.raw = true
	return s
}

// gaugeBit is a retained fault bit of byte k of the payload, published under the
// decoder's index 8*(byteOffset)+bit for the retained byte named byteField. Its
// disposition is the decoder's own: accepted when the selected dictionary names
// the bit, raw otherwise (see gaugeBitDisposition).
func gaugeBit(frame uint32, layout, branch string, index int, slice signalSlice, byteField string) gaugeSpec {
	s := gaugeRow(frame, layout, branch, fmt.Sprintf("retained_fault_bit_%d", index), slice, "", 1, "")
	s.bitOf = byteField
	return s
}

// gaugeBitDisposition maps the decoder's status of a retained fault bit to the
// row disposition: the decoder's accepted bit stays accepted, every other bit is raw.
func gaugeBitDisposition(status string) string {
	if status == "accepted" {
		return dispositionAccepted
	}
	return dispositionRaw
}

// gaugeBitFields returns the decoder field name of every retained fault bit row,
// keyed by row identifier.
func gaugeBitFields() map[string]string {
	out := make(map[string]string)
	for _, s := range gaugeSpecs() {
		if s.bitOf != "" {
			out[gaugeNativeRow(s).ID] = s.field
		}
	}
	return out
}

// gaugeSpecs returns the row specifications of frames 0x315 to 0x318 for the
// standard PCS selection, in frame order. Rows that a branch shares with another
// branch on the same frame carry the layout between frame and field.
func gaugeSpecs() []gaugeSpec {
	const frame315, frame316, frame317, frame318 = 0x315, 0x316, 0x317, 0x318
	var out []gaugeSpec
	add := func(s ...gaugeSpec) { out = append(out, s...) }

	// 0x315: words on L0 and L1; cells, checkpoint and algorithm otherwise.
	add(
		gaugeRow(frame315, "", branchEarly, "word_0", bigWord(0), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchEarly, "word_2", bigWord(16), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchEarly, "word_4", bigWord(32), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchEarly, "word_6", bigWord(48), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchStandard, "cell_word", bigWord(0), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchStandard, "checkpoint", bigWord(16), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchStandard, "algorithm", bigWord(32), "", 1, qGaugeValid),
		gaugeRow(frame315, "", branchStandard, "capacity", bigWord(48), "Ah", 10, qGaugeValidRelease),
	)

	// 0x316: the cell positions on L0 and L1; the L2 accumulator, capacity and
	// cycles; the L6 raw fields at the L2 byte positions; the L3 and L4 words;
	// the L5 and L7 bytes; the L8 capacity and cycles; the other words.
	add(
		gaugeRow(frame316, "early", branchEarly, "cell_5", bigWord(0), "mV", 1, qGaugeCell+"5"),
		gaugeRow(frame316, "early", branchEarly, "cell_6", bigWord(16), "mV", 1, qGaugeCell+"6"),
		gaugeRow(frame316, "early", branchEarly, "cell_7", bigWord(32), "mV", 1, qGaugeCell+"7"),
		gaugeRow(frame316, "early", branchEarly, "cell_8", bigWord(48), "mV", 1, qGaugeCell+"8"),

		gaugeRow(frame316, "l2", "l2", "accumulator", bigWord(0), "", 1, qGaugeValid),
		gaugeRow(frame316, "l2", "l2", "initial_accumulator", bigWord(0), "", 1, qGaugeInitial),
		gaugeRow(frame316, "l2", "l2", "capacity", bigWord(16), "Ah", 10, qGaugeValid),
		gaugeRow(frame316, "l2", "l2", "cycles", bigWord(32), "cycles", 10, qGaugeValid),
		gaugeRow(frame316, "l2", "l2", "state", byteAt(48), "", 1, qGaugeValid),
		gaugeRow(frame316, "l2", "l2", "failed_precheck_state", byteAt(48), "", 1, qGaugeFailedPrecheck),
		gaugeRow(frame316, "l2", "l2", "budget", byteAt(56), "", 1, qGaugeValid),

		gaugeRawRow(frame316, "l6", "l6", "accumulator", bigWord(0)),
		gaugeRawRow(frame316, "l6", "l6", "initial_accumulator", bigWord(0)),
		gaugeRawRow(frame316, "l6", "l6", "capacity", bigWord(16)),
		gaugeRawRow(frame316, "l6", "l6", "cycles", bigWord(32)),
		gaugeRawRow(frame316, "l6", "l6", "state", byteAt(48)),
		gaugeRawRow(frame316, "l6", "l6", "failed_precheck_state", byteAt(48)),
		gaugeRawRow(frame316, "l6", "l6", "budget", byteAt(56)),

		gaugeRow(frame316, "l3", "l3", "retained_fault", byteAt(0), "", 1, qGaugeValid),
		gaugeRow(frame316, "l3", "l3", "algorithm", byteAt(8), "", 1, qGaugeValid),
		gaugeRawRow(frame316, "l3", "l3", "word_2", bigWord(16)),
		gaugeRawRow(frame316, "l3", "l3", "word_4", bigWord(32)),
		gaugeRawRow(frame316, "l3", "l3", "word_6", bigWord(48)),
		gaugeRow(frame316, "l4", "l4", "retained_fault", byteAt(0), "", 1, qGaugeValid),
		gaugeRow(frame316, "l4", "l4", "algorithm", byteAt(8), "", 1, qGaugeValid),
		gaugeRawRow(frame316, "l4", "l4", "word_2", bigWord(16)),
		gaugeRawRow(frame316, "l4", "l4", "word_4", bigWord(32)),
		gaugeRawRow(frame316, "l4", "l4", "word_6", bigWord(48)),

		gaugeRawRow(frame316, "l5", "l5", "word_0", bigWord(0)),
		gaugeRawRow(frame316, "l5", "l5", "word_2", bigWord(16)),
		gaugeRow(frame316, "l5", "l5", "capacity_byte", byteAt(32), "", 1, qGaugeValid),
		gaugeRow(frame316, "l5", "l5", "gauge_byte", byteAt(40), "", 1, qGaugeValid),
		gaugeRawRow(frame316, "l5", "l5", "word_6", bigWord(48)),
		gaugeRawRow(frame316, "l7", "l7", "word_0", bigWord(0)),
		gaugeRawRow(frame316, "l7", "l7", "word_2", bigWord(16)),
		gaugeRow(frame316, "l7", "l7", "capacity_byte", byteAt(32), "", 1, qGaugeValid),
		gaugeRow(frame316, "l7", "l7", "gauge_byte", byteAt(40), "", 1, qGaugeValid),
		gaugeRawRow(frame316, "l7", "l7", "word_6", bigWord(48)),

		gaugeRow(frame316, "l8", "l8", "retained_fault", byteAt(0), "", 1, qGaugeValid),
		gaugeRow(frame316, "l8", "l8", "algorithm", byteAt(8), "", 1, qGaugeValid),
		gaugeRow(frame316, "l8", "l8", "word_2", bigWord(16), "", 1, qGaugeValid),
		gaugeRow(frame316, "l8", "l8", "capacity", bigWord(32), "Ah", 10, qGaugeValid),
		gaugeRow(frame316, "l8", "l8", "cycles", bigWord(48), "cycles", 10, qGaugeValid),

		gaugeRawRow(frame316, "other", branchOther, "word_0", bigWord(0)),
		gaugeRawRow(frame316, "other", branchOther, "word_2", bigWord(16)),
		gaugeRawRow(frame316, "other", branchOther, "word_4", bigWord(32)),
		gaugeRawRow(frame316, "other", branchOther, "word_6", bigWord(48)),
	)

	// 0x317: words on L0; retained bytes on L1 (eight) and otherwise (six), with
	// the truncated SOH and the slot on the non-L1 branch.
	add(
		gaugeRow(frame317, "", "l0", "word_0", bigWord(0), "", 1, qGaugeValid),
		gaugeRow(frame317, "", "l0", "word_2", bigWord(16), "", 1, qGaugeValid),
		gaugeRow(frame317, "", "l0", "word_4", bigWord(32), "", 1, qGaugeValid),
		gaugeRow(frame317, "", "l0", "word_6", bigWord(48), "", 1, qGaugeValid),
	)
	for k := 0; k < 8; k++ {
		add(gaugeRow(frame317, "l1", "l1", fmt.Sprintf("retained_%d", 8+k), byteAt(uint16(8*k)), "", 1, qGaugeValid))
	}
	for k := 0; k < 6; k++ {
		add(gaugeRow(frame317, "other", branchOther, fmt.Sprintf("retained_%d", 8+k), byteAt(uint16(8*k)), "", 1, qGaugeValid))
	}
	add(
		gaugeRow(frame317, "", branchOther, "soh_truncated", byteAt(48), "percent", 1, qGaugeSOHTruncated),
		gaugeRow(frame317, "", branchOther, "slot", byteAt(56), "", 1, qGaugeSlot),
	)

	// 0x318: words on L0; retained bytes otherwise.
	add(
		gaugeRow(frame318, "", "l0", "word_0", bigWord(0), "", 1, qGaugeValid),
		gaugeRow(frame318, "", "l0", "word_2", bigWord(16), "", 1, qGaugeValid),
		gaugeRow(frame318, "", "l0", "word_4", bigWord(32), "", 1, qGaugeValid),
		gaugeRow(frame318, "", "l0", "word_6", bigWord(48), "", 1, qGaugeValid),
	)
	for k := 0; k < 8; k++ {
		add(gaugeRow(frame318, "other", branchOther, fmt.Sprintf("retained_%d", k), byteAt(uint16(8*k)), "", 1, qGaugeValid))
	}

	// Retained fault bits: one row per bit of each retained byte the decoder
	// accepts. Index 8*byteOffset+bit with the retained byte offset of the frame.
	for k := 0; k < 8; k++ {
		for bit := 0; bit < 8; bit++ {
			slice := bitsAt(uint16(8*k+bit), 1)
			add(gaugeBit(frame317, "l1", "l1", 64+8*k+bit, slice, fmt.Sprintf("retained_%d", 8+k)))
			if k < 6 {
				add(gaugeBit(frame317, "other", branchOther, 64+8*k+bit, slice, fmt.Sprintf("retained_%d", 8+k)))
			}
			add(gaugeBit(frame318, "other", branchOther, 8*k+bit, slice, fmt.Sprintf("retained_%d", k)))
		}
	}

	// Gateway placeholder: the eight raw bytes and the zero placeholder, on each frame.
	for _, frame := range []uint32{frame315, frame316, frame317, frame318} {
		for at := 0; at < 8; at++ {
			add(gaugeRawRow(frame, "", branchPlaceholder, fmt.Sprintf("byte_%d", at), byteAt(uint16(8*at))))
		}
		placeholder := gaugeRow(frame, "", branchPlaceholder, "placeholder", byteAt(0), "", 1, qGaugeZeroPayload)
		placeholder.numerator = true
		add(placeholder)
	}
	add(gaugeParallelBankSpecs(out)...)
	return out
}

// gaugeNumeratorIDs returns the decoder field name of every row whose raw word
// is the decoder's numerator, keyed by row identifier.
func gaugeNumeratorIDs() map[string]string {
	out := make(map[string]string)
	for _, s := range gaugeSpecs() {
		if s.numerator {
			out[gaugeNativeRow(s).ID] = s.field
		}
	}
	return out
}

// gaugeBitHook gates a retained fault bit on the accepted disposition of its
// retained byte. A byte the decoder does not accept gives no bit: the row does
// not apply to the observation.
func gaugeBitHook(byteField string) roweval.RowHook {
	byteHook := rowHook(byteField, qGaugeValid)
	return func(in roweval.DetachedInputs, id roweval.RowID, dir roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		dv, reason := byteHook(in, id, dir, v)
		if reason != "" {
			return roweval.DerivedValue{}, roweval.ReasonNoMatch
		}
		return dv, ""
	}
}

// gaugeTable returns the native rows of the gauge frames and the row hooks they
// name. The table has no bindings.
func gaugeTable() roweval.Table[Selector] {
	specs := gaugeSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := gaugeNativeRow(s)
		rows = append(rows, row)
		if s.bitOf != "" {
			hooks[row.Decode.Hook] = gaugeBitHook(s.bitOf)
		} else {
			hooks[row.Decode.Hook] = rowHook(s.field, s.gate)
		}
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// gaugeRawIDs returns the identifiers of the rows the decoder never gates to a value.
func gaugeRawIDs() map[string]bool {
	raw := make(map[string]bool)
	for _, s := range gaugeSpecs() {
		if s.raw {
			raw[gaugeNativeRow(s).ID] = true
		}
	}
	return raw
}

// gaugeFrameDisposition admits the standard identifiers 0x315 to 0x318 with DLC 8
// and raw DLC 8. Every other frame matches no gauge row.
func gaugeFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x315, 0x316, 0x317, 0x318:
		return ""
	}
	return roweval.ReasonNoMatch
}

// GaugeResult is the evaluation of one gauge observation: the evidence it
// carries, the selection attribution of its fields and the evaluation of every
// row that applies to it, keyed by row identifier.
type GaugeResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
}

// EvaluateGauge evaluates the native gauge rows of one observation under the
// selection and decoder input it was received with. A frame the gauge decoder
// refuses gives the decoder's status as the reason and no rows.
func EvaluateGauge(s canbusreg.Selection, in canbusreg.PCSIdentityPCSInput) (GaugeResult, roweval.Reason, error) {
	res := GaugeResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSGauge(s, in)
	if decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	if reason := gaugeFrameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	rows, err := evaluateGaugeRows(gaugeTable(), frame, gaugeQualifiers(s, in), gaugeRawIDs(), gaugeBitFields(), gaugeNumeratorIDs(), decoded.Fields)
	res.Rows = rows
	return res, "", err
}

// evaluateGaugeRows evaluates every row of the frame's identifier. Each row is
// evaluated on its own: rows that read the same payload slice under different
// fields are distinct native fields, so the evaluator never sees them as one
// ambiguous match. A row whose conditions do not hold, or whose hook gives no
// applicable value, is not in the result. A retained fault bit row takes the
// accepted or raw disposition the decoder gives its field in fields.
func evaluateGaugeRows(t roweval.Table[Selector], frame canbus.Frame, q roweval.Qualifiers, raw map[string]bool, bits, numerators map[string]string, fields map[string]canbusreg.NativeField) (map[string]roweval.Evaluation, error) {
	out := make(map[string]roweval.Evaluation)
	for _, row := range t.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		single := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: t.Hooks}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At.Slice), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(single, a)
		if err != nil {
			return nil, err
		}
		if ev.Native == nil || ev.Native.Reason == roweval.ReasonNoMatch {
			continue
		}
		if ev.Native.Reason != "" {
			ev.Native.Value = nil
			ev.Native.Unit = ""
		}
		// A numerator row keeps the decoder's raw zero, not the byte its slice reads.
		if field, ok := numerators[ev.Native.RowID]; ok {
			if df, ok := fields[field]; ok {
				ev.Native.Raw = uint64(df.Numerator)
			}
		}
		ev.Native.Disposition = disposition(ev.Native, raw[ev.Native.RowID])
		if key, ok := bits[ev.Native.RowID]; ok && ev.Native.Reason == "" {
			ev.Native.Disposition = gaugeBitDisposition(fields[key].Status)
		}
		out[ev.Native.RowID] = ev
	}
	return out, nil
}
