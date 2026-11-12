package canbusreg

import (
	"encoding/binary"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// PCSIdentityPCSInput supplies independent gauge and identity interpretation gates.
// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 316 layout selection from identity and other PIDs;
// 315…318: cells, gauge fields and retained faults; Opaque fields.
// All meanings are protocol candidates, never physical qualification.
// Context flags are explicit caller assertions, never inferred from gauge bytes.
// SerialSlotValid associates the explicit native slot with Selection.Slot.
// ThresholdStateKnown with AlternativeEligible=false selects the supplied normal
// threshold path; an unknown state or eligible alternative remains raw.
// FieldStates and FieldAges retain the per-field withholding semantics of CX05.
type PCSIdentityPCSInput struct {
	PCSStandardPCSInput
	ZeroIntegration, FailedPrecheck          bool
	ThresholdStateKnown, AlternativeEligible bool
	SerialSlotValid                          bool
	SerialSlot                               uint8
}

func pCSIdentityAdmit(s CXSelection, i PCSIdentityPCSInput) string {
	c := s.SelectionSpecValue()
	f := i.Evidence.Frame
	if c.Interface.Name() == "" || c.Interface.Index() <= 0 || c.Epoch == 0 || c.Bank == "" || c.Endpoint == "" || (c.Scope != "local" && c.Scope != "aggregate") || (c.Flavor != "pcs-standard" && c.Flavor != "pcs-parallel") || c.Direction != "bms-to-pcs" || i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot || i.FieldAge < 0 || f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 {
		return "invalid"
	}
	if len(i.NativeRecord) > 0 {
		decoded, err := canbus.DecodeSocketCANRecord(i.NativeRecord)
		if err != nil || decoded != f {
			return "invalid"
		}
	}
	if i.Evidence.RawRecord != [canbus.SocketCANRecordSize]byte{} {
		decoded, err := canbus.DecodeSocketCANRecord(i.Evidence.RawRecord[:])
		if err != nil || decoded != f {
			return "invalid"
		}
	}
	if !i.PublisherResolved {
		return "conflict"
	}
	return "decoded"
}
func pCSIdentityAdd(r *CXNativeResult, i PCSIdentityPCSInput, name string, n, d int64, unit, meaning, grade string, gate bool) {
	status := "raw"
	if gate {
		status = "accepted"
	} else {
		unit, meaning, grade = "", "", "Unknown"
	}
	age := i.FieldAge
	if a, ok := i.FieldAges[name]; ok {
		age = a
	}
	if state, ok := i.FieldStates[name]; ok {
		switch state {
		case "absent", "invalid", "unsupported", "stale", "conflict", "unassigned":
			status = state
		default:
			status = "invalid"
		}
	}
	if age < 0 {
		status = "invalid"
	}
	if status != "accepted" {
		unit, meaning = "", ""
	}
	r.Fields[name] = CXNativeField{Numerator: n, Denominator: d, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: r.Raw, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: age}
}

// DecodePCSGauge consumes supplied frames only; it performs no I/O.
func DecodePCSGauge(s CXSelection, i PCSIdentityPCSInput) CXNativeResult {
	r := selectionResult(s, i.LINKInput, pCSIdentityAdmit(s, i))
	if r.Status != "decoded" {
		return r
	}
	p := r.Raw
	c := s.SelectionSpecValue()
	g := s.SelectionGroup()
	id := c.ProjectCode + c.Release
	local := c.Scope == "local"
	selected := g != "" && c.Flavor == "pcs-standard"
	early := g == "L0" || g == "L1"
	valid := selected && local && i.MeasurementValid
	be := func(at int) int64 { return int64(binary.BigEndian.Uint16(p[at : at+2])) }
	add := func(name string, n, d int64, unit, meaning, grade string, gate bool) {
		pCSIdentityAdd(&r, i, name, n, d, unit, meaning, grade, gate)
	}
	raw := func(name string, n int64) { add(name, n, 1, "", "", "Unknown", false) }
	words := func(gate bool) {
		for at := 0; at < 8; at += 2 {
			add(fmt.Sprintf("word_%d", at), be(at), 1, "", "profile word", "Hypothesis", gate)
		}
	}
	pid := i.Evidence.Frame.ID().Value()
	gateway := id == "QCAA0001" && c.Scope == "aggregate"
	placeholder := gateway && (pid == 0x315 || pid == 0x316 || pid == 0x317 || pid == 0x318 || pid == 0x321 || pid == 0x324 || pid == 0x325) || c.Flavor == "pcs-parallel" && (c.ProjectCode == "QAAB" || c.ProjectCode == "QAAb") && g != "" && pid == 0x315
	if placeholder {
		for at, b := range p {
			raw(fmt.Sprintf("byte_%d", at), int64(b))
		}
		add("placeholder", 0, 1, "", "zero placeholder", "Hypothesis", p == [8]byte{})
		return r
	}
	switch pid {
	case 0x315:
		if early {
			words(valid)
			break
		}
		add("cell_word", be(0), 1, "", "cell-related word", "Proven", valid)
		add("checkpoint", be(2), 1, "", "checkpoint", "Proven", valid)
		add("algorithm", be(4), 1, "", "algorithm summary", "Proven", valid)
		den := int64(1)
		unit := ""
		gate := valid && id == "QAAA0006"
		if gate {
			den = 10
			unit = "Ah"
		}
		add("capacity", be(6), den, unit, "capacity", "Proven", gate)
	case 0x316:
		switch {
		case early:
			for at := 0; at < 8; at += 2 {
				pos := 5 + at/2
				add(fmt.Sprintf("cell_%d", pos), be(at), 1, "mV", "partial cell position", "Hypothesis", valid && i.CountValid && i.Count >= uint16(pos))
			}
		case g == "L5" || g == "L7":
			raw("word_0", be(0))
			raw("word_2", be(2))
			add("capacity_byte", int64(p[4]), 1, "", "capacity code", "Hypothesis", valid)
			add("gauge_byte", int64(p[5]), 1, "", "gauge code", "Hypothesis", valid)
			raw("word_6", be(6))
		case g == "L2" || g == "L6":
			add("accumulator", be(0), 1, "", "biased accumulator; never Ah or SOC", "Hypothesis", valid && g == "L2")
			add("initial_accumulator", be(0), 1, "", "zero-integration initial accumulator", "Hypothesis", valid && g == "L2" && i.ZeroIntegration && be(0) == 5000)
			for at, name := range []string{"capacity", "cycles"} {
				d := int64(1)
				unit := ""
				gate := valid && g == "L2"
				if gate {
					d = 10
					unit = []string{"Ah", "cycles"}[at]
				}
				add(name, be(2+at*2), d, unit, name, "Proven", gate)
			}
			add("state", int64(p[6]), 1, "", "latched state", "Hypothesis", valid && g == "L2")
			add("failed_precheck_state", int64(p[6]), 1, "", "failed precheck state", "Hypothesis", valid && g == "L2" && i.FailedPrecheck && p[6] == 100)
			add("budget", int64(p[7]), 1, "", "residual budget; never balance mask", "Hypothesis", valid && g == "L2")
		case g == "L3" || g == "L4" || g == "L8":
			add("retained_fault", int64(p[0]), 1, "", "retained fault byte", "Proven", valid)
			add("algorithm", int64(p[1]), 1, "", "algorithm byte", "Proven", valid)
			for at := 2; at < 8; at += 2 {
				name := fmt.Sprintf("word_%d", at)
				d := int64(1)
				unit := ""
				gate := valid && g == "L8"
				if g == "L8" && at >= 4 {
					if at == 4 {
						name = "capacity"
					} else {
						name = "cycles"
					}
					if gate {
						d = 10
						unit = []string{"Ah", "cycles"}[(at-4)/2]
					}
				}
				meaning := "opaque biased code"
				if at == 4 {
					meaning = "capacity"
				} else if at == 6 {
					meaning = "cycle count"
				}
				add(name, be(at), d, unit, meaning, "Proven", gate)
			}
		default:
			words(false)
		}
	case 0x317:
		if g == "L0" {
			words(valid)
			break
		}
		full := g == "L1"
		limit := 6
		if full {
			limit = 8
		}
		for at := 0; at < limit; at++ {
			grade := "Proven"
			if full {
				grade = "Hypothesis"
			}
			add(fmt.Sprintf("retained_%d", 8+at), int64(p[at]), 1, "", "retained fault byte", grade, valid)
		}
		if !full {
			sohContract := id == "QAAA0006" || g == "L8"
			add("soh_truncated", int64(p[6]), 1, "percent", "truncated estimate; independent rounding", "Proven", valid && sohContract && p[7] == 0 && p[6] <= 100)
			add("slot", int64(p[7]), 1, "", "local slot zero or aggregate rotation", "Proven", selected && (!local || p[7] == 0))
		}
	case 0x318:
		if g == "L0" {
			words(valid)
			break
		}
		for at, b := range p {
			add(fmt.Sprintf("retained_%d", at), int64(b), 1, "", "retained fault byte; never current faults", "Proven", valid)
		}
	default:
		r.Status = "unsupported"
	}
	return r
}
