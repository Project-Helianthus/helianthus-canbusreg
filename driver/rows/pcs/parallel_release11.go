package pcs

import (
	"fmt"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the native rows of the release-11 parallel families that
// ParallelReleaseDecodeParallel emits fields for: the addressed families 0x510,
// 0x520, 0x610, 0x650, 0x680 and 0x6D0 and the coordinator summary 0x590. The
// decoder emits no field for 0x37F and 0x3F7 (status raw only) and does not
// decode 0x6FF, so those identifiers have no row. Every row is bound to the
// parallel bank (flavor pcs-parallel, bank parallel) and to the release-11
// contract group L8, and its identifier carries the family and the field. An
// addressed family is the identifier without its group nibble, so a row's
// selector is the family base; the group stays attribution.

const (
	parallelRowPrefix = "growatt.bms.pcs.parallel."

	qState = "parallel-state:"

	stateAccepted = "accepted"
	stateRaw      = "raw"
	stateWithheld = "withheld"
	stateConflict = "conflict"
	stateInvalid  = "invalid"
	stateEmpty    = "empty"
	stateUnknown  = "unknown"
	stateAbsent   = "absent"

	qDTCStatus = "parallel-dtc-status:"
	qDTCIndex  = "parallel-dtc-index:"

	scopeLocal     = "local"
	scopeAggregate = "aggregate"
	groupRelease11 = "L8"
)

// relSpec describes one native row of a release-11 parallel family. The layout
// token names the scope under which the same field has its own conversion; it
// is empty when one conversion serves both scopes.
type relSpec struct {
	frame         uint32
	layout, field string
	slice         signalSlice
	unit          string
	divisor       int64
	offset        int64
	scope         string
	slot          bool
	role          string
}

func (r relSpec) id() string {
	layout := ""
	if r.layout != "" {
		layout = r.layout + "."
	}
	return fmt.Sprintf("%s%X.%s%s", parallelRowPrefix, r.frame, layout, r.field)
}

func (r relSpec) stateKey() string { return qState + fmt.Sprintf("%X.%s", r.frame, r.field) }

func plain(frame uint32, field string, slice signalSlice) relSpec {
	return relSpec{frame: frame, field: field, slice: slice, divisor: 1}
}

func parallelReleaseSpecs() []relSpec {
	var specs []relSpec
	slotted := func(s relSpec) relSpec { s.slot = true; return s }
	scoped := func(layout string, s relSpec) relSpec { s.layout, s.scope = layout, layout; return s }
	aggregateOnly := func(s relSpec) relSpec { s.scope = scopeAggregate; return s }
	for _, s := range []relSpec{
		plain(0x510, "slot", byteAt(0)),
		plain(0x510, "minimum_cell", littleWord(8)),
		plain(0x510, "independent_b3", byteAt(24)),
		plain(0x510, "state_4", byteAt(32)),
		plain(0x510, "state_5", byteAt(40)),
		plain(0x510, "state_6", byteAt(48)),
		plain(0x510, "state_7", byteAt(56)),
		plain(0x520, "slot", byteAt(0)),
		plain(0x520, "zero_5", byteAt(40)),
		plain(0x520, "zero_6", byteAt(48)),
		plain(0x520, "zero_7", byteAt(56)),
	} {
		specs = append(specs, slotted(s))
	}
	for _, d := range []struct {
		field string
		at    uint16
	}{{"dtc_a", 8}, {"dtc_b", 24}} {
		word, index := plain(0x520, d.field, bigWord(d.at)), plain(0x520, d.field+"_index", bigWord(d.at))
		word.role, index.role = roleWord, roleIndex
		specs = append(specs, slotted(word), slotted(index))
	}
	specs = append(specs,
		aggregateOnly(plain(0x590, "state_0", byteAt(0))),
		aggregateOnly(plain(0x590, "state_1", byteAt(8))),
		aggregateOnly(plain(0x590, "state_2", byteAt(16))),
		aggregateOnly(plain(0x590, "aggregate_3", littleWord(24))),
		aggregateOnly(plain(0x590, "aggregate_5", littleWord(40))),
		aggregateOnly(plain(0x590, "input_b7", byteAt(56))),
	)
	specs = append(specs,
		plain(0x610, "mux", bitsAt(15, 1)),
		relSpec{frame: 0x610, field: "voltage", slice: bitsAt(0, 15), unit: "V", divisor: 10},
		relSpec{frame: 0x610, field: "current", slice: littleWord(16), unit: "A", divisor: 10, offset: -1000},
		scoped(scopeAggregate, plain(0x610, "port", littleWord(32))),
		scoped(scopeLocal, relSpec{frame: 0x610, field: "port", slice: littleWord(32), unit: "V", divisor: 10}),
		relSpec{frame: 0x610, field: "soc", slice: littleWord(48), unit: "percent", divisor: 10},
		plain(0x650, "maximum_index", byteAt(0)),
		plain(0x650, "zero_1", byteAt(8)),
		plain(0x650, "minimum_index", byteAt(24)),
		plain(0x650, "zero_4", byteAt(32)),
		plain(0x650, "soh", byteAt(56)),
		plain(0x680, "limit_a", littleWord(0)),
		plain(0x680, "limit_b", littleWord(16)),
		scoped(scopeAggregate, plain(0x680, "soc_report", littleWord(32))),
		scoped(scopeLocal, relSpec{frame: 0x680, field: "soc_report", slice: littleWord(32), unit: "percent", divisor: 10}),
		plain(0x680, "state_bit_2", byteAt(48)),
		plain(0x680, "state_bit_1", byteAt(56)),
		scoped(scopeAggregate, plain(0x6d0, "cell_sum", littleWord(0))),
		scoped(scopeLocal, relSpec{frame: 0x6d0, field: "cell_sum", slice: littleWord(0), unit: "V", divisor: 10}),
		plain(0x6d0, "pack_over_200", byteAt(16)),
		plain(0x6d0, "state_nonzero", byteAt(24)),
		plain(0x6d0, "zero_4", byteAt(32)),
		plain(0x6d0, "zero_5", byteAt(40)),
		plain(0x6d0, "zero_6", byteAt(48)),
		plain(0x6d0, "zero_7", byteAt(56)),
	)
	for _, t := range []struct {
		field string
		at    uint16
	}{{"temperature_maximum", 16}, {"temperature_minimum", 40}, {"average_temperature", 48}} {
		specs = append(specs,
			scoped(scopeAggregate, plain(0x650, t.field, byteAt(t.at))),
			scoped(scopeLocal, relSpec{frame: 0x650, field: t.field, slice: byteAt(t.at), unit: "C", divisor: 1, offset: -40}),
		)
	}
	return specs
}

func parallelReleaseRow(r relSpec) roweval.NativeRow[Selector] {
	equal := map[string]string{qFlavor: "pcs-parallel", qBank: "parallel", qContractGroup: groupRelease11}
	if r.scope != "" {
		equal[qScope] = r.scope
	}
	if r.slot {
		equal[qSlotContext] = slotFrameContext
	}
	if r.role != "" {
		equal[qDTCRole] = r.role
	}
	row := roweval.NativeRow[Selector]{
		ID:      r.id(),
		Unit:    r.unit,
		SpecRef: fmt.Sprintf("CAN %#x", r.frame),
		At:      Selector{Identifier: r.frame, DLC: 8, RawDLC: 8, Slice: r.slice},
		When:    roweval.When{Equal: equal},
		Wire:    roweval.Layout{BitOffset: r.slice.offset, Width: r.slice.width, Codec: "unsigned", ByteOrder: r.slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: r.divisor}},
	}
	if r.offset != 0 {
		row.Decode.Numeric.Add = wholeDecimal(r.offset)
	}
	row.Decode.Hook = rowHookPrefix + row.ID
	return row
}

