package link

import (
	"encoding/binary"
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
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type positionFile struct {
	Cases []positionCase `json:"cases"`
}

type positionCase struct {
	Name      string                  `json:"name"`
	Kind      string                  `json:"kind"`
	Family    string                  `json:"family"`
	Payload   string                  `json:"payload"`
	State     positionState           `json:"state"`
	Selection vectorSelection         `json:"selection"`
	Gates     []string                `json:"gates"`
	Reason    string                  `json:"reason"`
	Status    string                  `json:"status"`
	Rows      map[string]vectorExpect `json:"rows"`
	Attr      *vectorAttribution      `json:"attribution"`
}

type positionState struct {
	Count      int  `json:"count"`
	CountValid bool `json:"count_valid"`
	Fixed16    bool `json:"fixed16"`
}

func loadPositionCases(t *testing.T) []positionCase {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f positionFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func (c positionCase) state() VectorState {
	return VectorState{Kind: c.Kind, Count: c.State.Count, CountValid: c.State.CountValid, Fixed16: c.State.Fixed16}
}

func (c positionCase) gates() (g [4]canbusreg.MeasurementGate) {
	for j, name := range c.Gates {
		switch name {
		case "accepted":
			g[j] = canbusreg.MeasurementGate{Configured: true, Valid: true}
		case "unconfigured":
		default:
			g[j] = canbusreg.MeasurementGate{Configured: true, Status: name}
		}
	}
	return
}

func (c positionCase) input(t *testing.T) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return inputFor(t, vectorCase{Name: c.Name, Family: c.Family, Selection: c.Selection}, payload)
}

func (c positionCase) decoder(s canbusreg.Selection) *canbusreg.LINKVector {
	st := c.state()
	switch {
	case st.Kind == kindTemperatures:
		return canbusreg.NewLINKTemperatures(s, st.Count, st.CountValid)
	case st.Fixed16:
		return canbusreg.NewLINKFixed16Cells(s, st.Count, st.CountValid)
	}
	return canbusreg.NewLINKCells(s, st.Count, st.CountValid)
}

func decoderField(kind, id string) string {
	n := id[strings.LastIndex(id, "_")+1:]
	if kind == kindTemperatures {
		return "sensor_" + n
	}
	return "cell_" + n
}

func bigRat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func TestVectorRowsMatchThePositionExpectations(t *testing.T) {
	for _, c := range loadPositionCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			s, in := c.input(t)
			res, reason, err := EvaluateVector(s, in, c.state(), c.gates())
			if err != nil || string(reason) != c.Reason || res.Status != c.Status {
				t.Fatalf("reason %q status %q err %v, expected %q %q", reason, res.Status, err, c.Reason, c.Status)
			}
			got := applicable(res)
			if len(got) != len(c.Rows) {
				t.Errorf("%d rows apply, expected %d", len(got), len(c.Rows))
			}
			for suffix, want := range c.Rows {
				ev, ok := got[rowPrefix+suffix]
				if !ok || ev.Native == nil {
					t.Errorf("row %s does not apply", suffix)
					continue
				}
				n := ev.Native
				if n.Disposition != want.Disposition || n.Unit != want.Unit || strconv.FormatUint(n.Raw, 10) != want.Raw {
					t.Errorf("row %s: %q %q raw %d, expected %q %q %s", suffix, n.Disposition, n.Unit, n.Raw, want.Disposition, want.Unit, want.Raw)
				}
				if want.Disposition != "accepted" {
					if n.Value != nil {
						t.Errorf("row %s: %s keeps no value", suffix, want.Disposition)
					}
					continue
				}
				if n.Value == nil || n.Value.Quantity == nil || ratOf(t, n.Value.Quantity.Number).Cmp(ratText(t, want.Value)) != 0 {
					t.Errorf("row %s: value %+v, expected %s", suffix, n.Value, want.Value)
				}
			}
			if c.Attr != nil {
				a := res.Attribution
				if a.Bank != c.Attr.Bank || a.Group != c.Attr.Group || a.Slot != c.Attr.Slot || a.HasGroup != c.Attr.HasGroup || a.HasSlot != c.Attr.HasSlot {
					t.Errorf("attribution %+v, expected %+v", a, *c.Attr)
				}
			}
		})
	}
}

