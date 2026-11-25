package bms

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// monitorFrame builds one extended addressed monitor frame of DLC 8.
func monitorFrame(t *testing.T, id uint32, data string) (canbus.Frame, []byte) {
	t.Helper()
	raw := vector{Identifier: id, Extended: true, DLC: 8, Data: data}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return frame, raw
}

// v108ReportFields returns the fields the frozen V1.08 report decoder gives for
// one frame, keyed by the report's own field names, and whether it admits the
// frame. A rejected frame gives no fields.
func v108ReportFields(t *testing.T, target canbus.Frame) (map[string]canbusreg.GrowattReportField, bool) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := canbusreg.NewGrowattBMSProfile(iface, canbusreg.BMSRevisionV108)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := profile.Classify(canbusreg.Evidence{Frame: target, Interface: iface, Sequence: 1}).Projection.(canbusreg.GrowattBMSReport)
	if !ok {
		return nil, false
	}
	return report.Fields, true
}

// monitorReportFields returns the fields the frozen addressed monitor decoder
// gives for one frame, and whether it admits the frame.
func monitorReportFields(t *testing.T, target canbus.Frame) (map[string]canbusreg.GrowattReportField, bool) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := canbusreg.NewGrowattMonitorAddressed(iface)
	if !ok {
		t.Fatal("the frozen monitor decoder refused its interface")
	}
	report, ok := p.Apply(canbusreg.Evidence{Frame: target, Interface: iface, Sequence: 1})
	if !ok {
		return nil, false
	}
	return report.Fields, true
}

// rawBitPattern is the bit pattern a raw row extracts from bytes: the bytes read
// big-endian for a big-endian row and little-endian for a bit slice.
func rawBitPattern(bytes []byte, bigEndianRow bool) uint64 {
	var v uint64
	if bigEndianRow {
		for _, b := range bytes {
			v = v<<8 | uint64(b)
		}
		return v
	}
	for i, b := range bytes {
		v |= uint64(b) << (8 * uint(i))
	}
	return v
}

// v108Renamed maps a row name to the frozen report's field name where the two
// differ: the pack fields, the average temperature, the lifetime alarm and the
// frame number of 0x324, whose row name is distinct from that of 0x325.
var v108Renamed = map[string]string{
	"pack_voltage": "voltage", "pack_current": "current", "average_temperature": "average_battery_temperature",
	"lifetime_alarm": "life_warning", "serial_frame_number": "frame_number",
}

// v108Unnamed are the V1.08 rows the frozen report names no field for: the raw
// status and protection words, the derating span and the reserved bytes. The
// report keeps none of them under a name, so they have no oracle and are not
// compared.
var v108Unnamed = map[string]bool{
	"status_word": true, "protection_flags": true, "warning_flags": true,
	"derating_reason": true, "reserved_byte_7": true, "reserved_bytes": true,
}

// sweepPatterns are the payloads every identifier of a sweep is fed: zero, all
// ones, ascending bytes, 100 and 254 in every byte, and a mixed pattern, so each
// frame gate is crossed both ways.
var sweepPatterns = []string{"0000000000000000", "FFFFFFFFFFFFFFFF", "0102030405060708", "6464646464646464", "FEFEFEFEFEFEFEFE", "0A0B0C0D0E0F1011"}

// checkV108RowAgainstReport compares one admitted V1.08 row and its native
// evaluation with the frozen V1.08 report field of the same name.
func checkV108RowAgainstReport(t *testing.T, row roweval.NativeRow[Selector], ev roweval.Evaluation, fields map[string]canbusreg.GrowattReportField) {
	t.Helper()
	name := rowNameIn(V108Profile, row.ID)
	if v108Unnamed[name] {
		return
	}
	if ev.Native == nil {
		t.Fatalf("%s: no native evaluation", row.ID)
	}
	field, named := fields[renamedOr(v108Renamed, name)]
	if !named {
		if name == "battery_type" && ev.Native.Reason == roweval.ReasonOutsideDomain && ev.Candidate == nil {
			return // type code 3 is not a battery type: the report names none and the row gives none
		}
		t.Fatalf("%s: the frozen V1.08 report names no field %s", row.ID, renamedOr(v108Renamed, name))
	}
	if len(field.Raw) > 0 {
		if ev.Native.Reason != "" || ev.Native.Value == nil {
			t.Fatalf("%s: raw row gave reason %q, the frozen report keeps bytes %x", row.ID, ev.Native.Reason, field.Raw)
		}
		if want := rawBitPattern(field.Raw, row.Wire.ByteOrder == bigEndian); ev.Native.Raw != want {
			t.Fatalf("%s: native raw %#x, frozen report bytes %x give %#x", row.ID, ev.Native.Raw, field.Raw, want)
		}
		return
	}
	want := field.Numerator
	if name == "remaining_capacity" || name == "full_charge_capacity" {
		want = field.Numerator / 10 // the report counts 10 mAh in mAh; the row keeps the count
	}
	if ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, fmt.Sprint(want)) {
		t.Fatalf("%s: native %+v reason %q, frozen report gives %d", row.ID, ev.Native.Value, ev.Native.Reason, want)
	}
}