var unknownReason roweval.Reason = stateUnknown

// parallelStateHook is the hook of one native row. The state is the decoder
// status of the field the observation supplies: an accepted or raw field keeps
// its value, a withheld, conflicting or invalid one keeps only its raw word,
// and a field the decoder does not emit is absent.
func parallelStateHook(key string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		switch in.Qualifiers().Values[key] {
		case stateAccepted, stateRaw:
			return roweval.DerivedValue{Value: v}, ""
		case stateWithheld:
			return roweval.DerivedValue{}, gateWithheld
		case stateConflict:
			return roweval.DerivedValue{}, roweval.Reason(stateConflict)
		case stateInvalid:
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		case stateEmpty:
			return roweval.DerivedValue{}, emptyReason
		case stateUnknown:
			return roweval.DerivedValue{}, unknownReason
		}
		return roweval.DerivedValue{}, roweval.Reason(stateAbsent)
	}
}

// parallelIndexHook is the hook of the index companion of one dictionary word.
// The index is the position of the word in the member dictionary, supplied as a
// qualifier; the companion exists only for an accepted word.
func parallelIndexHook(word string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, _ roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		q := in.Qualifiers().Values
		if q[qDTCStatus+word] != stateAccepted {
			return roweval.DerivedValue{}, roweval.Reason(stateAbsent)
		}
		n, err := strconv.ParseInt(q[qDTCIndex+word], 10, 64)
		if err != nil {
			return roweval.DerivedValue{}, roweval.ReasonDecode
		}
		return roweval.DerivedValue{Value: semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: wholeDecimal(n)}}}, ""
	}
}