func TestVectorRowsEqualTheFrozenDecoder(t *testing.T) {
	for _, c := range loadPositionCases(t) {
		if c.Reason != "" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			s, in := c.input(t)
			res, _, err := EvaluateVector(s, in, c.state(), c.gates())
			if err != nil {
				t.Fatal(err)
			}
			decoded := c.decoder(s).DecodeLINKVector(in, c.gates())
			if decoded.Status != res.Status {
				t.Errorf("decoder status %q, evaluator %q", decoded.Status, res.Status)
			}
			seen := 0
			for id, ev := range applicable(res) {
				field, ok := decoded.Fields[decoderField(c.Kind, id)]
				if !ok {
					t.Errorf("row %s has no decoder field", id)
					continue
				}
				seen++
				n := ev.Native
				if field.Status != n.Disposition || field.Unit != n.Unit {
					t.Errorf("%s: decoder %q %q, evaluator %q %q", id, field.Status, field.Unit, n.Disposition, n.Unit)
				}
				if n.Value != nil && ratOf(t, n.Value.Quantity.Number).Cmp(bigRat(field.Numerator, field.Denominator)) != 0 {
					t.Errorf("%s: decoder %d/%d, evaluator %+v", id, field.Numerator, field.Denominator, n.Value.Quantity.Number)
				}
				if field.Status != "accepted" && n.Value != nil {
					t.Errorf("%s: a %s position keeps no value", id, field.Status)
				}
				if field.Evidence != res.Evidence || field.Raw != decoded.Raw || field.Group != res.Attribution.Group || field.Slot != res.Attribution.Slot || field.Bank != res.Attribution.Bank {
					t.Errorf("%s: decoder evidence or attribution differs from the evaluator", id)
				}
				if res.Grades[id] != field.Grade {
					t.Errorf("%s: grade %q, decoder %q", id, res.Grades[id], field.Grade)
				}
			}
			if seen != len(decoded.Fields) {
				t.Errorf("%d rows for %d decoder fields", seen, len(decoded.Fields))
			}
		})
	}
}

func TestEveryPositionRowHasAnAcceptedVector(t *testing.T) {
	accepted := make(map[string]bool)
	dispositions := make(map[string]bool)
	for _, c := range loadPositionCases(t) {
		for suffix, want := range c.Rows {
			dispositions[want.Disposition] = true
			if want.Disposition == "accepted" {
				accepted[rowPrefix+suffix] = true
			}
		}
	}
	for _, row := range VectorTable().Native {
		field := rowField(row.ID)
		if (strings.HasPrefix(field, "cell_voltage_") || strings.HasPrefix(field, "temperature_")) && !accepted[row.ID] {
			t.Errorf("row %s has no accepted vector", row.ID)
		}
	}
	for _, d := range []string{"accepted", "absent", "unconfigured", "invalid", "stale", "conflict"} {
		if !dispositions[d] {
			t.Errorf("no vector keeps the disposition %s", d)
		}
	}
	if err := roweval.ValidateTable(VectorTable(), Descriptors(), roweval.Roles{}); err != nil {
		t.Fatal(err)
	}
}

func TestVectorRowIdsNameTheirPosition(t *testing.T) {
	ids := make(map[string]uint16)
	for _, row := range VectorTable().Native {
		if _, dup := ids[row.ID]; dup {
			t.Errorf("row %s is defined twice", row.ID)
		}
		ids[row.ID] = row.At.Family
	}
	for n := 1; n <= 16; n++ {
		id := fmt.Sprintf("growatt.bms.link.cell_voltage_%d", n)
		if ids[id] != uint16(0x1810+(n-1)/4) {
			t.Errorf("%s is on family %04X", id, ids[id])
		}
	}
	for n := 1; n <= 4; n++ {
		if ids[fmt.Sprintf("growatt.bms.link.temperature_%d", n)] != 0x1830 {
			t.Errorf("temperature_%d is not on 1830", n)
		}
	}
	if ids["growatt.bms.link.1806.mean"] != 0x1806 {
		t.Error("the 1806 cell mean takes the frame qualifier")
	}
	if _, ok := ids["growatt.bms.link.mean"]; ok {
		t.Error("an unqualified mean row must not exist")
	}
}

func meanCase(release string) vectorCase {
	return vectorCase{Name: "mean", Family: "1806", Selection: vectorSelection{Project: "QAAA", Release: release, Bank: "ordinary", Source: "02", Destination: "F4"}}
}

func vectorContributors(t *testing.T, release string, count int) canbusreg.VectorSnapshot {
	t.Helper()
	c := meanCase(release)
	c.Family = "1810"
	words := make([]byte, 8)
	for j, v := range []uint16{3300, 3301, 3302, 3303} {
		binary.LittleEndian.PutUint16(words[j*2:], v)
	}
	s, in := inputFor(t, c, words)
	in.Evidence.Sequence = 1
	a := canbusreg.NewLINKCells(s, count, true)
	var g [4]canbusreg.MeasurementGate
	for j := range g {
		g[j] = canbusreg.MeasurementGate{Configured: true, Valid: true}
	}
	a.DecodeLINKVector(in, g)
	return a.SnapshotLINKVector(time.Second, 3*time.Second)
}

