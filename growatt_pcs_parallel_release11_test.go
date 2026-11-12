package canbusreg

import (
	"encoding/binary"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func parallelReleaseTestSpec(t *testing.T) SelectionSpec {
	c := parallelIdentityTestSpec(t)
	c.ProjectCode, c.Release, c.Slot = "QAAA", "0011", "2"
	return c
}
func parallelReleaseTestSource(c SelectionSpec, n int64) *CXNativeField {
	return &CXNativeField{Numerator: n, Denominator: 1, Status: "accepted", Grade: "Proven", Evidence: Evidence{Interface: c.Interface}, Epoch: c.Epoch, Scope: c.Scope, Bank: c.Bank, Endpoint: c.Endpoint, Group: c.Group, Slot: c.Slot}
}
func parallelReleaseTestDecode(t *testing.T, c SelectionSpec, id uint32, p [8]byte, x ParallelContext) CXNativeResult {
	t.Helper()
	i := parallelIdentityTestInput(t, c, id, p)
	r := ParallelReleaseDecodeParallel(parallelIdentityTestSelection(t, c), i, x)
	if r.Raw != p || r.Evidence != i.Evidence || r.PhysicalQualified {
		t.Fatal("lost native evidence", r)
	}
	for _, f := range r.Fields {
		if f.Raw != p || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.Age != i.FieldAge || f.PhysicalQualified {
			t.Fatal("lost field provenance", f)
		}
	}
	return r
}
func parallelReleaseTestWant(t *testing.T, r CXNativeResult, k string, n, d int64, status, grade string) {
	t.Helper()
	f, ok := r.Fields[k]
	if !ok || f.Numerator != n || f.Denominator != d || f.Status != status || f.Grade != grade {
		t.Fatalf("%s: %+v", k, f)
	}
}
func TestCX11CurrentAndMux(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	for _, mux := range []uint16{0, 0x8000} {
		for _, code := range []uint16{10000, 6000} {
			p := [8]byte{}
			binary.LittleEndian.PutUint16(p[:2], mux|513)
			binary.LittleEndian.PutUint16(p[2:4], code)
			binary.LittleEndian.PutUint16(p[4:6], 517)
			binary.LittleEndian.PutUint16(p[6:], 734)
			r := parallelReleaseTestDecode(t, c, 0x613, p, ParallelContext{})
			parallelReleaseTestWant(t, r, "voltage", 513, 10, "accepted", "Proven")
			parallelReleaseTestWant(t, r, "mux", int64(mux>>15), 1, "accepted", "Proven")
			parallelReleaseTestWant(t, r, "current", int64(code)-10000, 10, "accepted", "Proven")
			parallelReleaseTestWant(t, r, "port", 517, 10, "accepted", "Proven")
			parallelReleaseTestWant(t, r, "soc", 734, 10, "accepted", "Proven")
		}
	}
}
func TestCX11SlotsAndMemberDictionary(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{2, 0x34, 0x12, 9, 4, 5, 6, 0}
	r := parallelReleaseTestDecode(t, c, 0x513, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "minimum_cell", 0x1234, 1, "accepted", "Proven")
	parallelReleaseTestWant(t, r, "independent_b3", 9, 1, "raw", "Unknown")
	p[7] = 8
	r = parallelReleaseTestDecode(t, c, 0x513, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "state_7", 8, 1, "invalid", "Unknown")
	parallelReleaseTestWant(t, r, "minimum_cell", 0x1234, 1, "accepted", "Proven")
	c.Scope = "aggregate"
	r = parallelReleaseTestDecode(t, c, 0x513, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "state_7", 8, 1, "raw", "Unknown")
	c.Scope = "local"
	c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "release11"
	r = parallelReleaseTestDecode(t, c, 0x513, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "state_7", 8, 1, "accepted", "Proven")
	p = [8]byte{2, 4, 0xa1, 0, 0, 0, 0, 0}
	r = parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "dtc_a", 1185, 1, "accepted", "Proven")
	c.ProjectCode, c.Release, c.Choice = "QAAA", "0011", ""
	r = parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "dtc_a", 1185, 1, "unknown", "Hypothesis")
	p[0] = 3
	r = parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	if len(r.Fields) != 0 {
		t.Fatal("slot mismatch interpreted")
	}
}
func TestCX11TemperatureAndSOHBoundaries(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	for _, code := range []byte{0, 39, 40, 41, 255} {
		p := [8]byte{1, 0, code, 2, 0, code, code, 0}
		r := parallelReleaseTestDecode(t, c, 0x653, p, ParallelContext{})
		for _, k := range []string{"temperature_maximum", "temperature_minimum", "average_temperature"} {
			parallelReleaseTestWant(t, r, k, int64(code)-40, 1, "accepted", "Hypothesis")
		}
	}
	for _, v := range []struct {
		n    int64
		code byte
	}{{9, 0}, {10, 1}, {2557, 255}, {2558, 0}} {
		p := [8]byte{1, 0, 40, 2, 0, 40, 40, v.code}
		x := ParallelContext{SOHTenths: parallelReleaseTestSource(c, v.n)}
		r := parallelReleaseTestDecode(t, c, 0x653, p, x)
		parallelReleaseTestWant(t, r, "soh", int64(v.code), 1, "accepted", "Hypothesis")
		x.SOHTenths = nil
		r = parallelReleaseTestDecode(t, c, 0x653, p, x)
		parallelReleaseTestWant(t, r, "soh", int64(v.code), 1, "withheld", "Unknown")
	}
}
func TestCX11TemperatureScopeNegative(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 0, 41, 2, 0, 39, 40, 255}
	c.Scope = "aggregate"
	r := parallelReleaseTestDecode(t, c, 0x653, p, ParallelContext{SOHTenths: parallelReleaseTestSource(c, 2557)})
	parallelReleaseTestWant(t, r, "temperature_maximum", 41, 1, "raw", "Unknown")
	parallelReleaseTestWant(t, r, "soh", 255, 1, "raw", "Unknown")
}
func TestCX11TemperatureReservedNegative(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 1, 41, 2, 0, 39, 40, 0}
	r := parallelReleaseTestDecode(t, c, 0x653, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "temperature_maximum", 41, 1, "invalid", "Unknown")
	parallelReleaseTestWant(t, r, "temperature_minimum", -1, 1, "accepted", "Hypothesis")
}
func TestCX11SOHSourceNegative(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 0, 41, 2, 0, 39, 40, 1}
	x := ParallelContext{SOHTenths: parallelReleaseTestSource(c, 10)}
	x.SOHTenths.Scope = "aggregate"
	r := parallelReleaseTestDecode(t, c, 0x653, p, x)
	parallelReleaseTestWant(t, r, "soh", 1, 1, "withheld", "Unknown")
}
func TestCX11SOCSubstitutionAndState(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	for _, v := range []struct{ soc, permission, wire int64 }{{0, 1, 10}, {1, 1, 1}, {0, 0, 0}, {734, 1, 734}, {734, 0, 0}} {
		p := [8]byte{1, 0, 2, 0, 0, 0, 1, 1}
		binary.LittleEndian.PutUint16(p[4:6], uint16(v.wire))
		x := ParallelContext{SOCTenths: parallelReleaseTestSource(c, v.soc), Permission: parallelReleaseTestSource(c, v.permission), State: parallelReleaseTestSource(c, 6)}
		r := parallelReleaseTestDecode(t, c, 0x683, p, x)
		parallelReleaseTestWant(t, r, "soc_report", v.wire, 10, "accepted", "Proven")
		parallelReleaseTestWant(t, r, "state_bit_2", 1, 1, "accepted", "Proven")
		parallelReleaseTestWant(t, r, "state_bit_1", 1, 1, "accepted", "Proven")
		x.State = nil
		r = parallelReleaseTestDecode(t, c, 0x683, p, x)
		parallelReleaseTestWant(t, r, "state_bit_2", 1, 1, "withheld", "Unknown")
	}
}
func TestCX11DiagnosticBoundaries(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	for _, a := range []int64{0, 1} {
		for _, b := range []int64{0, 1} {
			for _, pack := range []int64{200, 201} {
				for _, state := range []int64{0, 1} {
					word := uint16(517)
					status := "accepted"
					if a != 0 || b != 0 {
						word = 65535
						status = "invalid"
					}
					p := [8]byte{0, 0, byte(pack - 200), byte(state), 0, 0, 0, 0}
					binary.LittleEndian.PutUint16(p[:2], word)
					x := ParallelContext{CategoryFirst: parallelReleaseTestSource(c, a), CategorySecond: parallelReleaseTestSource(c, b), PackCode: parallelReleaseTestSource(c, pack), State: parallelReleaseTestSource(c, state), CellSumValid: parallelReleaseTestSource(c, 1)}
					r := parallelReleaseTestDecode(t, c, 0x6d3, p, x)
					parallelReleaseTestWant(t, r, "cell_sum", int64(word), 10, status, "Hypothesis")
					parallelReleaseTestWant(t, r, "pack_over_200", pack-200, 1, "accepted", "Hypothesis")
					parallelReleaseTestWant(t, r, "state_nonzero", state, 1, "accepted", "Hypothesis")
				}
			}
		}
	}
}
func TestCX11DiagnosticFirstScopeNegative(t *testing.T) {
	parallelReleaseTestDiagnosticNegative(t, "first")
}
func TestCX11DiagnosticSecondScopeNegative(t *testing.T) {
	parallelReleaseTestDiagnosticNegative(t, "second")
}
func TestCX11PackSelectorNegative(t *testing.T)  { parallelReleaseTestDiagnosticNegative(t, "pack") }
func TestCX11StateEndpointNegative(t *testing.T) { parallelReleaseTestDiagnosticNegative(t, "state") }
func parallelReleaseTestDiagnosticNegative(t *testing.T, kind string) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{255, 255, 1, 1, 0, 0, 0, 0}
	x := ParallelContext{CategoryFirst: parallelReleaseTestSource(c, 1), CategorySecond: parallelReleaseTestSource(c, 1), PackCode: parallelReleaseTestSource(c, 201), State: parallelReleaseTestSource(c, 1)}
	switch kind {
	case "first":
		x.CategoryFirst.Scope = "aggregate"
	case "second":
		x.CategorySecond.Scope = "aggregate"
	case "pack":
		c.ProjectCode, c.Release = "QAAB", "0004"
	case "state":
		x.State.Endpoint = "other"
	}
	r := parallelReleaseTestDecode(t, c, 0x6d3, p, x)
	if kind == "pack" {
		if len(r.Fields) != 0 {
			t.Fatal("unknown selector interpreted")
		}
		return
	}
	key := "cell_sum"
	n := int64(65535)
	d := int64(10)
	if kind == "state" {
		key = "state_nonzero"
		n = 1
		d = 1
	}
	parallelReleaseTestWant(t, r, key, n, d, "withheld", "Unknown")
}
func TestCX11CellSumRequiresValidityEvidence(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{5, 2, 0, 0, 0, 0, 0, 0}
	base := func() ParallelContext {
		return ParallelContext{CategoryFirst: parallelReleaseTestSource(c, 0), CategorySecond: parallelReleaseTestSource(c, 0), PackCode: parallelReleaseTestSource(c, 200), State: parallelReleaseTestSource(c, 0)}
	}
	r := parallelReleaseTestDecode(t, c, 0x6d3, p, base())
	parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "withheld", "Unknown")
	x := base()
	x.CellSumValid = parallelReleaseTestSource(c, 0)
	r = parallelReleaseTestDecode(t, c, 0x6d3, p, x)
	parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "withheld", "Unknown")
	x.CellSumValid = parallelReleaseTestSource(c, 1)
	x.CellSumValid.Scope = "aggregate"
	r = parallelReleaseTestDecode(t, c, 0x6d3, p, x)
	parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "withheld", "Unknown")
	x.CellSumValid = parallelReleaseTestSource(c, 1)
	r = parallelReleaseTestDecode(t, c, 0x6d3, p, x)
	parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "accepted", "Hypothesis")
}
func TestCX11MissingCounters(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{5, 2, 0, 0, 0, 0, 0, 0}
	for _, which := range []int{0, 1, 2} {
		x := ParallelContext{PackCode: parallelReleaseTestSource(c, 200), State: parallelReleaseTestSource(c, 0)}
		if which == 1 {
			x.CategoryFirst = parallelReleaseTestSource(c, 0)
		}
		if which == 2 {
			x.CategorySecond = parallelReleaseTestSource(c, 0)
		}
		r := parallelReleaseTestDecode(t, c, 0x6d3, p, x)
		parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "withheld", "Unknown")
		parallelReleaseTestWant(t, r, "pack_over_200", 0, 1, "accepted", "Hypothesis")
	}
}
func TestCX11SummaryAndReceiverOnly(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	c.Scope = "aggregate"
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	r := parallelReleaseTestDecode(t, c, 0x590, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "aggregate_3", 1284, 1, "raw", "Unknown")
	parallelReleaseTestWant(t, r, "input_b7", 8, 1, "raw", "Unknown")
	for _, id := range []uint32{0x37f, 0x3f7} {
		r = parallelReleaseTestDecode(t, c, id, p, ParallelContext{})
		if r.Status != "raw" || len(r.Fields) != 0 {
			t.Fatal("control traffic interpreted", r)
		}
	}
}

