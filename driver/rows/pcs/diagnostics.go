package pcs

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Diagnostic rows cover the summary bits of frames 0x312 and 0x323 and the
// selected diagnostic code words of the 0x333 and 0x52X frames and of the
// LINK-scoped frame. Each row reads one native value from the payload; the
// decoder decides admission, meaning and disposition, and its field status is
// carried to the rows as a qualifier. Alternatives and conditional alternatives
// are decoder output and have no row: no all-clear is derived here.

const (
	// qDiagStatus prefixes the decoder field status of one key, such as "A/12".
	qDiagStatus = "diag-status:"
	// qDTCDictionary is present when the selection carries a numeric dictionary.
	qDTCDictionary = "dtc-dictionary"
	// diagHookPrefix prefixes the hook name of a diagnostic row.
	diagHookPrefix = "diag:"

	dtcSelectedWord = "selected_word"
	dtcNumericIndex = "numeric_index"
)

// DiagnosticResult is the native evaluation of one diagnostic observation,
// keyed by row identifier. Rows that do not apply to the selection carry
// roweval.ReasonNoMatch.
type DiagnosticResult struct {
	Evidence canbusreg.Evidence
	// Attribution is the bank, group and slot the observation was decoded under.
	Attribution Attribution
	// Diagnostics retains the native record the decoder was given, even when the
	// decoder refuses the observation. It is raw evidence, not a condition.
	Diagnostics []byte
	Rows        map[string]roweval.Evaluation
}

// attributionOf reads the selection context the observation was decoded under;
// an empty group or slot is absent, never zero.
func diagAttribution(i canbusreg.LINKInput) Attribution {
	return Attribution{Scope: i.Scope, Bank: i.Bank, Group: i.Group, Slot: i.Slot, HasGroup: i.Group != "", HasSlot: i.Slot != ""}
}

// diagSpec is one native row with the key the decoder emits it under and the
// hook the row uses, when it has one.
type diagSpec struct {
	id, key string
	row     roweval.NativeRow[Selector]
	hook    roweval.RowHook
}

// diagFlavor is a PCS flavor with the bank it pairs with.
type diagFlavor struct {
	token, flavor, bank string
}

var diagSummaryFlavors = []diagFlavor{
	{token: "standard", flavor: "pcs-standard", bank: "ordinary"},
	{token: "parallel", flavor: "pcs-parallel", bank: "parallel"},
}

// diagSummaryBit is one key of a summary frame and its bit offset in the payload.
type diagSummaryBit struct {
	key    string
	offset uint16
}

// diagSummaryBits lists the keys DecodePCSSummaries emits for a summary frame.
// The 0x312 words A and B are little-endian, so value bit b is payload bit b for
// A and payload bit 16+b for B. The 0x323 keys are the bytes B4 to B7.
func diagSummaryBits(frame uint32) []diagSummaryBit {
	var bits []diagSummaryBit
	switch frame {
	case 0x312:
		for b := uint16(0); b < 16; b++ {
			bits = append(bits, diagSummaryBit{fmt.Sprintf("A/%d", b), b}, diagSummaryBit{fmt.Sprintf("B/%d", b), 16 + b})
		}
	case 0x323:
		for i, name := range []string{"B4", "B5", "B6", "B7"} {
			for b := uint16(0); b < 8; b++ {
				bits = append(bits, diagSummaryBit{fmt.Sprintf("%s/%d", name, b), uint16(32+8*i) + b})
			}
		}
	}
	return bits
}

// diagSummaryFrame admits the standard summary frames with DLC 8 and raw DLC 8.
func diagSummaryFrame(f canbus.Frame) bool {
	id := f.ID().Value()
	return !f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 && (id == 0x312 || id == 0x323)
}