// ParallelReleaseTable returns the native rows of the release-11 parallel
// families and the row hooks those rows name.
func ParallelReleaseTable() roweval.Table[Selector] {
	specs := parallelReleaseSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := parallelReleaseRow(s)
		rows = append(rows, row)
		if s.role == roleIndex {
			hooks[row.Decode.Hook] = parallelIndexHook(s.field[:len(s.field)-len("_index")])
		} else {
			hooks[row.Decode.Hook] = parallelStateHook(s.stateKey())
		}
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// parallelFamily is the family base of a frame the table names: the identifier
// without its group nibble, or the exact summary identifier.
func parallelFamily(frame canbus.Frame) (uint32, bool) {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return 0, false
	}
	id := frame.ID().Value()
	switch id &^ 15 {
	case 0x510, 0x520, 0x610, 0x650, 0x680, 0x6d0:
		return id &^ 15, true
	case 0x590:
		return 0x590, id == 0x590
	}
	return 0, false
}

// contextValue reads one independently attributed context source the way the
// decoder does: it must be accepted, graded, integral, within the maximum and
// attributed to the same selection and interface.
func contextValue(s canbusreg.Selection, f *canbusreg.NativeField, maximum int64) (int64, bool) {
	c := s.SelectionSpecValue()
	if f == nil {
		return 0, false
	}
	return f.Numerator, f.Status == "accepted" && (f.Grade == "Proven" || f.Grade == "Hypothesis") && f.Denominator == 1 && f.Numerator >= 0 && f.Numerator <= maximum && f.Evidence.Interface == c.Interface && f.Epoch == c.Epoch && f.Scope == c.Scope && f.Bank == c.Bank && f.Endpoint == c.Endpoint && f.Group == c.Group && f.Slot == c.Slot && f.Age >= 0
}

// derivedState is the status of a field the decoder derives from a context
// source: withheld when the source is not established, conflict when the wire
// byte differs from the expectation, accepted otherwise.
func derivedState(known bool, wire, expected int64) string {
	switch {
	case !known:
		return stateWithheld
	case wire != expected:
		return stateConflict
	}
	return stateAccepted
}

func zeroState(b byte) string {
	if b != 0 {
		return stateInvalid
	}
	return stateAccepted
}

func flagOf(on bool) int64 {
	if on {
		return 1
	}
	return 0
}

func parallelLE(p [8]byte, at int) int64 { return int64(p[at]) | int64(p[at+1])<<8 }

