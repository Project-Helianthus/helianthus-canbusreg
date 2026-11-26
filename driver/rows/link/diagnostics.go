package link

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const (
	qDictionary  = "dictionary"
	qBlock1850   = "block-1850"
	qBlock1851   = "block-1851"
	qBlock1852   = "block-1852"
	qStateGroup  = "state-group"
	qRelease0011 = "release-0011"

	dictionaryNone   = "none"
	dictionaryLegacy = "legacy"
	dictionaryModern = "modern"

	dispositionEmpty   = "empty"
	dispositionIgnored = "ignored"
	dispositionUnknown = "unknown"

	gradeProven     = "Proven"
	gradeHypothesis = "Hypothesis"
	gradeUnknown    = "Unknown"

	frameDiagnostic0 = 0x1850
	frameSelected    = 0x1853
	frameState       = 0x1861
	frameMonitor     = 0x1869

	legacyCount = 134
	modernCount = 136
)

var namedConditions = []int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 15, 16, 17, 20, 21, 22, 23, 30, 33, 34, 38, 39, 40, 41, 42, 44, 45,
	52, 57, 58, 60, 61, 62, 129, 130,
}

var namedModernConditions = []int{31, 35, 91, 93, 102, 103, 128, 131, 133, 134, 135}

var modernSpecialWords = [...]uint16{1185, 1151, 1161, 1181, 1188}

func conditionNamed(index int, dictionary string) bool {
	count := 0
	switch dictionary {
	case dictionaryLegacy:
		count = legacyCount
	case dictionaryModern:
		count = modernCount
	}
	if index >= count {
		return false
	}
	for _, n := range namedConditions {
		if n == index {
			return true
		}
	}
	if dictionary == dictionaryModern {
		for _, n := range namedModernConditions {
			if n == index {
				return true
			}
		}
	}
	return false
}

type diagSpec struct {
	row         rowSpec
	id          string
	grade       string
	disposition func(raw uint64) string
	index       bool
}

func always(d string) func(uint64) string { return func(uint64) string { return d } }

func zeroOnly(raw uint64) string {
	if raw != 0 {
		return dispositionInvalid
	}
	return dispositionAccepted
}

func wordState(raw uint64) string {
	if raw == 0 {
		return dispositionEmpty
	}
	return dispositionAccepted
}

func diagBase(frame uint16, field string, slice signalSlice, extra map[string]string) rowSpec {
	return rowSpec{frame: frame, field: field, slice: slice, divisor: 1, extra: extra}
}

func with(m map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(m)+len(kv)/2)
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

var (
	moduleRoute      = map[string]string{}
	monitorRoute     = map[string]string{qDestination: "F5"}
	coordinatorRoute = map[string]string{qDirection: "coordinator-to-module", qScope: "aggregate", qSource: "F4", qDestination: "DF"}
)

func faultSpecs() []diagSpec {
	var out []diagSpec
	for index := 0; index < 192; index++ {
		frame := uint16(frameDiagnostic0 + index/64)
		gate := with(moduleRoute, "block-"+strconv.FormatUint(uint64(frame), 16), "true")
		slice := bitsAt(uint16(index%64), 1)
		id := fmt.Sprintf("%scurrent_fault_%d", rowPrefix, index)
		field := fmt.Sprintf("current_fault_%d", index)
		dictionaries := []string{dictionaryNone, dictionaryLegacy, dictionaryModern}
		same := true
		for _, d := range dictionaries[1:] {
			if conditionNamed(index, d) != conditionNamed(index, dictionaries[0]) {
				same = false
			}
		}
		if same {
			out = append(out, diagSpec{row: diagBase(frame, field, slice, gate), id: id, grade: gradeUnknown, disposition: always(dispositionRaw)})
			continue
		}
		for _, d := range dictionaries {
			s := diagSpec{row: diagBase(frame, field, slice, with(gate, qDictionary, d)), id: id, grade: gradeUnknown, disposition: always(dispositionRaw)}
			if conditionNamed(index, d) {
				s.grade, s.disposition = gradeHypothesis, always(dispositionAccepted)
			}
			out = append(out, s)
		}
	}
	return out
}

