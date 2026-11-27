package link

import (
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// appliedByTheCorpus evaluates every vector of every suite of the package, the
// cell mean and the balance enable states, and returns the row ids that
// applied to at least one observation.
func appliedByTheCorpus(t *testing.T) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	add := func(res Result) {
		for id := range applicable(res) {
			got[id] = true
		}
	}
	for _, c := range loadVectors(t) {
		add(evaluated(t, c))
	}
	for _, c := range loadFine(t) {
		add(fineEvaluated(t, c))
	}
	for _, c := range pageCases(t) {
		res, _ := pageEvaluate(t, c)
		add(res)
	}
	for _, c := range diagVectors(t) {
		add(diagEvaluate(t, c).res)
	}
	for _, c := range loadPositionCases(t) {
		s, in := c.input(t)
		res, _, err := EvaluateVector(s, in, c.state(), c.gates())
		if err != nil {
			t.Fatal(err)
		}
		add(res)
	}
	accepted := canbusreg.MeasurementGate{Configured: true, Valid: true}
	s, in := meanFrame(t, "0011", 3301)
	res, _, err := EvaluateCellMean(s, in, accepted, vectorContributors(t, "0011", 4), false)
	if err != nil {
		t.Fatal(err)
	}
	add(res)
	s, in = balanceInput(t, "0006", 0xA005)
	res, _, err = EvaluateBalance(s, in, 16, true, accepted, true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	add(res)
	return got
}

func TestEveryNativeRowOfTheAssembledTableAppliesToAVector(t *testing.T) {
	applied := appliedByTheCorpus(t)
	rows := AllTable().Native
	if len(rows) == 0 {
		t.Fatal("the assembled table has no rows")
	}
	for _, r := range rows {
		if !applied[r.ID] {
			t.Errorf("row %s applies to no vector", r.ID)
		}
	}
}

func TestEveryFieldKeyTheTelemetryDecoderEmitsOverTheCorpusHasARow(t *testing.T) {
	ids := map[string]bool{}
	for _, r := range AllTable().Native {
		ids[r.ID] = true
	}
	seen := 0
	for _, c := range loadVectors(t) {
		s, in := caseInput(t, c)
		for field := range canbusreg.DecodeLINKTelemetry(s, in).Fields {
			seen++
			family := strings.ToUpper(c.Family)
			if !ids[rowPrefix+field] && !ids[rowPrefix+family+"."+field] {
				t.Errorf("%s: decoder field %s has no row", c.Name, field)
			}
		}
	}
	if seen == 0 {
		t.Fatal("the decoder emitted no field over the corpus")
	}
}

func TestRowIdsAreUniqueWithinEachPartAndEveryHookExists(t *testing.T) {
	table := AllTable()
	for _, p := range tableParts() {
		seen := map[string]bool{}
		for _, r := range p.Native {
			if r.ID == "" || !strings.HasPrefix(r.ID, rowPrefix) {
				t.Errorf("row id %q is outside the package namespace", r.ID)
			}
			seen[r.ID] = true
		}
	}
	for _, r := range table.Native {
		if _, ok := table.Hooks[r.Decode.Hook]; !ok {
			t.Errorf("row %s names the missing hook %s", r.ID, r.Decode.Hook)
		}
		if !r.At.Extended || r.At.DLC != 8 || r.At.RawDLC != 8 {
			t.Errorf("row %s is not on an extended identifier with DLC 8 and raw DLC 8", r.ID)
		}
	}
}

// TestNoRowAppliesToAForeignFrameGeometry sweeps every row of the assembled
// table over a neighbouring family, a short and an empty payload, a standard
// identifier and a source address FF: none of them is a frame the row reads.
func TestNoRowAppliesToAForeignFrameGeometry(t *testing.T) {
	table := AllTable()
	q := Qualifiers(mustSelection(t), canbusreg.LINKInput{})
	standard, err := canbus.NewStandardID(0x105)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range table.Native {
		family := uint32(row.At.Family)
		frames := map[string]func() (canbus.Frame, error){
			"neighbouring family above": func() (canbus.Frame, error) { return extendedFrame((family+0x100)<<16|0xf402, 8) },
			"neighbouring family below": func() (canbus.Frame, error) { return extendedFrame((family-0x100)<<16|0xf402, 8) },
			"seven data bytes":          func() (canbus.Frame, error) { return extendedFrame(family<<16|0xf402, 7) },
			"no data bytes":             func() (canbus.Frame, error) { return extendedFrame(family<<16|0xf402, 0) },
			"standard identifier":       func() (canbus.Frame, error) { return canbus.NewFrame(standard, make([]byte, 8)) },
		}
		for name, build := range frames {
			frame, err := build()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
			ev, err := roweval.Evaluate(roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: table.Hooks}, a)
			if err != nil || ev.Reason != roweval.ReasonNoMatch {
				t.Errorf("%s: %s gave %q %v", row.ID, name, ev.Reason, err)
			}
		}
	}
}