func TestCX11AdmissionIdentityAndGeometry(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	s := parallelIdentityTestSelection(t, c)
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "age", "ide", "dlc", "raw-dlc", "address"} {
		t.Run(key, func(t *testing.T) {
			i := parallelIdentityTestInput(t, c, 0x613, p)
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
				i.Group = "4"
			case "slot":
				i.Slot = "3"
			case "age":
				i.FieldAge = -1
			case "ide":
				id, _ := canbus.NewExtendedID(0x613)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				id, _ := canbus.NewStandardID(0x613)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:7])
			case "raw-dlc":
				record := i.Evidence.RawRecord
				binary.LittleEndian.PutUint32(record[:4], 0x613)
				record[4] = 8
				record[7] = 9
				copy(record[8:], p[:])
				f, err := canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
				i.Evidence.Frame = f
				i.Evidence.RawRecord = record
			case "address":
				id, _ := canbus.NewStandardID(0x614)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			}
			r := ParallelReleaseDecodeParallel(s, i, ParallelContext{})
			if len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() || r.Evidence != i.Evidence || r.PhysicalQualified {
				t.Fatal("admitted mismatched key", r)
			}
		})
	}
}

func TestCX11SelectorNegatives(t *testing.T) {
	for _, key := range []string{"case", "release", "flavor", "direction", "choice-missing", "choice-legacy", "group-canonical", "group-bound", "slot-missing"} {
		t.Run(key, func(t *testing.T) {
			c := parallelReleaseTestSpec(t)
			p := [8]byte{2, 1, 2, 3, 4, 5, 6, 0}
			switch key {
			case "case":
				c.ProjectCode = "qAAA"
			case "release":
				c.Release = "0010"
			case "flavor":
				c.Flavor = "pcs-standard"
			case "direction":
				c.Direction = "pcs-to-bms"
			case "choice-missing":
				c.ProjectCode, c.Release = "QAAa", "9011"
			case "choice-legacy":
				c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "legacy19"
			case "group-canonical":
				c.Group = "03"
			case "group-bound":
				c.Group = "8"
			case "slot-missing":
				c.Slot = ""
			}
			r := parallelReleaseTestDecode(t, c, 0x513, p, ParallelContext{})
			if len(r.Fields) != 0 {
				t.Fatal("unselected member interpreted", r)
			}
		})
	}
}

