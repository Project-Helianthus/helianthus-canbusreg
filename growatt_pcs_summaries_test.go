package canbusreg

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func summaryTestFixture(t *testing.T, id uint32, p [8]byte, legacy bool) (Selection, PCSSummaryPCSInput) {
	t.Helper()
	s, i := standardTestFixture(t, id, p)
	if !legacy {
		s, i = standardTestReselect(t, s, i, func(c *SelectionSpec) { c.Release = "0011" })
	}
	return s, PCSSummaryPCSInput{PCSStandardPCSInput: i, LocalSummaryQualified: true}
}

var summaryTestExpected = []struct {
	id      uint32
	word    string
	bit     int
	indices []int
}{
	{0x312, "A", 1, []int{9}}, {0x312, "A", 2, []int{7}}, {0x312, "A", 3, []int{3}},
	{0x312, "A", 4, []int{1, 62}}, {0x312, "A", 5, []int{24}}, {0x312, "A", 6, []int{23, 49, 88, 97}},
	{0x312, "A", 7, []int{21, 24}}, {0x312, "A", 10, []int{5}}, {0x312, "A", 11, []int{87, 90, 128, 133}},
	{0x312, "A", 12, []int{40, 41}}, {0x312, "A", 13, []int{15}}, {0x312, "A", 14, []int{34}}, {0x312, "A", 15, []int{13}},
	{0x312, "B", 1, []int{8}}, {0x312, "B", 2, []int{6}}, {0x312, "B", 3, []int{2}}, {0x312, "B", 4, []int{0, 98}},
	{0x312, "B", 6, []int{22, 89}}, {0x312, "B", 7, []int{20}}, {0x312, "B", 12, []int{40}},
	{0x312, "B", 13, []int{14}}, {0x312, "B", 14, []int{33}}, {0x312, "B", 15, []int{12}},
	{0x323, "B4", 2, []int{25, 83}}, {0x323, "B4", 3, []int{27, 28, 29}}, {0x323, "B4", 4, []int{38}},
	{0x323, "B4", 5, []int{35}}, {0x323, "B4", 6, []int{30}}, {0x323, "B4", 7, []int{31}},
	{0x323, "B5", 0, []int{42}}, {0x323, "B5", 1, []int{43}}, {0x323, "B5", 2, []int{44}}, {0x323, "B5", 3, []int{45}},
	{0x323, "B5", 4, []int{48}}, {0x323, "B5", 5, []int{49}}, {0x323, "B5", 6, []int{51}},
	{0x323, "B6", 0, []int{53}}, {0x323, "B6", 1, []int{39, 54}}, {0x323, "B6", 2, []int{55}},
	{0x323, "B6", 3, []int{56}}, {0x323, "B6", 4, []int{84}}, {0x323, "B6", 5, []int{88, 97}},
	{0x323, "B7", 2, []int{57}}, {0x323, "B7", 3, []int{58}}, {0x323, "B7", 4, []int{85}}, {0x323, "B7", 5, []int{89}},
}

func summaryTestPayload(word string, bit int) (p [8]byte) {
	at := map[string]int{"A": 0, "B": 2, "B4": 4, "B5": 5, "B6": 6, "B7": 7}[word]
	p[at+bit/8] = 1 << (bit % 8)
	return
}
func TestCX14MappedBitsAndIndependentContributors(t *testing.T) {
	for _, v := range summaryTestExpected {
		for _, source := range v.indices {
			t.Run(fmt.Sprintf("%x/%s/%d/source%d", v.id, v.word, v.bit, source), func(t *testing.T) {
				// Each synthetic source alone produces the same OR summary, never attribution.
				s, i := summaryTestFixture(t, v.id, summaryTestPayload(v.word, v.bit), false)
				off := false
				i.ControlActive = &off
				key := fmt.Sprintf("%s/%d", v.word, v.bit)
				r := DecodePCSSummaries(s, i)
				b := r.Bits[key]
				if !reflect.DeepEqual(b.Alternatives, v.indices) || b.Field.Status != "accepted" || b.Field.Grade != "Proven" {
					t.Fatalf("%s: %#v", key, b)
				}
				f := b.Field
				if f.Raw != i.Evidence.Frame.Bytes() || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Age != i.FieldAge || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.PhysicalQualified || r.PhysicalQualified {
					t.Fatal("lost evidence", f)
				}
				zero, _ := canbus.NewFrame(i.Evidence.Frame.ID(), make([]byte, 8))
				i.Evidence.Frame = zero
				clear := DecodePCSSummaries(s, i).Bits[key]
				if len(clear.Alternatives) != 0 || len(clear.ConditionalAlternatives) != 0 || clear.Field.Numerator != 0 || clear.Field.Status != "accepted" || clear.Field.Meaning != "summary bit clear; independent diagnostics not inferred" {
					t.Fatal("clear summary interpreted", clear)
				}
			})
		}
	}
}

