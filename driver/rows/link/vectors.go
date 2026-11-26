package link

import (
	"fmt"
	"strings"
	"time"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const (
	kindCells         = "cells"
	kindTemperatures  = "temperatures"
	qGroupPresent     = "group-present"
	qMeanStatus       = "mean-status"
	positionPrefix    = "position-"
	enablePrefix      = "enable-"
	cellVoltageField  = "cell_voltage_"
	temperatureField  = "temperature_"
	balanceEnableName = "enable_"
	cellMeanField     = "mean"
	cellMeanFrame     = 0x1806
	balanceFrame      = 0x1801
	temperatureFrame  = 0x1830
	cellFrameFirst    = 0x1810
	cellFrameLast     = 0x1813
	cellCapacity      = 16
	sensorCapacity    = 4
	sensorInvalidWord = 65535
)

// VectorState is the independent count assertion a vector is decoded under: the
// kind (cells or temperatures), the reported count, whether that count is
// valid, and the independently asserted fixed-16 cell path. It carries the
// arguments of NewLINKCells, NewLINKFixed16Cells and NewLINKTemperatures.
type VectorState struct {
	Kind       string
	Count      int
	CountValid bool
	Fixed16    bool
}

// vectorSpec is a native row of a vector frame with the qualifier key that
// carries the decoder's status for the row.
type vectorSpec struct {
	rowSpec
	statusKey string
}

func vectorSpecs() []vectorSpec {
	var out []vectorSpec
	for n := 1; n <= cellCapacity; n++ {
		frame := uint16(cellFrameFirst + (n-1)/4)
		out = append(out, vectorSpec{
			rowSpec: rowSpec{frame: frame, field: fmt.Sprintf("%s%d", cellVoltageField, n), slice: littleWord(uint16(((n - 1) % 4) * 2)), unit: "mV", divisor: 1,
				extra: map[string]string{qGroupPresent: "true"}},
			statusKey: fmt.Sprintf("%s%d", positionPrefix, n),
		})
	}
	celsiusBias := semreg.Decimal{Coefficient: "-4", Exponent10: 1}
	for n := 1; n <= sensorCapacity; n++ {
		out = append(out, vectorSpec{
			rowSpec: rowSpec{frame: temperatureFrame, field: fmt.Sprintf("%s%d", temperatureField, n), slice: littleWord(uint16((n - 1) * 2)), unit: "C", divisor: 8, add: celsiusBias,
				extra: map[string]string{qGroupPresent: "true"}},
			statusKey: fmt.Sprintf("%s%d", positionPrefix, n),
		})
	}
	out = append(out, vectorSpec{
		rowSpec:   rowSpec{frame: cellMeanFrame, field: cellMeanField, slice: littleWord(6), unit: "mV", divisor: 1, extra: map[string]string{qGroupPresent: "true"}},
		statusKey: qMeanStatus,
	})
	for n := 1; n <= cellCapacity; n++ {
		out = append(out, vectorSpec{
			rowSpec:   rowSpec{frame: balanceFrame, field: fmt.Sprintf("%s%d", balanceEnableName, n), slice: bitsAt(uint16(n-1), 1), divisor: 1, extra: map[string]string{fmt.Sprintf("%s%d", enablePrefix, n): "true"}},
			statusKey: "",
		})
	}
	return out
}

func withhold(key string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		if key != "" && in.Qualifiers().Values[key] != "accepted" {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		return roweval.DerivedValue{Value: v}, ""
	}
}

// VectorTable returns the native rows of the cell-voltage vectors 1810 to 1813,
// the temperature vector 1830, the cell mean of 1806 and the balancing enable
// states of 1801, with the hooks those rows name. A value the decoder does not
// accept is withheld with its raw word kept.
func VectorTable() roweval.Table[Selector] {
	specs := vectorSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s.rowSpec)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = withhold(s.statusKey)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

func vectorAdmitted(s canbusreg.Selection, in canbusreg.LINKInput) bool {
	return admitted(s, in) && s.SelectionGroup() != "" && in.Evidence.Sequence > 0 && in.Evidence.Monotonic >= 0
}

func gateVerdict(g canbusreg.MeasurementGate) string {
	if !g.Configured {
		return "unconfigured"
	}
	if g.Age < 0 {
		return "invalid"
	}
	known := func(s string) bool {
		switch s {
		case "absent", "unsupported", "stale", "conflict", "invalid":
			return true
		}
		return false
	}
	if g.Status != "" && g.Status != "accepted" {
		if known(g.Status) {
			return g.Status
		}
		return "invalid"
	}
	if !g.Valid {
		switch g.Status {
		case "absent", "unsupported", "stale", "conflict":
			return g.Status
		}
		return "invalid"
	}
	return "accepted"
}

// positionStatus is the disposition of one vector position: the gate verdict,
// then the count rules, then the invalid sensor word. The fixed-16 cell path is
// not qualified by the reported count.
func positionStatus(st VectorState, index int, word uint16, g canbusreg.MeasurementGate) string {
	status := gateVerdict(g)
	if !st.CountValid && !st.Fixed16 {
		status = "absent"
	} else if index > st.Count && !st.Fixed16 {
		status = "unconfigured"
	}
	if st.Kind == kindTemperatures && word == sensorInvalidWord {
		status = "invalid"
	}
	return status
}

func observationAttribution(in canbusreg.LINKInput) Attribution {
	return Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""}
}