func TestCX11SourceIdentityAndValidity(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 0, 40, 2, 0, 40, 40, 1}
	for _, key := range []string{"interface-name", "interface-index", "epoch", "scope", "bank", "endpoint", "group", "slot", "age", "status-invalid", "status-stale", "status-conflict", "status-absent", "grade", "denominator", "negative", "overflow"} {
		t.Run(key, func(t *testing.T) {
			f := parallelReleaseTestSource(c, 10)
			switch key {
			case "interface-name":
				f.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				f.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				f.Epoch++
			case "scope":
				f.Scope = "aggregate"
			case "bank":
				f.Bank = "other"
			case "endpoint":
				f.Endpoint = "other"
			case "group":
				f.Group = "4"
			case "slot":
				f.Slot = "3"
			case "age":
				f.Age = -1
			case "status-invalid":
				f.Status = "invalid"
			case "status-stale":
				f.Status = "stale"
			case "status-conflict":
				f.Status = "conflict"
			case "status-absent":
				f.Status = ""
			case "grade":
				f.Grade = "Unknown"
			case "denominator":
				f.Denominator = 10
			case "negative":
				f.Numerator = -1
			case "overflow":
				f.Numerator = 65536
			}
			r := parallelReleaseTestDecode(t, c, 0x653, p, ParallelContext{SOHTenths: f})
			parallelReleaseTestWant(t, r, "soh", 1, 1, "withheld", "Unknown")
			parallelReleaseTestWant(t, r, "temperature_maximum", 0, 1, "accepted", "Hypothesis")
		})
	}
}