func TestCX14SummaryA4Extra99Negative(t *testing.T) {
	p := summaryTestPayload("A", 4)
	s, i := summaryTestFixture(t, 0x312, p, true)
	b := DecodePCSSummaries(s, i).Bits["A/4"]
	if !reflect.DeepEqual(b.Alternatives, []int{1, 62, 99}) || b.Field.Grade != "Hypothesis" {
		t.Fatal(b)
	}
	s, i = summaryTestFixture(t, 0x312, p, false)
	b = DecodePCSSummaries(s, i).Bits["A/4"]
	if !reflect.DeepEqual(b.Alternatives, []int{1, 62}) || b.Field.Raw != p {
		t.Fatal(b)
	}
}
func TestCX14SummaryA11OnlyNegative(t *testing.T) {
	for _, index := range []int{87, 90, 128, 133} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			// Identical summary from each invented independent source cannot attribute it.
			p := summaryTestPayload("A", 11)
			s, i := summaryTestFixture(t, 0x312, p, true)
			b := DecodePCSSummaries(s, i).Bits["A/11"]
			if !reflect.DeepEqual(b.Alternatives, []int{87, 90}) || b.Field.Grade != "Hypothesis" || b.Field.Raw != p {
				t.Fatal(b)
			}
			s, i = summaryTestFixture(t, 0x312, p, false)
			b = DecodePCSSummaries(s, i).Bits["A/11"]
			if !reflect.DeepEqual(b.Alternatives, []int{87, 90, 128, 133}) {
				t.Fatal(b)
			}
		})
	}
}
func TestCX14SummaryA12Only41Negative(t *testing.T) {
	for _, index := range []int{40, 41} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			p := summaryTestPayload("A", 12)
			s, i := summaryTestFixture(t, 0x312, p, true)
			b := DecodePCSSummaries(s, i).Bits["A/12"]
			if !reflect.DeepEqual(b.Alternatives, []int{41}) || b.Field.Grade != "Hypothesis" || b.Field.Raw != p || len(b.ConditionalAlternatives) != 0 {
				t.Fatal(b)
			}
			s, i = summaryTestFixture(t, 0x312, p, false)
			b = DecodePCSSummaries(s, i).Bits["A/12"]
			if !reflect.DeepEqual(b.Alternatives, []int{41}) || !reflect.DeepEqual(b.ConditionalAlternatives, []int{40}) {
				t.Fatal(b)
			}
		})
	}
}
func TestCX14SummaryA0State5NegativeBoundaries(t *testing.T) {
	for _, state := range []int{-1, 0, 4, 5, 6} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			p := summaryTestPayload("A", 0)
			s, i := summaryTestFixture(t, 0x312, p, true)
			if state >= 0 {
				v := uint8(state)
				i.StateCode = &v
			}
			b := DecodePCSSummaries(s, i).Bits["A/0"]
			if (b.Field.Status == "accepted") != (state == 5) || len(b.Alternatives) != 0 || b.Field.Raw != p {
				t.Fatal(b)
			}
			if state != 5 && (b.Field.Grade != "Unknown" || b.Field.Meaning != "") {
				t.Fatal(b)
			}
			s, i = summaryTestFixture(t, 0x312, p, false)
			v := uint8(5)
			i.StateCode = &v
			b = DecodePCSSummaries(s, i).Bits["A/0"]
			if b.Field.Status != "conflict" || b.Field.Meaning != "" {
				t.Fatal(b)
			}
		})
	}
}
func TestCX14Condition40Gate(t *testing.T) {
	for control := -1; control <= 1; control++ {
		for c131 := -1; c131 <= 1; c131++ {
			for c132 := -1; c132 <= 1; c132++ {
				t.Run(fmt.Sprintf("control%d/131%d/132%d", control, c131, c132), func(t *testing.T) {
					ptr := func(n int) *bool {
						if n < 0 {
							return nil
						}
						v := n == 1
						return &v
					}
					s, i := summaryTestFixture(t, 0x312, summaryTestPayload("A", 12), false)
					i.ControlActive = ptr(control)
					i.Condition131 = ptr(c131)
					i.Condition132 = ptr(c132)
					b := DecodePCSSummaries(s, i).Bits["A/12"]
					want := []int{41}
					var conditional []int
					if control == 0 || c131 == 1 || c132 == 1 {
						want = []int{40, 41}
					} else if control < 0 || c131 < 0 || c132 < 0 {
						conditional = []int{40}
					}
					if !reflect.DeepEqual(b.Alternatives, want) || !reflect.DeepEqual(b.ConditionalAlternatives, conditional) {
						t.Fatal(b)
					}
				})
			}
		}
	}
}