func meanFrame(t *testing.T, release string, mean uint16) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	p := make([]byte, 8)
	binary.LittleEndian.PutUint16(p[6:], mean)
	s, in := inputFor(t, meanCase(release), p)
	in.Evidence.Sequence = 2
	return s, in
}

func TestCellMeanRowFollowsTheDecoderAcceptance(t *testing.T) {
	accepted := canbusreg.MeasurementGate{Configured: true, Valid: true}
	cases := []struct {
		name        string
		release     string
		mean        uint16
		gate        canbusreg.MeasurementGate
		disposition string
		value       string
	}{
		{"matching mean of four contributors", "0011", 3301, accepted, "accepted", "3301"},
		{"mean word that is not the contributor mean", "0011", 3300, accepted, "raw", ""},
		{"stale gate", "0011", 3301, canbusreg.MeasurementGate{Configured: true, Status: "stale"}, "stale", ""},
		{"unconfigured gate", "0011", 3301, canbusreg.MeasurementGate{}, "raw", ""},
		{"selection outside the release eleven path", "0006", 3301, accepted, "raw", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snapshot := vectorContributors(t, c.release, 4)
			s, in := meanFrame(t, c.release, c.mean)
			res, reason, err := EvaluateCellMean(s, in, c.gate, snapshot, false)
			if err != nil || reason != "" {
				t.Fatal(reason, err)
			}
			ev := res.Rows["growatt.bms.link.1806.mean"]
			if ev.Native == nil || ev.Native.Disposition != c.disposition || ev.Native.Raw != uint64(c.mean) {
				t.Fatalf("%+v", ev.Native)
			}
			decoded := canbusreg.DecodeLINKCellMean(s, in, c.gate, snapshot, false).Fields["mean"]
			if decoded.Status != ev.Native.Disposition || res.Grades["growatt.bms.link.1806.mean"] != decoded.Grade {
				t.Errorf("decoder %q %q", decoded.Status, decoded.Grade)
			}
			if c.value == "" {
				if ev.Native.Value != nil || ev.Native.Unit != "" {
					t.Errorf("%s keeps no quantity", c.disposition)
				}
				return
			}
			if ev.Native.Unit != "mV" || ratOf(t, ev.Native.Value.Quantity.Number).Cmp(ratText(t, c.value)) != 0 {
				t.Errorf("value %+v", ev.Native.Value)
			}
		})
	}
}

func TestCellMeanRowRefusesWhatTheDecoderRefuses(t *testing.T) {
	snapshot := vectorContributors(t, "0011", 4)
	s, in := meanFrame(t, "0011", 3301)
	other := in
	other.Evidence.Frame = mustFrame(t, 0x18100000|uint32(0xf4)<<8|2, 8)
	if res, _, _ := EvaluateCellMean(s, other, canbusreg.MeasurementGate{}, snapshot, false); len(res.Rows) != 0 || res.Status != "unsupported" {
		t.Errorf("%q %d", res.Status, len(res.Rows))
	}
	short := in
	short.Evidence.Frame = mustFrame(t, 0x18060000|uint32(0xf4)<<8|2, 4)
	if res, reason, _ := EvaluateCellMean(s, short, canbusreg.MeasurementGate{}, snapshot, false); reason != "invalid" || len(res.Rows) != 0 {
		t.Errorf("%q %d", reason, len(res.Rows))
	}
}