// diagSummaryHook withholds a summary bit whose decoder status is not accepted
// or raw, and keeps the bit value otherwise.
func diagSummaryHook(key string) roweval.RowHook {
	return func(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		state, ok := in.Qualifiers().Values[qDiagStatus+key]
		if !ok {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		if state == "accepted" || state == "raw" {
			return roweval.DerivedValue{Value: v}, ""
		}
		return roweval.DerivedValue{}, dispositionReason(state)
	}
}

// diagSummarySpecs returns the native rows of one summary frame for both PCS
// flavors. Each row names the flavor and the bank that pairs with it.
func diagSummarySpecs(frame uint32) []diagSpec {
	var specs []diagSpec
	for _, fl := range diagSummaryFlavors {
		for _, bit := range diagSummaryBits(frame) {
			id := fmt.Sprintf("growatt.bms.pcs.%s.%X.%s", fl.token, frame, bit.key)
			row := roweval.NativeRow[Selector]{
				ID:      id,
				SpecRef: fmt.Sprintf("CAN %#x", frame),
				At:      Selector{Identifier: frame, DLC: 8, RawDLC: 8, Slice: signalSlice{offset: bit.offset, width: 1, order: bitSlice}},
				When:    roweval.When{Equal: map[string]string{qFlavor: fl.flavor, qBank: fl.bank}},
				Wire:    roweval.Layout{BitOffset: bit.offset, Width: 1, Codec: "unsigned", ByteOrder: bitSlice, BitOrder: "lsb0", WordOrder: "single"},
				Decode:  roweval.Convert{Hook: diagHookPrefix + id},
			}
			specs = append(specs, diagSpec{id: id, key: bit.key, row: row, hook: diagSummaryHook(bit.key)})
		}
	}
	return specs
}

// EvaluateSummaryBits evaluates the summary bit rows of one observation under the
// selection and decoder input it was received with. A frame or input the decoder
// refuses gives the decoder's status and no rows.
func EvaluateSummaryBits(s canbusreg.Selection, in canbusreg.PCSSummaryPCSInput) (DiagnosticResult, roweval.Reason, error) {
	res := DiagnosticResult{Evidence: in.Evidence, Attribution: diagAttribution(in.LINKInput), Rows: make(map[string]roweval.Evaluation)}
	frame := in.Evidence.Frame
	decoded := canbusreg.DecodePCSSummaries(s, in)
	res.Diagnostics = decoded.Diagnostics
	if len(decoded.Bits) == 0 {
		return res, roweval.Reason(decoded.Status), nil
	}
	if !diagSummaryFrame(frame) {
		return res, roweval.ReasonNoMatch, nil
	}
	c := s.SelectionSpecValue()
	q := roweval.Qualifiers{Values: map[string]string{qFlavor: c.Flavor, qBank: c.Bank}}
	for key, b := range decoded.Bits {
		q.Values[qDiagStatus+key] = b.Field.Status
	}
	for _, spec := range diagSummarySpecs(frame.ID().Value()) {
		ev, err := diagEvaluate(spec, frame, q)
		if err != nil {
			return res, "", err
		}
		res.Rows[spec.id] = ev
	}
	return res, "", nil
}

// diagEvaluate evaluates one row under the qualifiers. The row is evaluated at
// the frame's own selector, so only the row's conditions decide whether it
// applies. A matched value keeps the decoder status as its disposition.
func diagEvaluate(spec diagSpec, frame canbus.Frame, q roweval.Qualifiers) (roweval.Evaluation, error) {
	row := spec.row
	row.At = frameSelector(frame, row.At.Slice)
	hooks := map[string]roweval.RowHook{}
	if spec.hook != nil {
		hooks[row.Decode.Hook] = spec.hook
	}
	t := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: hooks}
	ev, err := roweval.Evaluate(t, roweval.Acquired[Selector]{Selector: row.At, Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()})
	if err != nil {
		return ev, err
	}
	if ev.Native != nil {
		if ev.Native.Reason != "" {
			ev.Native.Value = nil
			ev.Native.Unit = ""
		} else {
			ev.Native.Disposition = q.Values[qDiagStatus+spec.key]
		}
	}
	return ev, nil
}

// diagDTCFamily is the frame family a diagnostic code word is read from.
type diagDTCFamily struct {
	token, path, flavor, bank string
	order                     string
	// specRef is the family the frame is admitted from, as the row declares it.
	specRef string
	// offsets are the caller word offsets at which the frame family carries a
	// selected code word, as the frame decoders locate them. Other offsets are
	// not word positions of the family and have no rows.
	offsets []int
}

// diagWordOffsetOf reports whether a caller offset is an established code word
// position of the family.
func diagWordOffsetOf(fam diagDTCFamily, offset int) bool {
	for _, o := range fam.offsets {
		if o == offset {
			return true
		}
	}
	return false
}

