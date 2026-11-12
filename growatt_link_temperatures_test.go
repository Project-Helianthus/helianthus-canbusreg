package canbusreg

import (
	"fmt"
	"testing"
	"time"
)

func TestCX12FineMeanEachStaleContributorAndAgeBoundary(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	raw := vectorTestWords(424, 400, 412, 0)
	for bad := 0; bad < 4; bad++ {
		for _, age := range []time.Duration{time.Second, time.Second + 1} {
			t.Run(fmt.Sprintf("slot-%d-age-%d", bad+1, age), func(t *testing.T) {
				gates := vectorTestGates()
				gates[bad].Age = age
				v := vectorTestSensors(t, spec, 4, vectorTestWords(400, 408, 416, 424), gates)
				r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v})
				if r.Raw != raw || (r.Fields["mean"].Status == "accepted") != (age == time.Second) {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestCX12TemperatureSelectorAndIndependentWords(t *testing.T) {
	raw := vectorTestWords(408, 400, 404, 1)
	for _, id := range []struct {
		code, release, choice string
		mean                  bool
	}{{"QAAA", "0009", "", true}, {"QAAA", "0010", "", true}, {"QAAA", "0011", "", true}, {"QAAa", "9010", "", false}, {"QAAa", "9011", "legacy19", false}, {"QAAa", "9011", "release11", true}} {
		t.Run(id.code+id.release+id.choice, func(t *testing.T) {
			spec := selectionTestSpec(t, id.code, id.release)
			spec.Choice = id.choice
			s := selectionTestSelect(t, spec)
			v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 408, 0, 0), vectorTestGates())
			g := SummaryGates{FineTemperatureValid: true, Maximum: vectorTestGates()[0], Mean: vectorTestGates()[0], Contributors: v}
			r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), g)
			if (r.Fields["mean"].Status == "accepted") != id.mean || r.Fields["maximum"].Status != "accepted" || r.Fields["minimum"].Status == "accepted" || r.Fields["reserved"].Status != "invalid" || r.Raw != raw {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12NoPhysicalSensorAssociation(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	p := vectorTestWords(400, 400, 400, 400)
	for _, configured := range []bool{false, true} {
		g := vectorTestGates()
		g[0].Configured = configured
		v := vectorTestSensors(t, spec, 4, p, g)
		f := v.Fields[1]
		if f.Raw != p || f.PhysicalQualified || (f.Meaning != "" && f.Meaning != "fine sensor code; location unspecified") {
			t.Fatal(f)
		}
	}
}

func TestCX12SummaryAssociationAndEvidenceRetention(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	raw := vectorTestWords(408, 400, 404, 0)
	for _, key := range []string{"source", "epoch", "project", "release", "flavor", "pcs-flavor", "choice", "scope", "bank", "endpoint", "group", "slot", "direction", "route-set", "destination", "interface-name", "interface-index", "later-summary", "count-valid", "missing-field", "invalid-field"} {
		t.Run(key, func(t *testing.T) {
			other := spec
			switch key {
			case "source":
				other.Source++
			case "epoch":
				other.Epoch++
			case "project":
				other.ProjectCode = "QAAB"
			case "release":
				other.Release = "0010"
			case "flavor":
				other.Flavor = "pcs-standard"
			case "pcs-flavor":
				other.PCSFlavor = "pcs-parallel"
			case "choice":
				other.Choice = "release11"
			case "scope":
				other.Scope = "aggregate"
			case "bank":
				other.Bank = "bank-b"
			case "endpoint":
				other.Endpoint = "endpoint-b"
			case "group":
				other.Group = "group-b"
			case "slot":
				other.Slot = "slot-b"
			case "direction":
				other.Direction = "coordinator-to-module"
			case "route-set":
				other.RouteSet = false
			case "destination":
				other.Destination++
			case "interface-name":
				other.Interface = vectorTestOtherInterface(t, "other", 1)
			case "interface-index":
				other.Interface = vectorTestOtherInterface(t, "synthetic", 2)
			}
			v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 408, 0, 0), vectorTestGates())
			if key != "later-summary" && key != "count-valid" && key != "missing-field" && key != "invalid-field" {
				v.Selection.spec = other
			}
			if key == "count-valid" {
				v.CountValid = false
			}
			if key == "missing-field" {
				delete(v.Fields, 2)
			}
			if key == "invalid-field" {
				f := v.Fields[2]
				f.Status = "invalid"
				v.Fields[2] = f
			}
			i := vectorTestSummaryInput(t, spec, 0x1862, raw)
			if key == "later-summary" {
				i.Evidence.Monotonic = 3 * time.Second
			}
			r := DecodeLINKTemperatureSummary(s, i, SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v})
			if r.Raw != raw || r.Fields["mean"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func vectorTestSensors(t *testing.T, spec SelectionSpec, count int, p [8]byte, g [4]MeasurementGate) VectorSnapshot {
	a := NewLINKTemperatures(selectionTestSelect(t, spec), count, true)
	a.DecodeLINKVector(selectionTestInput(t, spec, 0x1830, p), g)
	return a.SnapshotLINKVector(time.Second, time.Second)
}
func TestCX12FineTemperaturesAndMean(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	p := vectorTestWords(400, 408, 0, 0)
	v := vectorTestSensors(t, spec, 2, p, vectorTestGates())
	f := v.Fields[1]
	if f.Numerator != 80 || f.Denominator != 8 || f.Unit != "C" || f.Meaning != "fine sensor code; location unspecified" || f.PhysicalQualified {
		t.Fatal(f)
	}
	g := SummaryGates{FineTemperatureValid: true, Maximum: vectorTestGates()[0], Minimum: vectorTestGates()[0], Mean: vectorTestGates()[0], Contributors: v}
	r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, vectorTestWords(408, 400, 404, 0)), g)
	if f := r.Fields["mean"]; f.Status != "accepted" || f.Numerator != 84 || f.Denominator != 8 {
		t.Fatal(r)
	}
}
func TestCX12MeanCountAndInvalidContributorNegatives(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	p := vectorTestWords(400, 408, 416, 424)
	for _, count := range []int{0, 1, 4, 5} {
		t.Run(string(rune('A'+count)), func(t *testing.T) {
			v := vectorTestSensors(t, spec, count, p, vectorTestGates())
			code := uint16(400)
			if count == 4 {
				code = 412
			}
			raw := vectorTestWords(424, 400, code, 0)
			g := SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v}
			r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), g)
			if (r.Fields["mean"].Status == "accepted") != (count == 1 || count == 4) || r.Raw != raw {
				t.Fatal(r)
			}
		})
	}
	for _, bad := range []string{"invalid-1", "invalid-2", "stale-1", "stale-2", "ffff-contribution", "fixed-four", "count-unqualified", "fine-gate"} {
		t.Run(bad, func(t *testing.T) {
			gates := vectorTestGates()
			rawSensors := p
			switch bad {
			case "invalid-1":
				gates[0].Valid = false
			case "invalid-2":
				gates[1].Valid = false
			case "stale-1":
				gates[0].Age = 2 * time.Second
			case "stale-2":
				gates[1].Age = 2 * time.Second
			case "ffff-contribution":
				rawSensors = vectorTestWords(400, 65535, 416, 424)
			}
			v := vectorTestSensors(t, spec, 2, rawSensors, gates)
			if bad == "count-unqualified" {
				v.CountValid = false
			}
			g := SummaryGates{FineTemperatureValid: bad != "fine-gate", Mean: vectorTestGates()[0], Contributors: v, FixedFourPath: bad == "fixed-four"}
			raw := vectorTestWords(408, 400, 404, 0)
			r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), g)
			if r.Raw != raw || r.Fields["mean"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}
func TestCX12FineZeroFFFFAndUnconfigured(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	a := NewLINKTemperatures(selectionTestSelect(t, spec), 4, true)
	g := vectorTestGates()
	g[2].Configured = false
	r := a.DecodeLINKVector(selectionTestInput(t, spec, 0x1830, vectorTestWords(0, 65535, 400, 400)), g)
	if r.Fields["sensor_1"].Numerator != -320 || r.Fields["sensor_2"].Status != "invalid" || r.Fields["sensor_3"].Status != "unconfigured" || r.Fields["sensor_4"].Status != "accepted" {
		t.Fatal(r)
	}
}

func vectorTestSummaryInput(t *testing.T, spec SelectionSpec, kind uint32, p [8]byte) LINKInput {
	i := selectionTestInput(t, spec, kind, p)
	i.Evidence.Sequence = 10
	return i
}

func TestCX12MeanFloorAndInvalidCodesNeverFiltered(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 409, 0, 0), vectorTestGates())
	raw := vectorTestWords(409, 400, 404, 0)
	r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v})
	if r.Fields["mean"].Status != "accepted" || r.Fields["mean"].Numerator != 84 {
		t.Fatal(r)
	}
	for _, reported := range []uint16{400, 32967} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 65535, 0, 0), vectorTestGates())
			raw := vectorTestWords(65535, 400, reported, 0)
			r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v})
			if r.Raw != raw || r.Fields["mean"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12SummaryRejectsReusedContributorEvidence(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	raw := vectorTestWords(408, 400, 404, 0)
	for _, bad := range []string{"epoch", "source", "destination", "interface", "scope", "bank", "endpoint", "group", "slot", "same-sequence", "future-sequence", "future-clock", "raw", "wrong-family"} {
		t.Run(bad, func(t *testing.T) {
			v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 408, 0, 0), vectorTestGates())
			f := v.Fields[1]
			switch bad {
			case "epoch":
				f.Epoch++
			case "source":
				other := spec
				other.Source++
				f.Evidence = selectionTestInput(t, other, 0x1830, f.Raw).Evidence
			case "destination":
				other := spec
				other.Destination++
				f.Evidence = selectionTestInput(t, other, 0x1830, f.Raw).Evidence
			case "interface":
				f.Evidence.Interface = vectorTestOtherInterface(t, "other", 1)
			case "scope":
				f.Scope = "aggregate"
			case "bank":
				f.Bank = "other"
			case "endpoint":
				f.Endpoint = "other"
			case "group":
				f.Group = "other"
			case "slot":
				f.Slot = "other"
			case "same-sequence":
				f.Evidence.Sequence = 10
			case "future-sequence":
				f.Evidence.Sequence = 11
			case "future-clock":
				f.Evidence.Monotonic = 2 * time.Second
			case "raw":
				f.Raw[0]++
			case "wrong-family":
				f.Evidence = selectionTestInput(t, spec, 0x1810, f.Raw).Evidence
			}
			v.Fields[1] = f
			r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, raw), SummaryGates{FineTemperatureValid: true, Mean: vectorTestGates()[0], Contributors: v})
			if r.Raw != raw || r.Fields["mean"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12SummaryGateRejectionKeepsGateStatusAndAge(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 408, 0, 0), vectorTestGates())
	stale := MeasurementGate{Configured: true, Valid: false, Status: "stale", Age: 7 * time.Second}
	g := SummaryGates{FineTemperatureValid: true, Maximum: stale, Minimum: vectorTestGates()[0], Mean: stale, Contributors: v}
	r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, vectorTestWords(408, 400, 404, 0)), g)
	for _, name := range []string{"maximum", "mean"} {
		if f := r.Fields[name]; f.Status != "stale" || f.Age != 7*time.Second {
			t.Fatal(name, f)
		}
	}
	if f := r.Fields["minimum"]; f.Status != "accepted" {
		t.Fatal(f)
	}
}

func TestCX12SummarySentinelStaysInvalidWithValidGates(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0011")
	s := selectionTestSelect(t, spec)
	v := vectorTestSensors(t, spec, 2, vectorTestWords(400, 408, 0, 0), vectorTestGates())
	g := SummaryGates{FineTemperatureValid: true, Maximum: vectorTestGates()[0], Minimum: vectorTestGates()[0], Mean: vectorTestGates()[0], Contributors: v}
	r := DecodeLINKTemperatureSummary(s, vectorTestSummaryInput(t, spec, 0x1862, vectorTestWords(65535, 65535, 65535, 0)), g)
	for _, name := range []string{"maximum", "minimum", "mean"} {
		if f := r.Fields[name]; f.Status != "invalid" || f.Unit != "" || f.Grade == "Proven" {
			t.Fatal(name, f)
		}
	}
}
