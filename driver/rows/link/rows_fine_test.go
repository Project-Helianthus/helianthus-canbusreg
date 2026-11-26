package link

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type fineFile struct {
	Cases []fineCase `json:"cases"`
}

type fineCase struct {
	Name      string                  `json:"name"`
	Family    string                  `json:"family"`
	Payload   string                  `json:"payload"`
	Selection vectorSelection         `json:"selection"`
	Status    string                  `json:"status"`
	Rows      map[string]vectorExpect `json:"rows"`
	Absent    []string                `json:"absent"`
	Fine      *fineGates              `json:"fine"`
}

type fineGates struct {
	FineValid    bool         `json:"fine_valid"`
	Maximum      string       `json:"maximum"`
	Minimum      string       `json:"minimum"`
	Mean         string       `json:"mean"`
	Contributors contributors `json:"contributors"`
}

type contributors struct {
	Count       int    `json:"count"`
	CountValid  bool   `json:"count_valid"`
	Payload     string `json:"payload"`
	StaleSensor int    `json:"stale_sensor"`
	FixedFour   bool   `json:"fixed_four"`
}

func loadFine(t *testing.T) []fineCase {
	t.Helper()
	data, err := os.ReadFile("testdata/telemetry_fine.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fineFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func gateOf(kind string) canbusreg.MeasurementGate {
	switch kind {
	case "stale":
		return canbusreg.MeasurementGate{Configured: true, Status: "stale"}
	case "unconfigured":
		return canbusreg.MeasurementGate{}
	}
	return canbusreg.MeasurementGate{Configured: true, Valid: true}
}

func (c fineCase) asVector() vectorCase {
	return vectorCase{Name: c.Name, Family: c.Family, Payload: c.Payload, Selection: c.Selection}
}

func fineInput(t *testing.T, c fineCase) (canbusreg.Selection, canbusreg.LINKInput, canbusreg.SummaryGates) {
	t.Helper()
	s, in := caseInput(t, c.asVector())
	var gates canbusreg.SummaryGates
	if c.Fine == nil {
		return s, in, gates
	}
	f := c.Fine
	sensors := vectorCase{Name: c.Name, Family: "1830", Payload: f.Contributors.Payload, Selection: c.Selection}
	_, sensor := caseInput(t, sensors)
	sensor.Evidence.Sequence = 2
	vector := canbusreg.NewLINKTemperatures(s, f.Contributors.Count, f.Contributors.CountValid)
	var cells [4]canbusreg.MeasurementGate
	for i := range cells {
		cells[i] = canbusreg.MeasurementGate{Configured: true, Valid: true}
		if i == f.Contributors.StaleSensor {
			cells[i].Age = 2 * time.Second
		}
	}
	vector.DecodeLINKVector(sensor, cells)
	return s, in, canbusreg.SummaryGates{FineTemperatureValid: f.FineValid, Maximum: gateOf(f.Maximum), Minimum: gateOf(f.Minimum), Mean: gateOf(f.Mean),
		Contributors: vector.SnapshotLINKVector(time.Second, time.Second), FixedFourPath: f.Contributors.FixedFour}
}

func fineEvaluated(t *testing.T, c fineCase) Result {
	t.Helper()
	s, in, gates := fineInput(t, c)
	res, reason, err := EvaluateFine(s, in, gates)
	if err != nil || reason != "" && reason != roweval.ReasonNoMatch {
		t.Fatalf("%s: evaluate refused: %q %v", c.Name, reason, err)
	}
	return res
}

func TestFineRowsMatchThePageVectors(t *testing.T) {
	for _, c := range loadFine(t) {
		t.Run(c.Name, func(t *testing.T) {
			res := fineEvaluated(t, c)
			if res.Status != c.Status {
				t.Errorf("status %q, page %q", res.Status, c.Status)
			}
			got := applicable(res)
			for id := range got {
				if _, ok := c.Rows[id]; !ok {
					t.Errorf("row %s applies but the page vector does not name it", id)
				}
			}
			for id, want := range c.Rows {
				ev, ok := got[id]
				if !ok || ev.Native == nil {
					t.Errorf("row %s does not apply", id)
					continue
				}
				n := ev.Native
				if n.Unit != want.Unit || n.Disposition != want.Disposition || strconv.FormatUint(n.Raw, 10) != want.Raw {
					t.Errorf("row %s: unit %q disposition %q raw %d, page %q %q %s", id, n.Unit, n.Disposition, n.Raw, want.Unit, want.Disposition, want.Raw)
				}
				if n.Value == nil || n.Value.Quantity == nil || ratOf(t, n.Value.Quantity.Number).Cmp(ratText(t, want.Value)) != 0 {
					t.Errorf("row %s: value %+v, page %s", id, n.Value, want.Value)
				}
			}
			for _, id := range c.Absent {
				if _, ok := got[id]; ok {
					t.Errorf("row %s applies under a selection the page excludes it from", id)
				}
			}
		})
	}
}

func TestFineRowsEqualTheFrozenDecoders(t *testing.T) {
	for _, c := range loadFine(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in, gates := fineInput(t, c)
			res := fineEvaluated(t, c)
			var status string
			var fields map[string]canbusreg.NativeField
			if c.Family == "1862" {
				d := canbusreg.DecodeLINKTemperatureSummary(s, in, gates)
				status, fields = d.Status, d.Fields
			} else {
				d := canbusreg.DecodeLINKTelemetry(s, in)
				status, fields = d.Status, d.Fields
			}
			if status != res.Status {
				t.Errorf("decoder status %q, evaluator %q", status, res.Status)
			}
			seen := make(map[string]bool)
			for id, ev := range applicable(res) {
				field, ok := fields[rowField(id)]
				if !ok {
					t.Errorf("row %s has no decoder field", id)
					continue
				}
				seen[rowField(id)] = true
				n := ev.Native
				if field.Status != n.Disposition || field.Unit != n.Unit {
					t.Errorf("%s: decoder %q %q, evaluator %q %q", id, field.Status, field.Unit, n.Disposition, n.Unit)
				}
				if want := big.NewRat(field.Numerator, field.Denominator); n.Value == nil || n.Value.Quantity == nil || ratOf(t, n.Value.Quantity.Number).Cmp(want) != 0 {
					t.Errorf("%s: decoder value %v, evaluator %+v", id, want, n.Value)
				}
				if field.Evidence != res.Evidence || field.Group != res.Attribution.Group || field.Slot != res.Attribution.Slot || field.Bank != res.Attribution.Bank {
					t.Errorf("%s: decoder evidence or attribution differs from the evaluator", id)
				}
				if res.Grades[id] != field.Grade {
					t.Errorf("%s: grade %q, decoder %q", id, res.Grades[id], field.Grade)
				}
			}
			for name := range fields {
				if !seen[name] {
					t.Errorf("decoder field %s has no applicable row", name)
				}
			}
		})
	}
}

