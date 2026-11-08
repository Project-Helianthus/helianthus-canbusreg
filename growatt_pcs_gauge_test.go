package canbusreg

import (
	"fmt"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func cx06Fixture(t *testing.T, identity string, pid uint32, p [8]byte) (CXSelection, CX06PCSInput) {
	t.Helper()
	s, base := cx05Fixture(t, pid, p)
	s, base = cx05Reselect(t, s, base, func(c *CX01SelectionSpec) {
		c.ProjectCode = identity[:4]
		c.Release = identity[4:]
		if identity == "QAAa9011" {
			c.Choice = "release11"
		}
	})
	return s, CX06PCSInput{CX05PCSInput: base, ThresholdStateKnown: true, SerialSlotValid: true}
}
func cx06Want(t *testing.T, r CXNativeResult, name, status string, n, d int64, unit string) {
	t.Helper()
	f, ok := r.Fields[name]
	if !ok || f.Status != status || f.Numerator != n || f.Denominator != d || f.Unit != unit || f.PhysicalQualified || r.PhysicalQualified {
		t.Fatalf("%s: %+v result=%s", name, f, r.Status)
	}
	if f.Raw != r.Raw || f.Evidence != r.Evidence || f.Epoch != r.Epoch {
		t.Fatal("lost evidence")
	}
}
func TestCX06GaugeVariants(t *testing.T) {
	for _, identity := range []string{"QAaT8252", "QAAA0002", "QAAA0005", "QAAa2005", "QAAa3005", "QAAa5005", "QAAa6005", "QAAa9005", "QAAa9006"} {
		t.Run("early_cells_"+identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x316, [8]byte{0, 10, 0, 20, 0, 30, 0, 40})
			i.Count = 6
			r := CX06DecodePCSGauge(s, i)
			cx06Want(t, r, "cell_5", "accepted", 10, 1, "mV")
			cx06Want(t, r, "cell_6", "accepted", 20, 1, "mV")
			cx06Want(t, r, "cell_7", "raw", 30, 1, "")
			for _, count := range []uint16{4, 5, 7, 8, 9} {
				i.Count = count
				r = CX06DecodePCSGauge(s, i)
				for pos := 5; pos <= 8; pos++ {
					f := r.Fields["cell_"+string(rune('0'+pos))]
					if (f.Status == "accepted") != (uint16(pos) <= count) {
						t.Fatal(count, pos, f)
					}
				}
			}
			i.CountValid = false
			cx06Want(t, CX06DecodePCSGauge(s, i), "cell_5", "raw", 10, 1, "")
			i.CountValid = true
			i.MeasurementValid = false
			cx06Want(t, CX06DecodePCSGauge(s, i), "cell_5", "raw", 10, 1, "")
		})
	}
	for _, identity := range []string{"QAAb5004", "QAAB0004", "QAAB0006"} {
		t.Run("byte_capacity_"+identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x316, [8]byte{0, 1, 0, 2, 3, 4, 5, 6})
			r := CX06DecodePCSGauge(s, i)
			cx06Want(t, r, "capacity_byte", "accepted", 3, 1, "")
			cx06Want(t, r, "gauge_byte", "accepted", 4, 1, "")
			i.MeasurementValid = false
			cx06Want(t, CX06DecodePCSGauge(s, i), "capacity_byte", "raw", 3, 1, "")
		})
	}
	for _, identity := range []string{"QAAA0006", "QAAB0002", "QAAB0003"} {
		t.Run("three_words_"+identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x316, [8]byte{0x13, 0x88, 0, 42, 0, 53, 100, 7})
			r := CX06DecodePCSGauge(s, i)
			if identity == "QAAA0006" {
				cx06Want(t, r, "capacity", "accepted", 42, 10, "Ah")
				cx06Want(t, r, "cycles", "accepted", 53, 10, "cycles")
			} else {
				cx06Want(t, r, "capacity", "raw", 42, 1, "")
			}
			status := "raw"
			if identity == "QAAA0006" {
				status = "accepted"
			}
			cx06Want(t, r, "state", status, 100, 1, "")
			cx06Want(t, r, "budget", status, 7, 1, "")
		})
	}
	for _, identity := range []string{"QAAA0007", "QAAA0008", "QAAA0009", "QAAA0010", "QAAA0011", "QAAa1011", "QAAa2007", "QAAa4010", "QAAa4011", "QAAa6008", "QAAa6009", "QAAa7004", "QAAa8008", "QAAa9010", "QAAa9011"} {
		t.Run("fault_words_"+identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x316, [8]byte{1, 2, 0, 3, 0, 4, 0, 5})
			r := CX06DecodePCSGauge(s, i)
			cx06Want(t, r, "retained_fault", "accepted", 1, 1, "")
			if s.CX01Group() == "L8" {
				cx06Want(t, r, "capacity", "accepted", 4, 10, "Ah")
				cx06Want(t, r, "cycles", "accepted", 5, 10, "cycles")
			} else {
				cx06Want(t, r, "word_4", "raw", 4, 1, "")
			}
		})
	}
}
func TestCX06NamedHypothesisNegatives(t *testing.T) {
	for _, tc := range []struct {
		name, identity string
		pid            uint32
		field          string
	}{
		{"early315_scope", "QAAA0005", 0x315, "word_0"}, {"early317_scope", "QAAA0005", 0x317, "retained_15"}, {"early318_scope", "QAaT8252", 0x318, "word_0"},
		{"accumulator_scope", "QAAA0006", 0x316, "accumulator"}, {"latched_state_scope", "QAAA0006", 0x316, "state"}, {"residual_budget_scope", "QAAA0006", 0x316, "budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := cx06Fixture(t, tc.identity, tc.pid, [8]byte{0x13, 0x88, 2, 3, 4, 5, 100, 7})
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
			r := CX06DecodePCSGauge(s, i)
			f := r.Fields[tc.field]
			if f.Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	for _, tc := range []struct {
		name         string
		zero, failed bool
	}{{"initial_without_zero_path", false, true}, {"hundred_without_failed_precheck", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0006", 0x316, [8]byte{0x13, 0x88, 0, 1, 0, 2, 100, 9})
			i.ZeroIntegration = tc.zero
			i.FailedPrecheck = tc.failed
			r := CX06DecodePCSGauge(s, i)
			field := "initial_accumulator"
			if !tc.failed {
				field = "failed_precheck_state"
			}
			if r.Fields[field].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	for _, n := range []uint16{4999, 5000, 5001} {
		s, i := cx06Fixture(t, "QAAA0006", 0x316, [8]byte{byte(n >> 8), byte(n), 0, 1, 0, 2, 100, 7})
		i.ZeroIntegration = true
		i.FailedPrecheck = true
		r := CX06DecodePCSGauge(s, i)
		if (r.Fields["initial_accumulator"].Status == "accepted") != (n == 5000) {
			t.Fatal(n, r)
		}
	}
	for _, n := range []byte{99, 100, 101} {
		s, i := cx06Fixture(t, "QAAA0006", 0x316, [8]byte{0, 1, 0, 2, 0, 3, n, 7})
		i.FailedPrecheck = true
		r := CX06DecodePCSGauge(s, i)
		if (r.Fields["failed_precheck_state"].Status == "accepted") != (n == 100) {
			t.Fatal(n, r)
		}
	}
}
func TestCX06RetainedFaultVariants(t *testing.T) {
	for _, identity := range []string{"QAAA0005", "QAAa2005", "QAAA0006", "QAAA0011", "QAAa9011", "QAAb5004"} {
		t.Run(identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x317, [8]byte{1, 2, 3, 4, 5, 6, 99, 0})
			r := CX06DecodePCSGauge(s, i)
			if s.CX01Group() == "L1" {
				cx06Want(t, r, "retained_15", "accepted", 0, 1, "")
			} else {
				cx06Want(t, r, "retained_13", "accepted", 6, 1, "")
				if identity == "QAAA0006" || identity == "QAAA0011" || identity == "QAAa9011" {
					cx06Want(t, r, "soh_truncated", "accepted", 99, 1, "percent")
				} else {
					cx06Want(t, r, "soh_truncated", "raw", 99, 1, "")
				}
				p := i.Evidence.Frame.Bytes()
				p[7] = 1
				id, _ := canbus.NewStandardID(0x317)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
				cx06Want(t, CX06DecodePCSGauge(s, i), "soh_truncated", "raw", 99, 1, "")
			}
		})
	}
	s, i := cx06Fixture(t, "QAAA0006", 0x318, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	r := CX06DecodePCSGauge(s, i)
	cx06Want(t, r, "retained_7", "accepted", 8, 1, "")
	if _, ok := r.Fields["current_fault"]; ok {
		t.Fatal(r)
	}
}
func TestCX06EarlyL1RetainedFaultBytes(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0005", 0x318, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	r := CX06DecodePCSGauge(s, i)
	for at := 0; at < 8; at++ {
		cx06Want(t, r, fmt.Sprintf("retained_%d", at), "accepted", int64(at+1), 1, "")
	}
	if _, ok := r.Fields["word_0"]; ok {
		t.Fatal(r)
	}
}
func TestCX06AdmissionAndRetention(t *testing.T) {
	for _, key := range []string{"interface_name", "interface_index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "publisher", "ide", "dlc", "age"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0006", 0x315, [8]byte{0, 1, 0, 2, 0, 3, 0, 4})
			switch key {
			case "interface_name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface_index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "other"
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
			case "age":
				i.FieldAge = -1
			case "ide":
				id, _ := canbus.NewExtendedID(0x315)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data())
			case "dlc":
				id, _ := canbus.NewStandardID(0x315)
				i.Evidence.Frame, _ = canbus.NewFrame(id, []byte{1})
			}
			r := CX06DecodePCSGauge(s, i)
			if len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	s, i := cx06Fixture(t, "QAAA0006", 0x315, [8]byte{0, 1, 0, 2, 0, 3, 0, 4})
	old := CX06DecodePCSGauge(s, i)
	i.FieldStates = map[string]string{"capacity": "stale"}
	i.FieldAges = nil
	r := CX06DecodePCSGauge(s, i)
	cx06Want(t, r, "capacity", "stale", 4, 10, "")
	kept := CX05RetainAccepted(old, r)
	if kept.Fields["capacity"] != old.Fields["capacity"] || r.Fields["checkpoint"].Status != "accepted" {
		t.Fatal(kept)
	}
}

func TestCX06EarlyWordsNegativeSelectors(t *testing.T) {
	for _, pid := range []uint32{0x315, 0x317, 0x318} {
		t.Run(string(rune(pid)), func(t *testing.T) {
			s, i := cx06Fixture(t, "QAaT8252", pid, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
			cx06Want(t, CX06DecodePCSGauge(s, i), "word_0", "accepted", 258, 1, "")
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.ProjectCode = "qaAT" })
			r := CX06DecodePCSGauge(s, i)
			if r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
			for _, f := range r.Fields {
				if f.Status == "accepted" {
					t.Fatal(r)
				}
			}
		})
	}
	for _, identity := range []string{"QAAA0005", "QAAa2005", "QAAa3005", "QAAa5005", "QAAa6005", "QAAa9005", "QAAa9006"} {
		s, i := cx06Fixture(t, identity, 0x317, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
		cx06Want(t, CX06DecodePCSGauge(s, i), "retained_15", "accepted", 8, 1, "")
		s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
		cx06Want(t, CX06DecodePCSGauge(s, i), "retained_15", "raw", 8, 1, "")
	}
}
func TestCX06ByteGaugeIndependentNegative(t *testing.T) {
	s, i := cx06Fixture(t, "QAAb5004", 0x316, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	i.FieldStates = map[string]string{"capacity_byte": "invalid"}
	r := CX06DecodePCSGauge(s, i)
	cx06Want(t, r, "capacity_byte", "invalid", 5, 1, "")
	cx06Want(t, r, "gauge_byte", "accepted", 6, 1, "")
	for _, key := range []string{"selector", "scope"} {
		t.Run(key, func(t *testing.T) {
			ss, ii := s, i
			ii.FieldStates = nil
			ss, ii.CX05PCSInput = cx05Reselect(t, s, ii.CX05PCSInput, func(c *CX01SelectionSpec) {
				if key == "selector" {
					c.ProjectCode = "ZZZZ"
				} else {
					c.Scope = "aggregate"
				}
			})
			rr := CX06DecodePCSGauge(ss, ii)
			if rr.Fields["gauge_byte"].Status == "accepted" || rr.Raw != r.Raw {
				t.Fatal(rr)
			}
		})
	}
}
func TestCX06UnassignedTypedRaw(t *testing.T) {
	for _, pid := range []uint32{0x315, 0x316, 0x317, 0x318} {
		s, i := cx06Fixture(t, "ZZZZ0001", pid, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
		r := CX06DecodePCSGauge(s, i)
		for _, f := range r.Fields {
			if f.Status != "raw" || f.Unit != "" || f.Meaning != "" || f.Grade != "Unknown" || f.Denominator != 1 {
				t.Fatal(f)
			}
		}
	}
}
func TestCX06RawDLCAndNativeRecords(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0006", 0x315, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	for _, key := range []string{"zero_native_record", "mismatched_native_record", "mismatched_evidence_record", "raw_dlc"} {
		t.Run(key, func(t *testing.T) {
			ii := i
			switch key {
			case "zero_native_record":
				ii.NativeRecord = make([]byte, canbus.SocketCANRecordSize)
			case "mismatched_native_record":
				ii.NativeRecord = make([]byte, canbus.SocketCANRecordSize)
				ii.NativeRecord[0] = 1
			case "mismatched_evidence_record":
				ii.Evidence.RawRecord[0] = 1
			case "raw_dlc":
				record := make([]byte, canbus.SocketCANRecordSize)
				record[0] = 0x15
				record[1] = 3
				record[4], record[7] = 8, 9
				copy(record[8:], i.Evidence.Frame.Data())
				frame, err := canbus.DecodeSocketCANRecord(record)
				if err != nil {
					t.Fatal(err)
				}
				ii.Evidence.Frame = frame
			}
			r := CX06DecodePCSGauge(s, ii)
			if r.Status != "invalid" || len(r.Fields) != 0 || r.Raw != ii.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}
func TestCX06FieldStatusesAndAges(t *testing.T) {
	for _, status := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned"} {
		s, i := cx06Fixture(t, "QAAA0006", 0x315, [8]byte{})
		i.FieldStates = map[string]string{"capacity": status}
		r := CX06DecodePCSGauge(s, i)
		cx06Want(t, r, "capacity", status, 0, 10, "")
		cx06Want(t, r, "checkpoint", "accepted", 0, 1, "")
	}
	s, i := cx06Fixture(t, "QAAA0006", 0x315, [8]byte{})
	i.FieldAges = map[string]time.Duration{"capacity": -1}
	r := CX06DecodePCSGauge(s, i)
	cx06Want(t, r, "capacity", "invalid", 0, 10, "")
	if r.Fields["checkpoint"].Age != i.FieldAge {
		t.Fatal(r)
	}
}

func TestCX06SOHTruncationAndIndependentRounding(t *testing.T) {
	for _, soh := range []byte{0, 99, 100, 101} {
		s, i := cx06Fixture(t, "QAAA0011", 0x317, [8]byte{1, 2, 3, 4, 5, 6, soh, 0})
		r := CX06DecodePCSGauge(s, i)
		if (r.Fields["soh_truncated"].Status == "accepted") != (soh <= 100) {
			t.Fatal(soh, r)
		}
		if r.Fields["retained_8"].Status != "accepted" {
			t.Fatal("SOH rejected sibling")
		}
	}
	s, i := cx06Fixture(t, "QAAA0011", 0x317, [8]byte{1, 2, 3, 4, 5, 6, 99, 0})
	r := CX06DecodePCSGauge(s, i)
	ss, ii := cx06Fixture(t, "QAAA0011", 0x313, [8]byte{0, 1, 0, 2, 0, 3, 50, 100})
	core := CX05DecodePCSStandard(ss, ii.CX05PCSInput)
	if core.Fields["soh"].Status != "accepted" || r.Fields["soh_truncated"].Status != "accepted" || r.Fields["soh_truncated"].Numerator != 99 || core.Fields["soh"].Numerator != 100 {
		t.Fatal("independent rounding rejected", r, core)
	}
}

func TestCX06L8WithheldKeepsFieldNames(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0011", 0x316, [8]byte{1, 2, 0, 3, 0, 4, 0, 5})
	i.MeasurementValid = false
	r := CX06DecodePCSGauge(s, i)
	for _, name := range []string{"capacity", "cycles"} {
		if _, ok := r.Fields[name]; !ok {
			t.Fatalf("withheld L8 lost %s: %v", name, r)
		}
		if f := r.Fields[name]; f.Status == "accepted" || f.Denominator != 1 || f.Unit != "" {
			t.Fatal(f)
		}
	}
	if _, ok := r.Fields["word_4"]; ok {
		t.Fatal(r)
	}
	i.MeasurementValid = true
	i.FieldStates = map[string]string{"capacity": "stale", "cycles": "invalid"}
	r = CX06DecodePCSGauge(s, i)
	cx06Want(t, r, "capacity", "stale", 4, 10, "")
	cx06Want(t, r, "cycles", "invalid", 5, 10, "")
	i.MeasurementValid = false
	r = CX06DecodePCSGauge(s, i)
	if r.Fields["capacity"].Status != "stale" || r.Fields["cycles"].Status != "invalid" {
		t.Fatal(r)
	}
}
