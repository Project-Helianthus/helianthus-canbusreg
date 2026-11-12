package canbusreg

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Sections: Cell and sensor counts; Cell voltages, temperatures and balancing;
// Fine temperatures and algorithm fields. All meanings are candidate protocol
// results; no value establishes physical qualification.
type MeasurementGate struct {
	Configured, Valid bool
	Status            string
	Age               time.Duration
}

type VectorSnapshot struct {
	Selection                         CXSelection
	Kind                              string
	ConfiguredCount, AddressableSlots int
	CountValid, CompleteCoverage      bool
	Fixed16Path                       bool
	ReceivedIndices, ValidIndices     []int
	Fields, LastGood                  map[int]CXNativeField
	Span, ReceivedSpan                time.Duration
	AsOf, MaxAge                      time.Duration
	PhysicalQualified                 bool
}

// CX12LINKVector retains bounded independent channel history. A caller must
// supply measurement age and validity, rather than infer them from reception.
type LINKVector struct {
	mu              sync.Mutex
	selection       CXSelection
	count, capacity int
	countValid      bool
	fixed16         bool
	kind            string
	fields, good    map[int]CXNativeField
	sequence        uint64
	monotonic       time.Duration
}

func NewLINKCells(s CXSelection, count int, countValid bool) *LINKVector {
	return lINKVectorNewVector(s, count, countValid, 16, "cells")
}

// CX12NewLINKFixed16Cells selects an independently asserted fixed-16 path.
// The reported count stays separate and does not qualify that assertion.
func NewLINKFixed16Cells(s CXSelection, count int, countValid bool) *LINKVector {
	a := lINKVectorNewVector(s, count, countValid, 16, "cells")
	a.fixed16 = true
	return a
}
func lINKVectorNewVector(s CXSelection, count int, valid bool, capacity int, kind string) *LINKVector {
	return &LINKVector{selection: s, count: count, countValid: valid, capacity: capacity, kind: kind, fields: make(map[int]CXNativeField), good: make(map[int]CXNativeField)}
}
func lINKVectorField(i LINKInput, n int64) CXNativeField {
	return CXNativeField{Numerator: n, Denominator: 1, Raw: i.Evidence.Frame.Bytes(), Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge, Grade: "Unknown", Status: "raw"}
}
func lINKVectorGate(g MeasurementGate) string {
	if !g.Configured {
		return "unconfigured"
	}
	if g.Age < 0 {
		return "invalid"
	}
	if g.Status != "" && g.Status != "accepted" {
		switch g.Status {
		case "absent", "unsupported", "stale", "conflict", "invalid":
			return g.Status
		}
		return "invalid"
	}
	if !g.Valid {
		switch g.Status {
		case "absent", "unsupported", "stale", "conflict":
			return g.Status
		}
		return "invalid"
	}
	return "accepted"
}

// cx12GateRejection keeps the independent gate verdict and age on a field the
// gate refused, instead of reverting to the frame's own raw status and age.
func lINKVectorGateRejection(f CXNativeField, g MeasurementGate) CXNativeField {
	st := lINKVectorGate(g)
	if st == "accepted" || st == "unconfigured" {
		return f
	}
	f.Status = st
	if g.Age >= 0 {
		f.Age = g.Age
	}
	return f
}

func lINKVectorAdmit(s CXSelection, i LINKInput) bool {
	return lINKAdmission(s, i) && s.SelectionGroup() != "" && i.Evidence.Sequence > 0 && i.Evidence.Monotonic >= 0
}
func (a *LINKVector) DecodeLINKVector(i LINKInput, gates [4]MeasurementGate) CXNativeResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := selectionResult(a.selection, i, "invalid")
	if !lINKVectorAdmit(a.selection, i) {
		return r
	}
	kind := uint16(i.Evidence.Frame.ID().Value() >> 16)
	start := 0
	prefix := "cell"
	if a.kind == "cells" {
		if kind < 0x1810 || kind > 0x1813 {
			r.Status = "unsupported"
			return r
		}
		start = int(kind-0x1810) * 4
	} else {
		if kind != 0x1830 {
			r.Status = "unsupported"
			return r
		}
		prefix = "sensor"
	}
	if i.Evidence.Sequence <= a.sequence || i.Evidence.Monotonic < a.monotonic {
		r.Status = "stale"
		return r
	}
	a.sequence = i.Evidence.Sequence
	a.monotonic = i.Evidence.Monotonic
	for j, g := range gates {
		index := start + j + 1
		n := int64(binary.LittleEndian.Uint16(r.Raw[j*2:]))
		f := lINKVectorField(i, n)
		f.Status = lINKVectorGate(g)
		f.Age = g.Age
		if !a.countValid && !a.fixed16 {
			f.Status = "absent"
		} else if index > a.count && !a.fixed16 {
			f.Status = "unconfigured"
		}
		if a.kind == "temperatures" && n == 65535 {
			f.Status = "invalid"
		}
		if f.Status == "accepted" {
			f.Grade = "Proven"
			if a.kind == "cells" {
				f.Unit = "mV"
				f.Meaning = "native cell voltage"
			} else {
				f.Numerator = n - 320
				f.Denominator = 8
				f.Unit = "C"
				f.Meaning = "fine sensor code; location unspecified"
			}
			a.good[index] = f
		}
		a.fields[index] = f
		r.Fields[fmt.Sprintf("%s_%d", prefix, index)] = f
	}
	r.Status = "accepted"
	return r
}
func lINKVectorAge(f CXNativeField, now time.Duration) (CXNativeField, bool) {
	if now < f.Evidence.Monotonic || f.Age < 0 {
		return f, false
	}
	elapsed := now - f.Evidence.Monotonic
	if elapsed > time.Duration(1<<63-1)-f.Age {
		return f, false
	}
	f.Age += elapsed
	return f, true
}
func (a *LINKVector) SnapshotLINKVector(now, maxAge time.Duration) VectorSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := VectorSnapshot{Selection: a.selection, Kind: a.kind, ConfiguredCount: a.count, AddressableSlots: a.capacity, CountValid: a.countValid, Fields: make(map[int]CXNativeField), LastGood: make(map[int]CXNativeField), AsOf: now, MaxAge: maxAge}
	v.Fixed16Path = a.fixed16
	var first, last time.Duration
	have := false
	var youngest, oldest time.Duration
	haveValid := false
	for index := 1; index <= a.capacity; index++ {
		if f, ok := a.fields[index]; ok {
			v.ReceivedIndices = append(v.ReceivedIndices, index)
			aged, valid := lINKVectorAge(f, now)
			f = aged
			if f.Status == "accepted" && (!valid || maxAge < 0 || f.Age > maxAge) {
				f.Status = "stale"
				f.Unit = ""
				f.Meaning = ""
			}
			v.Fields[index] = f
			if !have || f.Evidence.Monotonic < first {
				first = f.Evidence.Monotonic
			}
			if !have || f.Evidence.Monotonic > last {
				last = f.Evidence.Monotonic
			}
			have = true
			if f.Status == "accepted" {
				v.ValidIndices = append(v.ValidIndices, index)
				if !haveValid || f.Age < youngest {
					youngest = f.Age
				}
				if !haveValid || f.Age > oldest {
					oldest = f.Age
				}
				haveValid = true
			}
		}
		if f, ok := a.good[index]; ok {
			f, _ = lINKVectorAge(f, now)
			v.LastGood[index] = f
		}
	}
	v.ReceivedSpan = last - first
	v.Span = oldest - youngest
	v.CompleteCoverage = a.countValid && a.count > 0 && a.count <= a.capacity
	for j := 1; j <= a.count && j <= a.capacity; j++ {
		if v.Fields[j].Status != "accepted" {
			v.CompleteCoverage = false
		}
	}
	return v
}