func selectedSpecs() []diagSpec {
	gate := with(moduleRoute, qGroupEight, "true")
	var out []diagSpec
	for j, name := range []string{"selected_a", "selected_b"} {
		out = append(out, diagSpec{row: diagBase(frameSelected, name, littleWord(uint16(j*2)), gate), id: rowPrefix + name, grade: gradeProven, disposition: wordState})
	}
	for at := uint16(4); at < 8; at++ {
		name := fmt.Sprintf("reserved_%d", at)
		out = append(out, diagSpec{row: diagBase(frameSelected, name, byteAt(at), gate), id: fmt.Sprintf("%s%04X.%s", rowPrefix, frameSelected, name), grade: gradeProven, disposition: zeroOnly})
	}
	for j, name := range []string{"selected_a_index", "selected_b_index"} {
		for _, d := range []string{dictionaryLegacy, dictionaryModern} {
			out = append(out, diagSpec{row: diagBase(frameSelected, name, littleWord(uint16(j*2)), with(moduleRoute, qDictionary, d)), id: rowPrefix + name, grade: gradeProven, index: true})
		}
	}
	return out
}

func stateSpecs() []diagSpec {
	var out []diagSpec
	gate := with(moduleRoute, qStateGroup, "true")
	for at := uint16(0); at < 3; at++ {
		name := fmt.Sprintf("state_%d", at)
		out = append(out, diagSpec{row: diagBase(frameState, name, byteAt(at), gate), id: fmt.Sprintf("%s%04X.%s", rowPrefix, frameState, name), grade: gradeUnknown, disposition: always(dispositionRaw)})
	}
	out = append(out, diagSpec{row: diagBase(frameState, "counter", byteAt(3), gate), id: fmt.Sprintf("%s%04X.counter", rowPrefix, frameState), grade: gradeProven, disposition: always(dispositionAccepted)})
	for at := uint16(4); at < 7; at++ {
		name := fmt.Sprintf("reserved_%d", at)
		out = append(out, diagSpec{row: diagBase(frameState, name, byteAt(at), gate), id: fmt.Sprintf("%s%04X.%s", rowPrefix, frameState, name), grade: gradeProven, disposition: zeroOnly})
	}
	out = append(out, additionalState(frameState, "additional_state", fmt.Sprintf("%s%04X.additional_state", rowPrefix, frameState), gate)...)
	return out
}

func additionalState(frame uint16, field, id string, gate map[string]string) []diagSpec {
	return []diagSpec{
		{row: diagBase(frame, field, byteAt(7), with(gate, qRelease0011, "false")), id: id, grade: gradeUnknown, disposition: always(dispositionRaw)},
		{row: diagBase(frame, field, byteAt(7), with(gate, qRelease0011, "true")), id: id, grade: gradeUnknown, disposition: always(dispositionIgnored)},
	}
}

func monitorSpecs() []diagSpec {
	var out []diagSpec
	for _, route := range []struct {
		prefix string
		extra  map[string]string
	}{{"", monitorRoute}, {"coordinator_", coordinatorRoute}} {
		gate := with(route.extra, qStateGroup, "true")
		qualify := func(name string) string {
			if route.prefix == "" && !strings.HasPrefix(name, "word_") {
				return fmt.Sprintf("%s%04X.%s", rowPrefix, frameMonitor, name)
			}
			return rowPrefix + route.prefix + name
		}
		for at := uint16(0); at < 3; at++ {
			name := fmt.Sprintf("%sstate_%d", route.prefix, at)
			out = append(out, diagSpec{row: diagBase(frameMonitor, name, byteAt(at), gate), id: qualify(fmt.Sprintf("state_%d", at)), grade: gradeUnknown, disposition: always(dispositionRaw)})
		}
		for _, at := range []uint16{3, 5} {
			name := fmt.Sprintf("%sword_%d", route.prefix, at)
			out = append(out, diagSpec{row: diagBase(frameMonitor, name, littleWord(at), gate), id: qualify(fmt.Sprintf("word_%d", at)), grade: gradeUnknown, disposition: always(dispositionRaw)})
		}
		out = append(out, additionalState(frameMonitor, route.prefix+"additional_state", qualify("additional_state"), gate)...)
	}
	return out
}

