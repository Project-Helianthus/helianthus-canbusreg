package canbusreg

import (
	"encoding/binary"
	"fmt"
	"testing"
)

var diagnosticTestNames = map[int]string{
	0: "Cell overvoltage — condition 0", 1: "Cell overvoltage — condition 1",
	2: "Cell undervoltage — condition 2", 3: "Cell undervoltage — condition 3",
	4: "Cell-voltage spread high — condition 4", 5: "Cell-voltage spread high — condition 5",
	6: "Pack overvoltage — condition 6", 7: "Pack overvoltage — condition 7",
	8: "Pack undervoltage — condition 8", 9: "Pack undervoltage — condition 9",
	11: "Reported SOC low", 12: "Cell temperature high — condition 12", 13: "Cell temperature high — condition 13",
	14: "Cell temperature low — condition 14", 15: "Cell temperature low — condition 15",
	16: "Cell-temperature spread high — condition 16", 17: "Cell-temperature spread high — condition 17",
	20: "Discharge current high — condition 20", 21: "Discharge current high — condition 21",
	22: "Charge current high — condition 22", 23: "Charge current high — condition 23",
	30: "Cell-voltage plausibility", 31: "Current zero-offset calibration out of range",
	33: "Cell temperature high — condition 33", 34: "Cell temperature high — condition 34",
	35: "Measurement acquisition communication failure", 38: "Nonvolatile storage access failure",
	39: "Charging current while a low-temperature condition is active", 40: "Cell temperature low — condition 40", 41: "Cell temperature low — condition 41",
	42: "Pack-voltage consistency difference", 44: "Configuration record failure", 45: "Hardware status condition",
	52: "Output-port overvoltage", 57: "Charging current high — additional condition", 58: "Discharging current high — additional condition",
	60: "Pack voltage exceeds port voltage", 61: "Port voltage exceeds pack voltage", 62: "Maximum cell voltage high",
	91: "Parallel discovery/association timeout", 93: "Parallel peer-word spread in 61X.B4–B5",
	102: "LINK member serial conflict", 103: "Parallel member serial conflict", 128: "Startup integrity check A failed",
	129: "Algorithm status condition — condition 129", 130: "Algorithm status condition — condition 130",
	131: "Adjacent member state mismatch", 133: "Startup integrity check B failed", 134: "Measurement status flag set", 135: "Pack/port voltage difference with hardware status",
}

func diagnosticTestModernOnly(index int) bool {
	switch index {
	case 31, 35, 91, 93, 102, 103, 128, 131, 133, 134, 135:
		return true
	}
	return false
}

func diagnosticTestIndex(t *testing.T, s CXSelection, index int) CXNativeField {
	t.Helper()
	word, ok := EncodeDTC(s, index)
	if !ok {
		t.Fatal(index)
	}
	var p [8]byte
	binary.LittleEndian.PutUint16(p[:2], word)
	i := selectionTestInput(t, s.SelectionSpecValue(), 0x1853, p)
	i.FieldAge = 17
	i.Evidence.RawRecord[15] = 71
	r := DecodeDTC(s, i, 0)
	f, ok := r.Fields["numeric_index"]
	if !ok {
		t.Fatal(r)
	}
	return f
}

func TestCX13AllConditionNamesAndOpaqueIndices(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s := dtcTestMember(t, modern)
		count := 134
		if modern {
			count = 136
		}
		for index := 0; index < count; index++ {
			t.Run(fmt.Sprintf("modern%v/index%d", modern, index), func(t *testing.T) {
				f := diagnosticTestIndex(t, s, index)
				r := LabelCondition(s, f)
				want, named := diagnosticTestNames[index]
				named = named && (modern || !diagnosticTestModernOnly(index))
				if !named {
					if r.Status != "unsupported" || r.Meaning != "" || r.Raw != f.Raw || r.Evidence != f.Evidence {
						t.Fatal(r)
					}
					return
				}
				if r.Meaning != want || r.Status != "accepted" || r.Grade != "Hypothesis" {
					t.Fatal(r)
				}
				r.Meaning = f.Meaning
				r.Status = f.Status
				r.Grade = f.Grade
				if r != f || r.PhysicalQualified {
					t.Fatal("metadata lost", r, f)
				}
			})
		}
	}
}

