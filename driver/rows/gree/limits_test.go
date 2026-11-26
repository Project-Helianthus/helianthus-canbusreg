package gree

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

func TestSetTemperatureDeclaresNoBoundsFromWireWidth(t *testing.T) {
	write := temperatureOperation().Write
	if write == nil || write.NativeBounds.Min != nil || write.NativeBounds.Max != nil || write.NativeBounds.Step != nil {
		t.Fatalf("temperature write must declare no native bounds: %+v", write)
	}
	target := temperatureFixture(t)
	for _, test := range []struct {
		value  string
		lo, hi byte
	}{
		{"27315", 0x00, 0x00},
		{"682665", 0xff, 0xff},
	} {
		arg := temperatureKelvin(test.value, -2)
		got, err := EncodeTemperatureWithLimitsState(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, arg, nil)
		if err != nil || got.LimitsState != TemperatureLimitsUnavailable || got.Frame.Identifier != 0x1cbfd073 || !bytes.Equal(got.Frame.Data, []byte{0x00, 0x03, test.lo, test.hi}) {
			t.Fatalf("%s hundredths of kelvin: got %+v, %v; want frame and unavailable limits", test.value, got, err)
		}
	}
}

func TestSuppliedLimitsStillBoundTheArgumentAndRounding(t *testing.T) {
	target := temperatureFixture(t)
	min, max := temperatureKelvin("28", 1), temperatureKelvin("303", 0)
	limits := &driver.ValueLimits{Min: &min, Max: &max, Origin: driver.LimitDeviceQuery, Evidence: evidence([]byte{8})}
	for _, value := range []string{"27995", "30305"} {
		arg := temperatureKelvin(value, -2)
		got, err := EncodeTemperatureWithLimitsState(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, arg, limits)
		if err == nil || got.LimitsState != TemperatureLimitsSupplied || !strings.Contains(err.Error(), "published limit") || got.Frame.Identifier != 0 || len(got.Frame.Data) != 0 {
			t.Fatalf("%s hundredths of kelvin: got %+v, %v; want limit refusal and no frame", value, got, err)
		}
	}
	got, err := EncodeTemperatureWithLimitsState(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, temperatureKelvin("29615", -2), limits)
	if err != nil || got.LimitsState != TemperatureLimitsSupplied || got.Frame.Identifier != 0x1cbfd073 || !bytes.Equal(got.Frame.Data, []byte{0x00, 0x03, 0xe6, 0x00}) {
		t.Fatalf("296.15 K: got %+v, %v; want raw 230 in the addressed write", got, err)
	}
}

func TestUnrepresentableTemperatureIsRefusedAsAnEncodingError(t *testing.T) {
	target := temperatureFixture(t)
	for _, test := range []struct {
		name  string
		value string
	}{
		{"negative native value", "27305"},
		{"above uint16 capacity", "682675"},
		{"fractional native value", "29616"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arg := temperatureKelvin(test.value, -2)
			got, err := EncodeTemperatureWithLimitsState(target.Zone, target.Equipment.Anchor, []SettingTarget{target}, arg, nil)
			if err == nil || got.LimitsState != TemperatureLimitsUnavailable || !strings.HasPrefix(err.Error(), "encode_error:") || strings.Contains(err.Error(), "limit") || got.Frame.Identifier != 0 || len(got.Frame.Data) != 0 {
				t.Fatalf("got %+v, %v; want encoding refusal and no frame", got, err)
			}
		})
	}
}

func temperatureFixture(t *testing.T) SettingTarget {
	t.Helper()
	_, p := imagePoint(t, bus(t, "synthetic", 1), 0x20, 0x73, 0, false, 230)
	in := readInput(p, indoor, zone)
	return SettingTarget{Equipment: in.Equipment, Zone: in.Subject, ZoneBasis: in.ZoneBasis}
}

func temperatureKelvin(coefficient string, exponent int32) semreg.Quantity {
	return semreg.Quantity{Number: semreg.Decimal{Coefficient: coefficient, Exponent10: exponent}, Unit: kelvin}
}