func TestSourceAddressFFIsNeverAdmittedByAnyEvaluator(t *testing.T) {
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range []string{"0006", "0011"} {
		_, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: release,
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "FF", RouteSet: true, Destination: 0xf4, Source: 0xff, Epoch: 1})
		if err == nil {
			t.Errorf("release %s: a LINK selection with endpoint FF was accepted", release)
		}
	}
}

func TestEveryTelemetryVectorIsRefusedUnderTheOtherBankAndAWrongLength(t *testing.T) {
	other := map[string]string{"ordinary": "parallel", "parallel": "ordinary"}
	banks := map[string]bool{}
	for _, c := range loadVectors(t) {
		banks[c.Selection.Bank] = true
		s, in := caseInput(t, c)
		in.Bank = other[c.Selection.Bank]
		if res, reason, _ := Evaluate(s, in); reason != "invalid" || len(res.Rows) != 0 {
			t.Errorf("%s: asserted bank mismatch gave %q with %d rows", c.Name, reason, len(res.Rows))
		}
		s, in = inputFor(t, c, make([]byte, 7))
		if res, reason, _ := Evaluate(s, in); reason != "invalid" || len(res.Rows) != 0 {
			t.Errorf("%s: seven data bytes gave %q with %d rows", c.Name, reason, len(res.Rows))
		}
	}
	if !banks["ordinary"] {
		t.Error("no vector asserts the ordinary bank")
	}
}

func TestEveryBindingNamesAnAssertedBankAndAnExistingNativeRow(t *testing.T) {
	table := AllTable()
	native := map[string]bool{}
	for _, r := range table.Native {
		native[r.ID] = true
	}
	for _, b := range table.Bindings {
		if !native[b.NativeID] {
			t.Errorf("binding of %s names no native row", b.NativeID)
		}
		if bank := b.When.Equal[qBank]; bank != "ordinary" && bank != "parallel" {
			t.Errorf("binding of %s asserts bank %q", b.NativeID, bank)
		}
		if b.Write != nil {
			t.Errorf("binding of %s has a way back", b.NativeID)
		}
	}
}

func TestEveryNativeRowIsBoundOrListedAndNoInterlockIsBound(t *testing.T) {
	table := AllTable()
	bound := map[string]bool{}
	for _, b := range table.Bindings {
		bound[b.NativeID] = true
		if strings.Contains(string(b.Read.Fact.ID), "interlock") {
			t.Errorf("%s feeds %s", b.NativeID, b.Read.Fact.ID)
		}
	}
	listed := map[string]bool{}
	for _, n := range NativeOnly() {
		listed[n.ID] = true
		if bound[n.ID] {
			t.Errorf("%s is bound and listed", n.ID)
		}
	}
	for _, r := range table.Native {
		if !bound[r.ID] && !listed[r.ID] {
			t.Errorf("%s is neither bound nor listed", r.ID)
		}
	}
}

func TestClosureOfCommandsHasOneVectoredRowAndNoRowForAnyOtherFamily(t *testing.T) {
	rows := Commands()
	if len(rows) != 1 || rows[0].Family != 0x188A {
		t.Fatalf("command rows %v", rows)
	}
	vectors := loadClockVectors(t)
	if len(vectors) == 0 {
		t.Fatal("the clock command has no vector")
	}
	for _, r := range AllTable().Native {
		if strings.HasPrefix(r.ID, rowPrefix+"command.") {
			t.Errorf("native read row %s is a command", r.ID)
		}
	}
	var listed []string
	for _, f := range UncoveredFamilies() {
		listed = append(listed, f.Family)
		if f.Class != "C" && f.Class != "Q" || f.Gap == "" {
			t.Errorf("family %s: class %q gap %q", f.Family, f.Class, f.Gap)
		}
	}
	for _, want := range []string{"1860DFF4", "1870-1889"} {
		found := false
		for _, l := range listed {
			found = found || l == want
		}
		if !found {
			t.Errorf("%s is not listed as left without a row", want)
		}
	}
	for _, v := range vectors {
		frame, err := EncodeClockInput(clockOf(v))
		if err != nil || frame.ID().Value()>>16 != 0x188A {
			t.Errorf("%s: %v family %X", v.Name, err, frame.ID().Value()>>16)
		}
	}
}
