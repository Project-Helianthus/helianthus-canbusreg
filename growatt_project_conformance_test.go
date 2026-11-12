package canbusreg

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestCX16FiniteSelectorConformance(t *testing.T) {
	var rows []struct {
		Identity, Choice, Group string
		Flavors                 []string
	}
	raw, err := os.ReadFile("testdata/growatt_project_conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 35 {
		t.Fatalf("finite fixture count: %d", len(rows))
	}
	for _, row := range rows {
		for _, flavor := range []string{"pcs-standard", "pcs-extended19", "pcs-parallel", "link"} {
			t.Run(row.Identity+"/"+row.Choice+"/"+flavor, func(t *testing.T) {
				s, i := replayTestFixture(t)
				c := s.SelectionSpecValue()
				c.ProjectCode, c.Release, c.Choice, c.Flavor = row.Identity[:4], row.Identity[4:], row.Choice, flavor
				id, extended := uint32(0x313), false
				if flavor == "pcs-parallel" {
					id = 0x610
				}
				if flavor == "pcs-extended19" {
					c.RouteSet, c.Destination, c.Source = true, 0xf2, 1
					id, extended = 0x1900f201, true
				}
				if flavor == "link" {
					c.PCSFlavor, c.Direction, c.RouteSet, c.Destination, c.Source = "pcs-standard", "module-to-coordinator", true, 0xf4, 1
					id, extended = 0x1805f401, true
				}
				s = replayTestReselect(t, &i, c)
				replayTestFrame(t, &i, id, extended, [8]byte{})
				allowed := false
				for _, f := range row.Flavors {
					if f == flavor {
						allowed = true
					}
				}
				p, err := NewSelectedProfile(s, []ReplayInput{i})
				if !allowed {
					if err == nil {
						t.Fatal("unlisted flavor/identity admitted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				r := replayTestClassify(t, p, i.PCS.Evidence)
				if r.Current.Selection.SelectionGroup() != row.Group || r.Current.Selection != s || r.Current.Raw != i.PCS.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
				for name, field := range r.Current.Fields {
					if field.PhysicalQualified || field.Evidence != i.PCS.Evidence || field.Epoch != c.Epoch || field.Raw != r.Current.Raw || field.Scope != c.Scope || field.Bank != c.Bank || field.Endpoint != c.Endpoint || field.Group != c.Group || field.Slot != c.Slot {
						t.Fatalf("evidence %s: %#v", name, field)
					}
				}
			})
		}
	}
}

func TestCX16CurrentCollisionSuppression(t *testing.T) {
	s, i := replayTestFixture(t)
	for _, key := range []string{"valid", "epoch", "cross-interface", "unlisted"} {
		t.Run(key, func(t *testing.T) {
			selected := i
			selected.PCS.Evidence.Sequence = 3
			if key == "epoch" {
				selected.PCS.Epoch++
			}
			if key == "cross-interface" {
				selected.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 2)
			}
			p := replayTestProfile(t, s, selected)
			v104 := GrowattLowVoltageBMSV104()
			a := selected
			replayTestFrame(t, &a, 0x311, false, [8]byte{1, 2, 3, 4, 5, 6, 0, 0})
			a.PCS.Evidence.Sequence = 1
			b := selected
			replayTestFrame(t, &b, 0x312, false, [8]byte{0, 0, 0, 0, 1, 0, 0, 16})
			b.PCS.Evidence.Sequence = 2
			c := selected.PCS.Evidence
			if key == "unlisted" {
				c.Sequence++
			}
			v104.Classify(a.PCS.Evidence)
			v104.Classify(b.PCS.Evidence)
			if got := NewRegistry(p, v104).Classify(c); got.Profile != "" || got.Projection != nil {
				t.Fatalf("collision admitted: %#v", got)
			}
		})
	}
	p1 := replayTestProfile(t, s, i)
	p2 := replayTestProfile(t, s, i)
	if got := NewRegistry(p1, p2).Classify(i.PCS.Evidence); got.Profile != "" {
		t.Fatal("selected collision admitted")
	}
}

func TestCX16LegacyAndCommonCompatibility(t *testing.T) {
	s, i := replayTestFixture(t)
	r := NewRegistry(GrowattLowVoltageBMSV104())
	for n, id := range []uint32{0x311, 0x312, 0x313} {
		payload := [8]byte{1, 2, 3, 4, 5, 6, 50, 100}
		if id == 0x312 {
			payload = [8]byte{0, 0, 0, 0, 1, 0, 0, 16}
		}
		replayTestFrame(t, &i, id, false, payload)
		i.PCS.Evidence.Sequence = uint64(n + 1)
		c := r.Classify(i.PCS.Evidence)
		if n < 2 && c.Profile != "" {
			t.Fatal("incomplete tuple admitted")
		}
		if n == 2 && c.Profile != growattLowVoltageBMSV104Profile {
			t.Fatal(c)
		}
	}
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(s.SelectionSpecValue().Interface)
	if !ok {
		t.Fatal("common selection")
	}
	common, ok := projector.Apply(i.PCS.Evidence)
	if !ok || common.Measurements == nil || common.Measurements.VoltageCentivolts != 258 || common.RawEvidence != i.PCS.Evidence {
		t.Fatal(common)
	}
	id, _ := canbus.NewExtendedID(0x1ee00410)
	frame, _ := canbus.NewFrame(id, []byte{0x20, 10})
	e := i.PCS.Evidence
	e.Frame = frame
	p := replayTestProfile(t, s, i)
	got := NewRegistry(p, GreeVRFCandidate()).Classify(e)
	if got.Profile != GreeVRFCandidateProfile {
		t.Fatalf("unrelated family collision: %#v", got)
	}
}

func TestCX16NoSelectionFromOfflineClassificationOrPartialIdentity(t *testing.T) {
	s, i := replayTestFixture(t)
	p := replayTestProfile(t, s, i)
	// The payload and interface remain fixed; a changed sequence has no context association.
	e := i.PCS.Evidence
	e.Sequence++
	got := replayTestClassify(t, p, e)
	if got.Current.Status != "unlisted" || len(got.Current.Fields) != 0 || got.Current.Evidence != e {
		t.Fatal(got)
	}
	c := s.SelectionSpecValue()
	c.ProjectCode, c.Release = "QAAa", "9011"
	unresolved, err := NewSelection(c)
	if err != nil {
		t.Fatal(err)
	}
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3})
	if _, err := NewSelectedProfile(unresolved, []ReplayInput{i}); err == nil {
		t.Fatal("partial identity selected ambiguous contract")
	}
}

