package link

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type vectorFile struct {
	Cases []vectorCase `json:"cases"`
}

type vectorCase struct {
	Name      string                  `json:"name"`
	Family    string                  `json:"family"`
	Payload   string                  `json:"payload"`
	Selection vectorSelection         `json:"selection"`
	Status    string                  `json:"status"`
	Rows      map[string]vectorExpect `json:"rows"`
	Absent    []string                `json:"absent"`
	Attr      *vectorAttribution      `json:"attribution"`
}

type vectorSelection struct {
	Project, Release, Choice, Group, Slot, Bank, Source, Destination string
}

type vectorAttribution struct {
	Bank     string `json:"bank"`
	Group    string `json:"group"`
	Slot     string `json:"slot"`
	HasGroup bool   `json:"has_group"`
	HasSlot  bool   `json:"has_slot"`
}

type vectorExpect struct {
	Value       string `json:"value"`
	Unit        string `json:"unit"`
	Disposition string `json:"disposition"`
	Raw         string `json:"raw"`
}

func loadVectors(t *testing.T) []vectorCase {
	t.Helper()
	data, err := os.ReadFile("testdata/telemetry_1800_1808.json")
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func hexByte(t *testing.T, text string) uint8 {
	t.Helper()
	n, err := strconv.ParseUint(text, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	return uint8(n)
}

func inputFor(t *testing.T, c vectorCase, payload []byte) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	v := c.Selection
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: v.Project, Release: v.Release, Choice: v.Choice,
		Direction: "module-to-coordinator", Scope: "local", Bank: v.Bank, Endpoint: v.Source, Group: v.Group, Slot: v.Slot, RouteSet: true,
		Destination: hexByte(t, v.Destination), Source: hexByte(t, v.Source), Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	family, err := strconv.ParseUint(c.Family, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	id, err := canbus.NewExtendedID(uint32(family)<<16 | uint32(spec.Destination)<<8 | uint32(spec.Source))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return s, canbusreg.LINKInput{Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 3, Monotonic: time.Second}, Epoch: 1,
		Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}
}

func caseInput(t *testing.T, c vectorCase) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return inputFor(t, c, payload)
}

func evaluated(t *testing.T, c vectorCase) Result {
	t.Helper()
	s, in := caseInput(t, c)
	res, reason, err := Evaluate(s, in)
	if err != nil || reason != "" {
		t.Fatalf("%s: evaluate refused: %q %v", c.Name, reason, err)
	}
	return res
}

func ratOf(t *testing.T, d semreg.Decimal) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", d.Coefficient, d.Exponent10))
	if !ok {
		t.Fatalf("decimal %v is not a number", d)
	}
	return r
}

func applicable(res Result) map[string]roweval.Evaluation {
	out := make(map[string]roweval.Evaluation)
	for id, ev := range res.Rows {
		if ev.Reason != roweval.ReasonNoMatch || ev.Native != nil {
			out[id] = ev
		}
	}
	return out
}

func TestTelemetryRowsMatchThePageVectors(t *testing.T) {
	for _, c := range loadVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			res := evaluated(t, c)
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
				if want.Disposition == "invalid" {
					if n.Value != nil || n.Reason == "" {
						t.Errorf("row %s: an invalid word keeps no value and states its reason", id)
					}
					continue
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
			if c.Attr != nil {
				a := res.Attribution
				if a.Bank != c.Attr.Bank || a.Group != c.Attr.Group || a.Slot != c.Attr.Slot || a.HasGroup != c.Attr.HasGroup || a.HasSlot != c.Attr.HasSlot {
					t.Errorf("attribution %+v, page %+v", a, *c.Attr)
				}
			}
		})
	}
}

func ratText(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("expectation %q is not a number", s)
	}
	return r
}

