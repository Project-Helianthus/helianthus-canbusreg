package canbusreg

import (
	"encoding/binary"
	"fmt"
	"time"
)

// LINKInput associates native evidence with explicit topology, epoch and field age.
// LINK native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Sections: Identify the peer before interpreting version-dependent fields;
// Identity and ordinary module telemetry; Serial number and date announcements.
type LINKInput struct {
	Evidence                                      Evidence
	Epoch                                         uint64
	Direction, Scope, Bank, Endpoint, Group, Slot string
	FieldAge                                      time.Duration
}

func selectionResult(s CXSelection, i LINKInput, status string) CXNativeResult {
	return CXNativeResult{Selection: s, Evidence: i.Evidence, Raw: i.Evidence.Frame.Bytes(), Epoch: i.Epoch, Status: status, Fields: make(map[string]CXNativeField)}
}
func lINKFrameAdmission(s CXSelection, i LINKInput) bool {
	f := i.Evidence.Frame
	c := s.spec
	id := f.ID().Value()
	return c.Flavor == "link" && c.RouteSet && f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 && id>>24 == 0x18 && uint8(id>>8) == c.Destination && uint8(id) == c.Source && i.Evidence.Interface == c.Interface && i.Epoch == c.Epoch && i.Direction == c.Direction && i.Scope == c.Scope && i.Bank == c.Bank && i.Endpoint == c.Endpoint && i.Group == c.Group && i.Slot == c.Slot && i.FieldAge >= 0
}
func lINKAdmission(s CXSelection, i LINKInput) bool {
	c := s.spec
	return lINKFrameAdmission(s, i) && c.Direction == "module-to-coordinator" && c.Destination == 0xf4 && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local"
}

// cx01LINKReportAdmission admits module reports addressed to the F5 route.
func lINKReportAdmission(s CXSelection, i LINKInput) bool {
	c := s.spec
	return lINKFrameAdmission(s, i) && c.Direction == "module-to-coordinator" && c.Destination == 0xf5 && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local"
}
func lINKDateAdmission(s CXSelection, i LINKInput) bool {
	c := s.spec
	return lINKFrameAdmission(s, i) && c.Direction == "module-to-coordinator" && c.Destination == 0xf5 && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local" && i.Evidence.Frame.ID().Value()>>16 == 0x1809
}
func lINKCoordinatorAdmission(s CXSelection, i LINKInput) bool {
	c := s.spec
	return lINKFrameAdmission(s, i) && c.Direction == "coordinator-to-module" && c.Scope == "aggregate" && c.Source == 0xf4 && (c.Destination == 0xf5 || c.Destination == 0xdf)
}

