package canbusreg

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGrowattV104ReportFieldsAndOptionalAbsence(t *testing.T) {
	p := bmsProvider(t)
	bmsStart(t, p, bmsNewSource(), 1)
	at := p.bmsInterface
	capacity := growattEvidence(t, at, 1, 0x314, []byte{1, 2, 3, 4, 0, 31, 0, 73})
	bmsFeed(p, capacity)
	first := p.Snapshot()
	if first.Projection != nil || len(first.Reports) != 1 || first.Reports[0x314].Report.Evidence != capacity || first.Reports[0x314].Generation != 1 {
		t.Fatal(first)
	}
	if _, ok := first.Reports[0x315]; ok {
		t.Fatal("unobserved cells presented")
	}
	checkGrowattReportField(t, p.bmsProfile, capacity, "remaining_capacity", 2580, 1, "mAh")
	checkGrowattReportField(t, p.bmsProfile, capacity, "full_charge_capacity", 7720, 1, "mAh")
	checkGrowattReportField(t, p.bmsProfile, capacity, "cell_voltage_spread", 31, 1, "mV")
	checkGrowattReportField(t, p.bmsProfile, capacity, "cycle_count", 73, 1, "count")
	request := growattEvidence(t, at, 2, 0x319, []byte{0xba, 0x0d, 0x10, 0x0c, 0x80, 3, 4, 2})
	checkGrowattReportField(t, p.bmsProfile, request, "charge_enable", 1, 1, "")
	checkGrowattReportField(t, p.bmsProfile, request, "discharge_enable", 0, 1, "")
	checkGrowattReportField(t, p.bmsProfile, request, "force_charge_request", 1, 1, "")
	checkGrowattReportField(t, p.bmsProfile, request, "battery_chemistry_code", 2, 1, "")
	checkGrowattReportField(t, p.bmsProfile, request, "minimum_cell_voltage", 3200, 1, "mV")
	checkGrowattReportField(t, p.bmsProfile, request, "maximum_cell_index", 3, 1, "")
	status := growattEvidence(t, at, 2, 0x312, []byte{0x21, 0x83, 0x20, 0x0a, 1, 0x42, 0x4d, 16})
	checkGrowattReportField(t, p.bmsProfile, status, "protection_short_circuit_discharge", 1, 1, "")
	checkGrowattReportField(t, p.bmsProfile, status, "warning_before_shutdown", 1, 1, "")
	got := p.bmsProfile.Classify(status).Projection.(GrowattBMSReport)
	if _, ok := got.Fields["protection_parallel_failure"]; ok {
		t.Fatal("V1.08 name applied to V1.04 raw bit")
	}
	update := growattEvidence(t, at, 3, 0x321, []byte{2, 93, 6, 5, 1, 4, 7, 8})
	checkGrowattReportField(t, p.bmsProfile, update, "successful_update_count", 5, 1, "count")
	identity := growattEvidence(t, at, 4, 0x320, []byte{0x42, 0x4d, 3, 9, 1, 2, 3, 4})
	bmsFeed(p, identity)
	date := p.Snapshot().Reports[0x320].Report.Fields["build_date_time"]
	if !bytes.Equal(date.Raw, []byte{1, 2, 3, 4}) || date.Unresolved == "" || date.Denominator != 0 {
		t.Fatal(date)
	}
	date.Raw[0] = 9
	if p.Snapshot().Reports[0x320].Report.Fields["build_date_time"].Raw[0] != 1 {
		t.Fatal("snapshot aliases native history")
	}
}

func TestGrowattV104ReportRejectsWrongSourceAndLayout(t *testing.T) {
	source := growattInterface(t, "synthetic", 1)
	profile, _ := NewGrowattBMSProfile(source, BMSRevisionV104)
	for _, e := range []Evidence{
		growattEvidence(t, growattInterface(t, "other", 1), 1, 0x314, make([]byte, 8)),
		growattEvidence(t, growattInterface(t, "synthetic", 2), 1, 0x314, make([]byte, 8)),
		growattEvidence(t, source, 1, 0x314, make([]byte, 7)),
		growattExtendedEvidence(t, source, 1, 0x314, make([]byte, 8)),
		growattEvidence(t, source, 1, 0x312, []byte{0, 0, 0, 0, 1, 0, 0, 0}),
		growattEvidence(t, source, 1, 0x313, []byte{0, 0, 0, 0, 0, 0, 101, 0}),
		growattEvidence(t, source, 1, 0x322, make([]byte, 8)),
	} {
		if r := profile.Classify(e); r.Profile != "" || r.Projection != nil {
			t.Fatalf("invalid frame got a report: %+v", e)
		}
	}
}