func TestCX16SlotBankAndMemberDictionary(t *testing.T) {
	for _, key := range []string{"valid", "bank", "member", "slot", "measurement"} {
		t.Run(key, func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.Release = "0011"
			if key == "bank" {
				c.Bank = "unlisted"
			}
			s = replayTestReselect(t, &i, c)
			i.PCS.SlotValid, i.PCS.NativeSlot, i.PCS.GroupValid, i.PCS.NativeGroup = true, 1, true, 0
			member := s
			if key == "member" {
				m := c
				m.ProjectCode = "QAAa"
				m.Release = "9011"
				m.Choice = "legacy19"
				member, _ = NewSelection(m)
			}
			i.PCS.Member = &member
			if key == "slot" {
				i.PCS.NativeSlot = 2
			}
			if key == "measurement" {
				i.PCS.MeasurementValid = false
			}
			replayTestFrame(t, &i, 0x326, false, [8]byte{1, 0, 1, 0x0f, 0xa0, 2, 0, 0})
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			field := r.Current.Fields["slots/current"]
			if (field.Status == "accepted") != (key == "valid" || key == "member") {
				t.Fatal(field)
			}
			if key == "member" {
				replayTestFrame(t, &i, 0x333, false, [8]byte{0, 1, 0x10, 0, 0, 0, 0, 0})
				r = replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
				if r.Current.Fields["slots/dtc_a"].Status == "accepted" || r.Current.Fields["slots/dtc_a"].Unit != "" {
					t.Fatal(r)
				}
				if r.Current.Fields["slots/slot"].Status != "accepted" {
					t.Fatal("invalid dictionary erased slot sibling")
				}
			}
		})
	}
}

