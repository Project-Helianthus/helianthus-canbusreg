package pcs

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// rowSpec describes one native row of a standard PCS frame. The field is the
// key DecodePCSStandard emits; the layout names the selection under which a
// repeated field name has its own slice or meaning (empty when unique); the
// gate is the gate qualifier the decoder tests (empty when the field is raw);
// the when conditions are added to the bank and flavor pairing every row carries.
type rowSpec struct {
	frame         uint32
	layout, field string
	slice         signalSlice
	unit          string
	divisor       int64
	gate          string
	percentDomain bool
	raw           bool
	when          map[string]string
}

// Slices of the standard frames. Offsets are bit offsets within the 8-byte
// payload; 16-bit words are big-endian except where the frame states otherwise.
func bigWord(at uint16) signalSlice { return signalSlice{offset: at, width: 16, order: bigEndian} }
func signedBigWord(at uint16) signalSlice {
	return signalSlice{offset: at, width: 16, order: bigEndian, signed: true}
}
func littleWord(at uint16) signalSlice {
	return signalSlice{offset: at, width: 16, order: littleEndian}
}
func byteAt(at uint16) signalSlice {
	return signalSlice{offset: at, width: 8, order: bitSlice}
}
func bitsAt(at, width uint16) signalSlice {
	return signalSlice{offset: at, width: width, order: bitSlice}
}

