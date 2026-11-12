package canbusreg

import (
	"encoding/binary"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Native contracts, section PCS summary bits and their diagnostic sources:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// PCSSummaryPCSInput carries caller-validated local state for this exact selection and
// epoch. Nil state is unknown. LocalSummaryQualified independently admits the
// QAAA0006 summary contract; it never marks hardware physically qualified.
// FieldStates and FieldAges use bit keys such as A/12 and B4/2.
type PCSSummaryPCSInput struct {
	PCSStandardPCSInput
	LocalSummaryQualified                     bool
	ControlActive, Condition131, Condition132 *bool
	StateCode                                 *uint8
}

// SummaryBit contains bounded numeric OR alternatives, never an active fault
// set. ConditionalAlternatives require missing control-state evidence. Neither
// list identifies a unique cause, even when it contains only one index.
type SummaryBit struct {
	Field                                 CXNativeField
	Alternatives, ConditionalAlternatives []int
}
type PCSSummaryPCSResult struct {
	CXNativeResult
	Bits        map[string]SummaryBit
	Diagnostics []byte
}

// DecodePCSSummaries is receive-only and stateless. Clear summary bits say
// nothing about independent current/retained diagnostics. Unknown, unassigned
// and status-only bits remain visible; no all-clear conclusion is produced.
func DecodePCSSummaries(s CXSelection, i PCSSummaryPCSInput) PCSSummaryPCSResult {
	r := PCSSummaryPCSResult{CXNativeResult: selectionResult(s, i.LINKInput, "invalid"), Bits: make(map[string]SummaryBit), Diagnostics: append([]byte(nil), i.NativeRecord...)}
	c := s.SelectionSpecValue()
	f := i.Evidence.Frame
	if len(i.NativeRecord) != 0 {
		d, e := canbus.DecodeSocketCANRecord(i.NativeRecord)
		if e != nil || d != f {
			return r
		}
	}
	if i.Evidence.RawRecord != [canbus.SocketCANRecordSize]byte{} {
		d, e := canbus.DecodeSocketCANRecord(i.Evidence.RawRecord[:])
		if e != nil || d != f {
			return r
		}
	}
	if c.Interface.Name() == "" || c.Interface.Index() <= 0 || c.Epoch == 0 || c.Bank == "" || c.Endpoint == "" ||
		(c.Flavor != "pcs-standard" && c.Flavor != "pcs-parallel") || c.Direction != "bms-to-pcs" ||
		i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot || i.FieldAge < 0 || f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 {
		return r
	}
	if !i.PublisherResolved {
		r.Status = "conflict"
		return r
	}
	id := f.ID().Value()
	if id != 0x312 && id != 0x323 {
		r.Status = "unsupported"
		return r
	}
	modern := s.SelectionGroup() == "L8" && c.Scope == "local"
	legacy := c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local" && s.SelectionGroup() == "L2" && i.LocalSummaryQualified
	supported := modern || legacy
	mappings := map[string]map[int][]int{
		"A":  {1: {9}, 2: {7}, 3: {3}, 4: {1, 62}, 5: {24}, 6: {23, 49, 88, 97}, 7: {21, 24}, 10: {5}, 11: {87, 90, 128, 133}, 12: {40, 41}, 13: {15}, 14: {34}, 15: {13}},
		"B":  {1: {8}, 2: {6}, 3: {2}, 4: {0, 98}, 6: {22, 89}, 7: {20}, 12: {40}, 13: {14}, 14: {33}, 15: {12}},
		"B4": {2: {25, 83}, 3: {27, 28, 29}, 4: {38}, 5: {35}, 6: {30}, 7: {31}},
		"B5": {0: {42}, 1: {43}, 2: {44}, 3: {45}, 4: {48}, 5: {49}, 6: {51}},
		"B6": {0: {53}, 1: {39, 54}, 2: {55}, 3: {56}, 4: {84}, 5: {88, 97}},
		"B7": {2: {57}, 3: {58}, 4: {85}, 5: {89}},
	}
	if legacy {
		// The legacy local frame carries diagnostic index 4 on B/10; release-11
		// keeps the same bit as an independent status selector instead.
		mappings["B"] = map[int][]int{}
		for k, v := range map[int][]int{1: {8}, 2: {6}, 3: {2}, 4: {0, 98}, 6: {22, 89}, 7: {20}, 10: {4}, 12: {40}, 13: {14}, 14: {33}, 15: {12}} {
			mappings["B"][k] = v
		}
	}
	words := []struct {
		name  string
		value uint16
		width int
	}{{"A", binary.LittleEndian.Uint16(r.Raw[:2]), 16}, {"B", binary.LittleEndian.Uint16(r.Raw[2:4]), 16}}
	if id == 0x323 {
		words = nil
		for at := 4; at < 8; at++ {
			words = append(words, struct {
				name  string
				value uint16
				width int
			}{fmt.Sprintf("B%d", at), uint16(r.Raw[at]), 8})
		}
	}
	for _, w := range words {
		for bit := 0; bit < w.width; bit++ {
			key := fmt.Sprintf("%s/%d", w.name, bit)
			field := CXNativeField{Numerator: int64((w.value >> bit) & 1), Denominator: 1, Grade: "Unknown", Status: "raw", Raw: r.Raw, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
			b := SummaryBit{Field: field}
			if supported && field.Numerator == 1 {
				b.Alternatives = append([]int(nil), mappings[w.name][bit]...)
				grade := "Proven"
				meaning := "bounded numeric OR alternatives; no unique attribution"
				if legacy && w.name == "B" && bit == 10 {
					grade = "Hypothesis"
				}
				if legacy && w.name == "A" {
					switch bit {
					case 4:
						b.Alternatives = []int{1, 62, 99}
						grade = "Hypothesis"
					case 11:
						b.Alternatives = []int{87, 90}
						grade = "Hypothesis"
					case 12:
						b.Alternatives = []int{41}
						grade = "Hypothesis"
					case 0:
						if i.StateCode != nil && *i.StateCode == 5 {
							meaning = "candidate state code 5; no diagnostic index"
							grade = "Hypothesis"
							b.Field.Status = "accepted"
						}
					}
				}
				if modern && w.name == "A" && bit == 12 {
					b.Alternatives = []int{41}
					// A known inactive control or either active condition admits 40;
					// only all three known exclusion predicates can rule it out here.
					if (i.ControlActive != nil && !*i.ControlActive) || (i.Condition131 != nil && *i.Condition131) || (i.Condition132 != nil && *i.Condition132) {
						b.Alternatives = []int{40, 41}
					} else if i.ControlActive == nil || i.Condition131 == nil || i.Condition132 == nil {
						b.ConditionalAlternatives = []int{40}
					}
				}
				if len(b.Alternatives) > 0 || b.Field.Status == "accepted" {
					b.Field.Status = "accepted"
					b.Field.Meaning = meaning
					b.Field.Grade = grade
				}
			}
			if supported && field.Numerator == 0 && len(mappings[w.name][bit]) > 0 {
				// A clear summary bit is a valid observation of that bit only; it
				// does not show that the independent diagnostics are clear.
				b.Field.Status, b.Field.Grade = "accepted", "Proven"
				b.Field.Meaning = "summary bit clear; independent diagnostics not inferred"
			}
			if legacy && w.name == "A" && bit == 0 && b.Field.Numerator == 0 {
				// A clear A/0 is a valid observation of that bit and must be able to
				// replace a previously retained set bit.
				b.Field.Status, b.Field.Grade = "accepted", "Proven"
				b.Field.Meaning = "summary bit clear; independent diagnostics not inferred"
			}
			if modern && w.name == "B" && (bit == 8 || bit == 10) && b.Field.Numerator == 1 {
				b.Field.Status, b.Field.Grade = "accepted", "Proven"
				b.Field.Meaning = "independent status source; no diagnostic index"
				if bit == 10 {
					b.Field.Meaning = "parallel status selector 4; no local diagnostic index"
				}
			}
			if modern && w.name == "B" && (bit == 8 || bit == 10) && b.Field.Numerator == 0 {
				b.Field.Status, b.Field.Grade = "accepted", "Proven"
				b.Field.Meaning = "independent status source clear; no diagnostic index"
			}
			// Expected-clear bits are not fault indices; a set bit is a conflict.
			clear := modern && ((w.name == "A" && (bit == 0 || bit == 9)) || (w.name == "B" && bit == 9) || (w.name == "B4" && bit < 2) || (w.name == "B5" && bit == 7) || (w.name == "B7" && bit < 2))
			if clear && b.Field.Numerator != 0 {
				b.Field.Status = "conflict"
			}
			if age, ok := i.FieldAges[key]; ok {
				b.Field.Age = age
			}
			if state, ok := i.FieldStates[key]; ok {
				switch state {
				case "absent", "invalid", "unsupported", "stale", "conflict", "unassigned":
					b.Field.Status = state
				default:
					b.Field.Status = "invalid"
				}
			}
			if b.Field.Age < 0 {
				b.Field.Status = "invalid"
			}
			if b.Field.Status != "accepted" {
				b.Alternatives = nil
				b.ConditionalAlternatives = nil
				b.Field.Meaning = ""
				b.Field.Unit = ""
				b.Field.Grade = "Unknown"
			}
			r.Bits[key] = b
			r.Fields[key] = b.Field
		}
	}
	r.Status = "decoded"
	if !supported {
		r.Status = "unsupported"
	}
	return r
}