// parallelStates computes the decoder status of every field of one family for
// the selection and its context, following ParallelReleaseDecodeParallel.
func parallelStates(s canbusreg.Selection, scope string, p [8]byte, family uint32, x canbusreg.ParallelContext) map[string]string {
	st := make(map[string]string)
	set := func(field, state string) { st[qState+fmt.Sprintf("%X.%s", family, field)] = state }
	local := scope == scopeLocal
	switch family {
	case 0x510:
		for _, f := range []string{"slot", "minimum_cell", "state_4", "state_5", "state_6"} {
			set(f, stateAccepted)
		}
		set("independent_b3", stateRaw)
		switch {
		case s.SelectionSpecValue().ProjectCode == "QAAa":
			set("state_7", stateAccepted)
		case local:
			set("state_7", zeroState(p[7]))
		default:
			set("state_7", stateRaw)
		}
	case 0x520:
		set("slot", stateAccepted)
		for at := 5; at < 8; at++ {
			set(fmt.Sprintf("zero_%d", at), zeroState(p[at]))
		}
		count := slotMemberCount(s)
		for _, d := range []struct {
			field string
			at    int
		}{{"dtc_a", 1}, {"dtc_b", 3}} {
			status, index := stateAbsent, 0
			word := uint16(p[d.at])<<8 | uint16(p[d.at+1])
			if count > 0 {
				status = stateUnknown
				if word == 0 {
					status = stateEmpty
				}
				for n := 0; n < count && word != 0; n++ {
					if expected, _ := canbusreg.EncodeDTC(s, n); expected == word {
						status, index = stateAccepted, n
						break
					}
				}
			}
			set(d.field, status)
			st[qDTCStatus+d.field] = status
			st[qDTCIndex+d.field] = strconv.Itoa(index)
		}
	case 0x590:
		for _, f := range []string{"state_0", "state_1", "state_2"} {
			set(f, stateAccepted)
		}
		for _, f := range []string{"aggregate_3", "aggregate_5", "input_b7"} {
			set(f, stateRaw)
		}
	case 0x610:
		for _, f := range []string{"mux", "voltage", "current", "soc"} {
			set(f, stateAccepted)
		}
		set("port", stateRaw)
		if local {
			set("port", stateAccepted)
		}
	case 0x650:
		parallelTemperatureStates(s, local, p, set, x)
	case 0x680:
		parallelCoordinatorStates(s, local, p, set, x)
	case 0x6d0:
		parallelCellSumStates(s, local, p, set, x)
	}
	return st
}

func parallelTemperatureStates(s canbusreg.Selection, local bool, p [8]byte, set func(field, state string), x canbusreg.ParallelContext) {
	set("maximum_index", stateAccepted)
	set("minimum_index", stateAccepted)
	set("zero_1", zeroState(p[1]))
	set("zero_4", zeroState(p[4]))
	for _, t := range []struct {
		field string
		bad   bool
	}{{"temperature_maximum", p[1] != 0}, {"temperature_minimum", p[4] != 0}, {"average_temperature", false}} {
		switch {
		case t.bad:
			set(t.field, stateInvalid)
		case local:
			set(t.field, stateAccepted)
		default:
			set(t.field, stateRaw)
		}
	}
	if p[1] != 0 {
		set("maximum_index", stateInvalid)
	}
	if p[4] != 0 {
		set("minimum_index", stateInvalid)
	}
	set("soh", stateRaw)
	if local {
		n, known := contextValue(s, x.SOHTenths, 65535)
		set("soh", derivedState(known, int64(p[7]), (820*n/8192)%256))
	}
}

func parallelCoordinatorStates(s canbusreg.Selection, local bool, p [8]byte, set func(field, state string), x canbusreg.ParallelContext) {
	set("limit_a", stateRaw)
	set("limit_b", stateRaw)
	set("soc_report", stateRaw)
	set("state_bit_2", stateRaw)
	set("state_bit_1", stateRaw)
	if !local {
		return
	}
	permission, kp := contextValue(s, x.Permission, 1)
	soc, ks := contextValue(s, x.SOCTenths, 65535)
	expected, known := int64(0), kp
	if permission != 0 {
		known = known && ks
		expected = soc
		if soc == 0 {
			expected = 10
		}
	}
	set("soc_report", derivedState(known, parallelLE(p, 4), expected))
	state, known := contextValue(s, x.State, 255)
	set("state_bit_2", derivedState(known, int64(p[6]), (state>>2)&1))
	set("state_bit_1", derivedState(known, int64(p[7]), (state>>1)&1))
}

func parallelCellSumStates(s canbusreg.Selection, local bool, p [8]byte, set func(field, state string), x canbusreg.ParallelContext) {
	set("cell_sum", stateRaw)
	set("pack_over_200", stateRaw)
	set("state_nonzero", stateRaw)
	for at := 4; at < 8; at++ {
		set(fmt.Sprintf("zero_%d", at), zeroState(p[at]))
	}
	if !local {
		return
	}
	a, ka := contextValue(s, x.CategoryFirst, 65535)
	b, kb := contextValue(s, x.CategorySecond, 65535)
	sum := parallelLE(p, 0)
	switch valid, kv := contextValue(s, x.CellSumValid, 1); {
	case !ka || !kb:
		set("cell_sum", stateWithheld)
	case a != 0 || b != 0:
		if sum != 65535 {
			set("cell_sum", stateConflict)
		} else {
			set("cell_sum", stateInvalid)
		}
	case sum == 65535:
		set("cell_sum", stateInvalid)
	case !kv || valid != 1:
		set("cell_sum", stateWithheld)
	default:
		set("cell_sum", stateAccepted)
	}
	pack, kp := contextValue(s, x.PackCode, 65535)
	state, ks := contextValue(s, x.State, 255)
	set("pack_over_200", derivedState(kp, int64(p[2]), flagOf(pack > 200)))
	set("state_nonzero", derivedState(ks, int64(p[3]), flagOf(state != 0)))
}