func mustFrame(t *testing.T, id uint32, length int) canbus.Frame {
	t.Helper()
	f, err := extendedFrame(id, length)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func balanceInput(t *testing.T, release string, mask uint16) (canbusreg.Selection, canbusreg.LINKInput) {
	t.Helper()
	c := vectorCase{Name: "balance", Family: "1801", Selection: vectorSelection{Project: "QAAA", Release: release, Bank: "ordinary", Source: "02", Destination: "F4"}}
	p := make([]byte, 8)
	binary.LittleEndian.PutUint16(p, mask)
	return inputFor(t, c, p)
}

func TestBalanceEnableRowsApplyWhereTheDecoderEmitsThem(t *testing.T) {
	gate := canbusreg.MeasurementGate{Configured: true, Valid: true}
	cases := []struct {
		name      string
		release   string
		count     int
		countOK   bool
		gate      canbusreg.MeasurementGate
		hwKnown   bool
		wantCount int
	}{
		{"selector release with three cells", "0006", 3, true, gate, true, 3},
		{"selector release with sixteen cells", "0006", 16, true, gate, true, 16},
		{"release outside the selector list", "0011", 3, true, gate, true, 0},
		{"count not valid", "0006", 3, false, gate, true, 0},
		{"count of zero", "0006", 0, true, gate, true, 0},
		{"count above sixteen", "0006", 17, true, gate, true, 0},
		{"gate not accepted", "0006", 3, true, canbusreg.MeasurementGate{Configured: true, Status: "stale"}, true, 0},
		{"hardware age unknown", "0006", 3, true, gate, false, 0},
	}
	const mask = 0xA005
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, in := balanceInput(t, c.release, mask)
			res, reason, err := EvaluateBalance(s, in, c.count, c.countOK, c.gate, c.hwKnown, 5*time.Second)
			if err != nil || reason != "" {
				t.Fatal(reason, err)
			}
			decoded := canbusreg.DecodeLINKBalance(s, in, c.count, c.countOK, c.gate, c.hwKnown, 5*time.Second)
			got := applicable(res)
			if len(got) != c.wantCount {
				t.Fatalf("%d enable rows, expected %d", len(got), c.wantCount)
			}
			for n := 1; n <= c.wantCount; n++ {
				id := fmt.Sprintf("growatt.bms.link.enable_%d", n)
				ev, ok := got[id]
				if !ok {
					t.Fatalf("%s does not apply", id)
				}
				bit := uint64(mask>>(n-1)) & 1
				field := decoded.Fields[fmt.Sprintf("enable_%d", n)]
				if ev.Native.Raw != bit || ev.Native.Unit != "" || ev.Native.Disposition != "accepted" || ratOf(t, ev.Native.Value.Quantity.Number).Cmp(bigRat(int64(bit), 1)) != 0 {
					t.Errorf("%s: %+v", id, ev.Native)
				}
				if field.Numerator != int64(bit) || field.Status != "accepted" || res.Grades[id] != field.Grade {
					t.Errorf("%s: decoder %+v", id, field)
				}
			}
			if len(decoded.Fields)-1 != c.wantCount {
				t.Errorf("decoder emits %d enable states", len(decoded.Fields)-1)
			}
		})
	}
}

func TestBalanceEnableRowsReadEachBitOfTheMask(t *testing.T) {
	gate := canbusreg.MeasurementGate{Configured: true, Valid: true}
	for n := 1; n <= 16; n++ {
		s, in := balanceInput(t, "0006", uint16(1)<<(n-1))
		res, _, err := EvaluateBalance(s, in, 16, true, gate, true, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for m := 1; m <= 16; m++ {
			want := uint64(0)
			if m == n {
				want = 1
			}
			if got := res.Rows[fmt.Sprintf("growatt.bms.link.enable_%d", m)].Native.Raw; got != want {
				t.Errorf("mask bit %d: enable_%d reads %d", n, m, got)
			}
		}
	}
}

func TestVectorEvaluationsRefuseWhatTheDecoderRefuses(t *testing.T) {
	base := loadPositionCases(t)[0]
	for name, mutate := range map[string]func(*canbusreg.LINKInput){
		"asserted bank":      func(in *canbusreg.LINKInput) { in.Bank = "parallel" },
		"asserted slot":      func(in *canbusreg.LINKInput) { in.Slot = "9" },
		"epoch":              func(in *canbusreg.LINKInput) { in.Epoch = 2 },
		"negative field age": func(in *canbusreg.LINKInput) { in.FieldAge = -time.Second },
		"sequence zero":      func(in *canbusreg.LINKInput) { in.Evidence.Sequence = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s, in := base.input(t)
			mutate(&in)
			res, reason, _ := EvaluateVector(s, in, base.state(), base.gates())
			if reason != "invalid" || len(res.Rows) != 0 || base.decoder(s).DecodeLINKVector(in, base.gates()).Status != "invalid" {
				t.Errorf("%q %d", reason, len(res.Rows))
			}
		})
	}
	t.Run("destination other than the coordinator", func(t *testing.T) {
		c := base
		c.Selection.Destination = "F5"
		s, in := c.input(t)
		res, reason, _ := EvaluateVector(s, in, c.state(), c.gates())
		if reason != "invalid" || len(res.Rows) != 0 || c.decoder(s).DecodeLINKVector(in, c.gates()).Status != "invalid" {
			t.Errorf("%q %d", reason, len(res.Rows))
		}
	})
	t.Run("source address FF", func(t *testing.T) {
		iface, _ := canbus.NewInterfaceIdentity("synthetic", 1)
		_, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: iface, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0011",
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "FF", RouteSet: true, Destination: 0xf4, Source: 0xff, Epoch: 1})
		if err == nil {
			t.Error("a LINK selection with endpoint FF was accepted")
		}
	})
}