func TestEveryFineRowAndUnitVariantHasAVectorUnderEachBank(t *testing.T) {
	type variant struct{ id, unit, bank string }
	covered := make(map[variant]bool)
	for _, c := range loadFine(t) {
		for id, want := range c.Rows {
			covered[variant{id, want.Unit, c.Selection.Bank}] = true
		}
	}
	for _, row := range FineTable().Native {
		if !covered[variant{row.ID, row.Unit, row.When.Equal[qBank]}] {
			t.Errorf("row %s with unit %q under bank %q has no vector", row.ID, row.Unit, row.When.Equal[qBank])
		}
	}
}

func TestFineTableIsValidAndEachWhenCarriesItsConditions(t *testing.T) {
	if err := roweval.ValidateTable(FineTable(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range FineTable().Native {
		when := row.When.Equal
		if when[qBank] != "ordinary" && when[qBank] != "parallel" {
			t.Errorf("row %s: asserted bank %q", row.ID, when[qBank])
		}
		qualified := strings.Count(strings.TrimPrefix(row.ID, rowPrefix), ".") == 1
		if qualified != sharedNames[rowField(row.ID)] {
			t.Errorf("row %s: frame qualifier %v, shared name %v", row.ID, qualified, sharedNames[rowField(row.ID)])
		}
		switch row.At.Family {
		case 0x1862:
			if when[qFineGroup] != "true" || !qualified {
				t.Errorf("row %s: the 1862 rows carry the group condition and the frame qualifier", row.ID)
			}
		case 0x180b:
			if when[qGroupPresent] != "true" || when[qLayout] == "" {
				t.Errorf("row %s: the 180B rows carry the resolved group and the layout class", row.ID)
			}
		default:
			t.Errorf("row %s belongs to family %04X", row.ID, row.At.Family)
		}
	}
}

func TestMeanRowsOfTheTwoFamiliesKeepTheirQualifiedIds(t *testing.T) {
	ids := map[string]bool{}
	for _, row := range Table().Native {
		ids[row.ID] = true
	}
	fine := map[string]bool{}
	for _, row := range FineTable().Native {
		fine[row.ID] = true
		if ids[row.ID] {
			t.Errorf("row %s is in both tables", row.ID)
		}
	}
	for _, id := range []string{"growatt.bms.link.1862.maximum", "growatt.bms.link.1862.minimum", "growatt.bms.link.1862.mean", "growatt.bms.link.1862.reserved", "growatt.bms.link.180B.b4", "growatt.bms.link.180B.b5"} {
		if !fine[id] {
			t.Errorf("missing row %s", id)
		}
	}
	if !ids["growatt.bms.link.1806.maximum"] || fine["growatt.bms.link.mean"] || fine["growatt.bms.link.maximum"] {
		t.Error("the 1806 and 1862 rows must stay frame-qualified")
	}
}

func TestEveryConditionOfAFineRowSelectsIt(t *testing.T) {
	frames := map[uint16]string{0x1862: "1862", 0x180b: "180B"}
	for _, row := range FineTable().Native {
		q := make(map[string]string)
		for k, v := range row.When.Equal {
			q[k] = v
		}
		payload := make([]byte, 8)
		ident, err := canbus.NewExtendedID(uint32(row.At.Family)<<16 | 0xf400 | 0x02)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := canbus.NewFrame(ident, payload)
		if err != nil {
			t.Fatal(err)
		}
		single := roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: FineTable().Hooks}
		apply := func(values map[string]string) roweval.Reason {
			a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: payload, Qualifiers: roweval.Qualifiers{Values: values}, Descriptors: Descriptors()}
			ev, err := roweval.Evaluate(single, a)
			if err != nil {
				t.Fatal(err)
			}
			return ev.Reason
		}
		if apply(q) == roweval.ReasonNoMatch {
			t.Errorf("row %s %s does not apply under its own conditions", frames[row.At.Family], row.ID)
		}
		for key := range row.When.Equal {
			other := make(map[string]string, len(q))
			for k, v := range q {
				other[k] = v
			}
			other[key] = other[key] + "-other"
			if apply(other) != roweval.ReasonNoMatch {
				t.Errorf("row %s applies although condition %s differs", row.ID, key)
			}
		}
	}
}