// CXDecodeLINKTelemetry decodes admitted LINK telemetry while retaining raw evidence and candidate grades.
func CXDecodeLINKTelemetry(s CXSelection, i LINKInput) CXNativeResult {
	r := selectionResult(s, i, "unsupported")
	if !lINKAdmission(s, i) && !lINKDateAdmission(s, i) && !lINKCoordinatorAdmission(s, i) {
		r.Status = "invalid"
		return r
	}
	p := r.Raw
	add := func(name string, n int64, d int64, unit, meaning, grade, status string) {
		r.Fields[name] = CXNativeField{Numerator: n, Denominator: d, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
	}
	raw := func(name string, n uint64) { add(name, int64(n), 1, "", "", "Unknown", "raw") }
	le := func(at int) uint64 { return uint64(binary.LittleEndian.Uint16(p[at : at+2])) }
	byteField := func(name string, at int) { raw(name, uint64(p[at])) }
	accepted := func(name string, n int64, d int64, unit, meaning string) {
		add(name, n, d, unit, meaning, s.grade, "accepted")
	}
	kind := uint16(i.Evidence.Frame.ID().Value() >> 16)
	if lINKCoordinatorAdmission(s, i) {
		if s.spec.Destination != 0xf5 {
			return r
		}
		switch kind {
		case 0x180d:
			for at, name := range []string{"soc_cache", "state_1", "state_2", "state_3", "maximum_index", "minimum_index", "temperature_6", "temperature_7"} {
				byteField(name, at)
			}
		case 0x180e:
			byteField("index_0", 0)
			byteField("index_1", 1)
			raw("extremum_2", le(2))
			raw("extremum_4", le(4))
			raw("additional_limit", le(6))
		case 0x180f:
			raw("limit", le(0))
			raw("current", le(2))
			byteField("count", 4)
			byteField("soh_cache", 5)
			raw("quotient", le(6))
		default:
			return r
		}
		r.Status = "raw"
		return r
	}
	localUnits := s.group == "L2" || s.group == "L8"
	switch kind {
	case 0x1800:
		for at, name := range []string{"bitmap_0", "role_flags", "cells", "sensors", "bitmap_4", "mode", "io", "soc_status"} {
			byteField(name, at)
		}
	case 0x1801:
		raw("mask", le(0))
		byteField("algorithm_soc", 2)
		raw("mixed", uint64(binary.BigEndian.Uint16(p[3:5])))
		byteField("cache", 5)
		byteField("version", 6)
		byteField("reserved", 7)
	case 0x1802:
		if !selectionASCII(p[:]) {
			r.Status = "invalid"
			return r
		}
		raw("project_code", uint64(binary.BigEndian.Uint32(p[:4])))
		raw("release", uint64(binary.BigEndian.Uint32(p[4:])))
		if string(p[:4]) != s.spec.ProjectCode || string(p[4:]) != s.spec.Release {
			r.Status = "conflict"
			return r
		}
	case 0x1803:
		raw("capacity", le(4))
		if s.group == "L2" {
			accepted("capacity", int64(le(4)), 10, "Ah", "capacity")
		}
		if s.group == "L8" {
			raw("configuration", uint64(binary.BigEndian.Uint16(p[:2])))
		} else {
			byteField("b0", 0)
			byteField("b1", 1)
			if s.group == "L2" {
				for at, name := range []string{"b0", "b1"} {
					if p[at] != 1 {
						f := r.Fields[name]
						f.Status = "invalid"
						r.Fields[name] = f
					}
				}
			}
		}
		for _, at := range []int{2, 3, 6, 7} {
			byteField(fmt.Sprintf("b%d", at), at)
		}
	case 0x1804:
		raw("word_0", uint64(binary.LittleEndian.Uint32(p[:4])))
		raw("word_1", uint64(binary.LittleEndian.Uint32(p[4:])))
	case 0x1805:
		bits := binary.LittleEndian.Uint64(p[:])
		names := []string{"pack_voltage", "current", "port_voltage", "discharge_limit", "charge_limit"}
		for j, width := range []uint{14, 13, 14, 12, 11} {
			shift := []uint{0, 14, 27, 41, 53}[j]
			n := int64((bits >> shift) & ((1 << width) - 1))
			raw(names[j], uint64(n))
			if localUnits && j < 3 {
				unit := "V"
				meaning := names[j]
				if j == 1 {
					n -= 4000
					unit = "A"
					meaning = "positive charge"
				}
				accepted(names[j], n, 10, unit, meaning)
			}
		}
	case 0x1806:
		byteField("spread_low", 0)
		raw("maximum", le(1))
		byteField("minimum_index", 3)
		raw("minimum", le(4))
		raw("tail", le(6))
	case 0x1807:
		raw("spread", le(0))
		byteField("count_b2", 2)
		byteField("count_b3", 3)
		raw("b4", le(4))
		byteField("whole_estimate", 6)
		byteField("auxiliary", 7)
		if s.group == "L8" {
			accepted("b4", int64(le(4)), 10, "percent", "fine SOC")
		}
	case 0x1808:
		byteField("maximum_index", 0)
		byteField("maximum_code", 1)
		byteField("minimum_index", 2)
		byteField("minimum_code", 3)
		byteField("extra_average", 4)
		byteField("spread", 5)
		byteField("b6", 6)
		byteField("b7", 7)
		if localUnits {
			accepted("temperature_maximum", int64(p[1])-40, 1, "C", "integer code")
			accepted("temperature_minimum", int64(p[3])-40, 1, "C", "integer code")
		}
	case 0x1809:
		if !lINKDateAdmission(s, i) {
			return r
		}
		raw("year", le(0))
		for at, name := range []string{"month", "day", "hour", "minute", "second", "status"} {
			byteField(name, at+2)
		}
		year, month, day := int(le(0)), time.Month(p[2]), int(p[3])
		date := time.Date(year, month, day, int(p[4]), int(p[5]), int(p[6]), 0, time.UTC)
		valid := year >= 1 && month >= 1 && month <= 12 && date.Year() == year && date.Month() == month && date.Day() == day && p[4] < 24 && p[5] < 60 && p[6] < 60
		for _, name := range []string{"year", "month", "day", "hour", "minute", "second"} {
			f := r.Fields[name]
			f.Grade = "Hypothesis"
			if valid && s.group != "" {
				f.Status = "accepted"
				f.Meaning = "calendar component; timezone unspecified"
			} else {
				f.Status = "invalid"
			}
			r.Fields[name] = f
		}
	case 0x1853, 0x1861, 0x1869:
		// The confirmer admits 0x1869 only on the F5 report route; this path
		// admits F4 frames only, so a 0x1869 frame here is not release evidence.
		if s.group == "L4" && uint16(i.Evidence.Frame.ID().Value()>>16) != 0x1869 {
			r.Status = "conflict"
			return r
		}
		return r
	case 0x180a:
		byteField("status", 0)
		raw("counter", uint64(p[1])|uint64(p[2])<<8|uint64(p[3])<<16)
		raw("helper", le(4))
		raw("tail", le(6))
	case 0x180b:
		if s.group == "" {
			return r
		}
		switch s.layout {
		case "C":
			for at, name := range []string{"checkpoint_0", "checkpoint_1", "checkpoint_2"} {
				raw(name, le(at*2))
			}
		case "W":
			raw("full_capacity", le(0))
			raw("fine_cycles", le(2))
			byteField("algorithm_state", 4)
			byteField("residual_budget", 5)
			if s.group == "L2" {
				accepted("full_capacity", int64(le(0)), 10, "Ah", "full capacity")
				accepted("fine_cycles", int64(le(2)), 10, "cycles", "fine cycles")
			}
		case "B":
			byteField("capacity_derived", 0)
			byteField("algorithm", 1)
			raw("cycles", le(2))
			byteField("b4", 4)
			byteField("b5", 5)
		}
		raw("remaining_capacity", le(6))
		if s.group == "L2" {
			accepted("remaining_capacity", int64(le(6)), 10, "Ah", "remaining capacity")
		}
	case 0x180c:
		for j, name := range []string{"word_0", "word_1", "word_2"} {
			raw(name, le(j*2))
		}
		byteField("state", 6)
		byteField("reserved", 7)
	case 0x1840:
		for j, name := range []string{"b0", "b1", "b2", "b3", "b4", "b5"} {
			byteField(name, j)
		}
		raw("tail", le(6))
	default:
		return r
	}
	r.Status = "accepted"
	return r
}
