package bms

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func TestValuesWithoutADefinedBindingKeepNativeRowsAndGiveNoCandidate(t *testing.T) {
	for _, profile := range []string{Profile, V108Profile, CommonProfile, MonitorProfile} {
		t.Run(profile, func(t *testing.T) {
			for _, v := range nativeOnlyVectors(profile) {
				t.Run(v.name, func(t *testing.T) {
					frame, a, evals := evaluateNativeOnlyVector(t, profile, v)
					checkNativeOnlyVector(t, profile, v, frame, a, evals)
				})
			}
		})
	}
}

type nativeOnlyVector struct {
	name, data string
	id         uint32
	values     map[string]string
	// group selects named fields and their values from the frozen decoder, not the row table.
	group bool
	// bound lists the only rows allowed to give candidates on frames with bound siblings.
	bound []string
	// operands are the existing bound rows that would feed a power product.
	operands map[string]string
}

func nativeOnlyVectors(profile string) []nativeOnlyVector {
	if profile == MonitorProfile {
		return monitorNativeOnlyVectors()
	}
	limits := map[string]string{"charge_voltage_limit": "570", "charge_current_limit": "123", "discharge_current_limit": "345"}
	bound := []string{"pack_voltage", "pack_current", "soc", "soh"}
	switch profile {
	case Profile:
		bound = append(bound, "maximum_cell_temperature")
	case V108Profile:
		bound = append(bound, "average_temperature")
	}
	vectors := []nativeOnlyVector{
		// Power has no native field or signed two-operand binding. Both operands
		// keep their native rows and bind individually, not as power.
		{name: "PowerOperandsStaySeparate", id: 0x313, data: "1401FF9C00F064D0", bound: bound,
			operands: map[string]string{"pack_voltage": "5121", "pack_current": "-100"}},
		{name: "PowerOperandsKeepChangedValues", id: 0x313, data: "1234007B00F064D0", bound: bound,
			operands: map[string]string{"pack_voltage": "4660", "pack_current": "123"}},
		// No signed read row carries device-reported limits.
		{name: "DeviceLimitsStayNative", id: 0x311, data: "023A007B01590002", values: limits},
	}
	if profile != CommonProfile {
		// Neither revision defines a mapping from wire codes to SemReg operating symbols.
		limits["operating_state"] = "2"
		voltageExtrema := []string{"maximum_cell_voltage", "minimum_cell_voltage"}
		vectors = append(vectors,
			nativeOnlyVector{name: "SoftStartingCodeStaysNative", id: 0x311, data: "023A007B01590000", values: map[string]string{"operating_state": "0"}},
			nativeOnlyVector{name: "StandbyCodeStaysNative", id: 0x311, data: "023A007B01590001", values: map[string]string{"operating_state": "1"}},
			nativeOnlyVector{name: "DischargingCodeStaysNative", id: 0x311, data: "023A007B01590003", values: map[string]string{"operating_state": "3"}},
			// The interlock bits have no assigned SemReg condition class.
			nativeOnlyVector{name: "SetInterlockBitsStayNative", id: 0x319, data: "E10D160CDA030701", values: interlockValues("1"), bound: voltageExtrema},
			nativeOnlyVector{name: "ClearInterlockBitsStayNative", id: 0x319, data: "010D160CDA030701", values: interlockValues("0"), bound: voltageExtrema},
		)
		for _, bit := range []struct{ field, data string }{
			{"charge_enable", "810D160CDA030701"},
			{"discharge_enable", "410D160CDA030701"},
			{"force_charge_request", "210D160CDA030701"},
		} {
			values := interlockValues("0")
			values[bit.field] = "1"
			vectors = append(vectors, nativeOnlyVector{name: bit.field + "AloneStaysNative", id: 0x319, data: bit.data, values: values, bound: voltageExtrema})
		}
	}
	for _, p := range []struct{ name, data, protection, warning, derating string }{
		{"StatusPatternStaysNative", "A659C9360172A610", "0xA659", "0xC936", "0xA672"},
		{"InverseStatusPatternStaysNative", "59A636C9018D5910", "0x59A6", "0x36C9", "0x598D"},
	} {
		// No signed reduction binds the status words or named bits to SemReg.
		// The common projection gives no revision-specific interpretation.
		values := map[string]string{"protection_flags": p.protection, "warning_flags": p.warning}
		if profile == V108Profile {
			values["derating_reason"] = p.derating // no storage element for the reason or its bits
		}
		vectors = append(vectors, nativeOnlyVector{name: p.name, id: 0x312, data: p.data, values: values, group: profile != CommonProfile})
	}
	if profile == V108Profile {
		vectors = append(vectors,
			nativeOnlyVector{name: "ExtendedStatusPatternStaysNative", id: 0x323, data: "10000000A659C936", group: true},
			nativeOnlyVector{name: "InverseExtendedStatusPatternStaysNative", id: 0x323, data: "1000000059A636C9", group: true},
			// The temperature extrema have indices but no defined cell-temperature binding.
			nativeOnlyVector{name: "TemperatureExtremaStayNative", id: 0x322, data: "00FFFFEC0409503C", values: map[string]string{
				"highest_cell_temperature": "255", "lowest_cell_temperature": "-20", "highest_temperature_index": "4", "lowest_temperature_index": "9",
			}},
		)
	}
	return vectors
}