func TestCX11ConflictsAndIndependentSiblings(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 0, 41, 2, 0, 39, 40, 2}
	r := parallelReleaseTestDecode(t, c, 0x653, p, ParallelContext{SOHTenths: parallelReleaseTestSource(c, 10)})
	parallelReleaseTestWant(t, r, "soh", 2, 1, "conflict", "Unknown")
	parallelReleaseTestWant(t, r, "temperature_minimum", -1, 1, "accepted", "Hypothesis")
	p = [8]byte{5, 2, 0, 1, 0, 0, 0, 0}
	x := ParallelContext{CategoryFirst: parallelReleaseTestSource(c, 1), CategorySecond: parallelReleaseTestSource(c, 0), PackCode: parallelReleaseTestSource(c, 200), State: parallelReleaseTestSource(c, 1)}
	r = parallelReleaseTestDecode(t, c, 0x6d3, p, x)
	parallelReleaseTestWant(t, r, "cell_sum", 517, 10, "conflict", "Hypothesis")
	parallelReleaseTestWant(t, r, "pack_over_200", 0, 1, "accepted", "Hypothesis")
	p = [8]byte{0, 0, 0, 0, 10, 0, 0, 0}
	x = ParallelContext{SOCTenths: parallelReleaseTestSource(c, 0), Permission: parallelReleaseTestSource(c, 0), State: parallelReleaseTestSource(c, 0)}
	r = parallelReleaseTestDecode(t, c, 0x683, p, x)
	parallelReleaseTestWant(t, r, "soc_report", 10, 10, "conflict", "Unknown")
	parallelReleaseTestWant(t, r, "state_bit_2", 0, 1, "accepted", "Proven")
	x.Permission = nil
	r = parallelReleaseTestDecode(t, c, 0x683, p, x)
	parallelReleaseTestWant(t, r, "soc_report", 10, 10, "withheld", "Unknown")
	p = [8]byte{2, 0x10, 0x00, 0x10, 0x01, 0, 0, 0}
	r = parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "dtc_a", 4096, 1, "accepted", "Proven")
	parallelReleaseTestWant(t, r, "dtc_b", 4097, 1, "accepted", "Proven")
	for at := 5; at < 8; at++ {
		bad := p
		bad[at] = 1
		r = parallelReleaseTestDecode(t, c, 0x523, bad, ParallelContext{})
		parallelReleaseTestWant(t, r, "dtc_b", 4097, 1, "accepted", "Proven")
	}
}