// TestV108TableMatchesTheFrozenReportOnEveryIdentifierAndPattern pins every V1.08
// row of frames 0x311 to 0x325 against the frozen V1.08 report on the same bytes,
// over several payloads each. The table admits a frame exactly when the frozen
// decoder admits it, and every row of an admitted frame is compared with the
// field of the same name the report gives.
func TestV108TableMatchesTheFrozenReportOnEveryIdentifierAndPattern(t *testing.T) {
	roles := storageRoles(t)
	table := V108Table()
	for id := uint32(0x311); id <= 0x325; id++ {
		for _, pattern := range sweepPatterns {
			frame, raw := commonFrame(t, id, pattern)
			evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
			if err != nil {
				t.Fatal(err)
			}
			fields, admitted := v108ReportFields(t, frame)
			if (reason == "") != admitted {
				t.Fatalf("%#x %s: the table gives reason %q, the frozen decoder admits %v", id, pattern, reason, admitted)
			}
			if !admitted {
				continue
			}
			for _, row := range table.Native {
				if row.At.Identifier == id {
					checkV108RowAgainstReport(t, row, evals[row.ID], fields)
				}
			}
		}
	}
}

// TestV108GateAdmitsEachFrameAtTheBoundsOfTheFrozenDecoder pins the V1.08 frame
// gate at the edges of each admitted frame, and that the frozen decoder gives the
// same admission. Values outside the bounds are "not valid"; an identifier the
// decoder does not list is no match.
func TestV108GateAdmitsEachFrameAtTheBoundsOfTheFrozenDecoder(t *testing.T) {
	roles := storageRoles(t)
	cases := []struct {
		id     uint32
		data   string
		reason roweval.Reason
	}{
		{0x312, "0000000000000000", "not valid"},
		{0x312, "0000000001000000", ""},
		{0x312, "00000000FE000000", ""},
		{0x312, "00000000FF000000", "not valid"},
		{0x313, "0000000000006465", ""}, // byte 6 is 100
		{0x313, "0000000000006564", "not valid"},
		{0x320, "5A3B000000000000", "not valid"},
		{0x320, "5A3B090000000000", ""},
		{0x320, "5A3B0A0000000000", "not valid"},
		{0x321, "0064000000000000", ""},
		{0x321, "0065000000000000", "not valid"},
		{0x322, "0000000000006464", ""},
		{0x322, "0000000000006564", "not valid"},
		{0x322, "0000000000006465", "not valid"},
		{0x323, "0000000000000000", "not valid"},
		{0x323, "0100000000000000", ""},
		{0x323, "FE00000000000000", ""},
		{0x323, "FF00000000000000", "not valid"},
		{0x326, "0000000000000000", roweval.ReasonNoMatch},
	}
	for _, c := range cases {
		frame, raw := commonFrame(t, c.id, c.data)
		_, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
		if err != nil {
			t.Fatal(err)
		}
		if reason != c.reason {
			t.Fatalf("%#x %s: reason %q, want %q", c.id, c.data, reason, c.reason)
		}
		if _, admitted := v108ReportFields(t, frame); admitted != (c.reason == "") {
			t.Fatalf("%#x %s: the frozen decoder admits %v, the table reason is %q", c.id, c.data, admitted, c.reason)
		}
	}
}

// TestV108ExtremaFrameGivesItsSignedTemperaturesAndIndices pins the V1.08 0x322
// native rows: the highest temperature is +25.5 degrees (255 in 0.1 degree), the
// lowest is -2.0 degrees (-20), the indices and the states of charge are the
// frame's bytes, and none of them has a storage candidate.
func TestV108ExtremaFrameGivesItsSignedTemperaturesAndIndices(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x322, "00FFFFEC0409503C")
	evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	want := map[string]string{
		"highest_cell_temperature": "255", "lowest_cell_temperature": "-20",
		"highest_temperature_index": "4", "lowest_temperature_index": "9",
		"highest_soc": "80", "lowest_soc": "60",
	}
	for name, value := range want {
		ev := evals[V108Profile+"."+name]
		if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, value) {
			t.Fatalf("%s: native %+v, want %s", name, ev.Native, value)
		}
		if ev.Candidate != nil {
			t.Fatalf("%s: a cell extremum produced a candidate %+v; no cell subject is bound", name, ev.Candidate)
		}
	}
}