func TestCX14AdmissionEveryKey(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "publisher", "ide", "dlc", "raw-dlc", "age", "native-record", "evidence-record"} {
		t.Run(key, func(t *testing.T) {
			s, i := summaryTestFixture(t, 0x312, summaryTestPayload("A", 4), false)
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "pcs-to-bms"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "other"
			case "endpoint":
				i.Endpoint = "other"
			case "group":
				i.Group = "other"
			case "slot":
				i.Slot = "other"
			case "publisher":
				i.PublisherResolved = false
			case "ide":
				id, _ := canbus.NewExtendedID(0x312)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data())
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), i.Evidence.Frame.Data()[:7])
			case "raw-dlc":
				var record [16]byte
				binary.NativeEndian.PutUint32(record[:4], 0x312)
				record[4] = 8
				record[7] = 9
				p := i.Evidence.Frame.Bytes()
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
			case "age":
				i.FieldAge = -1
			case "native-record":
				i.NativeRecord = []byte{1}
			case "evidence-record":
				i.Evidence.RawRecord[0] = 1
			}
			r := DecodePCSSummaries(s, i)
			if len(r.Bits) != 0 || len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() || r.Evidence != i.Evidence || r.PhysicalQualified {
				t.Fatal(r)
			}
			if !reflect.DeepEqual(r.Diagnostics, i.NativeRecord) {
				t.Fatal("native record lost")
			}
		})
	}
}