func interlockValues(value string) map[string]string {
	return map[string]string{"charge_enable": value, "discharge_enable": value, "force_charge_request": value}
}

func monitorNativeOnlyVectors() []nativeOnlyVector {
	vectors := []nativeOnlyVector{
		// USOC, SOH and ASOC have a scale but no specified unit.
		{name: "UnspecifiedUnitsKeepNativeValues", id: 0x04040005, data: "2103EE02AB020900", values: map[string]string{
			"usoc": "80.1", "soh": "75", "asoc": "68.3",
		}},
		// No read-row limit carriage.
		{name: "PermittedCurrentsStayNative", id: 0x04050005, data: "7B00C80101020304", values: map[string]string{
			"permitted_charge_current": "12.3", "permitted_discharge_current": "45.6",
		}},
		// Unqualified integer byte order: these are native bit patterns, not quantities in Ah or Wh.
		{name: "CumulativeChargeKeepsRawBytes", id: 0x04060005, data: "0102030405060708", values: map[string]string{
			"cumulative_incoming_charge": "0x04030201", "cumulative_outgoing_charge": "0x08070605",
		}},
		{name: "CumulativeEnergyKeepsRawBytes", id: 0x04070005, data: "91A2B3C4D5E6F708", values: map[string]string{
			"cumulative_incoming_energy": "0xC4B3A291", "cumulative_outgoing_energy": "0x08F7E6D5",
		}},
	}
	// No severity is assigned to the alarm and fault codes. Keep the raw
	// code bytes and the named subfields of all four condition layouts.
	for i, name := range []string{"ProtectionAndAlarm", "SystemFaults", "OtherErrors", "HardwareErrors"} {
		id := 0x040A0005 + uint32(i)<<16
		vectors = append(vectors,
			nativeOnlyVector{name: name + "StayNative", id: id, data: "93A65BC65AD629E1", group: true},
			nativeOnlyVector{name: name + "KeepChangedCodes", id: id, data: "6C59A439A529D61E", group: true},
		)
	}
	return vectors
}