// TestV108BatteryTypeCodeThreeGivesNoValue pins that battery type code 3, which
// the frozen decoder names as no type, gives the row no value and no candidate,
// while the bits of the same byte keep their own rows.
func TestV108BatteryTypeCodeThreeGivesNoValue(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x319, "FF0D160CDA030701")
	evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	battery := evals[V108Profile+".battery_type"]
	if battery.Native == nil || battery.Native.Reason != roweval.ReasonOutsideDomain || battery.Candidate != nil {
		t.Fatalf("battery type code 3: native %+v candidate %+v, want outside the domain and no candidate", battery.Native, battery.Candidate)
	}
	fields, admitted := v108ReportFields(t, frame)
	if !admitted {
		t.Fatalf("the frozen decoder rejects a frame the table admits")
	}
	if _, named := fields["battery_type"]; named {
		t.Fatalf("the frozen decoder names a battery type for code 3")
	}
	if ev := evals[V108Profile+".charge_enable"]; ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, "1") {
		t.Fatalf("charge_enable of the same byte: %+v, want 1", ev.Native)
	}
}

// TestEvaluateFrameRefusesRowsOfAnotherProfile pins that the V1.04 frame gate
// evaluates only the V1.04 table. A V1.08, common-projection or addressed monitor
// table given the same valid V1.04 bytes gives no match and no native value or
// candidate under its own profile; the V1.04 table still evaluates the frame.
func TestEvaluateFrameRefusesRowsOfAnotherProfile(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x313, "FF9CFF9CFF3864D0")
	for name, table := range map[string]roweval.Table[Selector]{
		"V1.08":         V108Table(),
		"common member": CommonTable(ScopeMember),
		"monitor":       MonitorTable(0x04010005),
	} {
		evals, reason, err := EvaluateFrame(table, frame, acquired(raw, roles))
		if err != nil || reason != roweval.ReasonNoMatch || evals != nil {
			t.Fatalf("%s table: reason %q err %v and %d evaluations, want no match and none", name, reason, err, len(evals))
		}
	}
	v104, reason, err := EvaluateFrame(V104Table(), frame, acquired(raw, roles))
	if err != nil || reason != "" || len(v104) == 0 {
		t.Fatalf("V1.04 table: reason %q err %v rows %d", reason, err, len(v104))
	}
}

// TestEveryRowOfTheTablesUsesOnlyIntegerPatternsAndByteOrders pins that every table of the
// package, the V1.04, V1.08, common-projection and addressed monitor tables,
// names only the signed and unsigned integer codecs and the byte orders the
// EX03 exemplar registers, carries no hook on a native row, and validates
// against the pinned catalog.
func TestEveryRowOfTheTablesUsesOnlyIntegerPatternsAndByteOrders(t *testing.T) {
	roles := cellStorageRoles(t)
	if n := len(V104Descriptors().Codecs); n != 2 {
		t.Fatalf("the package registers %d codecs, want the signed and unsigned integer codecs only", n)
	}
	tables := map[string]roweval.Table[Selector]{
		"V1.04": V104Table(), "V1.08": V108Table(), "common": CommonTable(ScopeMember),
		"monitor": MonitorTable(0x04010005), "monitor module": MonitorTable(0x08310029),
	}
	for name, table := range tables {
		if err := roweval.ValidateTable(table, V104Descriptors(), roles); err != nil {
			t.Fatalf("%s table: %v", name, err)
		}
		for _, row := range table.Native {
			if row.Wire.Codec != "signed" && row.Wire.Codec != "unsigned" {
				t.Fatalf("%s: %s names codec %q", name, row.ID, row.Wire.Codec)
			}
			if row.Wire.ByteOrder != bigEndian && row.Wire.ByteOrder != littleEndian && row.Wire.ByteOrder != bitSlice {
				t.Fatalf("%s: %s names byte order %q", name, row.ID, row.Wire.ByteOrder)
			}
			if row.Decode.Hook != "" {
				t.Fatalf("%s: %s names hook %q on a native row", name, row.ID, row.Decode.Hook)
			}
		}
	}
}