func TestCX11AggregateScopeWithholdsLocalRules(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	c.Scope = "aggregate"
	p := [8]byte{5, 2, 1, 1, 10, 0, 1, 1}
	for _, v := range []struct {
		id  uint32
		key string
		n   int64
	}{{0x613, "port", 10}, {0x683, "soc_report", 10}, {0x6d3, "cell_sum", 517}} {
		r := parallelReleaseTestDecode(t, c, v.id, p, ParallelContext{CategoryFirst: parallelReleaseTestSource(c, 0), CategorySecond: parallelReleaseTestSource(c, 0), PackCode: parallelReleaseTestSource(c, 201), State: parallelReleaseTestSource(c, 1), SOCTenths: parallelReleaseTestSource(c, 0), Permission: parallelReleaseTestSource(c, 1)})
		parallelReleaseTestWant(t, r, v.key, v.n, 1, "raw", "Unknown")
	}
}

func TestCX11MissingMemberDictionary(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	c.ProjectCode, c.Release = "QAAa", "9011"
	p := [8]byte{2, 4, 0xa1, 0, 0, 0, 0, 0}
	r := parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	if len(r.Fields) != 0 {
		t.Fatal("ambiguous member borrowed a dictionary", r)
	}
	c.Choice = "release11"
	r = parallelReleaseTestDecode(t, c, 0x523, p, ParallelContext{})
	parallelReleaseTestWant(t, r, "dtc_a", 1185, 1, "accepted", "Proven")
}

