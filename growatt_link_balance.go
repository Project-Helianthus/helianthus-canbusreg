package canbusreg

import (
	"encoding/binary"
	"fmt"
	"time"
)

// The age supplied here is the hardware read age, never CAN arrival age.
func DecodeLINKBalance(s CXSelection, i LINKInput, count int, countValid bool, g MeasurementGate, hardwareAgeKnown bool, hardwareAge time.Duration) CXNativeResult {
	r := selectionResult(s, i, "invalid")
	if !lINKAdmission(s, i) || i.Evidence.Sequence == 0 || i.Evidence.Monotonic < 0 {
		return r
	}
	if i.Evidence.Frame.ID().Value()>>16 != 0x1801 {
		r.Status = "unsupported"
		return r
	}
	mask := int64(binary.LittleEndian.Uint16(r.Raw[:2]))
	r.Fields["mask"] = lINKVectorField(i, mask)
	selector := false
	switch s.spec.ProjectCode + s.spec.Release {
	case "QAAA0005", "QAAA0006", "QAAB0002", "QAAB0003", "QAAB0004", "QAAB0006":
		selector = true
	}
	if selector && s.SelectionGroup() != "" && countValid && count >= 1 && count <= 16 && lINKVectorGate(g) == "accepted" && hardwareAgeKnown && hardwareAge >= 0 {
		for j := 0; j < count; j++ {
			f := lINKVectorField(i, (mask>>j)&1)
			f.Status = "accepted"
			f.Grade = "Hypothesis"
			f.Meaning = "retained register enable state"
			f.Age = hardwareAge
			r.Fields[fmt.Sprintf("enable_%d", j+1)] = f
		}
	}
	r.Status = "accepted"
	return r
}