// TestMonitorSweepMatchesTheFrozenDecoderOnEveryLayout pins every addressed
// monitor row of every live base against the frozen addressed monitor decoder,
// over several payloads, at a system address for the 04 family and at a module
// address (BMU 3, BCU 0x29) for the 08, 10 and 14 families. A frame the table
// admits is admitted by the decoder, and each row's value is the decoder's value.
func TestMonitorSweepMatchesTheFrozenDecoderOnEveryLayout(t *testing.T) {
	roles := storageRoles(t)
	patterns := []string{"0000000000000000", "FFFFFFFFFFFFFFFF", "0102030405060708", "8FE0A1B2C3D4E5F6"}
	rows := 0
	for base := range monitorLayouts {
		id := base | 0x05
		if base>>24 != 0x04 {
			id = base | 3<<20 | 0x29
		}
		for _, pattern := range patterns {
			frame, raw := monitorFrame(t, id, pattern)
			evals, reason, err := EvaluateMonitorFrame(frame, acquired(raw, roles))
			if err != nil {
				t.Fatal(err)
			}
			fields, admitted := monitorReportFields(t, frame)
			if (reason == "") != admitted {
				t.Fatalf("%#x %s: the table gives reason %q, the frozen decoder admits %v", id, pattern, reason, admitted)
			}
			for _, row := range MonitorTable(id).Native {
				rows++
				name := rowNameIn(MonitorProfile, row.ID)
				field, named := fields[name]
				if !named {
					t.Fatalf("%#x %s: the frozen monitor decoder names no field %s", id, pattern, name)
				}
				ev := evals[row.ID]
				if ev.Native == nil || ev.Native.Reason != "" {
					t.Fatalf("%#x %s: %s native reason %q", id, pattern, name, ev.Native.Reason)
				}
				if len(field.Raw) > 0 {
					if want := rawBitPattern(field.Raw, false); ev.Native.Raw != want {
						t.Fatalf("%#x %s: %s raw %#x, frozen bytes %x give %#x", id, pattern, name, ev.Native.Raw, field.Raw, want)
					}
					continue
				}
				den := field.Denominator
				if den == 0 {
					den = 1
				}
				want := new(big.Rat).SetFrac64(field.Numerator, den)
				got, ok := decimalRat(ev.Native.Value.Quantity.Number)
				if !ok || got.Cmp(want) != 0 {
					t.Fatalf("%#x %s: %s = %v, frozen decoder gives %d/%d", id, pattern, name, got, field.Numerator, den)
				}
			}
		}
	}
	if rows < 200 {
		t.Fatalf("the sweep compared %d rows, want every live field of every base", rows)
	}
}

// TestMonitorFramesBindOnlyTheirOwnPackAndAddress pins the addressed monitor
// rows to their identifier: a module frame gives the four cell voltages of its
// own family and BCU and BMU only, a table of another BMU gives no row for it,
// and the pack voltage and current of the system family bind to the configured
// pack with their decoded signed SI values.
func TestMonitorFramesBindOnlyTheirOwnPackAndAddress(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := monitorFrame(t, 0x04010005, "8813CC74A00FA10F")
	evals, reason, err := EvaluateMonitorFrame(frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted system frame: reason %q err %v", reason, err)
	}
	voltage := evals[MonitorProfile+".internal_pack_voltage"]
	if voltage.Native == nil || voltage.Native.Value == nil || !decimalEquals(voltage.Native.Value.Quantity.Number, "500") {
		t.Fatalf("internal pack voltage native %+v, want 500 V", voltage.Native)
	}
	if c := voltage.Candidate; c == nil || c.Key.FactID != "storage.pack.voltage" || !decimalEquals(c.Value.Quantity.Number, "500") || string(c.Value.Quantity.Unit) != "unit.volt" {
		t.Fatalf("pack voltage candidate %+v, want storage.pack.voltage 500 unit.volt", voltage.Candidate)
	}
	current := evals[MonitorProfile+".current"]
	if current.Native == nil || current.Native.Value == nil || !decimalEquals(current.Native.Value.Quantity.Number, "-10") {
		t.Fatalf("pack current native %+v, want -10 A", current.Native)
	}
	if c := current.Candidate; c == nil || c.Key.FactID != "storage.pack.current" || !decimalEquals(c.Value.Quantity.Number, "-10") || string(c.Value.Quantity.Unit) != "unit.ampere" {
		t.Fatalf("pack current candidate %+v, want storage.pack.current -10 unit.ampere", current.Candidate)
	}
	if c := voltage.Candidate; c.Key.Dimensions[0].Value.Text == nil || *c.Key.Dimensions[0].Value.Text != acquired(raw, roles).Coordinates[packSlot] {
		t.Fatalf("pack voltage key %+v does not name the configured pack", c.Key)
	}

	module, mraw := monitorFrame(t, 0x08310029, "E50CE60CDA0CD80C")
	mevals, reason, err := EvaluateMonitorFrame(module, acquired(mraw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted module frame: reason %q err %v", reason, err)
	}
	if len(mevals) != len(monitorLayouts[0x08010000]) {
		t.Fatalf("module frame gave %d rows, its family has %d", len(mevals), len(monitorLayouts[0x08010000]))
	}
	for name, want := range map[string]string{"cell_voltage_5": "3301", "cell_voltage_6": "3302", "cell_voltage_7": "3290", "cell_voltage_8": "3288"} {
		ev := mevals[MonitorProfile+"."+name]
		if ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, want) {
			t.Fatalf("%s native %+v, want %s mV", name, ev.Native, want)
		}
	}
	for _, row := range MonitorTable(0x08310129).Native {
		if row.At.Identifier == 0x08310029 {
			t.Fatalf("the table of BMU 1 has a row at the identifier of BMU 3: %s", row.ID)
		}
	}
	other, oraw := monitorFrame(t, 0x08310029, "E50CE60CDA0CD80C")
	evs, _, err := evaluateAdmitted(MonitorTable(0x08310129), other, acquired(oraw, roles))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("the table of BMU 1 evaluated a frame of BMU 3: %d rows", len(evs))
	}
}