func TestGrowattV104PreservesWireDLCAsNativeEvidence(t *testing.T) {
	p := bmsProvider(t)
	bmsStart(t, p, bmsNewSource(), 1)
	frames := []struct {
		id      uint32
		rawDLC  byte
		payload []byte
	}{
		{0x314, 9, []byte{1, 2, 3, 4, 0, 31, 0, 73}},
		{0x311, 15, growattLimitsPayload()},
		{0x312, 8, growattStatusPayload()},
		{0x313, 8, growattMeasurementPayload()},
	}
	var limits Evidence
	for i, tc := range frames {
		e := growattEvidence(t, p.bmsInterface, uint64(i+1), tc.id, tc.payload)
		binary.NativeEndian.PutUint32(e.RawRecord[:4], tc.id)
		e.RawRecord[4] = 8
		if tc.rawDLC > 8 {
			e.RawRecord[7] = tc.rawDLC
		}
		copy(e.RawRecord[8:], tc.payload)
		frame, err := canbus.DecodeSocketCANRecord(e.RawRecord[:])
		if err != nil || frame.RawDLC() != tc.rawDLC || frame.PayloadLength() != 8 {
			t.Fatalf("invalid synthetic input for %#x: %#v, %v", tc.id, frame, err)
		}
		e.Frame = frame
		bmsFeed(p, e)
		got := p.Snapshot()
		if report := got.Reports[tc.id]; report.Report.Evidence != e || report.Generation != 1 || got.Raw != e || got.State != "receiving" {
			t.Fatalf("%#x: wire DLC %d changed admission or evidence: %+v", tc.id, tc.rawDLC, got)
		}
		if tc.id == 0x314 && (got.Projection != nil || got.Reports[tc.id].Report.Fields["remaining_capacity"].Numerator != 2580) {
			t.Fatalf("capacity not independently decoded: %+v", got)
		}
		if tc.id == 0x311 {
			limits = e
		}
	}
	got := p.Snapshot()
	if !got.ProjectionCurrent || got.Projection == nil || got.Projection.LimitsEvidence != limits {
		t.Fatalf("complete tuple did not retain the original limits: %+v", got)
	}
}

func TestGrowattV104ExpiredLimitIsNotRepublished(t *testing.T) {
	p := bmsProvider(t)
	bmsStart(t, p, bmsNewSource(), 1)
	source := p.bmsInterface
	oldLimits := growattEvidence(t, source, 1, 0x311, []byte{1, 0, 0, 1, 0, 2, 0, 0})
	bmsFeed(p, oldLimits)
	bmsFeed(p, growattEvidence(t, source, 2, 0x312, growattStatusPayload()))
	bmsFeed(p, growattEvidence(t, source, 3, 0x313, growattMeasurementPayload()))
	if got := p.Snapshot(); !got.ProjectionCurrent || got.Projection == nil || got.Projection.LimitsEvidence != oldLimits {
		t.Fatalf("initial tuple missing: %+v", got)
	}
	for seq := uint64(4); seq <= 17; seq++ {
		bmsFeed(p, growattEvidence(t, source, seq, 0x314, make([]byte, 8)))
	}
	status := growattEvidence(t, source, 18, 0x312, growattStatusPayload())
	measurements := growattEvidence(t, source, 19, 0x313, growattMeasurementPayload())
	bmsFeed(p, status)
	bmsFeed(p, measurements)
	got := p.Snapshot()
	if got.ProjectionCurrent || got.Projection == nil || got.Projection.LimitsEvidence != oldLimits ||
		got.Projection.StatusEvidence.Sequence != 2 || got.Projection.MeasurementsEvidence.Sequence != 3 {
		t.Fatalf("expired tuple republished or lost historical evidence: %+v", got)
	}
	if got.State != "receiving" || got.Reports[0x312].Report.Evidence != status || got.Reports[0x313].Report.Evidence != measurements {
		t.Fatalf("new reports withheld while tuple was incomplete: %+v", got)
	}
	fresh := growattEvidence(t, source, 20, 0x311, growattLimitsPayload())
	bmsFeed(p, fresh)
	got = p.Snapshot()
	if !got.ProjectionCurrent || got.Projection == nil || got.Projection.Limits.ChargeVoltageDecivolts != 0x238 ||
		got.Projection.LimitsEvidence != fresh || got.Projection.StatusEvidence != status || got.Projection.MeasurementsEvidence != measurements {
		t.Fatalf("new limits not published with the current tuple: %+v", got)
	}
}
