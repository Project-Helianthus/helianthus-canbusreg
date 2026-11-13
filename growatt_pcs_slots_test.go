package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func slotTestFixture(t *testing.T, pid uint32, p [8]byte) (Selection, PCSSlotPCSInput) {
	t.Helper()
	s, i := identityTestFixture(t, "QAAA0011", pid, p)
	return s, PCSSlotPCSInput{PCSIdentityPCSInput: i, SlotValid: true, NativeSlot: p[0], GroupValid: true, NativeGroup: p[0]}
}
func slotTestWant(t *testing.T, r NativeResult, name, status string, n, d int64, unit string) {
	t.Helper()
	f, ok := r.Fields[name]
	if !ok || f.Status != status || f.Numerator != n || f.Denominator != d || f.Unit != unit || f.PhysicalQualified || r.PhysicalQualified {
		t.Fatalf("%s: %+v (%s)", name, f, r.Status)
	}
	i := r.Selection.SelectionSpecValue()
	if f.Raw != r.Raw || f.Evidence != r.Evidence || f.Epoch != r.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot {
		t.Fatal("lost native context", f)
	}
}
func TestCX08SlotArithmetic(t *testing.T) {
	s, i := slotTestFixture(t, 0x326, [8]byte{2, 0x11, 0x70, 0x0f, 0xa0, 44})
	r := DecodePCSSlots(s, i)
	slotTestWant(t, r, "voltage", "accepted", 4464, 100, "V")
	slotTestWant(t, r, "current", "accepted", 0, 10, "A")
	slotTestWant(t, r, "soc_low", "accepted", 44, 1, "")
	if byte(44) != byte(300%256) {
		t.Fatal("synthetic SOC collision")
	}
	for _, bank := range []string{"ordinary", "parallel"} {
		s2, j := s, i
		s2, j.PCSStandardPCSInput = standardTestReselect(t, s, j.PCSStandardPCSInput, func(c *SelectionSpec) { c.Bank = bank })
		r = DecodePCSSlots(s2, j)
		n := int64(0)
		if bank == "parallel" {
			n = -6000
		}
		slotTestWant(t, r, "current", "accepted", n, 10, "A")
	}
	i.Bank = ""
	if r = DecodePCSSlots(s, i); r.Status != "invalid" || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal(r)
	}
	s, i = slotTestFixture(t, 0x326, [8]byte{2, 0, 0, 0x27, 0x10})
	s, i.PCSStandardPCSInput = standardTestReselect(t, s, i.PCSStandardPCSInput, func(c *SelectionSpec) { c.Bank = "parallel" })
	slotTestWant(t, DecodePCSSlots(s, i), "current", "accepted", 0, 10, "A")
}
func TestCX08SlotsAndIndependentFields(t *testing.T) {
	s, i := slotTestFixture(t, 0x327, [8]byte{2, 0x0d, 0xac, 0x0c, 0xe4, 3, 4})
	r := DecodePCSSlots(s, i)
	slotTestWant(t, r, "maximum_cell", "accepted", 3500, 1, "mV")
	slotTestWant(t, r, "minimum_index", "accepted", 4, 1, "")
	i.FieldStates = map[string]string{"maximum_cell": "stale"}
	i.FieldAges = map[string]time.Duration{"maximum_cell": 3 * time.Second}
	update := DecodePCSSlots(s, i)
	slotTestWant(t, update, "maximum_cell", "stale", 3500, 1, "")
	slotTestWant(t, update, "minimum_cell", "accepted", 3300, 1, "mV")
	retained := RetainAccepted(r, update)
	if retained.Fields["maximum_cell"] != r.Fields["maximum_cell"] {
		t.Fatal("lost last-known-good")
	}
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict"} {
		i.FieldStates["maximum_cell"] = state
		slotTestWant(t, DecodePCSSlots(s, i), "maximum_cell", state, 3500, 1, "")
	}
}
func TestCX08MemberDictionary(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	r := DecodePCSSlots(s, i)
	slotTestWant(t, r, "dtc_a_index", "accepted", 0, 1, "")
	wrong, _ := NewSelection(func() SelectionSpec {
		c := s.SelectionSpecValue()
		c.ProjectCode = "QAAa"
		c.Release = "9011"
		c.Choice = "release11"
		return c
	}())
	i.Member = &wrong
	r = DecodePCSSlots(s, i)
	if r.Fields["dtc_a"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal("wrong dictionary", r)
	}
	i.Member = nil
	if DecodePCSSlots(s, i).Fields["dtc_a"].Status == "accepted" {
		t.Fatal("inferred member")
	}
}
func TestCX08MemberIndexInheritsSourceStatus(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned"} {
		i.FieldStates = map[string]string{"dtc_a": state}
		r := DecodePCSSlots(s, i)
		if r.Fields["dtc_a_index"].Status != state || r.Fields["dtc_a_index"].Meaning != "" {
			t.Fatal(state, r.Fields["dtc_a_index"])
		}
		slotTestWant(t, r, "dtc_b_index", "accepted", 1, 1, "")
	}
	i.FieldStates = nil
	i.FieldAges = map[string]time.Duration{"dtc_a": -time.Second}
	if r := DecodePCSSlots(s, i); r.Fields["dtc_a_index"].Status != "invalid" {
		t.Fatal("negative age", r.Fields["dtc_a_index"])
	}
	i.FieldAges = map[string]time.Duration{"dtc_a": 2 * time.Second}
	if r := DecodePCSSlots(s, i); r.Fields["dtc_a_index"].Age != 2*time.Second {
		t.Fatal("age", r.Fields["dtc_a_index"])
	}
}
func TestCX08ReleaseAndPadding(t *testing.T) {
	for _, pid := range []uint32{0x326, 0x327, 0x333, 0x334} {
		s, i := slotTestFixture(t, pid, [8]byte{})
		for _, identity := range []string{"QAAA0011", "QAAa9011", "QAAA0099"} {
			s2, j := s, i
			s2, j.PCSStandardPCSInput = standardTestReselect(t, s, j.PCSStandardPCSInput, func(c *SelectionSpec) {
				c.ProjectCode = identity[:4]
				c.Release = identity[4:]
				if identity == "QAAa9011" {
					c.Choice = "release11"
				}
			})
			r := DecodePCSSlots(s2, j)
			if identity == "QAAA0099" {
				for _, f := range r.Fields {
					if f.Status == "accepted" {
						t.Fatal("unknown release", r)
					}
				}
			} else if r.Fields["state"].Status != "accepted" && pid == 0x334 {
				t.Fatal(r)
			}
		}
		p := i.Evidence.Frame.Bytes()
		p[7] = 1
		id, _ := canbus.NewStandardID(pid)
		i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
		r := DecodePCSSlots(s, i)
		for _, f := range r.Fields {
			if f.Status == "accepted" {
				t.Fatal("padding admitted", r)
			}
		}
	}
	s, i := slotTestFixture(t, 0x334, [8]byte{7, 9})
	if DecodePCSSlots(s, i).Fields["state"].Status == "accepted" {
		t.Fatal("old member padding")
	}
	s, i.PCSStandardPCSInput = standardTestReselect(t, s, i.PCSStandardPCSInput, func(c *SelectionSpec) { c.ProjectCode = "QAAa"; c.Release = "9011"; c.Choice = "release11" })
	slotTestWant(t, DecodePCSSlots(s, i), "independent", "accepted", 9, 1, "")
}
func TestCX08AdmissionIdentity(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "publisher", "ide", "dlc", "raw-dlc", "age", "native-record"} {
		t.Run(key, func(t *testing.T) {
			s, i := slotTestFixture(t, 0x326, [8]byte{2, 0x11, 0x70, 0x0f, 0xa0})
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
				id, _ := canbus.NewExtendedID(0x326)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data())
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), []byte{2})
			case "raw-dlc":
				var record [16]byte
				binary.NativeEndian.PutUint32(record[:4], 0x326)
				record[4], record[7] = 8, 9
				payload := i.Evidence.Frame.Bytes()
				copy(record[8:], payload[:])
				i.Evidence.Frame, _ = canbus.DecodeSocketCANRecord(record[:])
			case "age":
				i.FieldAge = -1
			case "native-record":
				i.NativeRecord = []byte{1}
			}
			r := DecodePCSSlots(s, i)
			if r.Status == "decoded" || r.Raw != i.Evidence.Frame.Bytes() || len(r.Fields) != 0 {
				t.Fatal(r)
			}
		})
	}
}