func TestCX14WrongProfilesAndHypothesisScopeNegatives(t *testing.T) {
	for _, key := range []string{"project-case", "release", "choice", "aggregate", "link", "extended19", "unqualified-local"} {
		for _, v := range []struct {
			id   uint32
			word string
			bit  int
		}{{0x312, "A", 4}, {0x312, "A", 11}, {0x312, "A", 12}, {0x312, "A", 0}, {0x323, "B4", 2}} {
			t.Run(fmt.Sprintf("%s/%x/%s/%d", key, v.id, v.word, v.bit), func(t *testing.T) {
				p := summaryTestPayload(v.word, v.bit)
				s, i := summaryTestFixture(t, v.id, p, true)
				c := s.SelectionSpecValue()
				switch key {
				case "project-case":
					c.ProjectCode = "qAAA"
				case "release":
					c.Release = "0007"
				case "choice":
					c.ProjectCode = "QAAa"
					c.Release = "9011"
					c.Choice = "legacy19"
				case "aggregate":
					c.Scope = "aggregate"
					i.Scope = c.Scope
				case "link":
					c.Flavor = "link"
					c.PCSFlavor = "pcs-standard"
					c.RouteSet = true
					c.Direction = "module-to-coordinator"
					i.Direction = c.Direction
				case "extended19":
					c.Flavor = "pcs-extended19"
				case "unqualified-local":
					i.LocalSummaryQualified = false
				}
				var err error
				s, err = NewSelection(c)
				if err != nil {
					t.Fatal(err)
				}
				state := uint8(5)
				i.StateCode = &state
				r := DecodePCSSummaries(s, i)
				if r.Raw != p || r.Evidence != i.Evidence {
					t.Fatal(r)
				}
				for _, b := range r.Bits {
					if b.Field.Status == "accepted" || b.Field.Meaning != "" || len(b.Alternatives) > 0 {
						t.Fatal(b)
					}
				}
			})
		}
	}
}
func TestCX14Local323IndependentQualification(t *testing.T) {
	for _, v := range summaryTestExpected {
		if v.id != 0x323 {
			continue
		}
		for _, source := range v.indices {
			t.Run(fmt.Sprintf("%s/%d/source%d", v.word, v.bit, source), func(t *testing.T) {
				p := summaryTestPayload(v.word, v.bit)
				s, i := summaryTestFixture(t, v.id, p, true)
				b := DecodePCSSummaries(s, i).Bits[fmt.Sprintf("%s/%d", v.word, v.bit)]
				if !reflect.DeepEqual(b.Alternatives, v.indices) || b.Field.Grade != "Proven" || b.Field.PhysicalQualified {
					t.Fatal(b)
				}
				i.LocalSummaryQualified = false
				r := DecodePCSSummaries(s, i)
				for _, b := range r.Bits {
					if b.Field.Status == "accepted" || len(b.Alternatives) > 0 {
						t.Fatal(b)
					}
				}
				if r.Raw != p {
					t.Fatal(r)
				}
			})
		}
	}
}
func TestCX14UnassignedStatusAndExpectedClear(t *testing.T) {
	for _, v := range []struct {
		id     uint32
		word   string
		bits   []int
		status string
	}{
		{0x312, "A", []int{8}, "raw"}, {0x312, "B", []int{0, 5, 11}, "raw"},
		{0x323, "B6", []int{6, 7}, "raw"}, {0x323, "B7", []int{6, 7}, "raw"},
		{0x312, "A", []int{0, 9}, "conflict"}, {0x312, "B", []int{9}, "conflict"},
		{0x323, "B4", []int{0, 1}, "conflict"}, {0x323, "B5", []int{7}, "conflict"}, {0x323, "B7", []int{0, 1}, "conflict"},
	} {
		for _, bit := range v.bits {
			t.Run(fmt.Sprintf("%x/%s/%d", v.id, v.word, bit), func(t *testing.T) {
				p := summaryTestPayload(v.word, bit)
				s, i := summaryTestFixture(t, v.id, p, false)
				r := DecodePCSSummaries(s, i)
				b := r.Bits[fmt.Sprintf("%s/%d", v.word, bit)]
				if b.Field.Status != v.status || b.Field.Numerator != 1 || b.Field.Raw != p || b.Field.Meaning != "" || len(b.Alternatives) > 0 {
					t.Fatal(b)
				}
			})
		}
	}
}
func TestCX14PartialRetentionAndClearIndependentFault(t *testing.T) {
	p := summaryTestPayload("A", 4)
	p[0] |= 2
	s, i := summaryTestFixture(t, 0x312, p, false)
	previous := DecodePCSSummaries(s, i)
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned", "bad-state", "negative-age"} {
		t.Run(state, func(t *testing.T) {
			next := i
			next.Evidence.Sequence++
			next.FieldStates = map[string]string{"A/4": state}
			if state == "negative-age" {
				next.FieldStates = nil
				next.FieldAges = map[string]time.Duration{"A/4": -1}
			}
			r := DecodePCSSummaries(s, next)
			if r.Bits["A/4"].Field.Status == "accepted" || len(r.Bits["A/4"].Alternatives) > 0 || r.Bits["A/1"].Field.Status != "accepted" {
				t.Fatal(r)
			}
			retained := RetainAccepted(previous.NativeResult, r.NativeResult)
			if retained.Fields["A/4"] != previous.Fields["A/4"] || retained.Fields["A/1"].Evidence != next.Evidence {
				t.Fatal(retained)
			}
		})
	}
	s, i = summaryTestFixture(t, 0x312, summaryTestPayload("B", 12), false)
	r := DecodePCSSummaries(s, i)
	if len(r.Bits["A/12"].Alternatives) != 0 || !reflect.DeepEqual(r.Bits["B/12"].Alternatives, []int{40}) {
		t.Fatal(r)
	}
}
func TestCX14ParallelLocalAndExplicitAmbiguity(t *testing.T) {
	for _, flavor := range []string{"pcs-standard", "pcs-parallel"} {
		for _, choice := range []string{"", "legacy19", "release11"} {
			t.Run(flavor+choice, func(t *testing.T) {
				s, i := summaryTestFixture(t, 0x312, summaryTestPayload("A", 4), false)
				c := s.SelectionSpecValue()
				c.ProjectCode = "QAAa"
				c.Release = "9011"
				c.Flavor = flavor
				c.Choice = choice
				s, err := NewSelection(c)
				if err != nil {
					t.Fatal(err)
				}
				b := DecodePCSSummaries(s, i).Bits["A/4"]
				if (len(b.Alternatives) > 0) != (choice == "release11") {
					t.Fatal(b)
				}
			})
		}
	}
}

func TestCX14IndependentStatusSources(t *testing.T) {
	for _, bit := range []int{8, 10} {
		t.Run(fmt.Sprint(bit), func(t *testing.T) {
			s, i := summaryTestFixture(t, 0x312, summaryTestPayload("B", bit), false)
			b := DecodePCSSummaries(s, i).Bits[fmt.Sprintf("B/%d", bit)]
			want := "independent status source; no diagnostic index"
			if bit == 10 {
				want = "parallel status selector 4; no local diagnostic index"
			}
			if b.Field.Status != "accepted" || b.Field.Meaning != want || b.Field.Grade != "Proven" || len(b.Alternatives) != 0 {
				t.Fatal(b)
			}
		})
	}
}