func TestFineRowsApplyOnlyToTheirFrameGeometry(t *testing.T) {
	q := FineQualifiers(mustFineSelection(t), canbusreg.LINKInput{}, canbusreg.SummaryGates{})
	q.Values[qGroupPresent], q.Values[qFineGroup] = "true", "true"
	cases := map[string]func() (canbus.Frame, error){
		"neighbouring family below": func() (canbus.Frame, error) { return extendedFrame(0x1861f402, 8) },
		"neighbouring family above": func() (canbus.Frame, error) { return extendedFrame(0x180cf402, 8) },
		"short payload":             func() (canbus.Frame, error) { return extendedFrame(0x1862f402, 4) },
		"standard identifier": func() (canbus.Frame, error) {
			id, err := canbus.NewStandardID(0x162)
			if err != nil {
				return canbus.Frame{}, err
			}
			return canbus.NewFrame(id, make([]byte, 8))
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			frame, err := build()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range FineTable().Native {
				if uint16(frame.ID().Value()>>16) == row.At.Family && frame.ID().Extended() && frame.DLC() == 8 {
					continue
				}
				a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
				ev, err := roweval.Evaluate(roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: FineTable().Hooks}, a)
				if err != nil || ev.Reason != roweval.ReasonNoMatch && frame.ID().Extended() && uint16(frame.ID().Value()>>16) != row.At.Family {
					t.Errorf("row %s: %v %v", row.ID, ev.Reason, err)
				}
			}
		})
	}
}