func evaluateNativeOnlyVector(t *testing.T, profile string, v nativeOnlyVector) (canbus.Frame, roweval.Acquired[Selector], map[string]roweval.Evaluation) {
	t.Helper()
	raw := vector{Identifier: v.id, Extended: profile == MonitorProfile, DLC: 8, Data: v.data}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve both subjects so an unresolved role or slot cannot mask a binding.
	a := acquired(raw, cellStorageRoles(t))
	a.Coordinates[cellSlot] = "cell.synthetic"
	var evals map[string]roweval.Evaluation
	var reason roweval.Reason
	switch profile {
	case Profile:
		evals, reason, err = EvaluateFrame(V104Table(), frame, a)
	case V108Profile:
		evals, reason, err = EvaluateV108Frame(frame, a)
	case CommonProfile:
		evals, reason, err = EvaluateCommonFrame(ScopeMember, frame, a)
	case MonitorProfile:
		evals, reason, err = EvaluateMonitorFrame(frame, a)
	}
	if err != nil || reason != "" {
		t.Fatalf("expected admitted frame: reason %q err %v", reason, err)
	}
	return frame, a, evals
}

func nativeConditionValues(t *testing.T, profile string, frame canbus.Frame) map[string]string {
	t.Helper()
	out := map[string]string{}
	var admitted bool
	switch profile {
	case Profile:
		fields, _, ok := reportFields(t, frame)
		admitted = ok
		for name, f := range fields {
			if isNativeStatusBit(name) {
				out[name] = fmt.Sprint(f)
			}
		}
	case V108Profile:
		fields, ok := v108ReportFields(t, frame)
		admitted = ok
		for name, f := range fields {
			if isNativeStatusBit(name) {
				out[name] = fmt.Sprint(f.Numerator)
			}
		}
	case MonitorProfile:
		fields, ok := monitorReportFields(t, frame)
		admitted = ok
		for name, f := range fields {
			out[name] = fmt.Sprint(f.Numerator)
		}
	}
	if !admitted || len(out) == 0 {
		t.Fatal("expected named condition fields in the report")
	}
	return out
}

func isNativeStatusBit(name string) bool {
	switch name {
	case "protection_flags", "warning_flags", "derating_reason":
		return false
	}
	return strings.HasPrefix(name, "protection_") || strings.HasPrefix(name, "warning_") || strings.HasPrefix(name, "derating_")
}

func checkNativeOnlyVector(t *testing.T, profile string, v nativeOnlyVector, frame canbus.Frame, a roweval.Acquired[Selector], evals map[string]roweval.Evaluation) {
	t.Helper()
	want := map[string]string{}
	if v.group {
		want = nativeConditionValues(t, profile, frame)
	}
	for name, value := range v.values {
		want[name] = value
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			id := profile + "." + name
			ev := evals[id]
			assertNativeOnlyValue(t, id, ev, want[name], a)
			if ev.Candidate != nil || ev.Reason != roweval.ReasonNoBinding {
				t.Fatalf("candidate %+v reason %q (%s), want no candidate with %q", ev.Candidate, ev.Reason, ev.Detail, roweval.ReasonNoBinding)
			}
		})
	}
	allowed := map[string]bool{}
	for _, name := range v.bound {
		allowed[profile+"."+name] = true
	}
	for id, ev := range evals {
		if ev.Candidate != nil && (!allowed[id] || ev.Candidate.Key.FactID == "storage.pack.power") {
			t.Errorf("unexpected candidate %s from %s", ev.Candidate.Key.FactID, id)
		}
	}
	for name, value := range v.operands {
		id := profile + "." + name
		ev := evals[id]
		assertNativeOnlyValue(t, id, ev, value, a)
		if ev.Candidate == nil {
			t.Errorf("%s: expected the operand's individual candidate", id)
		}
	}
}

func assertNativeOnlyValue(t *testing.T, id string, ev roweval.Evaluation, want string, a roweval.Acquired[Selector]) {
	t.Helper()
	n := ev.Native
	if n == nil || n.RowID != id || n.Reason != "" || n.Value == nil || n.Value.Quantity == nil || !decimalEquals(n.Value.Quantity.Number, want) {
		t.Fatalf("%s: native %+v, want value %s", id, n, want)
	}
	if !reflect.DeepEqual(n.Evidence, a.Template.Evidence) {
		t.Fatalf("%s: native evidence differs from the acquired record", id)
	}
}
