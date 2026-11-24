package canbusreg

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestGrowattMonitorAddressedSelection(t *testing.T) {
	source := growattInterface(t, "synthetic", 1)
	p, ok := NewGrowattMonitorAddressed(source)
	if !ok {
		t.Fatal("explicit profile refused")
	}
	valid := growattExtendedEvidence(t, source, 1, 0x08310029, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	got, ok := p.Apply(valid)
	if !ok || got.Evidence != valid || got.Base != 0x08010000 || got.BCU != 0x29 || !got.HasBMU || got.BMU != 3 || got.Fields["cell_voltage_5"].Numerator != 513 {
		t.Fatalf("addressed cell slot: %+v", got)
	}
	for _, address := range []struct {
		id                 uint32
		controller, module uint8
	}{{0x0831002a, 0x2a, 3}, {0x08410029, 0x29, 4}} {
		other, ok := p.Apply(growattExtendedEvidence(t, source, 2, address.id, valid.Frame.Data()))
		if !ok || other.Base != got.Base || other.BCU != address.controller || other.BMU != address.module || !other.HasBMU {
			t.Fatal(other)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(Evidence) Evidence
	}{
		{"interface-name", func(e Evidence) Evidence { e.Interface = growattInterface(t, "other", 1); return e }},
		{"interface-index", func(e Evidence) Evidence { e.Interface = growattInterface(t, "synthetic", 2); return e }},
		{"payload-length", func(e Evidence) Evidence { return growattExtendedEvidence(t, source, 1, 0x08310029, []byte{1}) }},
		{"native-link", func(e Evidence) Evidence { return growattExtendedEvidence(t, source, 1, 0x1850f402, e.Frame.Data()) }},
		{"address-unused-byte", func(e Evidence) Evidence { return growattExtendedEvidence(t, source, 1, 0x08310129, e.Frame.Data()) }},
		{"unknown-base", func(e Evidence) Evidence { return growattExtendedEvidence(t, source, 1, 0x08340029, e.Frame.Data()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := p.Apply(tc.edit(valid)); ok {
				t.Fatal("unexpected match")
			}
		})
	}
	var unselected *GrowattMonitorAddressed
	if _, ok := unselected.Apply(valid); ok {
		t.Fatal("implicit selection")
	}
}

func TestGrowattMonitorAddressedOverlapScalesAndUnknowns(t *testing.T) {
	source := growattInterface(t, "synthetic", 1)
	p, _ := NewGrowattMonitorAddressed(source)
	for _, tc := range []struct {
		id      uint32
		payload []byte
		want    map[string]GrowattReportField
	}{
		{0x04010015, []byte{35, 2, 0x2f, 0x75, 33, 2, 20, 2}, map[string]GrowattReportField{"internal_pack_voltage": {Numerator: 547, Denominator: 10, Unit: "V"}, "current": {Numerator: -1, Denominator: 10, Unit: "A"}}},
		{0x040a0015, []byte{0x83, 0, 0, 0, 0, 0, 0, 0}, map[string]GrowattReportField{"protect_alarm_0": {Numerator: -125, Denominator: 1}, "internal_sum_voltage_high": {Numerator: 3, Denominator: 1}, "cell_voltage_low": {Numerator: 2, Denominator: 1}}},
		{0x14000015, []byte{0x81, 0x80, 0, 0, 0, 0, 0, 0}, map[string]GrowattReportField{"cell_balance_1": {Numerator: 1, Denominator: 1}, "cell_balance_2": {Denominator: 1}, "cell_balance_8": {Numerator: 1, Denominator: 1}, "cell_balance_9": {Numerator: 1, Denominator: 1}, "cell_balance_16": {Denominator: 1}}},
		{0x04060015, []byte{1, 2, 3, 4, 5, 6, 7, 8}, map[string]GrowattReportField{"cumulative_incoming_charge": {Raw: []byte{1, 2, 3, 4}, Unresolved: "integer byte order unspecified"}, "cumulative_outgoing_charge": {Raw: []byte{5, 6, 7, 8}, Unresolved: "integer byte order unspecified"}}},
		{0x04070015, []byte{8, 7, 6, 5, 4, 3, 2, 1}, map[string]GrowattReportField{"cumulative_incoming_energy": {Raw: []byte{8, 7, 6, 5}, Unresolved: "integer byte order unspecified"}, "cumulative_outgoing_energy": {Raw: []byte{4, 3, 2, 1}, Unresolved: "integer byte order unspecified"}}},
	} {
		e := growattExtendedEvidence(t, source, 1, tc.id, tc.payload)
		got, ok := p.Apply(e)
		if !ok || got.BCU != 0x15 {
			t.Fatal(tc.id, got)
		}
		for k, v := range tc.want {
			if !reflect.DeepEqual(got.Fields[k], v) {
				t.Errorf("%X/%s: got %+v want %+v", tc.id, k, got.Fields[k], v)
			}
		}
	}
}

func TestGrowattMonitorAddressedEveryLiveFrame(t *testing.T) {
	data, err := os.ReadFile("testdata/growatt_monitor_addressed_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Base, Field, Unit string
		Numerator         int64
		Denominator       *int64
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 26 {
		t.Fatalf("got %d live frame vectors", len(vectors))
	}
	source := growattInterface(t, "synthetic", 1)
	p, _ := NewGrowattMonitorAddressed(source)
	seen := map[uint32]bool{}
	for _, v := range vectors {
		t.Run(v.Base, func(t *testing.T) {
			value, err := strconv.ParseUint(v.Base, 16, 32)
			if err != nil {
				t.Fatal(err)
			}
			base := uint32(value)
			if seen[base] {
				t.Fatal("duplicate base")
			}
			seen[base] = true
			evidence := growattExtendedEvidence(t, source, 1, base|0x31, []byte{129, 2, 3, 4, 5, 6, 7, 8})
			got, ok := p.Apply(evidence)
			if !ok || got.Base != base || got.BCU != 0x31 || got.Evidence != evidence {
				t.Fatalf("report missing or incorrectly addressed: %+v", got)
			}
			f, ok := got.Fields[v.Field]
			if !ok {
				t.Fatal("missing", v.Field)
			}
			if v.Denominator == nil {
				if f.Denominator != 0 || f.Unresolved == "" || !reflect.DeepEqual(f.Raw, []byte{129, 2, 3, 4}) {
					t.Fatal(f)
				}
			} else if f.Numerator != v.Numerator || f.Denominator != *v.Denominator || f.Unit != v.Unit {
				t.Fatal(v.Field, f)
			}
		})
	}
}

func TestGrowattMonitorAddressedDiagnosticRecordBoundary(t *testing.T) {
	source := growattInterface(t, "synthetic", 1)
	p, _ := NewGrowattMonitorAddressed(source)
	for _, tc := range []struct {
		request, response uint32
		length            int
		field             string
	}{
		{0x18810000, 0x18a10000, 64, "hardware_version"},
		{0x18870000, 0x18a70000, 124, "work_mode"},
		{0x188b0000, 0x18ab0000, 24, "soc_version"},
		{0x188e0000, 0x18ae0000, 70, "software_version_r"},
	} {
		for _, id := range []uint32{tc.request | 0x2900, tc.response | 0x29} {
			evidence := growattExtendedEvidence(t, source, 1, id, make([]byte, 8))
			got, ok := p.Apply(evidence)
			if !ok || got.Base != id&0xffff0000 || got.Unresolved == "" || got.BCU != 0x29 || got.HasBMU || got.Evidence != evidence || len(got.Fields) != 0 {
				t.Fatal(got)
			}
		}
		if _, ok := p.Apply(growattExtendedEvidence(t, source, 2, tc.response|0x29, []byte{1, 2, 3})); !ok {
			t.Fatal("short final diagnostic fragment withheld")
		}
		if _, ok := p.Apply(growattExtendedEvidence(t, source, 2, tc.response|0x29, []byte{1})); ok {
			t.Fatal("incomplete diagnostic header admitted")
		}
		data := make([]byte, tc.length)
		data[tc.length-2], data[tc.length-1] = 0x12, 0x34
		if _, ok := p.DecodeRecord(tc.response, data[:len(data)-1]); ok {
			t.Fatal("incomplete record decoded")
		}
		if _, ok := p.DecodeRecord(tc.request, data); ok {
			t.Fatal("request decoded as response")
		}
		got, ok := p.DecodeRecord(tc.response, data)
		if _, present := got[tc.field]; !ok || !present {
			t.Fatal(tc.field, got)
		}
		if tc.response == 0x18ab0000 && (got[tc.field].Label != "V1.2.52" || len(got[tc.field].Raw) != 2) {
			t.Fatal(got[tc.field])
		}
		if tc.response == 0x18a70000 && (got["rtc"].Denominator != 0 || got["rtc"].Unresolved == "" || got["work_mode"].Numerator != 52 || got["work_mode"].Label != "") {
			t.Fatal(got)
		}
		if tc.response == 0x18a10000 {
			if got["product_date"].Unresolved != "invalid calendar date" {
				t.Fatal(got["product_date"])
			}
			data[0], data[1], data[2] = 4, 2, 29
			valid, _ := p.DecodeRecord(tc.response, data)
			if valid["product_date"].Label != "2004-02-29" {
				t.Fatal(valid["product_date"])
			}
		}
	}
}