// TestMonitorNativeOnlyFieldsGiveNoCandidate pins that the monitor fields the
// page leaves without a binding here keep their native values and give no
// candidate: the usable state of charge (its unit is not stated by the decoder)
// and the cell voltage of a module frame (no cell subject is bound). The cell
// extrema and the maximum cell temperature of their own families are bound
// only for a resolved cell; rows_cells_test.go pins them.
func TestMonitorNativeOnlyFieldsGiveNoCandidate(t *testing.T) {
	roles := storageRoles(t)
	cases := []struct {
		id    uint32
		data  string
		name  string
		value string
	}{
		{0x04040005, "2003000000000000", "usoc", "80"},
		{0x08010029, "E50CE60CDA0CD80C", "cell_voltage_5", "3301"},
	}
	for _, c := range cases {
		frame, raw := monitorFrame(t, c.id, c.data)
		evals, reason, err := EvaluateMonitorFrame(frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", c.name, reason, err)
		}
		ev := evals[MonitorProfile+"."+c.name]
		if ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, c.value) {
			t.Fatalf("%s: native %+v, want %s", c.name, ev.Native, c.value)
		}
		if ev.Candidate != nil || ev.Reason != roweval.ReasonNoBinding {
			t.Fatalf("%s: candidate %+v reason %q, want no candidate and %q", c.name, ev.Candidate, ev.Reason, roweval.ReasonNoBinding)
		}
	}
}

// TestMonitorGateRefusesWhatItDoesNotDecode pins the addressed monitor frame
// gate: a standard identifier, a DLC other than 8, a base with no live fields and
// a diagnostic record family are no match and give no evaluation.
func TestMonitorGateRefusesWhatItDoesNotDecode(t *testing.T) {
	roles := storageRoles(t)
	cases := []struct {
		name string
		v    vector
	}{
		{"standard identifier", vector{Identifier: 0x311, DLC: 8, Data: "8813CC74A00FA10F"}},
		{"DLC 7", vector{Identifier: 0x04010005, Extended: true, DLC: 7, Data: "8813CC74A00FA1"}},
		{"family without live fields", vector{Identifier: 0x04FF0005, Extended: true, DLC: 8, Data: "8813CC74A00FA10F"}},
		{"diagnostic record family", vector{Identifier: 0x18810005, Extended: true, DLC: 8, Data: "8813CC74A00FA10F"}},
	}
	for _, c := range cases {
		raw := c.v.record(t)
		frame, err := canbus.DecodeSocketCANRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		evals, reason, err := EvaluateMonitorFrame(frame, acquired(raw, roles))
		if err != nil || reason != roweval.ReasonNoMatch || evals != nil {
			t.Fatalf("%s: reason %q err %v and %d evaluations, want no match and none", c.name, reason, err, len(evals))
		}
	}
}
