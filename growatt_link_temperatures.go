package canbusreg

import "encoding/binary"

// NewLINKTemperatures creates bounded history for four sensor channels using an independently asserted count.
func NewLINKTemperatures(s CXSelection, count int, countValid bool) *LINKVector {
	return lINKVectorNewVector(s, count, countValid, 4, "temperatures")
}

// SummaryGates carries independent validity assertions and contributor evidence for temperature summaries.
type SummaryGates struct {
	FineTemperatureValid   bool
	Maximum, Minimum, Mean MeasurementGate
	Contributors           VectorSnapshot
	FixedFourPath          bool
}

// DecodeLINKTemperatureSummary applies per-field gates and contributor checks while preserving unresolved raw codes.
func DecodeLINKTemperatureSummary(s CXSelection, i LINKInput, g SummaryGates) CXNativeResult {
	r := selectionResult(s, i, "invalid")
	if !lINKVectorAdmit(s, i) {
		return r
	}
	if i.Evidence.Frame.ID().Value()>>16 != 0x1862 || (s.SelectionGroup() != "L4" && s.SelectionGroup() != "L8") {
		r.Status = "unsupported"
		return r
	}
	names := [3]string{"maximum", "minimum", "mean"}
	gates := [3]MeasurementGate{g.Maximum, g.Minimum, g.Mean}
	mean, validMean := lINKVectorContributors(s, i, g.Contributors, "temperatures", g.Contributors.ConfiguredCount)
	meanSelector := s.SelectionGroup() == "L8" || s.spec.ProjectCode == "QAAA" && (s.spec.Release == "0009" || s.spec.Release == "0010")
	for j, name := range names {
		n := int64(binary.LittleEndian.Uint16(r.Raw[j*2:]))
		f := lINKVectorField(i, n)
		gate := gates[j]
		valid := g.FineTemperatureValid && lINKVectorGate(gate) == "accepted" && n != 65535
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
		} else {
			f = lINKVectorGateRejection(f, gate)
			if n == 65535 {
				f.Status = "invalid"
			}
		}
		r.Fields[name] = f
	}
	f := lINKVectorField(i, int64(binary.LittleEndian.Uint16(r.Raw[6:])))
	if f.Numerator != 0 {
		f.Status = "invalid"
	}
	r.Fields["reserved"] = f
	r.Status = "accepted"
	return r
}