func TestTelemetryRowsEqualTheFrozenDecoder(t *testing.T) {
	status := map[string]string{"accepted": "accepted", "raw": "raw", "invalid": "invalid"}
	for _, c := range loadVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in := caseInput(t, c)
			res := evaluated(t, c)
			decoded := canbusreg.DecodeLINKTelemetry(s, in)
			if decoded.Status != res.Status {
				t.Errorf("decoder status %q, evaluator %q", decoded.Status, res.Status)
			}
			got := applicable(res)
			seen := make(map[string]bool)
			for id, ev := range got {
				field, ok := decoded.Fields[rowField(id)]
				if !ok {
					if res.Status != "invalid" {
						t.Errorf("row %s has no decoder field", id)
					}
					continue
				}
				seen[rowField(id)] = true
				n := ev.Native
				if status[field.Status] != n.Disposition || field.Unit != n.Unit {
					t.Errorf("%s: decoder %q %q, evaluator %q %q", id, field.Status, field.Unit, n.Disposition, n.Unit)
				}
				if n.Value != nil && n.Value.Quantity != nil {
					want := big.NewRat(field.Numerator, field.Denominator)
					if ratOf(t, n.Value.Quantity.Number).Cmp(want) != 0 {
						t.Errorf("%s: decoder value %v, evaluator %+v", id, want, n.Value.Quantity.Number)
					}
				}
				if field.Evidence != res.Evidence || field.Group != res.Attribution.Group || field.Slot != res.Attribution.Slot || field.Bank != res.Attribution.Bank {
					t.Errorf("%s: decoder evidence or attribution differs from the evaluator", id)
				}
				if res.Grades[id] != field.Grade {
					t.Errorf("%s: grade %q, decoder %q", id, res.Grades[id], field.Grade)
				}
			}
			for name := range decoded.Fields {
				if !seen[name] {
					t.Errorf("decoder field %s has no applicable row", name)
				}
			}
		})
	}
}

func TestEveryRowAndUnitVariantHasAVector(t *testing.T) {
	type variant struct{ id, unit string }
	covered := make(map[variant]bool)
	invalid := make(map[string]bool)
	for _, c := range loadVectors(t) {
		for id, want := range c.Rows {
			covered[variant{id, want.Unit}] = true
			if want.Disposition == "invalid" {
				invalid[id] = true
			}
		}
	}
	for _, row := range Table().Native {
		if !covered[variant{row.ID, row.Unit}] {
			t.Errorf("row %s with unit %q has no vector", row.ID, row.Unit)
		}
	}
	for _, id := range []string{"growatt.bms.link.project_code", "growatt.bms.link.release", "growatt.bms.link.1803.b0"} {
		if !invalid[id] {
			t.Errorf("row %s has no invalid vector", id)
		}
	}
}

func TestTableIsValidAndRowIdsNameTheirFrame(t *testing.T) {
	if err := roweval.ValidateTable(Table(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range Table().Native {
		field := rowField(row.ID)
		qualified := strings.Count(strings.TrimPrefix(row.ID, rowPrefix), ".") == 1
		if qualified != sharedNames[field] {
			t.Errorf("row %s: frame qualifier %v, shared name %v", row.ID, qualified, sharedNames[field])
		}
		if qualified && !strings.HasPrefix(row.ID, fmt.Sprintf("%s%04X.", rowPrefix, row.At.Family)) {
			t.Errorf("row %s does not carry its own frame", row.ID)
		}
	}
}

func rowField(id string) string { return id[strings.LastIndex(id, ".")+1:] }

func TestRowsApplyOnlyToTheirFrameGeometry(t *testing.T) {
	cases := []struct {
		name  string
		frame func() (canbus.Frame, error)
	}{
		{"neighbouring family", func() (canbus.Frame, error) { return extendedFrame(0x18090000, 8) }},
		{"short payload", func() (canbus.Frame, error) { return extendedFrame(0x1805f402, 4) }},
		{"standard identifier", func() (canbus.Frame, error) {
			id, err := canbus.NewStandardID(0x105)
			if err != nil {
				return canbus.Frame{}, err
			}
			return canbus.NewFrame(id, make([]byte, 8))
		}},
	}
	q := Qualifiers(mustSelection(t), canbusreg.LINKInput{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frame, err := c.frame()
			if err != nil {
				t.Fatal(err)
			}
			for _, family := range []uint16{0x1800, 0x1805, 0x1808} {
				row := Table().Native[0]
				row.At.Family = family
				a := roweval.Acquired[Selector]{Selector: frameSelector(frame, row.At), Payload: frame.Data(), Qualifiers: q, Descriptors: Descriptors()}
				ev, err := roweval.Evaluate(roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{row}, Hooks: Table().Hooks}, a)
				if err != nil || ev.Reason != roweval.ReasonNoMatch {
					t.Errorf("family %04X: %v %v", family, ev.Reason, err)
				}
			}
		})
	}
}