// ParallelReleaseResult is the evaluation of one release-11 parallel
// observation: the evidence it carries, its attribution (an absent group or slot
// stays absent) and the evaluation of every applicable row, keyed by row
// identifier.
type ParallelReleaseResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
}

// parallelQualifiers is the selection vocabulary of one observation: the bank
// pairing, the contract group, the scope and the decoder status of each field.
func parallelQualifiers(s canbusreg.Selection, in canbusreg.LINKInput, x canbusreg.ParallelContext, family uint32) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	values := parallelStates(s, c.Scope, in.Evidence.Frame.Bytes(), family, x)
	values[qFlavor] = c.Flavor
	values[qBank] = c.Bank
	values[qScope] = c.Scope
	values[qContractGroup] = s.SelectionGroup()
	values[qSlotContext] = slotFrameContext
	return roweval.Qualifiers{Values: values}
}

// EvaluateParallelRelease evaluates the native rows of one parallel
// observation under the selection and context it was received with. A frame
// the decoder does not accept gives the decoder's status as the reason and no
// rows, so an observation it refuses is never evaluated as a value.
func EvaluateParallelRelease(s canbusreg.Selection, in canbusreg.LINKInput, x canbusreg.ParallelContext) (ParallelReleaseResult, roweval.Reason, error) {
	res := ParallelReleaseResult{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	decoded := canbusreg.ParallelReleaseDecodeParallel(s, in, x)
	if decoded.Status != "accepted" {
		return res, roweval.Reason(decoded.Status), nil
	}
	frame := in.Evidence.Frame
	family, ok := parallelFamily(frame)
	if !ok {
		return res, roweval.ReasonNoMatch, nil
	}
	rows, err := evaluateParallelRows(ParallelReleaseTable(), frame, family, parallelQualifiers(s, in, x, family))
	if err != nil {
		return res, "", err
	}
	res.Rows = rows
	return res, "", nil
}

// evaluateParallelRows evaluates every row of the family under the qualifiers.
// A row whose conditions do not hold is not returned.
func evaluateParallelRows(table roweval.Table[Selector], frame canbus.Frame, family uint32, q roweval.Qualifiers) (map[string]roweval.Evaluation, error) {
	out := make(map[string]roweval.Evaluation)
	for _, row := range table.Native {
		if row.At.Identifier != family {
			continue
		}
		one := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: table.Hooks}
		sel := Selector{Identifier: family, DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
		a := roweval.Acquired[Selector]{Selector: sel, Payload: frame.Data(), Qualifiers: rowQualifiers(q, row), Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(one, a)
		if err != nil {
			return nil, err
		}
		if ev.Native == nil {
			if ev.Reason != roweval.ReasonNoMatch {
				out[row.ID] = ev
			}
			continue
		}
		if ev.Native.Reason != "" {
			ev.Native.Value = nil
			ev.Native.Unit = ""
		}
		ev.Native.Disposition = parallelDisposition(ev.Native, q.Values[rowStateKey(row)])
		out[ev.Native.RowID] = ev
	}
	return out, nil
}

// rowStateKey is the state qualifier key of a row of the table.
func rowStateKey(row roweval.NativeRow[Selector]) string {
	for _, r := range parallelReleaseSpecs() {
		if r.id() == row.ID {
			return r.stateKey()
		}
	}
	return ""
}

// parallelDisposition states how the native value of one row was obtained: a
// raw field keeps its value as raw evidence, a withheld one keeps its raw word.
func parallelDisposition(n *roweval.Native, state string) string {
	if state == stateRaw && n.Reason == "" {
		return dispositionRaw
	}
	return slotDisposition(n, false)
}
