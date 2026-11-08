package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func cx08Fixture(t *testing.T, pid uint32, p [8]byte) (CXSelection, CX08PCSInput) {
	t.Helper()
	s, i := cx06Fixture(t, "QAAA0011", pid, p)
	return s, CX08PCSInput{CX06PCSInput: i, SlotValid: true, NativeSlot: p[0], GroupValid: true, NativeGroup: p[0]}
}
func cx08Want(t *testing.T, r CXNativeResult, name, status string, n, d int64, unit string) {
	t.Helper()
	f, ok := r.Fields[name]
	if !ok || f.Status != status || f.Numerator != n || f.Denominator != d || f.Unit != unit || f.PhysicalQualified || r.PhysicalQualified {
		t.Fatalf("%s: %+v (%s)", name, f, r.Status)
	}
	i := r.Selection.CX01Spec()
	if f.Raw != r.Raw || f.Evidence != r.Evidence || f.Epoch != r.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot {
		t.Fatal("lost native context", f)
	}
}
func TestCX08SlotArithmetic(t *testing.T) {
	s, i := cx08Fixture(t, 0x326, [8]byte{2, 0x11, 0x70, 0x0f, 0xa0, 44})
	r := CX08DecodePCSSlots(s, i)
	cx08Want(t, r, "voltage", "accepted", 4464, 100, "V")
	cx08Want(t, r, "current", "accepted", 0, 10, "A")
	cx08Want(t, r, "soc_low", "accepted", 44, 1, "")
	if byte(44) != byte(300%256) {
		t.Fatal("synthetic SOC collision")
	}
	for _, bank := range []string{"standard", "parallel", "other"} {
		s2, j := s, i
		s2, j.CX05PCSInput = cx05Reselect(t, s, j.CX05PCSInput, func(c *CX01SelectionSpec) { c.Bank = bank })
		r = CX08DecodePCSSlots(s2, j)
		status, n, d, unit := "accepted", int64(0), int64(10), "A"
		if bank == "parallel" {
			n = -6000
		}
		if bank == "other" {
			status, n, d, unit = "raw", 4000, 1, ""
		}
		cx08Want(t, r, "current", status, n, d, unit)
	}
	i.Bank = ""
	if r = CX08DecodePCSSlots(s, i); r.Status != "invalid" || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal(r)
	}
	s, i = cx08Fixture(t, 0x326, [8]byte{2, 0, 0, 0x27, 0x10})
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Bank = "parallel" })
	cx08Want(t, CX08DecodePCSSlots(s, i), "current", "accepted", 0, 10, "A")
}
func TestCX08SlotsAndIndependentFields(t *testing.T) {
	s, i := cx08Fixture(t, 0x327, [8]byte{2, 0x0d, 0xac, 0x0c, 0xe4, 3, 4})
	r := CX08DecodePCSSlots(s, i)
	cx08Want(t, r, "maximum_cell", "accepted", 3500, 1, "mV")
	cx08Want(t, r, "minimum_index", "accepted", 4, 1, "")
	i.FieldStates = map[string]string{"maximum_cell": "stale"}
	i.FieldAges = map[string]time.Duration{"maximum_cell": 3 * time.Second}
	update := CX08DecodePCSSlots(s, i)
	cx08Want(t, update, "maximum_cell", "stale", 3500, 1, "")
	cx08Want(t, update, "minimum_cell", "accepted", 3300, 1, "mV")
	retained := CX05RetainAccepted(r, update)
	if retained.Fields["maximum_cell"] != r.Fields["maximum_cell"] {
		t.Fatal("lost last-known-good")
	}
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict"} {
		i.FieldStates["maximum_cell"] = state
		cx08Want(t, CX08DecodePCSSlots(s, i), "maximum_cell", state, 3500, 1, "")
	}
}
func TestCX08MemberDictionary(t *testing.T) {
	s, i := cx08Fixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	r := CX08DecodePCSSlots(s, i)
	cx08Want(t, r, "dtc_a_index", "accepted", 0, 1, "")
	wrong, _ := NewCXSelection(func() CX01SelectionSpec {
		c := s.CX01Spec()
		c.ProjectCode = "QAAa"
		c.Release = "9011"
		c.Choice = "release11"
		return c
	}())
	i.Member = &wrong
	r = CX08DecodePCSSlots(s, i)
	if r.Fields["dtc_a"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal("wrong dictionary", r)
	}
	i.Member = nil
	if CX08DecodePCSSlots(s, i).Fields["dtc_a"].Status == "accepted" {
		t.Fatal("inferred member")
	}
}
func TestCX08MemberIndexInheritsSourceStatus(t *testing.T) {
	s, i := cx08Fixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
	i.NativeSlot = 2
	i.Member = &s
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned"} {
		i.FieldStates = map[string]string{"dtc_a": state}
		r := CX08DecodePCSSlots(s, i)
		if r.Fields["dtc_a_index"].Status != state || r.Fields["dtc_a_index"].Meaning != "" {
			t.Fatal(state, r.Fields["dtc_a_index"])
		}
		cx08Want(t, r, "dtc_b_index", "accepted", 1, 1, "")
	}
	i.FieldStates = nil
	i.FieldAges = map[string]time.Duration{"dtc_a": -time.Second}
	if r := CX08DecodePCSSlots(s, i); r.Fields["dtc_a_index"].Status != "invalid" {
		t.Fatal("negative age", r.Fields["dtc_a_index"])
	}
	i.FieldAges = map[string]time.Duration{"dtc_a": 2 * time.Second}
	if r := CX08DecodePCSSlots(s, i); r.Fields["dtc_a_index"].Age != 2*time.Second {
		t.Fatal("age", r.Fields["dtc_a_index"])
	}
}
func TestCX08ReleaseAndPadding(t *testing.T) {
	for _, pid := range []uint32{0x326, 0x327, 0x333, 0x334} {
		s, i := cx08Fixture(t, pid, [8]byte{})
		for _, identity := range []string{"QAAA0011", "QAAa9011", "QAAA0099"} {
			s2, j := s, i
			s2, j.CX05PCSInput = cx05Reselect(t, s, j.CX05PCSInput, func(c *CX01SelectionSpec) {
				c.ProjectCode = identity[:4]
				c.Release = identity[4:]
				if identity == "QAAa9011" {
					c.Choice = "release11"
				}
			})
			r := CX08DecodePCSSlots(s2, j)
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
		r := CX08DecodePCSSlots(s, i)
		for _, f := range r.Fields {
			if f.Status == "accepted" {
				t.Fatal("padding admitted", r)
			}
		}
	}
	s, i := cx08Fixture(t, 0x334, [8]byte{7, 9})
	if CX08DecodePCSSlots(s, i).Fields["state"].Status == "accepted" {
		t.Fatal("old member padding")
	}
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.ProjectCode = "QAAa"; c.Release = "9011"; c.Choice = "release11" })
	cx08Want(t, CX08DecodePCSSlots(s, i), "independent", "accepted", 9, 1, "")
}
func TestCX08AdmissionIdentity(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "publisher", "ide", "dlc", "raw-dlc", "age", "native-record"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx08Fixture(t, 0x326, [8]byte{2, 0x11, 0x70, 0x0f, 0xa0})
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
			r := CX08DecodePCSSlots(s, i)
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
				s, i := cx08Fixture(t, pid, [8]byte{2, 2})
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
				r := CX08DecodePCSSlots(s, i)
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
			s, i := cx08Fixture(t, 0x333, [8]byte{3, 2, 0x10, 0, 0x10, 1})
			i.NativeSlot = 2
			c := s.CX01Spec()
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
				c.Bank = "other"
			case "endpoint":
				c.Endpoint = "other"
			case "group":
				c.Group = "other"
			case "slot":
				c.Slot = "other"
			}
			member, err := NewCXSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			i.Member = &member
			r := CX08DecodePCSSlots(s, i)
			if r.Fields["dtc_a"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	s, i := cx08Fixture(t, 0x333, [8]byte{})
	i.Member = &s
	if r := CX08DecodePCSSlots(s, i); r.Fields["dtc_a"].Status != "empty" {
		t.Fatal(r)
	}
}