func TestCX16LINKCountBoundariesAndIndependentValidity(t *testing.T) {
	for _, count := range []int{0, 1, 16, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1801, [8]byte{1})
			i.CellCount, i.CellCountValid = count, true
			i.BalanceGate = MeasurementGate{Configured: true, Valid: true}
			i.HardwareAgeKnown = true
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			_, accepted := r.Current.Fields["balance/enable_1"]
			if accepted != (count >= 1 && count <= 16) {
				t.Fatal(r)
			}
		})
	}
	for _, key := range []string{"count-valid", "hardware-age", "measurement-valid", "selector"} {
		t.Run(key, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1801, [8]byte{1})
			i.CellCount, i.CellCountValid, i.HardwareAgeKnown = 2, true, true
			i.BalanceGate = MeasurementGate{Configured: true, Valid: true}
			switch key {
			case "count-valid":
				i.CellCountValid = false
			case "hardware-age":
				i.HardwareAgeKnown = false
			case "measurement-valid":
				i.BalanceGate.Valid = false
			case "selector":
				c := s.SelectionSpecValue()
				c.Release = "0011"
				s = replayTestReselect(t, &i, c)
			}
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			if _, ok := r.Current.Fields["balance/enable_1"]; ok {
				t.Fatal(r)
			}
			if r.Current.Raw != i.PCS.Evidence.Frame.Bytes() || r.Current.Fields["link/mask"].Status != "raw" {
				t.Fatal("raw sibling lost")
			}
		})
	}
	for _, invalid := range []bool{false, true} {
		t.Run("cell-validity/"+fmt.Sprint(invalid), func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1810, [8]byte{0, 0, 1, 0, 2, 0, 3, 0})
			i.CellCount, i.CellCountValid = 4, true
			for j := range i.CellGates {
				i.CellGates[j] = MeasurementGate{Configured: true, Valid: true}
			}
			i.CellGates[1].Valid = !invalid
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			if r.Current.Fields["cells/cell_1"].Status != "accepted" || r.Current.Fields["cells/cell_1"].Numerator != 0 {
				t.Fatal(r)
			}
			if (r.Current.Fields["cells/cell_2"].Status == "accepted") == invalid {
				t.Fatal(r)
			}
		})
	}
}

func TestCX16InvalidContributorsWithholdMeanOnly(t *testing.T) {
	for _, key := range []string{"valid", "selection", "kind", "count", "count-valid", "capacity", "as-of", "max-age", "missing", "status", "epoch", "interface-name", "interface-index", "scope", "bank", "endpoint", "group", "slot", "sequence", "monotonic", "raw", "numerator", "denominator", "age", "route"} {
		t.Run(key, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1810, [8]byte{0xe4, 0x0c, 0xe4, 0x0c})
			c := s.SelectionSpecValue()
			c.Release = "0011"
			s = replayTestReselect(t, &i, c)
			vector := NewLINKCells(s, 2, true)
			var gates [4]MeasurementGate
			for j := range gates {
				gates[j] = MeasurementGate{Configured: true, Valid: true}
			}
			vector.DecodeLINKVector(i.PCS.LINKInput, gates)
			snapshot := vector.SnapshotLINKVector(time.Second, 5*time.Second)
			i.PCS.Evidence.Sequence, i.PCS.Evidence.Monotonic = 2, 2*time.Second
			replayTestFrame(t, &i, 0x1806f401, true, [8]byte{0, 0, 0, 0, 0, 0, 0xe4, 0x0c})
			f := snapshot.Fields[1]
			switch key {
			case "selection":
				snapshot.Selection = Selection{}
			case "kind":
				snapshot.Kind = "temperatures"
			case "count":
				snapshot.ConfiguredCount = 0
			case "count-valid":
				snapshot.CountValid = false
			case "capacity":
				snapshot.AddressableSlots = 1
			case "as-of":
				snapshot.AsOf = 3 * time.Second
			case "max-age":
				snapshot.MaxAge = -1
			case "missing":
				delete(snapshot.Fields, 2)
			case "status":
				f.Status = "stale"
			case "epoch":
				f.Epoch++
			case "interface-name":
				f.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				f.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "scope":
				f.Scope = "aggregate"
			case "bank":
				f.Bank = "other"
			case "endpoint":
				f.Endpoint = "other"
			case "group":
				f.Group = "1"
			case "slot":
				f.Slot = "2"
			case "sequence":
				f.Evidence.Sequence = 2
			case "monotonic":
				f.Evidence.Monotonic = 3 * time.Second
			case "raw":
				f.Raw[0]++
			case "numerator":
				f.Numerator++
			case "denominator":
				f.Denominator = 10
			case "age":
				f.Age = 6 * time.Second
			case "route":
				id, _ := canbus.NewExtendedID(0x1810f402)
				f.Evidence.Frame, _ = canbus.NewFrame(id, f.Raw[:])
			}
			snapshot.Fields[1] = f
			i.CellContributors = snapshot
			i.MeanGate = MeasurementGate{Configured: true, Valid: true}
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			mean := r.Current.Fields["mean/mean"]
			if (mean.Status == "accepted") != (key == "valid") {
				t.Fatalf("mean: %#v", mean)
			}
			if r.Current.Fields["link/maximum"].Status != "raw" || r.Current.Raw != i.PCS.Evidence.Frame.Bytes() {
				t.Fatal("raw siblings lost")
			}
		})
	}
}

