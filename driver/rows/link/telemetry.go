package link

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
)

type check int

const (
	checkNone check = iota
	checkASCII
	checkConstantOne
)

// rowSpec describes one native row of a module telemetry frame. The field is
// the key DecodeLINKTelemetry emits. A row with a unit is the variant that
// the selection conditions in extra make accepted; the same id then also has a
// raw variant under the opposite condition.
type rowSpec struct {
	frame   uint16
	field   string
	slice   signalSlice
	unit    string
	divisor int64
	add     semreg.Decimal
	extra   map[string]string
	check   check
}

// sharedNames are the field names that more than one LINK frame family emits.
// Their row ids take the frame family as qualifier.
var sharedNames = map[string]bool{
	"b0": true, "b1": true, "b2": true, "b3": true, "b4": true, "b5": true, "b6": true, "b7": true,
	"word_0": true, "word_1": true, "spread": true, "maximum_index": true, "minimum_index": true,
	"tail": true, "status": true, "reserved": true, "current": true, "mask": true, "counter": true, "maximum": true, "minimum": true, "mean": true,
}

func (r rowSpec) rowID() string {
	if sharedNames[r.field] {
		return fmt.Sprintf("%s%04X.%s", rowPrefix, r.frame, r.field)
	}
	return rowPrefix + r.field
}

// conditions is the When of the row: the module telemetry route and the extra
// selection conditions of the row. The bank is asserted by configuration and
// the decoder does not depend on it, so the row applies under both banks.
func (r rowSpec) conditions() map[string]string {
	m := map[string]string{qFlavor: "link", qDirection: "module-to-coordinator", qScope: "local", qDestination: "F4"}
	for k, v := range r.extra {
		m[k] = v
	}
	return m
}

func byteAt(at uint16) signalSlice { return signalSlice{offset: at * 8, width: 8, order: bitSlice} }
func littleWord(at uint16) signalSlice {
	return signalSlice{offset: at * 8, width: 16, order: littleEndian}
}
func bigWord(at uint16) signalSlice { return signalSlice{offset: at * 8, width: 16, order: bigEndian} }
func bigLong(at uint16) signalSlice { return signalSlice{offset: at * 8, width: 32, order: bigEndian} }
func littleLong(at uint16) signalSlice {
	return signalSlice{offset: at * 8, width: 32, order: littleEndian}
}
func bitsAt(at, width uint16) signalSlice {
	return signalSlice{offset: at, width: width, order: bitSlice}
}

func raw(frame uint16, field string, slice signalSlice, extra ...string) rowSpec {
	s := rowSpec{frame: frame, field: field, slice: slice, divisor: 1}
	if len(extra) > 0 {
		s.extra = map[string]string{extra[0]: extra[1]}
	}
	return s
}

// scaled returns the accepted variant of a field, applicable when the
// condition holds, and the raw variant, applicable when it does not.
func scaled(frame uint16, field string, slice signalSlice, unit string, divisor int64, add semreg.Decimal, cond string) []rowSpec {
	accepted := rowSpec{frame: frame, field: field, slice: slice, unit: unit, divisor: divisor, add: add, extra: map[string]string{cond: "true"}}
	unscaled := rowSpec{frame: frame, field: field, slice: slice, divisor: 1, extra: map[string]string{cond: "false"}}
	return []rowSpec{accepted, unscaled}
}

func bytesOf(frame uint16, names ...string) []rowSpec {
	var out []rowSpec
	for at, name := range names {
		out = append(out, raw(frame, name, byteAt(uint16(at))))
	}
	return out
}

// telemetrySpecs returns the rows of the frames 1800 to 1808 in frame order.
func telemetrySpecs() []rowSpec {
	zero := semreg.Decimal{}
	currentBias := semreg.Decimal{Coefficient: "-4", Exponent10: 2}
	celsiusBias := semreg.Decimal{Coefficient: "-4", Exponent10: 1}
	var s []rowSpec
	s = append(s, bytesOf(0x1800, "bitmap_0", "role_flags", "cells", "sensors", "bitmap_4", "mode", "io", "soc_status")...)

	s = append(s, raw(0x1801, "mask", littleWord(0)), raw(0x1801, "algorithm_soc", byteAt(2)), raw(0x1801, "mixed", bigWord(3)),
		raw(0x1801, "cache", byteAt(5)), raw(0x1801, "version", byteAt(6)), raw(0x1801, "reserved", byteAt(7)))

	project, release := raw(0x1802, "project_code", bigLong(0)), raw(0x1802, "release", bigLong(4))
	project.check, release.check = checkASCII, checkASCII
	s = append(s, project, release)

	s = append(s, scaled(0x1803, "capacity", littleWord(4), "Ah", 10, zero, qGroupTwo)...)
	configuration, firstByte, secondByte := raw(0x1803, "configuration", bigWord(0), qConfigWord, "true"), raw(0x1803, "b0", byteAt(0), qConfigWord, "false"), raw(0x1803, "b1", byteAt(1), qConfigWord, "false")
	firstByte.check, secondByte.check = checkConstantOne, checkConstantOne
	s = append(s, configuration, firstByte, secondByte, raw(0x1803, "b2", byteAt(2)), raw(0x1803, "b3", byteAt(3)), raw(0x1803, "b6", byteAt(6)), raw(0x1803, "b7", byteAt(7)))

	s = append(s, raw(0x1804, "word_0", littleLong(0)), raw(0x1804, "word_1", littleLong(4)))

	s = append(s, scaled(0x1805, "pack_voltage", bitsAt(0, 14), "V", 10, zero, qLocalUnits)...)
	s = append(s, scaled(0x1805, "current", bitsAt(14, 13), "A", 10, currentBias, qLocalUnits)...)
	s = append(s, scaled(0x1805, "port_voltage", bitsAt(27, 14), "V", 10, zero, qLocalUnits)...)
	s = append(s, raw(0x1805, "discharge_limit", bitsAt(41, 12)), raw(0x1805, "charge_limit", bitsAt(53, 11)))

	s = append(s, raw(0x1806, "spread_low", byteAt(0)), raw(0x1806, "maximum", littleWord(1)), raw(0x1806, "minimum_index", byteAt(3)),
		raw(0x1806, "minimum", littleWord(4)), raw(0x1806, "tail", littleWord(6)))

	s = append(s, raw(0x1807, "spread", littleWord(0)), raw(0x1807, "count_b2", byteAt(2)), raw(0x1807, "count_b3", byteAt(3)))
	s = append(s, scaled(0x1807, "b4", littleWord(4), "percent", 10, zero, qGroupEight)...)
	s = append(s, raw(0x1807, "whole_estimate", byteAt(6)), raw(0x1807, "auxiliary", byteAt(7)))

	s = append(s, raw(0x1808, "maximum_index", byteAt(0)), raw(0x1808, "maximum_code", byteAt(1)), raw(0x1808, "minimum_index", byteAt(2)),
		raw(0x1808, "minimum_code", byteAt(3)), raw(0x1808, "extra_average", byteAt(4)), raw(0x1808, "spread", byteAt(5)),
		raw(0x1808, "b6", byteAt(6)), raw(0x1808, "b7", byteAt(7)))
	for _, t := range []struct {
		field string
		at    uint16
	}{{"temperature_maximum", 1}, {"temperature_minimum", 3}} {
		s = append(s, rowSpec{frame: 0x1808, field: t.field, slice: byteAt(t.at), unit: "C", divisor: 1, add: celsiusBias, extra: map[string]string{qLocalUnits: "true"}})
	}
	return s
}