// diagParallelAttributed admits a 0x52X word only for the group nibble and the
// slot byte the selection names, as the parallel frame decoder does. Another
// member's code is not attributed to the selected member.
func diagParallelAttributed(member canbusreg.Selection, frame canbus.Frame) bool {
	c := member.SelectionSpecValue()
	g, e := strconv.ParseUint(c.Group, 10, 8)
	slot, es := strconv.ParseUint(c.Slot, 10, 8)
	if e != nil || es != nil || g > 7 || c.Group != strconv.FormatUint(g, 10) || c.Slot != strconv.FormatUint(slot, 10) {
		return false
	}
	data := frame.Data()
	return len(data) > 0 && frame.ID().Value()&0xf == uint32(g) && data[0] == byte(slot)
}

// diagStandardAttributed admits a 0x333 word only for the group and slot bytes
// the selection names, as the standard frame decoder does. A selection that
// names neither has no member to attribute the word to, and the frame decoder
// requires a validated group and slot, so such a word is refused: it is raw
// evidence only and no index is published under an empty attribution. A frame
// of another member keeps no attributed index.
func diagStandardAttributed(member canbusreg.Selection, frame canbus.Frame) bool {
	c := member.SelectionSpecValue()
	if c.Group == "" || c.Slot == "" {
		return false
	}
	g, e := strconv.ParseUint(c.Group, 10, 8)
	slot, es := strconv.ParseUint(c.Slot, 10, 8)
	if e != nil || es != nil || c.Group != strconv.FormatUint(g, 10) || c.Slot != strconv.FormatUint(slot, 10) {
		return false
	}
	data := frame.Data()
	return len(data) > 1 && data[0] == byte(g) && data[1] == byte(slot)
}

// diagDTCFamilyOf admits the 0x333 frame, the 0x520 to 0x52F frames and the
// extended LINK-scoped 1853 frames with DLC 8 and raw DLC 8. The bank is empty
// for LINK, which pairs with no PCS bank.
func diagDTCFamilyOf(f canbus.Frame) (diagDTCFamily, bool) {
	if f.DLC() != 8 || f.RawDLC() != 8 {
		return diagDTCFamily{}, false
	}
	id := f.ID().Value()
	if !f.ID().Extended() {
		switch {
		case id == 0x333:
			return diagDTCFamily{token: "333", path: "pcs.standard", flavor: "pcs-standard", bank: "ordinary", order: bigEndian, specRef: "CAN 0x333", offsets: []int{2, 4}}, true
		case id >= 0x520 && id <= 0x52F:
			return diagDTCFamily{token: "52X", path: "pcs.parallel", flavor: "pcs-parallel", bank: "parallel", order: bigEndian, specRef: "CAN 0x520-0x52F", offsets: []int{1, 3}}, true
		}
		return diagDTCFamily{}, false
	}
	if id>>16 == 0x1853 {
		return diagDTCFamily{token: "1853", path: "link", flavor: "link", order: littleEndian, specRef: "CAN 0x1853xxxx extended", offsets: []int{0, 2}}, true
	}
	return diagDTCFamily{}, false
}

// diagIndexValue is the numeric index as a unitless quantity. The decimal is
// normalised as the semreg decimal rules require: a coefficient ends in no zero
// digit, so trailing zeros move into the exponent, as in 10 = 1e1.
func diagIndexValue(index int) semreg.Value {
	coefficient, exponent := strconv.Itoa(index), int32(0)
	for index != 0 && strings.HasSuffix(coefficient, "0") {
		coefficient = strings.TrimSuffix(coefficient, "0")
		exponent++
	}
	return semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: semreg.Decimal{Coefficient: coefficient, Exponent10: exponent}}}
}

// diagWord reads the unsigned integer of a quantity; a fractional or negative
// quantity has no word.
func diagWord(v semreg.Value) (uint64, bool) {
	if v.Kind != semreg.ValueQuantity || v.Quantity == nil || v.Quantity.Number.Exponent10 < 0 {
		return 0, false
	}
	n, ok := new(big.Int).SetString(v.Quantity.Number.Coefficient, 10)
	if !ok {
		return 0, false
	}
	n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(v.Quantity.Number.Exponent10)), nil))
	if !n.IsUint64() {
		return 0, false
	}
	return n.Uint64(), true
}