func TestCX11NativeEvidenceAndIndependentResults(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	p := [8]byte{1, 0, 41, 2, 0, 39, 40, 1}
	x := ParallelContext{SOHTenths: parallelReleaseTestSource(c, 10)}
	source := *x.SOHTenths
	for _, sequence := range []uint64{7, 23} {
		i := parallelIdentityTestInput(t, c, 0x653, p)
		record := i.Evidence.RawRecord
		binary.LittleEndian.PutUint32(record[:4], 0x653)
		record[4] = 8
		copy(record[8:], p[:])
		f, err := canbus.DecodeSocketCANRecord(record[:])
		if err != nil {
			t.Fatal(err)
		}
		i.Evidence.Frame = f
		i.Evidence.RawRecord = record
		i.Evidence.Sequence = sequence
		i.Evidence.Monotonic *= 2
		i.FieldAge *= 3
		good := ParallelReleaseDecodeParallel(parallelIdentityTestSelection(t, c), i, x)
		parallelReleaseTestWant(t, good, "soh", 1, 1, "accepted", "Hypothesis")
		for _, field := range good.Fields {
			if field.Evidence != i.Evidence || field.Raw != p || field.Age != i.FieldAge || field.PhysicalQualified {
				t.Fatal("native evidence not retained", field)
			}
		}
		i.Endpoint = "other"
		bad := ParallelReleaseDecodeParallel(parallelIdentityTestSelection(t, c), i, x)
		if bad.Status != "invalid" || len(bad.Fields) != 0 {
			t.Fatal("invalid update accepted")
		}
		parallelReleaseTestWant(t, good, "soh", 1, 1, "accepted", "Hypothesis")
		if *x.SOHTenths != source {
			t.Fatal("source modified")
		}
	}
}

func TestCX11SummaryVariantsAndLocalRejection(t *testing.T) {
	for _, modern := range []bool{false, true} {
		c := parallelReleaseTestSpec(t)
		if modern {
			c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "release11"
		}
		p := [8]byte{1, 2, 3, 4, 5, 6, 7, 0}
		r := parallelReleaseTestDecode(t, c, 0x590, p, ParallelContext{})
		if len(r.Fields) != 0 {
			t.Fatal("summary accepted as local")
		}
		c.Scope = "aggregate"
		for _, b7 := range []byte{0, 1, 255} {
			p[7] = b7
			r = parallelReleaseTestDecode(t, c, 0x590, p, ParallelContext{})
			parallelReleaseTestWant(t, r, "input_b7", int64(b7), 1, "raw", "Unknown")
			parallelReleaseTestWant(t, r, "state_0", 1, 1, "accepted", "Proven")
		}
	}
}