func extendedFrame(id uint32, length int) (canbus.Frame, error) {
	ident, err := canbus.NewExtendedID(id)
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(ident, make([]byte, length))
}

func mustSelection(t *testing.T) canbusreg.Selection {
	t.Helper()
	c := loadVectors(t)[0]
	s, _ := caseInput(t, c)
	return s
}

func TestEvaluationRefusesWhatTheDecoderRefuses(t *testing.T) {
	base := loadVectors(t)[0]
	t.Run("wrong data length", func(t *testing.T) {
		s, in := inputFor(t, base, make([]byte, 7))
		res, reason, _ := Evaluate(s, in)
		if reason != "invalid" || len(res.Rows) != 0 || canbusreg.DecodeLINKTelemetry(s, in).Status != "invalid" {
			t.Errorf("%q %d", reason, len(res.Rows))
		}
	})
	t.Run("source address FF", func(t *testing.T) {
		c := base
		c.Selection.Source = "FF"
		iface, _ := canbus.NewInterfaceIdentity("synthetic", 1)
		_, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006",
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "FF", RouteSet: true, Destination: 0xf4, Source: 0xff, Epoch: 1})
		if err == nil {
			t.Error("a LINK selection with endpoint FF was accepted")
		}
	})
	for name, mutate := range map[string]func(*canbusreg.LINKInput){
		"asserted bank":      func(in *canbusreg.LINKInput) { in.Bank = "parallel" },
		"asserted group":     func(in *canbusreg.LINKInput) { in.Group = "1" },
		"asserted slot":      func(in *canbusreg.LINKInput) { in.Slot = "9" },
		"epoch":              func(in *canbusreg.LINKInput) { in.Epoch = 2 },
		"negative field age": func(in *canbusreg.LINKInput) { in.FieldAge = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			s, in := caseInput(t, base)
			mutate(&in)
			res, reason, _ := Evaluate(s, in)
			if reason != "invalid" || len(res.Rows) != 0 || canbusreg.DecodeLINKTelemetry(s, in).Status != "invalid" {
				t.Errorf("%q %d", reason, len(res.Rows))
			}
		})
	}
	t.Run("destination other than the coordinator", func(t *testing.T) {
		c := base
		c.Selection.Destination = "F5"
		s, in := caseInput(t, c)
		res, reason, _ := Evaluate(s, in)
		if reason != "invalid" || len(res.Rows) != 0 || canbusreg.DecodeLINKTelemetry(s, in).Status != "invalid" {
			t.Errorf("%q %d", reason, len(res.Rows))
		}
	})
	t.Run("neighbouring family", func(t *testing.T) {
		c := base
		c.Family = "1809"
		res := func() Result { s, in := caseInput(t, c); r, reason, _ := Evaluate(s, in); _ = reason; return r }()
		if len(res.Rows) != 0 {
			t.Errorf("rows for a family outside the table: %d", len(res.Rows))
		}
	})
}