// cx12Contributors rechecks detached snapshots at the summary's clock; callers
// cannot use coverage alone to renew expired or differently associated values.
func lINKVectorContributors(s CXSelection, i LINKInput, v VectorSnapshot, kind string, count int) (int64, bool) {
	countGate := v.CountValid && v.ConfiguredCount == count
	if v.Fixed16Path {
		countGate = kind == "cells" && count == 16 && v.ConfiguredCount >= 0 && v.ConfiguredCount <= 16
	}
	if v.Selection != s || v.Kind != kind || !countGate || count < 1 || count > v.AddressableSlots || v.MaxAge < 0 || i.Evidence.Monotonic < v.AsOf {
		return 0, false
	}
	var sum int64
	for j := 1; j <= count; j++ {
		f, ok := v.Fields[j]
		if !ok || f.Status != "accepted" || f.Epoch != i.Epoch || f.Evidence.Interface != i.Evidence.Interface || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.Evidence.Sequence >= i.Evidence.Sequence || f.Evidence.Monotonic > i.Evidence.Monotonic {
			return 0, false
		}
		input := LINKInput{Evidence: f.Evidence, Epoch: f.Epoch, Direction: s.spec.Direction, Scope: f.Scope, Bank: f.Bank, Endpoint: f.Endpoint, Group: f.Group, Slot: f.Slot}
		family := f.Evidence.Frame.ID().Value() >> 16
		expected := uint32(0x1830)
		if kind == "cells" {
			expected = 0x1810 + uint32((j-1)/4)
		}
		if !lINKVectorAdmit(s, input) || family != expected || f.Raw != f.Evidence.Frame.Bytes() {
			return 0, false
		}
		elapsed := i.Evidence.Monotonic - v.AsOf
		if f.Age < 0 || f.Age > v.MaxAge || elapsed > v.MaxAge-f.Age {
			return 0, false
		}
		// Each numerator must derive from the raw word its own index selects.
		word := int64(binary.LittleEndian.Uint16(f.Raw[((j-1)%4)*2:]))
		if kind == "temperatures" {
			if f.Denominator != 8 || word == 65535 || f.Numerator != word-320 {
				return 0, false
			}
			sum += f.Numerator + 320
		} else {
			if f.Denominator != 1 || f.Numerator != word {
				return 0, false
			}
			sum += f.Numerator
		}
	}
	return sum / int64(count), true
}

func DecodeLINKCellMean(s CXSelection, i LINKInput, g MeasurementGate, v VectorSnapshot, fixed16Valid bool) CXNativeResult {
	r := selectionResult(s, i, "invalid")
	if !lINKVectorAdmit(s, i) {
		return r
	}
	if i.Evidence.Frame.ID().Value()>>16 != 0x1806 {
		r.Status = "unsupported"
		return r
	}
	n := int64(binary.LittleEndian.Uint16(r.Raw[6:]))
	f := lINKVectorField(i, n)
	count := v.ConfiguredCount
	allowed := !v.Fixed16Path && count >= 1 && count <= 16
	// The independent fixed-16 path still needs all sixteen valid slots.
	if fixed16Valid && v.Fixed16Path {
		allowed = true
		count = 16
	}
	mean, valid := lINKVectorContributors(s, i, v, "cells", count)
	if s.SelectionGroup() == "L8" && allowed && valid && mean == n && lINKVectorGate(g) == "accepted" {
		f.Status = "accepted"
		f.Grade = "Hypothesis"
		f.Unit = "mV"
		f.Meaning = "native cell mean"
		f.Age = g.Age
	} else {
		f = lINKVectorGateRejection(f, g)
	}
	r.Fields["mean"] = f
	r.Status = "accepted"
	return r
}