func TestCX16LINKVectorCountBoundaryPairs(t *testing.T) {
	for _, kind := range []string{"cells", "sensors"} {
		maximum, family := 16, uint32(0x1810)
		if kind == "sensors" {
			maximum, family = 4, 0x1830
		}
		for _, count := range []int{0, 1, maximum, maximum + 1} {
			t.Run(fmt.Sprintf("%s/%d", kind, count), func(t *testing.T) {
				s, i := replayTestLINK(t, family, [8]byte{0, 0, 1, 0, 2, 0, 3, 0})
				var gates [4]MeasurementGate
				for j := range gates {
					gates[j] = MeasurementGate{Configured: true, Valid: true}
				}
				i.CellCount, i.CellCountValid, i.CellGates = count, true, gates
				i.SensorCount, i.SensorCountValid, i.SensorGates = count, true, gates
				r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
				name := "cells/cell_1"
				if kind == "sensors" {
					name = "sensors/sensor_1"
				}
				if (r.Current.Fields[name].Status == "accepted") != (count >= 1 && count <= maximum) {
					t.Fatal(r)
				}
				if r.Current.Raw != i.PCS.Evidence.Frame.Bytes() {
					t.Fatal("native bytes lost")
				}
			})
		}
	}
}

func TestCX16NativeContractConfirmationNeedsExplicitChoiceAndAssociation(t *testing.T) {
	for _, key := range []string{"confirmed", "conflict", "no-association"} {
		t.Run(key, func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "release11"
			if key == "conflict" {
				c.Choice = "legacy19"
			}
			s = replayTestReselect(t, &i, c)
			i.TopologyAssociated = key != "no-association"
			replayTestFrame(t, &i, 0x326, false, [8]byte{})
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			if key == "conflict" {
				replayTestWithheld(t, r, i)
				if r.Current.Status != "conflict" {
					t.Fatal(r)
				}
				return
			}
			want := "confirmed"
			if key == "no-association" {
				want = "unsupported"
			}
			if r.Native["confirmation"].Status != want || r.Current.Selection != s {
				t.Fatal(r)
			}
		})
	}
}