func diagnosticSpecs() []diagSpec {
	var s []diagSpec
	s = append(s, faultSpecs()...)
	s = append(s, selectedSpecs()...)
	s = append(s, stateSpecs()...)
	s = append(s, monitorSpecs()...)
	return s
}

func diagnosticIndex(word uint64, dictionary string) (uint64, bool) {
	switch dictionary {
	case dictionaryLegacy:
		n := uint64(0)
		for shift, scale := uint(0), uint64(1); shift < 16; shift, scale = shift+4, scale*10 {
			digit := word >> shift & 15
			if digit > 9 {
				return 0, false
			}
			n += digit * scale
		}
		if n < 1000 || n-1000 >= legacyCount {
			return 0, false
		}
		return n - 1000, true
	case dictionaryModern:
		for i, w := range modernSpecialWords {
			if word == uint64(w) {
				return uint64(131 + i), true
			}
		}
		if word >= 1000 && word <= 1130 {
			return word - 1000, true
		}
	}
	return 0, false
}

func diagnosticHook(index bool) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		if !index || v.Quantity == nil {
			return roweval.DerivedValue{Value: v}, ""
		}
		word, err := strconv.ParseUint(v.Quantity.Number.Coefficient, 10, 64)
		if err != nil {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		for e := v.Quantity.Number.Exponent10; e > 0; e-- {
			word *= 10
		}
		n, ok := diagnosticIndex(word, in.Qualifiers().Values[qDictionary])
		if !ok {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		out := v
		q := *v.Quantity
		q.Number = semreg.Decimal{Coefficient: strconv.FormatUint(n, 10)}
		out.Quantity = &q
		return roweval.DerivedValue{Value: out}, ""
	}
}

// DiagnosticsTable returns the native rows of the fault bitmaps, the selected
// diagnostic words and their numeric indices, and the state frames, and the
// hooks those rows name.
func DiagnosticsTable() roweval.Table[Selector] {
	specs := diagnosticSpecs()
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s.row)
		row.ID = s.id
		row.Decode.Hook = rowHookPrefix + s.id
		rows = append(rows, row)
		hooks[row.Decode.Hook] = diagnosticHook(s.index)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

func memberGroup(s canbusreg.Selection) string {
	c := s.SelectionSpecValue()
	if c.Direction != "coordinator-to-module" {
		return s.SelectionGroup()
	}
	c.Direction = "module-to-coordinator"
	member, err := canbusreg.NewSelection(c)
	if err != nil {
		return ""
	}
	return member.SelectionGroup()
}

// DiagnosticQualifiers returns the selection vocabulary of the diagnostic
// rows: the LINK qualifiers with the contract group of the reporting member,
// the fault blocks the group carries, the numeric dictionary of the selection
// and the release whose input ignores the additional state byte.
func DiagnosticQualifiers(s canbusreg.Selection, in canbusreg.LINKInput) roweval.Qualifiers {
	q := Qualifiers(s, in)
	c := s.SelectionSpecValue()
	group := memberGroup(s)
	q.Values[qContractGroup] = group
	q.Values[qGroupEight] = boolText(group == "L8")
	q.Values[qStateGroup] = boolText(group == "L7" || group == "L8")
	q.Values[qBlock1850] = boolText(group != "")
	q.Values[qBlock1851] = boolText(group != "" && group != "L0")
	q.Values[qBlock1852] = boolText(group == "L3" || group == "L4" || group == "L5" || group == "L7" || group == "L8")
	q.Values[qRelease0011] = boolText(c.ProjectCode == "QAAA" && c.Release == "0011")
	dictionary := dictionaryNone
	switch {
	case group != "L8":
	case c.ProjectCode == "QAAA" && c.Release == "0011":
		dictionary = dictionaryLegacy
	case c.ProjectCode == "QAAa" && c.Release == "9011" && c.Choice == "release11":
		dictionary = dictionaryModern
	}
	q.Values[qDictionary] = dictionary
	return q
}

