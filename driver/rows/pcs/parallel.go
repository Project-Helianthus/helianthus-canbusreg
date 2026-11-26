package pcs

import (
	"fmt"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// The addressed parallel identity families of the PCS parallel profile, as
// ParallelIdentityDecodeParallel emits them: 0x500, 0x600 to 0x670 addressed by
// the group nibble of the identifier, and the coordinator summaries 0x690,
// 0x6A0, 0x6B0 and 0x6E0. Each field of the decoder is one native row, with the
// family in the row identifier. The decoder keeps admission, publisher
// resolution and the meaning of fault bits; the rows carry the decoder's field
// status as their disposition, as the diagnostic rows do.
//
// The families 0x510, 0x520, 0x590, 0x680, 0x6C0 and 0x6D0, and the
// endpoint-to-controller status frames 0x37F and 0x3F7, are admitted by the
// decoder but it emits no field for them: availability alone supplies no field
// meaning. They therefore have no row.

const (
	// qFamilyAdmitted is "true" when the selection's contract group admits the
	// family and the identifier is addressed to the selected group (addressed
	// families) or is the coordinator identifier itself under an aggregate scope.
	qFamilyAdmitted = "family-admitted"
	// qAggregateProduct65535 is "true" for the aggregate 0x600 product word 65535.
	qAggregateProduct65535 = "aggregate-product-65535"
	// qSummaryPrefix prefixes the qualifier that is "true" when the 0x6B0 byte
	// of that index is one the selection carries.
	qSummaryPrefix = "summary-field-"

	parallelPrefix = "growatt.bms.pcs.parallel."
)

// ParallelResult is the native evaluation of one parallel identity observation,
// keyed by row identifier.
type ParallelResult struct {
	Evidence    canbusreg.Evidence
	Attribution Attribution
	Rows        map[string]roweval.Evaluation
}

type parallelRow struct {
	family uint32
	diagSpec
}

func parallelCoordinator(family uint32) bool {
	switch family {
	case 0x690, 0x6a0, 0x6b0, 0x6e0:
		return true
	}
	return false
}

// parallelContractAdmits states the contract groups that admit a family.
func parallelContractAdmits(family uint32, group string) bool {
	later := group == "L5" || group == "L7" || group == "L8"
	switch family {
	case 0x500, 0x6e0:
		return later
	}
	return group == "L6" || later
}

func parallelRowOf(family uint32, key string, slice signalSlice, extra map[string]string, hook roweval.RowHook) parallelRow {
	id := fmt.Sprintf("%s%X.%s", parallelPrefix, family, key)
	when := map[string]string{qFlavor: "pcs-parallel", qBank: "parallel", qFamilyAdmitted: "true"}
	if parallelCoordinator(family) {
		when[qScope] = "aggregate"
	}
	for k, v := range extra {
		when[k] = v
	}
	if hook == nil {
		hook = diagSummaryHook(key)
	}
	row := roweval.NativeRow[Selector]{
		ID:      id,
		SpecRef: fmt.Sprintf("CAN %#x", family),
		At:      Selector{Identifier: family, DLC: 8, RawDLC: 8, Slice: slice},
		When:    roweval.When{Equal: when},
		Wire:    roweval.Layout{BitOffset: slice.offset, Width: slice.width, Codec: "unsigned", ByteOrder: slice.order, BitOrder: "lsb0", WordOrder: "single"},
		Decode:  roweval.Convert{Hook: diagHookPrefix + id},
	}
	return parallelRow{family: family, diagSpec: diagSpec{id: id, key: key, row: row, hook: hook}}
}

// parallelAssignedWithheld withholds the raw product word 65535 an aggregate
// selection reports for an unassigned member.
func parallelAssignedWithheld(roweval.DetachedInputs, roweval.RowID, roweval.Direction, roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
}

func parallelSpecs() []parallelRow {
	var rows []parallelRow
	add := func(family uint32, key string, slice signalSlice) {
		rows = append(rows, parallelRowOf(family, key, slice, nil, nil))
	}
	bytes := func(family uint32, from int, names func(int) string, to int) {
		for at := from; at < to; at++ {
			add(family, names(at), byteAt(uint16(8*at)))
		}
	}
	words := func(family uint32, names func(int) string) {
		for at := 0; at < 8; at += 2 {
			add(family, names(at), littleWord(uint16(8*at)))
		}
	}
	for _, f := range []struct {
		family uint32
		offset int
	}{{0x500, 16}, {0x660, 0}, {0x670, 8}} {
		for at := 0; at < 8; at++ {
			add(f.family, fmt.Sprintf("fault_%d", f.offset+at), byteAt(uint16(8*at)))
			for bit := 0; bit < 8; bit++ {
				add(f.family, fmt.Sprintf("current_fault_%d", 8*(f.offset+at)+bit), bitsAt(uint16(8*at+bit), 1))
			}
		}
	}
	rows = append(rows,
		parallelRowOf(0x600, "product", littleWord(0), map[string]string{qAggregateProduct65535: "false"}, nil),
		parallelRowOf(0x600, "product", littleWord(0), map[string]string{qAggregateProduct65535: "true"}, parallelAssignedWithheld),
	)
	rows[len(rows)-1].id = parallelPrefix + "600.aggregate.product"
	rows[len(rows)-1].row.ID = rows[len(rows)-1].id
	rows[len(rows)-1].row.Decode.Hook = diagHookPrefix + rows[len(rows)-1].id
	add(0x600, "hardware", byteAt(16))
	add(0x600, "version_mux", littleWord(24))
	bytes(0x600, 5, func(at int) string { return fmt.Sprintf("count_config_%d", at) }, 8)

	for at, name := range []string{"voltage_mux", "current", "port", "soc"} {
		add(0x610, name, littleWord(uint16(16*at)))
	}
	add(0x620, "limit_0", littleWord(0))
	add(0x620, "limit_2", littleWord(16))
	for at, name := range []string{"permission_4", "permission_5", "state", "flags"} {
		add(0x620, name, byteAt(uint16(8*(4+at))))
	}
	words(0x630, func(at int) string { return fmt.Sprintf("quantity_%d", at) })

	add(0x640, "maximum_index", byteAt(0))
	add(0x640, "zero_1", byteAt(8))
	add(0x640, "maximum_word", littleWord(16))
	add(0x640, "minimum_index", byteAt(32))
	add(0x640, "zero_5", byteAt(40))
	add(0x640, "minimum_word", littleWord(48))

	for at, name := range []string{"maximum_index", "zero_1", "maximum_code", "minimum_index", "zero_4", "minimum_code", "extra", "soh"} {
		add(0x650, name, byteAt(uint16(8*at)))
	}

	bytes(0x690, 0, func(at int) string { return fmt.Sprintf("summary_%d", at) }, 8)
	words(0x6a0, func(at int) string { return fmt.Sprintf("word_%d", at) })
	for at := 0; at < 5; at++ {
		rows = append(rows, parallelRowOf(0x6b0, fmt.Sprintf("summary_%d", at), byteAt(uint16(8*at)), map[string]string{qSummaryPrefix + strconv.Itoa(at): "true"}, nil))
	}
	add(0x6e0, "word_0", littleWord(0))
	add(0x6e0, "word_2", littleWord(16))
	bytes(0x6e0, 4, func(at int) string { return fmt.Sprintf("state_%d", at) }, 8)
	return rows
}

// ParallelIdentityTable returns the native rows of the parallel identity
// families and the hooks they name. The table has no bindings.
func ParallelIdentityTable() roweval.Table[Selector] {
	specs := parallelSpecs()
	t := roweval.Table[Selector]{Hooks: make(map[string]roweval.RowHook, len(specs))}
	for _, s := range specs {
		t.Native = append(t.Native, s.row)
		t.Hooks[s.row.Decode.Hook] = s.hook
	}
	return t
}

// parallelSummaryCount is the number of 0x6B0 bytes the decoder emits.
func parallelSummaryCount(s canbusreg.Selection) int {
	c := s.SelectionSpecValue()
	count := 1
	if c.ProjectCode+c.Release == "QAAB0004" {
		count = 4
	}
	if c.ProjectCode+c.Release == "QAAb5004" || s.SelectionGroup() == "L7" || s.SelectionGroup() == "L8" {
		count = 5
	}
	return count
}

func parallelQualifiers(s canbusreg.Selection, in canbusreg.LINKInput, dec canbusreg.NativeResult, family uint32) roweval.Qualifiers {
	c := s.SelectionSpecValue()
	frame := in.Evidence.Frame
	group, _ := strconv.ParseUint(c.Group, 10, 8)
	addressed := frame.ID().Value()&15 == uint32(group)
	if parallelCoordinator(family) {
		addressed = c.Scope == "aggregate" && frame.ID().Value() == family
	}
	data := frame.Data()
	v := map[string]string{
		qFlavor:         c.Flavor,
		qBank:           c.Bank,
		qScope:          c.Scope,
		qFamilyAdmitted: boolText(parallelContractAdmits(family, s.SelectionGroup()) && addressed),
	}
	v[qAggregateProduct65535] = boolText(family == 0x600 && c.Scope == "aggregate" && len(data) >= 2 && data[0] == 0xff && data[1] == 0xff)
	for k := 0; k < parallelSummaryCount(s); k++ {
		v[qSummaryPrefix+strconv.Itoa(k)] = "true"
	}
	for key, f := range dec.Fields {
		v[qDiagStatus+key] = f.Status
	}
	return roweval.Qualifiers{Values: v}
}

// EvaluateParallelIdentity evaluates the native rows of one parallel identity
// observation under the selection and input it was received with. An
// observation the decoder refuses, or for which it emits no field (the frames
// the decoder admits without field meaning, and 0x37F and 0x3F7), gives the
// decoder's status and no rows.
func EvaluateParallelIdentity(s canbusreg.Selection, in canbusreg.LINKInput) (ParallelResult, roweval.Reason, error) {
	res := ParallelResult{Evidence: in.Evidence, Attribution: diagAttribution(in), Rows: make(map[string]roweval.Evaluation)}
	dec := canbusreg.ParallelIdentityDecodeParallel(s, in)
	if len(dec.Fields) == 0 {
		return res, roweval.Reason(dec.Status), nil
	}
	frame := in.Evidence.Frame
	family := frame.ID().Value() &^ 15
	q := parallelQualifiers(s, in, dec, family)
	for _, spec := range parallelSpecs() {
		if spec.family != family {
			continue
		}
		ev, err := diagEvaluate(spec.diagSpec, frame, q)
		if err != nil {
			return res, "", err
		}
		if ev.Native != nil || ev.Reason != roweval.ReasonNoMatch {
			res.Rows[spec.id] = ev
		}
	}
	return res, "", nil
}
