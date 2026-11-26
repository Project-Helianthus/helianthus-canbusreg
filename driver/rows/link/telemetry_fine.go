package link

import (
	"strings"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const (
	qGroupPresent = "group-present"
	qFineGroup    = "fine-group"
	qGateMaximum  = "gate-maximum"
	qGateMinimum  = "gate-minimum"
	qGateMean     = "gate-mean"
)

const (
	frameFineTemperatures = 0x1862
	frameLayouts          = 0x180b
)

var fineBanks = []string{"ordinary", "parallel"}

func fineRow(frame uint16, field string, slice signalSlice, extra map[string]string) rowSpec {
	return rowSpec{frame: frame, field: field, slice: slice, divisor: 1, extra: extra}
}

func fineScaled(frame uint16, field string, slice signalSlice, unit string, divisor int64, add semreg.Decimal, extra map[string]string, cond string) []rowSpec {
	accepted := fineRow(frame, field, slice, withCondition(extra, cond, "true"))
	accepted.unit, accepted.divisor, accepted.add = unit, divisor, add
	return []rowSpec{accepted, fineRow(frame, field, slice, withCondition(extra, cond, "false"))}
}

func withCondition(extra map[string]string, key, value string) map[string]string {
	m := make(map[string]string, len(extra)+1)
	for k, v := range extra {
		m[k] = v
	}
	m[key] = value
	return m
}

func layoutConditions(layout string) map[string]string {
	return map[string]string{qGroupPresent: "true", qLayout: layout}
}

// fineSpecs returns the rows of the 1862 fine temperature summary and of the
// 180B layouts, one variant of each row for each asserted bank.
func fineSpecs() []rowSpec {
	zero := semreg.Decimal{}
	celsiusBias := semreg.Decimal{Coefficient: "-4", Exponent10: 1}
	var s []rowSpec
	fine := map[string]string{qFineGroup: "true"}
	for _, t := range []struct {
		field, gate string
		at          uint16
	}{{"maximum", qGateMaximum, 0}, {"minimum", qGateMinimum, 2}, {"mean", qGateMean, 4}} {
		s = append(s, fineScaled(frameFineTemperatures, t.field, littleWord(t.at), "C", 8, celsiusBias, fine, t.gate)...)
	}
	s = append(s, fineRow(frameFineTemperatures, "reserved", littleWord(6), fine))

	c, w, b := layoutConditions("C"), layoutConditions("W"), layoutConditions("B")
	for at, name := range []string{"checkpoint_0", "checkpoint_1", "checkpoint_2"} {
		s = append(s, fineRow(frameLayouts, name, littleWord(uint16(at*2)), c))
	}
	s = append(s, fineScaled(frameLayouts, "full_capacity", littleWord(0), "Ah", 10, zero, w, qGroupTwo)...)
	s = append(s, fineScaled(frameLayouts, "fine_cycles", littleWord(2), "cycles", 10, zero, w, qGroupTwo)...)
	s = append(s, fineRow(frameLayouts, "algorithm_state", byteAt(4), w), fineRow(frameLayouts, "residual_budget", byteAt(5), w))
	s = append(s, fineRow(frameLayouts, "capacity_derived", byteAt(0), b), fineRow(frameLayouts, "algorithm", byteAt(1), b),
		fineRow(frameLayouts, "cycles", littleWord(2), b), fineRow(frameLayouts, "b4", byteAt(4), b), fineRow(frameLayouts, "b5", byteAt(5), b))
	s = append(s, fineRow(frameLayouts, "remaining_capacity", littleWord(6), c), fineRow(frameLayouts, "remaining_capacity", littleWord(6), b))
	s = append(s, fineScaled(frameLayouts, "remaining_capacity", littleWord(6), "Ah", 10, zero, w, qGroupTwo)...)

	var out []rowSpec
	for _, bank := range fineBanks {
		for _, r := range s {
			r.extra = withCondition(r.extra, qBank, bank)
			out = append(out, r)
		}
	}
	return out
}

// FineTable returns the native rows of the 1862 fine temperature summary and
// of the 180B layouts and the hooks those rows name.
func FineTable() roweval.Table[Selector] {
	specs := fineSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s)
		rows = append(rows, row)
		hooks[row.Decode.Hook] = rowHook(s)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// FineQualifiers extends the selection vocabulary with the conditions the fine
// rows test: a resolved contract group, the 1862 groups L4 and L8, and the
// verdict the temperature summary decoder gives each measurement word.
func FineQualifiers(s canbusreg.Selection, in canbusreg.LINKInput, gates canbusreg.SummaryGates) roweval.Qualifiers {
	q := Qualifiers(s, in)
	group := s.SelectionGroup()
	q.Values[qGroupPresent] = boolText(group != "")
	q.Values[qFineGroup] = boolText(group == "L4" || group == "L8")
	if uint16(in.Evidence.Frame.ID().Value()>>16) == frameFineTemperatures {
		decoded := canbusreg.DecodeLINKTemperatureSummary(s, in, gates)
		for name, key := range map[string]string{"maximum": qGateMaximum, "minimum": qGateMinimum, "mean": qGateMean} {
			q.Values[key] = boolText(decoded.Fields[name].Status == "accepted")
		}
	}
	return q
}

// EvaluateFine evaluates the native rows of one 1862 or 180B observation. It
// admits what the decoders admit; the gates qualify only the 1862 measurement
// words, and a word the summary decoder does not accept keeps the decoder's
// status as its disposition with the raw word.
func EvaluateFine(s canbusreg.Selection, in canbusreg.LINKInput, gates canbusreg.SummaryGates) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	if !admitted(s, in) {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	if family != frameFineTemperatures && family != frameLayouts {
		res.Status = "unsupported"
		return res, roweval.ReasonNoMatch, nil
	}
	var fields map[string]canbusreg.NativeField
	if family == frameFineTemperatures {
		decoded := canbusreg.DecodeLINKTemperatureSummary(s, in, gates)
		res.Status, fields = decoded.Status, decoded.Fields
	} else {
		decoded := canbusreg.DecodeLINKTelemetry(s, in)
		res.Status, fields = decoded.Status, decoded.Fields
	}
	if res.Status == "invalid" {
		return res, "invalid", nil
	}
	rows, err := evaluateRows(FineTable(), frame, FineQualifiers(s, in, gates))
	if err != nil {
		return res, "", err
	}
	res.Rows = rows
	res.Grades = make(map[string]string)
	for id, ev := range rows {
		f, ok := fields[id[strings.LastIndex(id, ".")+1:]]
		if !ok || ev.Reason == roweval.ReasonNoMatch {
			continue
		}
		res.Grades[id] = f.Grade
		if family == frameFineTemperatures && ev.Native != nil && f.Status != ev.Native.Disposition {
			ev.Native.Disposition = f.Status
		}
	}
	return res, "", nil
}