func wordAt(p []byte, at int) uint16 { return uint16(p[at]) | uint16(p[at+1])<<8 }

// evaluateStatusRows evaluates the rows of the frame's family; a row the hook
// withholds keeps its raw word and takes the status its key carries.
func evaluateStatusRows(frameFamily uint16, in canbusreg.LINKInput, q roweval.Qualifiers) (map[string]roweval.Evaluation, error) {
	table := VectorTable()
	keys := make(map[string]string)
	for _, s := range vectorSpecs() {
		keys[s.rowID()] = s.statusKey
	}
	out := make(map[string]roweval.Evaluation)
	frame := in.Evidence.Frame
	for _, row := range table.Native {
		if row.At.Family != frameFamily {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(table, a)
		if err != nil {
			return nil, err
		}
		if ev.Native == nil {
			continue
		}
		switch {
		case ev.Native.Reason != "":
			ev.Native.Value, ev.Native.Unit = nil, ""
			ev.Native.Disposition = q.Values[keys[ev.Native.RowID]]
		default:
			ev.Native.Disposition = dispositionAccepted
		}
		out[ev.Native.RowID] = ev
	}
	for _, row := range table.Native {
		if row.At.Family == frameFamily {
			if _, ok := out[row.ID]; !ok {
				out[row.ID] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
			}
		}
	}
	return out, nil
}

// EvaluateVector evaluates the vector rows of one cell-voltage frame (1810 to
// 1813) or temperature frame (1830) under the count assertion and the four
// per-channel gates. Every channel of the frame is present: a position without
// a valid count is absent, one beyond the count is unconfigured, a sensor word
// FFFF is invalid, and none of them carries a value. A frame without a group
// context, or outside the route, gives the reason invalid and no rows.
func EvaluateVector(s canbusreg.Selection, in canbusreg.LINKInput, st VectorState, gates [4]canbusreg.MeasurementGate) (Result, roweval.Reason, error) {
	res := Result{Evidence: in.Evidence, Attribution: observationAttribution(in)}
	if !vectorAdmitted(s, in) {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	start := 0
	switch {
	case st.Kind == kindCells && family >= cellFrameFirst && family <= cellFrameLast:
		start = int(family-cellFrameFirst) * 4
	case st.Kind == kindTemperatures && family == temperatureFrame:
	default:
		res.Status = "unsupported"
		return res, roweval.ReasonNoMatch, nil
	}
	values := Qualifiers(s, in).Values
	values[qGroupPresent] = "true"
	res.Grades = make(map[string]string)
	statuses := make(map[int]string)
	for j, g := range gates {
		index := start + j + 1
		status := positionStatus(st, index, wordAt(frame.Data(), j*2), g)
		values[fmt.Sprintf("%s%d", positionPrefix, index)] = status
		statuses[index] = status
	}
	rows, err := evaluateStatusRows(family, in, roweval.Qualifiers{Values: values})
	if err != nil {
		return res, "", err
	}
	res.Status, res.Rows = "accepted", rows
	for id, ev := range rows {
		if ev.Reason == roweval.ReasonNoMatch {
			continue
		}
		res.Grades[id] = "Unknown"
		if ev.Native != nil && ev.Native.Disposition == dispositionAccepted {
			res.Grades[id] = "Proven"
		}
	}
	return res, "", nil
}

// EvaluateCellMean evaluates the 1806 cell mean row. The contributor, count and
// gate checks stay in DecodeLINKCellMean; the row is accepted in millivolt only
// where the decoder accepts the mean, and otherwise keeps the decoder's
// disposition with the raw word and no value.
func EvaluateCellMean(s canbusreg.Selection, in canbusreg.LINKInput, g canbusreg.MeasurementGate, v canbusreg.VectorSnapshot, fixed16Valid bool) (Result, roweval.Reason, error) {
	res := Result{Evidence: in.Evidence, Attribution: observationAttribution(in)}
	if !vectorAdmitted(s, in) {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	if in.Evidence.Frame.ID().Value()>>16 != cellMeanFrame {
		res.Status = "unsupported"
		return res, roweval.ReasonNoMatch, nil
	}
	decoded := canbusreg.DecodeLINKCellMean(s, in, g, v, fixed16Valid)
	field := decoded.Fields[cellMeanField]
	values := Qualifiers(s, in).Values
	values[qGroupPresent] = "true"
	values[qMeanStatus] = field.Status
	rows, err := evaluateStatusRows(cellMeanFrame, in, roweval.Qualifiers{Values: values})
	if err != nil {
		return res, "", err
	}
	res.Status, res.Rows = "accepted", rows
	res.Grades = make(map[string]string)
	for id, ev := range rows {
		if ev.Reason != roweval.ReasonNoMatch {
			res.Grades[id] = field.Grade
		}
	}
	return res, "", nil
}

// EvaluateBalance evaluates the 1801 balancing enable rows. A row applies only
// where DecodeLINKBalance emits the enable state: the selector releases, a
// group context, a valid count covering the position, an accepted gate and a
// known hardware age.
func EvaluateBalance(s canbusreg.Selection, in canbusreg.LINKInput, count int, countValid bool, g canbusreg.MeasurementGate, hardwareAgeKnown bool, hardwareAge time.Duration) (Result, roweval.Reason, error) {
	res := Result{Evidence: in.Evidence, Attribution: observationAttribution(in)}
	if !admitted(s, in) || in.Evidence.Sequence == 0 || in.Evidence.Monotonic < 0 {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	if in.Evidence.Frame.ID().Value()>>16 != balanceFrame {
		res.Status = "unsupported"
		return res, roweval.ReasonNoMatch, nil
	}
	decoded := canbusreg.DecodeLINKBalance(s, in, count, countValid, g, hardwareAgeKnown, hardwareAge)
	values := Qualifiers(s, in).Values
	res.Grades = make(map[string]string)
	for name, f := range decoded.Fields {
		if strings.HasPrefix(name, balanceEnableName) {
			values[enablePrefix+strings.TrimPrefix(name, balanceEnableName)] = "true"
			res.Grades[rowPrefix+name] = f.Grade
		}
	}
	rows, err := evaluateStatusRows(balanceFrame, in, roweval.Qualifiers{Values: values})
	if err != nil {
		return res, "", err
	}
	res.Status, res.Rows = "accepted", rows
	return res, "", nil
}