func diagnosticFrame(s canbusreg.Selection, in canbusreg.LINKInput) bool {
	c := s.SelectionSpecValue()
	f := in.Evidence.Frame
	id := f.ID().Value()
	if c.Flavor != "link" || !c.RouteSet || !f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 || id>>24 != 0x18 ||
		uint8(id>>8) != c.Destination || uint8(id) != c.Source || c.Endpoint != fmt.Sprintf("%02X", uint8(id)) ||
		in.Evidence.Interface != c.Interface || in.Epoch != c.Epoch || in.Direction != c.Direction || in.Scope != c.Scope ||
		in.Bank != c.Bank || in.Endpoint != c.Endpoint || in.Group != c.Group || in.Slot != c.Slot || in.FieldAge < 0 ||
		in.Evidence.Sequence == 0 || in.Evidence.Monotonic < 0 {
		return false
	}
	module := c.Direction == "module-to-coordinator" && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local" && (c.Destination == 0xf4 || c.Destination == 0xf5)
	coordinator := c.Direction == "coordinator-to-module" && c.Scope == "aggregate" && c.Source == 0xf4 && c.Destination == 0xdf
	return module || coordinator
}

// EvaluateDiagnostics evaluates the diagnostic rows of one observation. It
// admits only what the diagnostics decoder admits: an observation outside the
// route, the frame geometry or the selection context gives the reason invalid
// and no rows. An admitted frame outside the families 1850 to 1853, 1861 and
// 1869, or to which no row applies under the selection, has the status
// unsupported.
func EvaluateDiagnostics(s canbusreg.Selection, in canbusreg.LINKInput) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	if !diagnosticFrame(s, in) {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	res.Status = "unsupported"
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	q := DiagnosticQualifiers(s, in)
	table := DiagnosticsTable()
	specs := diagnosticSpecs()
	res.Rows = make(map[string]roweval.Evaluation)
	res.Grades = make(map[string]string)
	hooks := table.Hooks
	for i, row := range table.Native {
		spec := specs[i]
		if row.At.Family != family {
			continue
		}
		a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
		ev, err := roweval.Evaluate(roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: hooks}, a)
		if err != nil {
			return res, "", err
		}
		if ev.Native == nil {
			if _, seen := res.Rows[spec.id]; !seen {
				res.Rows[spec.id] = roweval.Evaluation{Reason: roweval.ReasonNoMatch}
			}
			continue
		}
		if prior, seen := res.Rows[spec.id]; seen && prior.Native != nil {
			return res, "", fmt.Errorf("row %s applies through more than one variant", spec.id)
		}
		switch {
		case spec.index && ev.Native.Reason != "":
			ev.Native.Value, ev.Native.Disposition = nil, dispositionUnknown
			if ev.Native.Raw == 0 {
				ev.Native.Disposition = dispositionEmpty
			}
		case ev.Native.Reason != "":
			ev.Native.Value, ev.Native.Disposition = nil, dispositionInvalid
		case spec.index:
			ev.Native.Disposition = dispositionAccepted
		default:
			ev.Native.Disposition = spec.disposition(ev.Native.Raw)
		}
		res.Rows[spec.id] = ev
		res.Grades[spec.id] = spec.grade
		res.Status = "accepted"
	}
	return res, "", nil
}