// diagIndexHook maps the selected word to its numeric index under the
// dictionary of the member, by the same encoding EncodeDTC uses. A word that
// matches no index has no index row value.
func diagIndexHook(member canbusreg.Selection) roweval.RowHook {
	return func(_ roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
		word, ok := diagWord(v)
		if !ok {
			return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
		}
		for index := 0; ; index++ {
			expected, ok := canbusreg.EncodeDTC(member, index)
			if !ok {
				break
			}
			if uint64(expected) == word {
				return roweval.DerivedValue{Value: diagIndexValue(index)}, ""
			}
		}
		return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
	}
}

// diagDTCSpecs returns the selected_word and numeric_index rows of one family at
// one caller-located word offset. The offset is part of the row identifier,
// because the decoder takes the offset from its caller. The row declares the
// identifier and format of the admitted frame, not a fixed one.
func diagDTCSpecs(fam diagDTCFamily, member canbusreg.Selection, offset int, frame canbus.Frame) []diagSpec {
	var specs []diagSpec
	for _, key := range []string{dtcSelectedWord, dtcNumericIndex} {
		id := fmt.Sprintf("growatt.bms.%s.%s.offset%d.%s", fam.path, fam.token, offset, key)
		when := map[string]string{qFlavor: fam.flavor, qDTCDictionary: "present"}
		if fam.bank != "" {
			when[qBank] = fam.bank
		}
		row := roweval.NativeRow[Selector]{
			ID:      id,
			SpecRef: fam.specRef,
			At:      Selector{Identifier: frame.ID().Value(), Extended: frame.ID().Extended(), DLC: 8, RawDLC: 8, Slice: signalSlice{offset: uint16(8 * offset), width: 16, order: fam.order}},
			When:    roweval.When{Equal: when},
			Wire:    roweval.Layout{BitOffset: uint16(8 * offset), Width: 16, Codec: "unsigned", ByteOrder: fam.order, BitOrder: "lsb0", WordOrder: "single"},
		}
		spec := diagSpec{id: id, key: key}
		if key == dtcNumericIndex {
			row.Decode = roweval.Convert{Hook: diagHookPrefix + id}
			spec.hook = diagIndexHook(member)
		}
		spec.row = row
		specs = append(specs, spec)
	}
	return specs
}

// EvaluateDTC evaluates the selected_word and numeric_index rows of one
// diagnostic word at the caller's offset. A frame, member or offset the decoder
// refuses gives the decoder's status and no rows. An offset that is not a word
// position of the frame family gives roweval.ReasonNoMatch, and a 0x52X word
// not attributed to the selected group and slot gives "invalid", as the frame
// decoder refuses it. A 0x333 word of another group or slot than the selection's,
// or under a selection that names no group and slot, is refused the same way.
func EvaluateDTC(member canbusreg.Selection, in canbusreg.LINKInput, offset int) (DiagnosticResult, roweval.Reason, error) {
	res := DiagnosticResult{Evidence: in.Evidence, Attribution: diagAttribution(in), Rows: make(map[string]roweval.Evaluation)}
	frame := in.Evidence.Frame
	decoded := canbusreg.DecodeDTC(member, in, offset)
	if _, ok := decoded.Fields[dtcSelectedWord]; !ok {
		return res, roweval.Reason(decoded.Status), nil
	}
	fam, ok := diagDTCFamilyOf(frame)
	if !ok || !diagWordOffsetOf(fam, offset) {
		return res, roweval.ReasonNoMatch, nil
	}
	if fam.token == "52X" && !diagParallelAttributed(member, frame) {
		return res, roweval.Reason("invalid"), nil
	}
	if fam.token == "333" && !diagStandardAttributed(member, frame) {
		return res, roweval.Reason("invalid"), nil
	}
	c := member.SelectionSpecValue()
	dictionary := "absent"
	if _, ok := canbusreg.EncodeDTC(member, 0); ok {
		dictionary = "present"
	}
	q := roweval.Qualifiers{Values: map[string]string{qFlavor: c.Flavor, qBank: c.Bank, qDTCDictionary: dictionary}}
	for key, f := range decoded.Fields {
		q.Values[qDiagStatus+key] = f.Status
	}
	for _, spec := range diagDTCSpecs(fam, member, offset, frame) {
		ev, err := diagEvaluate(spec, frame, q)
		if err != nil {
			return res, "", err
		}
		res.Rows[spec.id] = ev
	}
	return res, "", nil
}
