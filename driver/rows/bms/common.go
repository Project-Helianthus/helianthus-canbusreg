package bms

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// CommonProfile is the identifier of the Growatt low-voltage BMS common field
// projection. The projection is selected by the caller and is not a protocol
// revision: selecting it does not select the V1.04 or the V1.08 profile, and
// the rows of either profile do not fill its fields.
const CommonProfile = "growatt.bms.low_voltage.can.common_projection.v1"

// Publisher scopes the common projection carries. A selected local source is
// published as member and a selected aggregate as aggregate; no frame carries
// a scope selector, so any other scope has no rows.
const (
	ScopeMember    = "member"
	ScopeAggregate = "aggregate"

	// scopeQualifier is the qualifier key the scope rows test.
	scopeQualifier = "scope"
)

// commonRows returns the native rows of the shared fields of frames 0x311 to
// 0x313: the limits of 0x311, the raw protection and warning bytes and the pack
// count of 0x312, and the voltage, current, SOC and SOH of 0x313. Byte 7's high
// bit of 0x313 stays raw evidence and gates nothing.
func commonRows(scope string) []roweval.NativeRow[Selector] {
	when := roweval.When{Equal: map[string]string{scopeQualifier: scope}}
	rows := []roweval.NativeRow[Selector]{
		nativeRowIn(CommonProfile, 0x311, "charge_voltage_limit", signalSlice{0, 16, bigEndian, false}, "native.decivolt"),
		nativeRowIn(CommonProfile, 0x311, "charge_current_limit", signalSlice{16, 16, bigEndian, false}, "native.deciampere"),
		nativeRowIn(CommonProfile, 0x311, "discharge_current_limit", signalSlice{32, 16, bigEndian, false}, "native.deciampere"),
		nativeRowIn(CommonProfile, 0x312, "protection_flags", signalSlice{0, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(CommonProfile, 0x312, "warning_flags", signalSlice{16, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(CommonProfile, 0x312, "pack_count", signalSlice{32, 8, bitSlice, false}, "native.count"),
		nativeRowIn(CommonProfile, 0x313, "pack_voltage", signalSlice{0, 16, bigEndian, true}, "native.centivolt"),
		nativeRowIn(CommonProfile, 0x313, "pack_current", signalSlice{16, 16, bigEndian, true}, "native.deciampere"),
		nativeRowIn(CommonProfile, 0x313, "soc", signalSlice{48, 8, bitSlice, false}, "native.percent"),
		nativeRowIn(CommonProfile, 0x313, "soh", signalSlice{56, 7, bitSlice, false}, "native.percent"),
	}
	for i := range rows {
		rows[i].When = when
	}
	return rows
}

// CommonTable returns the rows of the common projection for one selected
// publisher scope. The scope is a selection input: member and aggregate give
// the rows; any other scope, including unknown, gives an empty table, so the
// projection publishes nothing under an unknown scope and no second scope is
// inferred.
//
// The bindings are the shared SOC, SOH, pack voltage and pack current. The
// charge and discharge limits and the pack power product have no binding here:
// the pinned read row carries no value limit and no derived product, so those
// rows stay native and are listed in the package report.
func CommonTable(scope string) roweval.Table[Selector] {
	if scope != ScopeMember && scope != ScopeAggregate {
		return roweval.Table[Selector]{}
	}
	return roweval.Table[Selector]{
		Native: commonRows(scope),
		Bindings: []roweval.BindingRow{
			packReadIn(CommonProfile, "pack_current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10, ""),
			packReadIn(CommonProfile, "pack_voltage", "CAN 0x313", "storage.pack.voltage", "unit.volt", 100, ""),
			packReadIn(CommonProfile, "soc", "CAN 0x313", "storage.state.soc", "unit.ratio", 100, ""),
			packReadIn(CommonProfile, "soh", "CAN 0x313", "storage.state.soh", "unit.ratio", 100, ""),
		},
	}
}

// CommonFrameDisposition is the frame gate of the common projection: the
// standard identifier with DLC 8 and raw DLC 8 of frames 0x311 to 0x313. The
// projection admits no other frame and no count or SOC range.
func CommonFrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	switch frame.ID().Value() {
	case 0x311, 0x312, 0x313:
		return ""
	}
	return roweval.ReasonNoMatch
}

// EvaluateCommonFrame evaluates one frame under the common projection of the
// selected scope. A frame the gate rejects, or a scope with no rows, gives
// roweval.ReasonNoMatch and no evaluation: the raw record stays with the
// caller as source-local evidence.
func EvaluateCommonFrame(scope string, frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	table := CommonTable(scope)
	if len(table.Native) == 0 {
		return nil, roweval.ReasonNoMatch, nil
	}
	if reason := CommonFrameDisposition(frame); reason != "" {
		return nil, reason, nil
	}
	values := make(map[string]string, len(a.Qualifiers.Values)+1)
	for k, v := range a.Qualifiers.Values {
		values[k] = v
	}
	values[scopeQualifier] = scope
	a.Qualifiers.Values = values
	return evaluateAdmitted(table, frame, a)
}