func TestCX14ClearedSummaryReplacesRetainedSetBit(t *testing.T) {
	s, i := summaryTestFixture(t, 0x312, summaryTestPayload("A", 4), false)
	previous := DecodePCSSummaries(s, i)
	if previous.Fields["A/4"].Status != "accepted" || previous.Fields["A/4"].Numerator != 1 {
		t.Fatal(previous.Fields["A/4"])
	}
	s, clear := summaryTestFixture(t, 0x312, [8]byte{}, false)
	clear.Evidence.Sequence = i.Evidence.Sequence + 1
	r := DecodePCSSummaries(s, clear)
	b := r.Bits["A/4"]
	if b.Field.Status != "accepted" || b.Field.Numerator != 0 || len(b.Alternatives) != 0 || b.Field.Meaning != "summary bit clear; independent diagnostics not inferred" {
		t.Fatal(b)
	}
	retained := RetainAccepted(previous.NativeResult, r.NativeResult)
	if retained.Fields["A/4"].Numerator != 0 || retained.Fields["A/4"].Evidence != clear.Evidence {
		t.Fatal(retained.Fields["A/4"])
	}
}

func TestCX14LegacyA0ClearReplacesRetainedState5(t *testing.T) {
	s, i := summaryTestFixture(t, 0x312, summaryTestPayload("A", 0), true)
	five := uint8(5)
	i.StateCode = &five
	previous := DecodePCSSummaries(s, i)
	if f := previous.Fields["A/0"]; f.Status != "accepted" || f.Numerator != 1 {
		t.Fatal(f)
	}
	s, clear := summaryTestFixture(t, 0x312, [8]byte{}, true)
	clear.Evidence.Sequence = i.Evidence.Sequence + 1
	r := DecodePCSSummaries(s, clear)
	if f := r.Fields["A/0"]; f.Status != "accepted" || f.Numerator != 0 || f.Grade != "Proven" {
		t.Fatal(f)
	}
	retained := RetainAccepted(previous.NativeResult, r.NativeResult)
	if f := retained.Fields["A/0"]; f.Numerator != 0 || f.Evidence != clear.Evidence {
		t.Fatal(f)
	}
}

func TestCX14ClearedIndependentStatusReplacesRetainedSetBit(t *testing.T) {
	for _, bit := range []int{8, 10} {
		t.Run(fmt.Sprint(bit), func(t *testing.T) {
			key := fmt.Sprintf("B/%d", bit)
			s, i := summaryTestFixture(t, 0x312, summaryTestPayload("B", bit), false)
			previous := DecodePCSSummaries(s, i)
			if previous.Fields[key].Status != "accepted" || previous.Fields[key].Numerator != 1 {
				t.Fatal(previous.Fields[key])
			}
			s, clear := summaryTestFixture(t, 0x312, [8]byte{}, false)
			clear.Evidence.Sequence = i.Evidence.Sequence + 1
			r := DecodePCSSummaries(s, clear)
			if f := r.Fields[key]; f.Status != "accepted" || f.Numerator != 0 || f.Grade != "Proven" {
				t.Fatal(f)
			}
			retained := RetainAccepted(previous.NativeResult, r.NativeResult)
			if f := retained.Fields[key]; f.Numerator != 0 || f.Evidence != clear.Evidence {
				t.Fatal(f)
			}
		})
	}
}

func TestCX14LegacyB10DiagnosticIndexAndReleaseElevenStatus(t *testing.T) {
	s, i := summaryTestFixture(t, 0x312, summaryTestPayload("B", 10), true)
	b := DecodePCSSummaries(s, i).Bits["B/10"]
	if !reflect.DeepEqual(b.Alternatives, []int{4}) || b.Field.Status != "accepted" || b.Field.Grade != "Hypothesis" {
		t.Fatal(b)
	}
	s, i = summaryTestFixture(t, 0x312, [8]byte{}, true)
	b = DecodePCSSummaries(s, i).Bits["B/10"]
	if b.Field.Status != "accepted" || len(b.Alternatives) != 0 || b.Field.Numerator != 0 {
		t.Fatal(b)
	}
	s, i = summaryTestFixture(t, 0x312, summaryTestPayload("B", 10), false)
	b = DecodePCSSummaries(s, i).Bits["B/10"]
	if len(b.Alternatives) != 0 || b.Field.Meaning != "parallel status selector 4; no local diagnostic index" {
		t.Fatal(b)
	}
}