func mustFineSelection(t *testing.T) canbusreg.Selection {
	t.Helper()
	s, _, _ := fineInput(t, loadFine(t)[0])
	return s
}

func TestFineEvaluationRefusesWhatTheDecodersRefuse(t *testing.T) {
	base := loadFine(t)[0]
	cases := map[string]func(*fineCase, *canbusreg.LINKInput){
		"wrong data length":  nil,
		"asserted bank":      func(_ *fineCase, in *canbusreg.LINKInput) { in.Bank = "parallel" },
		"asserted group":     func(_ *fineCase, in *canbusreg.LINKInput) { in.Group = "1" },
		"asserted slot":      func(_ *fineCase, in *canbusreg.LINKInput) { in.Slot = "9" },
		"epoch":              func(_ *fineCase, in *canbusreg.LINKInput) { in.Epoch = 2 },
		"negative field age": func(_ *fineCase, in *canbusreg.LINKInput) { in.FieldAge = -time.Second },
		"destination":        func(c *fineCase, _ *canbusreg.LINKInput) { c.Selection.Destination = "F5" },
		"no frame sequence":  func(_ *fineCase, in *canbusreg.LINKInput) { in.Evidence.Sequence = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			if name == "wrong data length" {
				s, in := inputFor(t, c.asVector(), make([]byte, 7))
				res, reason, _ := EvaluateFine(s, in, canbusreg.SummaryGates{})
				if reason != "invalid" || len(res.Rows) != 0 {
					t.Errorf("%q %d", reason, len(res.Rows))
				}
				return
			}
			s, in, gates := fineInput(t, c)
			if c.Selection.Destination == "F5" {
				t.Fatal("base case is not the coordinator route")
			}
			if cm := (fineCase{Selection: c.Selection}); mutate != nil {
				mutate(&cm, &in)
				if cm.Selection.Destination != c.Selection.Destination {
					c.Selection = cm.Selection
					s, in, gates = fineInput(t, c)
				}
			}
			res, reason, _ := EvaluateFine(s, in, gates)
			if reason != "invalid" || len(res.Rows) != 0 {
				t.Errorf("%q %d", reason, len(res.Rows))
			}
		})
	}
	t.Run("source address FF", func(t *testing.T) {
		iface, _ := canbus.NewInterfaceIdentity("synthetic", 1)
		_, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0011",
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "FF", RouteSet: true, Destination: 0xf4, Source: 0xff, Epoch: 1})
		if err == nil {
			t.Error("a LINK selection with endpoint FF was accepted")
		}
	})
	t.Run("neighbouring family", func(t *testing.T) {
		c := base
		c.Family = "1861"
		s, in, gates := fineInput(t, c)
		res, _, _ := EvaluateFine(s, in, gates)
		if len(res.Rows) != 0 || res.Status != "unsupported" {
			t.Errorf("%q %d", res.Status, len(res.Rows))
		}
	})
}

func TestFineEvidenceIsTheEvidenceOfTheFrame(t *testing.T) {
	c := loadFine(t)[0]
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	res := fineEvaluated(t, c)
	if res.Evidence.Frame.Bytes() != [8]byte(payload) {
		t.Errorf("evidence %v, frame %x", res.Evidence.Frame.Bytes(), payload)
	}
}
