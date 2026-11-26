package gree

import (
	"testing"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
)

func TestEverySettingMethodNamesItsOwnDataFamilyCoordinateAsReadback(t *testing.T) {
	want := vectors[map[string]*ReadbackCoordinate](t, "method_readback")
	for _, row := range CommandRows() {
		expected, ok := want[row.Name]
		if !ok {
			t.Errorf("no readback vector for %s", row.Name)
			continue
		}
		delete(want, row.Name)
		if expected == nil {
			if row.Setting != nil || row.Readback != nil {
				t.Errorf("%s: unexpected setting or readback", row.Name)
			}
			continue
		}
		if row.Setting == nil || *row.Setting != expected.Coordinate {
			t.Errorf("%s: setting = %#v, want %#v", row.Name, row.Setting, expected.Coordinate)
		}
		if row.Readback == nil || *row.Readback != *expected {
			t.Errorf("%s: readback = %#v, want %#v", row.Name, row.Readback, expected)
		}
	}
	for name := range want {
		t.Errorf("missing method %s", name)
	}
}

func TestPropertyMethodsStateNoReadback(t *testing.T) {
	found := false
	for _, row := range CommandRows() {
		if row.Property != nil {
			found = true
			if row.Readback != nil || row.SynchronousReadback {
				t.Errorf("%s: property must not claim passive readback", row.Name)
			}
		}
	}
	if !found {
		t.Fatal("no property methods")
	}
}

func TestTerminalModeNamesBothTheModeSettingAndThePowerSetting(t *testing.T) {
	want := map[semreg.DefinitionID][]Coordinate{
		"thermal.mode.terminal_unit": {
			{0x71, 0, reg.GreeVRFDictionaryByte}, // cool, dry, fan_only, heat
			{0x6f, 0, reg.GreeVRFDictionaryBit},  // off
		},
		"thermal.setpoint.temperature_setpoint": {{0x73, 0, reg.GreeVRFDictionaryByte}},
	}
	settings := make(map[string]Coordinate)
	for _, row := range CommandRows() {
		if row.Setting != nil {
			settings[row.Name] = *row.Setting
		}
	}
	for _, row := range ReadRows() {
		expected, ok := want[row.Field]
		if !ok {
			continue
		}
		delete(want, row.Field)
		if len(row.WayBack) != len(expected) {
			t.Errorf("%s: way back = %v, want %v", row.Field, row.WayBack, expected)
			continue
		}
		for i, name := range row.WayBack {
			if setting, ok := settings[name]; !ok || setting != expected[i] {
				t.Errorf("%s: way back %q resolves to %+v, want %+v", row.Field, name, setting, expected[i])
			}
		}
	}
	for field := range want {
		t.Errorf("missing binding for %s", field)
	}
}

func TestReadBindingsWithoutACommandStateTheyHaveNone(t *testing.T) {
	for _, row := range ReadRows() {
		switch row.Field {
		case "thermal.mode.terminal_unit", "thermal.setpoint.temperature_setpoint":
		default:
			if len(row.WayBack) != 0 {
				t.Errorf("%s at %s: unexpected way back %v", row.Field, row.Point, row.WayBack)
			}
		}
	}
}
