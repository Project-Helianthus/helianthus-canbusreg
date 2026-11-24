package canbusreg

import (
	"strings"
	"testing"
)

func TestGrowattV108SelectedLayoutAndCommonFields(t *testing.T) {
	source := growattInterface(t, "synthetic", 1)
	v104, _ := NewGrowattBMSProfile(source, BMSRevisionV104)
	v108, _ := NewGrowattBMSProfile(source, BMSRevisionV108)
	if missing, err := NewGrowattBMSProfile(source, ""); missing != nil || err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatal(missing, err)
	}
	common, _ := NewGrowattLowVoltageBMSCommonProjector(source)
	evidence := growattEvidence(t, source, 1, 0x313, []byte{0x14, 0, 0xff, 0xf6, 0xff, 0xc9, 81, 0xd5})
	checkGrowattReportField(t, v108, evidence, "average_battery_temperature", -55, 10, "°C")
	checkGrowattReportField(t, v108, evidence, "life_warning", 1, 1, "")
	checkGrowattReportField(t, v104, evidence, "maximum_cell_temperature", -55, 10, "°C")
	checkGrowattReportField(t, v104, evidence, "soh_valid", 1, 1, "")
	got, ok := common.Apply(evidence)
	if !ok || got.Measurements == nil || got.Measurements.VoltageCentivolts != 5120 || got.Measurements.CurrentDeciamps != -10 || got.Measurements.SOCPercent != 81 || got.Measurements.SOHValue != 85 {
		t.Fatalf("common: %+v", got)
	}
	status := growattEvidence(t, source, 2, 0x312, []byte{0, 0x01, 0, 0, 1, 0xc1, 0x42, 0})
	checkGrowattReportField(t, v108, status, "derating_hardware_fault", 1, 1, "")
	checkGrowattReportField(t, v108, status, "derating_bus_fault", 1, 1, "")
	checkGrowattReportField(t, v108, status, "derating_low_cell_voltage", 1, 1, "")
	checkGrowattReportField(t, v108, status, "derating_cell_voltage_difference", 1, 1, "")
	checkGrowattReportField(t, v108, status, "protection_parallel_version_disagreement", 1, 1, "")
	if got := v104.Classify(status); got.Profile != "" {
		t.Fatal("reserved byte substituted for valid V1.04 cell count", got)
	}
	legacyIdentity := growattEvidence(t, source, 3, 0x320, []byte{0, 0, 10, 4, 9, 1, 2, 3})
	checkGrowattReportField(t, v104, legacyIdentity, "software_version", 4, 1, "")
	if got := v108.Classify(legacyIdentity); got.Profile != "" || got.Projection != nil {
		t.Fatal("V1.04 identity outside the V1.08 hardware version domain admitted", got)
	}
	for _, frame := range []uint32{0x315, 0x316, 0x317, 0x318} {
		cell := growattEvidence(t, source, 3, frame, []byte{0x0c, 0x80, 0x0d, 0x11, 0x0d, 0x12, 0x0d, 0x13})
		if got := v108.Classify(cell); got.Profile != "growatt.bms.low_voltage.can.v1_08" {
			t.Fatal(got)
		}
		if got := NewRegistry(v104, v108).Classify(cell); got.Profile != "" {
			t.Fatal("ambiguous interpretation promoted", got)
		}
	}
	for _, tc := range []struct {
		id      uint32
		payload []byte
	}{
		{0x315, []byte{1, 2, 3, 4, 5, 6, 7}},
		{0x312, []byte{0, 0, 0, 0, 255, 0, 0, 0}},
		{0x320, []byte{0, 0, 10, 0, 0, 0, 0, 0}},
		{0x321, []byte{0, 101, 0, 0, 0, 0, 0, 0}},
		{0x323, []byte{0, 0, 0, 0, 0, 0, 0, 0}},
	} {
		if got := v108.Classify(growattEvidence(t, source, 4, tc.id, tc.payload)); got.Profile != "" {
			t.Fatal("wrong layout admitted", got)
		}
	}
}

func checkGrowattReportField(t *testing.T, profile Profile, e Evidence, name string, n, d int64, unit string) {
	t.Helper()
	c := profile.Classify(e)
	r, ok := c.Projection.(GrowattBMSReport)
	if !ok || c.Profile == "" || r.Evidence != e {
		t.Fatalf("%s: report or evidence missing: %+v", name, c)
	}
	f := r.Fields[name]
	if f.Numerator != n || f.Denominator != d || f.Unit != unit {
		t.Errorf("%s: %+v, want %d/%d %s", name, f, n, d, unit)
	}
}