func TestCX16ReplayEvidenceKeyEveryField(t *testing.T) {
	for _, key := range []string{"payload", "identifier", "ide", "dlc", "raw-dlc", "interface-name", "interface-index", "sequence", "monotonic", "raw-record"} {
		t.Run(key, func(t *testing.T) {
			s, i := replayTestFixture(t)
			p := replayTestProfile(t, s, i)
			other := i
			payload := i.PCS.Evidence.Frame.Bytes()
			switch key {
			case "payload":
				payload[0]++
				replayTestFrame(t, &other, 0x313, false, payload)
			case "identifier":
				replayTestFrame(t, &other, 0x314, false, payload)
			case "ide":
				replayTestFrame(t, &other, 0x313, true, payload)
			case "dlc":
				id, _ := canbus.NewStandardID(0x313)
				other.PCS.Evidence.Frame, _ = canbus.NewFrame(id, payload[:7])
			case "raw-dlc":
				raw := make([]byte, canbus.SocketCANRecordSize)
				raw[0], raw[1], raw[4], raw[7] = 0x13, 3, 8, 9
				copy(raw[8:], payload[:])
				var err error
				other.PCS.Evidence.Frame, err = canbus.DecodeSocketCANRecord(raw)
				if err != nil {
					t.Fatal(err)
				}
			case "interface-name":
				other.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				other.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "sequence":
				other.PCS.Evidence.Sequence++
			case "monotonic":
				other.PCS.Evidence.Monotonic++
			case "raw-record":
				other.PCS.Evidence.RawRecord[0] = 1
			}
			r := replayTestClassify(t, p, other.PCS.Evidence)
			if r.Current.Status != "unlisted" || len(r.Current.Fields) != 0 || r.Current.Evidence != other.PCS.Evidence {
				t.Fatal(r)
			}
		})
	}
}

func TestCX16LINKSourceBoundaryPairs(t *testing.T) {
	for _, source := range []uint8{0xf3, 0xf4, 0xf5, 0xfe, 0xff} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1805, [8]byte{})
			c := s.SelectionSpecValue()
			c.Source = source
			s = replayTestReselect(t, &i, c)
			replayTestFrame(t, &i, 0x1805f400|uint32(source), true, [8]byte{})
			r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
			f := r.Current.Fields["link/pack_voltage"]
			if (f.Status == "accepted") != (source != 0xf4 && source != 0xff) {
				t.Fatal(r)
			}
		})
	}
}

func TestCX16ImmutableSelectionAndNativeRecordBound(t *testing.T) {
	s, i := replayTestFixture(t)
	for _, key := range []string{"group", "layout", "grade"} {
		t.Run(key, func(t *testing.T) {
			forged := s
			switch key {
			case "group":
				forged.group = "L8"
			case "layout":
				forged.layout = "B"
			case "grade":
				forged.grade = "Unknown"
			}
			if _, err := NewSelectedProfile(forged, []ReplayInput{i}); err == nil {
				t.Fatal("forged selection accepted")
			}
		})
	}
	for _, n := range []int{72, 73} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			i.PCS.NativeRecord = make([]byte, n)
			p, err := NewSelectedProfile(s, []ReplayInput{i})
			if n == 73 {
				if err == nil {
					t.Fatal("unbounded native record")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			replayTestWithheld(t, replayTestClassify(t, p, i.PCS.Evidence), i)
		})
	}
}

func TestCX16UnrelatedFrameDoesNotEraseMemberHistory(t *testing.T) {
	s, i := replayTestFixture(t)
	c := s.SelectionSpecValue()
	c.Release = "0011"
	s = replayTestReselect(t, &i, c)
	i.PCS.Member = &s
	i.PCS.SlotValid, i.PCS.GroupValid, i.PCS.NativeSlot, i.PCS.NativeGroup = true, true, 1, 0
	replayTestFrame(t, &i, 0x333, false, [8]byte{0, 1, 0x10, 0, 0, 0, 0, 0})
	other := i
	other.PCS.Member = nil
	other.PCS.Evidence.Sequence, other.PCS.Evidence.Monotonic = 2, 2*time.Second
	replayTestFrame(t, &other, 0x313, false, [8]byte{})
	p := replayTestProfile(t, s, i, other)
	before := replayTestClassify(t, p, i.PCS.Evidence)
	if before.LastGood["slots/dtc_a"].Status != "accepted" || !before.Native["slots"].HasMember || before.Native["slots"].Member != s {
		t.Fatal(before)
	}
	after := replayTestClassify(t, p, other.PCS.Evidence)
	if after.LastGood["slots/dtc_a"] != before.LastGood["slots/dtc_a"] || after.RetainedNative["slots"].Member != s || !after.RetainedNative["slots"].HasMember {
		t.Fatal("unrelated observation erased member history")
	}
}