func TestCX08ExplicitSlotAssociations(t *testing.T) {
	for _, pid := range []uint32{0x326, 0x327, 0x333} {
		for _, key := range []string{"slot-valid", "slot-value", "group-valid", "group-value"} {
			t.Run(string(rune(pid))+key, func(t *testing.T) {
				s, i := slotTestFixture(t, pid, [8]byte{2, 2})
				if pid != 0x333 && (key == "group-valid" || key == "group-value") {
					return
				}
				switch key {
				case "slot-valid":
					i.SlotValid = false
				case "slot-value":
					i.NativeSlot++
				case "group-valid":
					i.GroupValid = false
				case "group-value":
					i.NativeGroup++
				}
				r := DecodePCSSlots(s, i)
				if r.Raw != i.Evidence.Frame.Bytes() {
					t.Fatal("lost raw")
				}
				for _, f := range r.Fields {
					if f.Status == "accepted" {
						t.Fatal(r)
					}
				}
			})
		}
	}
}
func TestCX08MemberAssociationKeys(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "flavor", "scope", "bank", "endpoint", "group", "slot"} {
		t.Run(key, func(t *testing.T) {
			s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
			i.NativeSlot = 2
			c := s.SelectionSpecValue()
			switch key {
			case "interface-name":
				c.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				c.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				c.Epoch++
			case "direction":
				c.Direction = "pcs-to-bms"
			case "flavor":
				c.Flavor = "pcs-parallel"
			case "scope":
				c.Scope = "aggregate"
			case "bank":
				c.Bank = "parallel"
			case "endpoint":
				c.Endpoint = "other"
			case "group":
				c.Group = "other"
			case "slot":
				c.Slot = "other"
			}
			member, err := NewSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			i.Member = &member
			r := DecodePCSSlots(s, i)
			if r.Fields["dtc_a"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	s, i := slotTestFixture(t, 0x333, [8]byte{})
	i.Member = &s
	if r := DecodePCSSlots(s, i); r.Fields["dtc_a"].Status != "empty" {
		t.Fatal(r)
	}
}

func TestCX08NonzeroToZeroDTCRetention(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	previous := DecodePCSSlots(s, i)
	if previous.Fields["dtc_a"].Status != "accepted" || previous.Fields["dtc_a_index"].Status != "accepted" {
		t.Fatal(previous)
	}
	s, i = slotTestFixture(t, 0x333, [8]byte{3, 2, 0, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	update := DecodePCSSlots(s, i)
	merged := RetainAccepted(previous, update)
	for _, name := range []string{"dtc_a", "dtc_a_index"} {
		if merged.Fields[name].Status != "empty" {
			t.Fatal(name, merged.Fields[name])
		}
	}
	if merged.Fields["dtc_b_index"].Status != "accepted" {
		t.Fatal(merged.Fields["dtc_b_index"])
	}
}

func TestCX08ZeroDTCIndexIndependentWithholding(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	previous := DecodePCSSlots(s, i)
	if previous.Fields["dtc_a_index"].Status != "empty" {
		t.Fatal(previous.Fields["dtc_a_index"])
	}
	s0, i0 := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i0.NativeSlot = 2
	i0.Member = &s0
	retained := DecodePCSSlots(s0, i0)
	i.FieldStates = map[string]string{"dtc_a_index": "stale"}
	r := DecodePCSSlots(s, i)
	if r.Fields["dtc_a_index"].Status != "stale" {
		t.Fatal(r.Fields["dtc_a_index"])
	}
	if m := RetainAccepted(retained, r); m.Fields["dtc_a_index"].Status != "accepted" {
		t.Fatal(m.Fields["dtc_a_index"])
	}
	i.FieldStates = nil
	i.FieldAges = map[string]time.Duration{"dtc_a_index": -time.Second}
	r = DecodePCSSlots(s, i)
	if r.Fields["dtc_a_index"].Status != "invalid" {
		t.Fatal(r.Fields["dtc_a_index"])
	}
	if m := RetainAccepted(retained, r); m.Fields["dtc_a_index"].Status != "accepted" {
		t.Fatal(m.Fields["dtc_a_index"])
	}
}

func TestCX08RetentionIdentityIncludesMember(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	previous := DecodePCSSlots(s, i)
	if !previous.HasMember || previous.Member != s {
		t.Fatal("member selection not recorded", previous.Member)
	}
	if previous.Fields["dtc_a_index"].Status != "accepted" {
		t.Fatal(previous.Fields["dtc_a_index"])
	}
	i.Member = nil
	i.FieldStates = map[string]string{"dtc_a": "stale"}
	update := DecodePCSSlots(s, i)
	if m := RetainAccepted(previous, update); m.Fields["dtc_a_index"].Status == "accepted" {
		t.Fatal("index retained across a member change", m.Fields["dtc_a_index"])
	}
}

func TestCX05RetainsEmptyAcrossPartialUpdate(t *testing.T) {
	s, i := slotTestFixture(t, 0x333, [8]byte{3, 2, 0, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	zero := DecodePCSSlots(s, i)
	if zero.Fields["dtc_a_index"].Status != "empty" {
		t.Fatal(zero.Fields["dtc_a_index"])
	}
	i.FieldStates = map[string]string{"dtc_a_index": "stale"}
	partial := DecodePCSSlots(s, i)
	merged := RetainAccepted(zero, partial)
	if merged.Fields["dtc_a_index"].Status != "empty" {
		t.Fatal("empty state dropped by partial update", merged.Fields["dtc_a_index"])
	}
	again := RetainAccepted(merged, partial)
	if again.Fields["dtc_a_index"].Status != "empty" {
		t.Fatal("empty state dropped on replay", again.Fields["dtc_a_index"])
	}
}
