package canbusreg

import "encoding/binary"

func CX12NewLINKTemperatures(s CXSelection, count int, countValid bool) *CX12LINKVector {
	return cx12NewVector(s, count, countValid, 4, "temperatures")
}

type CX12SummaryGates struct {
	FineTemperatureValid   bool
	Maximum, Minimum, Mean CX12MeasurementGate
	Contributors           CX12VectorSnapshot
	FixedFourPath          bool
}

func CX12DecodeLINKTemperatureSummary(s CXSelection, i CX01LINKInput, g CX12SummaryGates) CXNativeResult {
	r := cx01Result(s, i, "invalid")
	if !cx12Admit(s, i) {
		return r
	}
	if i.Evidence.Frame.ID().Value()>>16 != 0x1862 || (s.CX01Group() != "L4" && s.CX01Group() != "L8") {
		r.Status = "unsupported"
		return r
	}
	names := [3]string{"maximum", "minimum", "mean"}
	gates := [3]CX12MeasurementGate{g.Maximum, g.Minimum, g.Mean}
	mean, validMean := cx12Contributors(s, i, g.Contributors, "temperatures", g.Contributors.ConfiguredCount)
	meanSelector := s.CX01Group() == "L8" || s.spec.ProjectCode == "QAAA" && (s.spec.Release == "0009" || s.spec.Release == "0010")
	for j, name := range names {
		n := int64(binary.LittleEndian.Uint16(r.Raw[j*2:]))
		f := cx12Field(i, n)
		gate := gates[j]
		valid := g.FineTemperatureValid && cx12Gate(gate) == "accepted" && n != 65535
		if j == 2 {
			valid = valid && meanSelector && validMean && mean == n && !g.FixedFourPath
		}
		if valid {
			f.Numerator = n - 320
			f.Denominator = 8
			f.Unit = "C"
			f.Meaning = "fine temperature code"
			f.Grade = "Proven"
			f.Status = "accepted"
			f.Age = gate.Age
		}
		r.Fields[name] = f
	}
	f := cx12Field(i, int64(binary.LittleEndian.Uint16(r.Raw[6:])))
	if f.Numerator != 0 {
		f.Status = "invalid"
	}
	r.Fields["reserved"] = f
	r.Status = "accepted"
	return r
}