// when returns the pairing every standard row carries: the flavor and the bank
// of its selection, plus the extra conditions of the row.
func when(extra ...string) map[string]string {
	m := map[string]string{qFlavor: "pcs-standard", qBank: "ordinary"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

// rawRow is a native row without a gate that the decoder never interprets: its
// raw word is the native value and its disposition is raw.
func rawRow(frame uint32, field string, slice signalSlice) rowSpec {
	return rowSpec{frame: frame, field: field, slice: slice, divisor: 1, raw: true, when: when()}
}

// standardSpecs returns the row specifications of frames 311 to 314, in frame order.
func standardSpecs() []rowSpec {
	var specs []rowSpec
	specs = append(specs, limitSpecs()...)
	specs = append(specs, summarySpecs()...)
	specs = append(specs, measurementSpecs()...)
	specs = append(specs, capacitySpecs()...)
	return specs
}

// limitSpecs are the rows of frame 0x311. The charge and discharge words are
// swapped on the gateway aggregate; each layout has its own row. The voltage
// and the aggregation-path words are the decoder's local, measured values; the
// hint bits are the local hypotheses; the status word is raw. No row carries a
// SemReg meaning, and the 0x311 scale is not established by the page.
func limitSpecs() []rowSpec {
	const f = 0x311
	local := func(s rowSpec) rowSpec { s.gate, s.when = gateLocalMeasured, when(qGateway, "false"); return s }
	gateway := func(s rowSpec) rowSpec { s.gate, s.when = gateLocalMeasured, when(qGateway, "true"); return s }
	hint := func(field string, slice signalSlice) rowSpec {
		return rowSpec{frame: f, field: field, slice: slice, divisor: 1, gate: gateLocal, when: when()}
	}
	return []rowSpec{
		{frame: f, field: "voltage_limit", slice: bigWord(0), unit: "V", divisor: 10, gate: gateLocalMeasured, when: when()},
		local(rowSpec{frame: f, layout: "direct", field: "charge_limit", slice: bigWord(16), unit: "A", divisor: 1}),
		gateway(rowSpec{frame: f, layout: "gateway", field: "charge_limit", slice: bigWord(32), unit: "A", divisor: 1}),
		local(rowSpec{frame: f, layout: "direct", field: "discharge_limit", slice: bigWord(32), unit: "A", divisor: 1}),
		gateway(rowSpec{frame: f, layout: "gateway", field: "discharge_limit", slice: bigWord(16), unit: "A", divisor: 1}),
		rawRow(f, "status", bigWord(48)),
		hint("raw_state", bitsAt(56, 2)),
		hint("fault_hint", bitsAt(58, 1)),
		hint("sleep_hint", bitsAt(60, 1)),
		hint("discharge_hint", bitsAt(61, 1)),
		hint("charge_hint", bitsAt(62, 1)),
		hint("force_hint", bitsAt(50, 1)),
	}
}

// summarySpecs are the rows of frame 0x312: the two little-endian status words,
// the reported count and the three padding bytes, all raw.
func summarySpecs() []rowSpec {
	const f = 0x312
	return []rowSpec{
		rawRow(f, "status_a", littleWord(0)),
		rawRow(f, "status_b", littleWord(16)),
		rawRow(f, "reported_count", byteAt(32)),
		rawRow(f, "padding_5", byteAt(40)),
		rawRow(f, "padding_6", byteAt(48)),
		rawRow(f, "padding_7", byteAt(56)),
	}
}

// measurementSpecs are the rows of frame 0x313. The voltage and the temperature
// have a legacy meaning (QAAA0006: pack voltage and coded minimum temperature)
// and a release-11 meaning (output-port voltage and count-aware fine average
// temperature); each meaning is its own row. The soh_high_bit is distinct too:
// its meaning differs between the two contracts.
func measurementSpecs() []rowSpec {
	const f = 0x313
	legacy := when(qRelease11, "false")
	release11 := when(qRelease11, "true")
	return []rowSpec{
		{frame: f, layout: "legacy", field: "voltage", slice: bigWord(0), unit: "V", divisor: 100, gate: gateMeasured, when: legacy},
		{frame: f, layout: "release11", field: "voltage", slice: bigWord(0), unit: "V", divisor: 100, gate: gateMeasured, when: release11},
		{frame: f, field: "current", slice: signedBigWord(16), unit: "A", divisor: 10, gate: gateMeasured, when: when()},
		{frame: f, layout: "legacy", field: "temperature", slice: signedBigWord(32), unit: "C", divisor: 10, gate: gateTemperature, when: legacy},
		{frame: f, layout: "release11", field: "temperature", slice: signedBigWord(32), unit: "C", divisor: 10, gate: gateTemperature, when: release11},
		{frame: f, field: "soc", slice: byteAt(48), unit: "percent", divisor: 1, gate: gateMeasured, percentDomain: true, when: when()},
		{frame: f, field: "soh", slice: bitsAt(56, 7), unit: "percent", divisor: 1, gate: gateMeasured, percentDomain: true, when: when()},
		rawRow(f, "soh_status", byteAt(56)),
		{frame: f, layout: "legacy", field: "soh_high_bit", slice: bitsAt(63, 1), divisor: 1, gate: gateMeasured, when: legacy},
		{frame: f, layout: "release11", field: "soh_high_bit", slice: bitsAt(63, 1), divisor: 1, gate: gateMeasured, when: release11},
	}
}

// capacitySpecs are the rows of frame 0x314: the remaining and full capacity in
// ampere-hours at two decimals, the cell spread in millivolts and the whole
// cycle count. Each is gated by the measurement assertion.
func capacitySpecs() []rowSpec {
	const f = 0x314
	return []rowSpec{
		{frame: f, field: "remaining_capacity", slice: bigWord(0), unit: "Ah", divisor: 100, gate: gateMeasured, when: when()},
		{frame: f, field: "full_capacity", slice: bigWord(16), unit: "Ah", divisor: 100, gate: gateMeasured, when: when()},
		{frame: f, field: "spread", slice: bigWord(32), unit: "mV", divisor: 1, gate: gateMeasured, when: when()},
		{frame: f, field: "cycles", slice: bigWord(48), unit: "cycles", divisor: 1, gate: gateMeasured, when: when()},
	}
}

// Qualifiers of the extrema frames 0x319, 0x322 and 0x330. They are added to the
// standard qualifiers by extremaQualifiers and read by the extrema hooks.
const (
	qIndexValid             = "index-valid"
	qIndexMinimum           = "index-minimum"
	qIndexMaximum           = "index-maximum"
	qMeasuredTemperature    = "measured-temperature"
	qGatewayMeasured        = "gateway-measured"
	qFixedAggregateLocation = "fixed-aggregate-location"
)

// extremaSpec is a native row of an extrema frame. A row with an admit function
// is withheld unless its gate is met and the native value passes admit; the
// function receives the qualifiers and the whole value of the field.
type extremaSpec struct {
	rowSpec
	admit func(q map[string]string, n int64) bool
}

// indexSpec is a native index row. The decoder interprets the index only when the
// index is valid for the selection scope and the value lies within the inclusive
// native bounds the caller selected.
func indexSpec(frame uint32, field string, slice signalSlice) extremaSpec {
	return extremaSpec{rowSpec: rowSpec{frame: frame, field: field, slice: slice, unit: "index", divisor: 1, gate: qIndexValid, when: when()}, admit: inIndexRange}
}

// fixedLocationSpec is the fixed-aggregate location candidate: the decoder keeps it
// as a candidate only when the byte is exactly 1. The raw location row reads the
// same byte, and the evaluator matches a row by its selector alone, so the
// candidate's selector carries the little-endian order: for one byte that order
// is the identity, so the decoded value is the same.
func fixedLocationSpec(frame uint32, field string, at uint16) extremaSpec {
	slice := signalSlice{offset: at, width: 8, order: littleEndian}
	return extremaSpec{rowSpec: rowSpec{frame: frame, field: field, slice: slice, divisor: 1, gate: qFixedAggregateLocation, when: when()}, admit: isOne}
}

func inIndexRange(q map[string]string, n int64) bool {
	lo, errLo := strconv.ParseInt(q[qIndexMinimum], 10, 64)
	hi, errHi := strconv.ParseInt(q[qIndexMaximum], 10, 64)
	return errLo == nil && errHi == nil && n >= lo && n <= hi
}

func isOne(_ map[string]string, n int64) bool { return n == 1 }

// extremaSpecs returns the row specifications of frames 0x319, 0x322 and 0x330, in
// frame order. The measured cell extrema use the decoder's measured gate; the
// temperature extrema use the measured gate with the temperature validity; the
// downstream SOC extrema of 0x322 use the gateway aggregate with measurement
// validity, and their percentage domain is the decoder's [0, 100].
func extremaSpecs() []extremaSpec {
	specs := []extremaSpec{
		{rowSpec: rawRow(0x319, "state_0", byteAt(0))},
		{rowSpec: rowSpec{frame: 0x319, field: "maximum_cell", slice: bigWord(8), unit: "mV", divisor: 1, gate: gateMeasured, when: when()}},
		{rowSpec: rowSpec{frame: 0x319, field: "minimum_cell", slice: bigWord(24), unit: "mV", divisor: 1, gate: gateMeasured, when: when()}},
		indexSpec(0x319, "maximum_index", byteAt(40)),
		indexSpec(0x319, "minimum_index", byteAt(48)),
		{rowSpec: rawRow(0x319, "state_7", byteAt(56))},
		{rowSpec: rowSpec{frame: 0x322, field: "temperature_maximum", slice: signedBigWord(0), unit: "C", divisor: 10, gate: qMeasuredTemperature, when: when()}},
		{rowSpec: rowSpec{frame: 0x322, field: "temperature_minimum", slice: signedBigWord(16), unit: "C", divisor: 10, gate: qMeasuredTemperature, when: when()}},
		indexSpec(0x322, "maximum_temperature_index", byteAt(32)),
		indexSpec(0x322, "minimum_temperature_index", byteAt(40)),
		{rowSpec: rowSpec{frame: 0x322, field: "extra_6", slice: byteAt(48), unit: "percent", divisor: 1, gate: qGatewayMeasured, percentDomain: true, when: when()}},
		{rowSpec: rowSpec{frame: 0x322, field: "extra_7", slice: byteAt(56), unit: "percent", divisor: 1, gate: qGatewayMeasured, percentDomain: true, when: when()}},
		{rowSpec: rawRow(0x330, "maximum_location", byteAt(0))},
		fixedLocationSpec(0x330, "maximum_location_fixed_aggregate", 0),
		indexSpec(0x330, "maximum_index", byteAt(8)),
		{rowSpec: rawRow(0x330, "minimum_location", byteAt(16))},
		fixedLocationSpec(0x330, "minimum_location_fixed_aggregate", 16),
		indexSpec(0x330, "minimum_index", byteAt(24)),
		{rowSpec: rowSpec{frame: 0x330, field: "maximum_cell", slice: bigWord(32), unit: "mV", divisor: 1, gate: gateMeasured, when: when()}},
		{rowSpec: rowSpec{frame: 0x330, field: "minimum_cell", slice: bigWord(48), unit: "mV", divisor: 1, gate: gateMeasured, when: when()}},
	}
	return specs
}

// extremaHook withholds a value the row's gate or admit function refuses. The
// field-state and field-age dispositions and the gate are those of rowHook.
func extremaHook(s extremaSpec) roweval.RowHook {
	base := rowHook(s.field, s.gate)
	return func(in roweval.DetachedInputs, id roweval.RowID, dir roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		d, reason := base(in, id, dir, v)
		if reason != "" || s.admit == nil {
			return d, reason
		}
		n, ok := wholeOf(v)
		if !ok || !s.admit(in.Qualifiers().Values, n) {
			return roweval.DerivedValue{}, gateWithheld
		}
		return d, ""
	}
}

// wholeOf returns the whole number a converted value carries.
func wholeOf(v roweval.TypedValue) (int64, bool) {
	if v.Quantity == nil {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(v.Quantity.Number.Coefficient + "e" + strconv.FormatInt(int64(v.Quantity.Number.Exponent10), 10))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return r.Num().Int64(), true
}

// gradeDetailPrefix prefixes the decoder grade (Proven, Hypothesis or Unknown) that
// an extrema native value carries in its detail.
const gradeDetailPrefix = "decoder grade "

// FullTable returns the native rows of Table together with the extrema rows of
// frames 0x319, 0x322 and 0x330, for a consumer that enumerates the standard PCS
// rows. EvaluateExtrema evaluates the extrema rows.
func FullTable() roweval.Table[Selector] {
	t := Table()
	ext := extremaTable()
	t.Native = append(t.Native, ext.Native...)
	for hook, fn := range ext.Hooks {
		t.Hooks[hook] = fn
	}
	return t
}

// extremaTable returns the native rows of frames 0x319, 0x322 and 0x330 with the
// hooks those rows name. It is kept apart from Table, whose rows and evaluator
// admit the frames 0x311 to 0x314 only; it has no bindings.
func extremaTable() roweval.Table[Selector] {
	specs := extremaSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s.rowSpec)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = extremaHook(s)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// extremaQualifiers returns the standard qualifiers of the selection and input,
// with the extrema qualifiers the decoder's index, measurement, temperature,
// gateway and fixed-aggregate conditions name.
func extremaQualifiers(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput) roweval.Qualifiers {
	q := Qualifiers(s, in)
	c := s.SelectionSpecValue()
	local := c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"
	release11 := s.SelectionGroup() == "L8"
	gateway := c.ProjectCode == "QCAA" && c.Release == "0001" && c.Scope == "aggregate"
	fixedAggregate := c.ProjectCode == "QAAB" && c.Release == "0004" && c.Scope == "aggregate"
	supported := local || release11 || gateway || fixedAggregate
	v := q.Values
	v[qIndexValid] = boolText(supported && in.IndexValid && in.IndexScope == c.Scope && in.IndexMinimum <= in.IndexMaximum)
	v[qIndexMinimum] = strconv.Itoa(int(in.IndexMinimum))
	v[qIndexMaximum] = strconv.Itoa(int(in.IndexMaximum))
	v[qMeasuredTemperature] = boolText(v[qMeasured] == "true" && in.TemperatureValid)
	v[qGatewayMeasured] = boolText(gateway && in.MeasurementValid)
	v[qFixedAggregateLocation] = boolText(fixedAggregate && s.SelectionGroup() != "" && in.IndexValid && in.IndexScope == c.Scope)
	return q
}

// extremaFrameDisposition admits only the standard identifier with DLC 8 and raw
// DLC 8 of the three extrema frames.
func extremaFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x319, 0x322, 0x330:
		return ""
	}
	return roweval.ReasonNoMatch
}

// EvaluateExtrema runs the decoder and the extrema rows on one observation. A
// frame the decoder refuses gives the decoder's status and no rows.
func EvaluateExtrema(s canbusreg.Selection, in canbusreg.PCSStandardPCSInput) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.DecodePCSStandard(s, in)
	res.Diagnostics = decoded.Diagnostics
	if decoded.Status != "decoded" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	if reason := extremaFrameDisposition(frame); reason != "" {
		return res, reason, nil
	}
	t := extremaTable()
	raw := extremaRawIDs()
	q := extremaQualifiers(s, in)
	out := make(map[string]roweval.Evaluation)
	for _, row := range t.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At.Slice), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(t, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil {
			if ev.Native.Reason != "" {
				ev.Native.Value = nil
				ev.Native.Unit = ""
			}
			ev.Native.Disposition = disposition(ev.Native, raw[ev.Native.RowID])
			if f, ok := decoded.Fields[ev.Native.RowID[strings.LastIndex(ev.Native.RowID, ".")+1:]]; ok {
				ev.Native.Detail = gradeDetailPrefix + f.Grade
			}
			out[ev.Native.RowID] = ev
		} else if ev.Reason != roweval.ReasonNoMatch {
			out[row.ID] = ev
		}
	}
	for _, row := range t.Native {
		if row.At.Identifier == frame.ID().Value() {
			if _, ok := out[row.ID]; !ok {
				out[row.ID] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
			}
		}
	}
	res.Rows = out
	return res, "", nil
}

// extremaRawIDs returns the identifiers of the extrema rows the decoder never
// interprets.
func extremaRawIDs() map[string]bool {
	raw := make(map[string]bool)
	for _, s := range extremaSpecs() {
		if s.raw {
			raw[nativeRow(s.rowSpec).ID] = true
		}
	}
	return raw
}
