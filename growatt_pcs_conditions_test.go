package canbusreg

import (
	"fmt"
	"testing"
)

func TestGrowattPCSAndLINKNamedFaultBits(t *testing.T) {
	link := dtcTestMember(t, true)
	for _, tc := range []struct {
		index int
		want  string
	}{
		{0, "Cell overvoltage — condition 0"}, {1, "Cell overvoltage — condition 1"}, {10, ""}, {62, "Maximum cell voltage high"}, {93, "Parallel peer-word spread in 61X.B4–B5"}, {134, "Measurement status flag set"}, {136, ""},
	} {
		var data [8]byte
		data[tc.index%64/8] = 1 << uint(tc.index%8)
		lr := DecodeLINKDiagnostics(link, selectionTestInput(t, link.SelectionSpecValue(), 0x1850+uint32(tc.index/64), data))
		c := parallelIdentityTestSpec(t)
		c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "release11"
		pcs := selectionTestSelect(t, c)
		pid := []uint32{0x663, 0x673, 0x503}[tc.index/64]
		pr := ParallelIdentityDecodeParallel(pcs, parallelIdentityTestInput(t, c, pid, data))
		for _, r := range []NativeResult{lr, pr} {
			key := fmt.Sprintf("current_fault_%d", tc.index)
			f := r.Fields[key]
			if f.Numerator != 1 || f.Raw != data || f.Meaning != tc.want || (f.Status == "raw") != (tc.want == "") {
				t.Errorf("%s %s: %+v", r.Selection.spec.Flavor, key, f)
			}
		}
	}
}

func TestGrowattPCSSummaryNamesDoNotChooseACause(t *testing.T) {
	s, i := standardTestFixture(t, 0x312, [8]byte{1 << 4})
	s, i = standardTestReselect(t, s, i, func(c *SelectionSpec) { c.Release = "0011" })
	result := DecodePCSSummaries(s, PCSSummaryPCSInput{PCSStandardPCSInput: i})
	got := result.Bits["A/4"]
	if len(got.Alternatives) != 2 || got.Alternatives[0] != 1 || got.Alternatives[1] != 62 || got.Field.Meaning != "bounded numeric OR alternatives; no unique attribution: Cell overvoltage — condition 1 OR Maximum cell voltage high" {
		t.Fatal(got)
	}
	if raw := result.Bits["A/8"].Field; raw.Status != "raw" || raw.Meaning != "" {
		t.Fatal(raw)
	}
}