func TestCX13EachLabelHypothesisNegatives(t *testing.T) {
	for index := range diagnosticTestNames {
		for _, modern := range []bool{false, true} {
			if !modern && index >= 134 {
				continue
			}
			s := dtcTestMember(t, modern)
			f := diagnosticTestIndex(t, s, index)
			for _, gate := range []string{"wrong-selector", "wrong-release", "legacy-choice", "out-of-range", "unnamed", "stale", "invalid", "absent", "conflict", "unsupported", "physical-qualified", "raw", "unit", "denominator", "evidence", "age", "epoch", "scope", "bank", "endpoint", "group", "slot", "numerator", "meaning", "grade"} {
				t.Run(fmt.Sprintf("label%d/modern%v/%s", index, modern, gate), func(t *testing.T) {
					selected := s
					input := f
					c := s.SelectionSpecValue()
					switch gate {
					case "wrong-selector":
						c.ProjectCode = "qAAa"
						selected = selectionTestSelect(t, c)
					case "wrong-release":
						c.Release = "9012"
						selected = selectionTestSelect(t, c)
					case "legacy-choice":
						c.ProjectCode = "QAAa"
						c.Release = "9011"
						c.Choice = "legacy19"
						selected = selectionTestSelect(t, c)
					case "out-of-range":
						input.Numerator = 136
					case "unnamed":
						input = diagnosticTestIndex(t, s, 10)
					case "stale", "invalid", "absent", "conflict", "unsupported":
						input.Status = gate
					case "physical-qualified":
						input.PhysicalQualified = true
					case "raw":
						input.Raw[6]++
					case "unit":
						input.Unit = "V"
					case "denominator":
						input.Denominator = 10
					case "evidence":
						input.Evidence.Sequence = 0
					case "age":
						input.Age = -1
					case "epoch":
						input.Epoch++
					case "scope":
						input.Scope = "aggregate"
					case "bank":
						input.Bank = "other"
					case "endpoint":
						input.Endpoint = "other"
					case "group":
						input.Group = "other"
					case "slot":
						input.Slot = "other"
					case "numerator":
						input.Numerator++
					case "meaning":
						input.Meaning = "retained fault index"
					case "grade":
						input.Grade = "Unknown"
					}
					r := LabelCondition(selected, input)
					if r.Meaning != "" || r.Status == "accepted" || r.Raw != input.Raw || r.Evidence != input.Evidence {
						t.Fatal(r)
					}
				})
			}
		}
	}
}

func TestCX13ConditionPCSParity(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, flavor := range []string{"pcs-standard", "pcs-parallel"} {
			for index, want := range diagnosticTestNames {
				if !modern && (diagnosticTestModernOnly(index) || index >= 134) {
					continue
				}
				c := dtcTestMember(t, modern).SelectionSpecValue()
				c.Flavor = flavor
				c.Direction = "bms-to-pcs"
				s := selectionTestSelect(t, c)
				word, _ := EncodeDTC(s, index)
				var p [8]byte
				offset := 2
				idValue := uint32(0x333)
				if flavor == "pcs-parallel" {
					offset = 1
					idValue = 0x520
				}
				binary.BigEndian.PutUint16(p[offset:offset+2], word)
				i := selectionTestInput(t, c, 0x1853, p)
				i.Evidence.Frame = diagnosticTestStandard(t, idValue, p)
				f := DecodeDTC(s, i, offset).Fields["numeric_index"]
				r := LabelCondition(s, f)
				if r.Status != "accepted" || r.Meaning != want {
					t.Fatal(flavor, index, r)
				}
			}
		}
	}
}
