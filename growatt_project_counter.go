package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 329: optional slot extensions; Measurement conversions; Opaque fields.
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Section: Identity and ordinary module telemetry.
// TailSelected asserts the separate release11 hypothesis feature; payload
// presence and zero padding never select it. Its residue has no physical unit
// and cannot reconstruct the separate signed state uniquely.
type CX08CounterInput struct {
	CX06PCSInput
	TailSelected bool
}

// CX08ExplainCounter is a synthetic calculation, never a receiving transform.
func CX08ExplainCounter(c uint32) uint32 {
	return uint32(uint64(c)*25+uint64(c)/2+uint64(c)/8) >> 8
}

// CX08ExplainTail computes low16(10*state), including negative synthetic states.
func CX08ExplainTail(state int64) uint16 { return uint16(state) * 10 }

// CX08RefineCounter exposes a separate receive-only refinement for composition.
// Both wire forms already contain transformed codes; no energy, capacity or
// time unit is inferred and CX01 decoding is left intact.
func CX08RefineCounter(s CXSelection, i CX08CounterInput) CXNativeResult {
	c := s.CX01Spec()
	status := cx06Admit(s, i.CX06PCSInput)
	link := c.Flavor == "link"
	if link {
		status = "invalid"
		if cx01LINKAdmission(s, i.CX01LINKInput) && i.Evidence.Frame.ID().Value()>>16 == 0x180a {
			// Preserve the selected LINK result and its native evidence.
			status = "decoded"
			if !i.PublisherResolved {
				status = "conflict"
			}
			if len(i.NativeRecord) != 0 {
				decoded, err := canbus.DecodeSocketCANRecord(i.NativeRecord)
				if err != nil || decoded != i.Evidence.Frame {
					status = "invalid"
				}
			}
			if i.Evidence.RawRecord != [canbus.SocketCANRecordSize]byte{} {
				decoded, err := canbus.DecodeSocketCANRecord(i.Evidence.RawRecord[:])
				if err != nil || decoded != i.Evidence.Frame {
					status = "invalid"
				}
			}
		}
	}
	r := cx01Result(s, i.CX01LINKInput, status)
	if status != "decoded" {
		return r
	}
	p := r.Raw
	if !link && i.Evidence.Frame.ID().Value() != 0x329 {
		r.Status = "unsupported"
		return r
	}
	code := int64(p[1])<<16 | int64(p[2])<<8 | int64(p[3])
	if link {
		code = int64(p[1]) | int64(p[2])<<8 | int64(p[3])<<16
	}
	selected := cx08Release11(s) && c.Scope == "local"
	valid := selected && i.MeasurementValid
	if !link {
		valid = valid && p[4] == 0 && p[5] == 0
	}
	cx06Add(&r, i.CX06PCSInput, "counter", code, 1, "", "opaque transmitted counter code", "Proven", valid)
	if !link {
		cx06Add(&r, i.CX06PCSInput, "module", int64(p[0]), 1, "", "native module", "Proven", selected && p[4] == 0 && p[5] == 0)
		tail := int64(binary.BigEndian.Uint16(p[6:8]))
		cx06Add(&r, i.CX06PCSInput, "tail", tail, 1, "", "low16 of ten times a separate signed state; no unique inverse", "Hypothesis", valid && i.TailSelected)
	}
	return r
}
