package canbusreg

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// TestGrowattEnergyCoverage locks GrowattEnergyCoverage to the fields the
// common projection and the V1.04 projection set from the existing synthetic
// frames. Each quantity is its own case.
//
// voltage fails if VoltageCentivolts is dropped, its container is nil, or the name is not supported.
// current fails if CurrentDeciamps is dropped, its container is nil, or the name is not supported.
// soc fails if SOCPercent is dropped, its container is nil, or the name is not supported.
// soh_value fails if SOHValue is dropped, its container is nil, or the name is not supported.
// charge_voltage_limit fails if ChargeVoltageDecivolts is dropped, its container is nil, or the name is not supported.
// charge_current_limit fails if ChargeCurrentDeciamps is dropped, its container is nil, or the name is not supported.
// discharge_current_limit fails if DischargeCurrentDeciamps is dropped, its container is nil, or the name is not supported.
// soh_valid fails if SOHValid appears on the common projection, disappears from the V1.04 projection, stays unset, or the two results swap support.
// maximum_cell_temperature fails if MaximumCellTemperatureDeciC appears on the common projection, disappears from the V1.04 projection, stays unset, or the two results swap support.
// remaining_energy fails if RemainingEnergy is added or the name is marked supported.
// usable_capacity fails if UsableCapacity is added or the name is marked supported.
// nominal_capacity fails if NominalCapacity is added or the name is marked supported.
// charge_capacity_ampere_hours fails if ChargeCapacityAmpereHours is added or the name is marked supported.
func TestGrowattEnergyCoverage(t *testing.T) {
	got := GrowattEnergyCoverage()
	want := energyCoverageWant()
	if len(got) != len(want) {
		t.Fatalf("results = %#v", got)
	}
	limits, measures := energyCommonProjections(t)
	revision := energyRevisionProjection(t)
	samples := map[string][]any{
		"common": {limits, measures},
		"v104":   {revision},
	}
	for result, wantStatus := range want {
		gotStatus, ok := got[result]
		if !ok || len(gotStatus) != len(wantStatus) {
			t.Fatalf("%s coverage = %#v", result, gotStatus)
		}
		for name := range gotStatus {
			if _, known := wantStatus[name]; !known {
				t.Fatalf("%s extra name %s", result, name)
			}
		}
		observed := samples[result]
		for name, status := range wantStatus {
			t.Run(result+"/"+name, func(t *testing.T) {
				if gotStatus[name] != status {
					t.Fatalf("status = %q, want %q", gotStatus[name], status)
				}
				field := energyGoField(name)
				switch status {
				case "supported":
					found, set, value := energySet(observed, field)
					expect, ok := energySynthetic(name)
					if !found || !set || !ok || !reflect.DeepEqual(value, expect) {
						t.Fatalf("supported %s field is nil or unset: found=%t set=%t value=%#v", name, found, set, value)
					}
				case "unsupported":
					if energyTypeHas(reflect.TypeOf(observed[0]), field, name) {
						t.Fatalf("unsupported %s is a struct field", name)
					}
				default:
					t.Fatalf("status %q", status)
				}
			})
		}
	}
}

func energyCoverageWant() map[string]map[string]string {
	shared := []string{
		"voltage", "current", "soc", "soh_value",
		"charge_voltage_limit", "charge_current_limit", "discharge_current_limit",
		"remaining_energy", "usable_capacity", "nominal_capacity", "charge_capacity_ampere_hours",
	}
	supported := map[string]bool{
		"voltage": true, "current": true, "soc": true, "soh_value": true,
		"charge_voltage_limit": true, "charge_current_limit": true, "discharge_current_limit": true,
	}
	common := map[string]string{}
	revision := map[string]string{}
	for _, name := range shared {
		state := "unsupported"
		if supported[name] {
			state = "supported"
		}
		common[name] = state
		revision[name] = state
	}
	common["soh_valid"] = "unsupported"
	common["maximum_cell_temperature"] = "unsupported"
	revision["soh_valid"] = "supported"
	revision["maximum_cell_temperature"] = "supported"
	return map[string]map[string]string{"common": common, "v104": revision}
}

func energyCommonProjections(t *testing.T) (GrowattLowVoltageBMSCommonProjection, GrowattLowVoltageBMSCommonProjection) {
	t.Helper()
	source := growattInterface(t, "can0", 1)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}
	limits, ok := projector.Apply(growattCommonEvidence(t, source, 1, 0x311, growattLimitsPayload()))
	if !ok || limits.Limits == nil || limits.Measurements != nil {
		t.Fatal("limits frame was not projected")
	}
	measures, ok := projector.Apply(growattCommonEvidence(t, source, 2, 0x313, growattMeasurementPayload()))
	if !ok || measures.Measurements == nil || measures.Limits != nil {
		t.Fatal("measurement frame was not projected")
	}
	return limits, measures
}

func energyRevisionProjection(t *testing.T) GrowattLowVoltageBMSV104Projection {
	t.Helper()
	source := growattInterface(t, "can0", 1)
	profile := GrowattLowVoltageBMSV104()
	profile.Classify(growattEvidence(t, source, 1, 0x311, growattLimitsPayload()))
	profile.Classify(growattEvidence(t, source, 2, 0x312, growattStatusPayload()))
	got := profile.Classify(growattEvidence(t, source, 3, 0x313, growattMeasurementPayload()))
	projection, ok := got.Projection.(GrowattLowVoltageBMSV104Projection)
	if !ok || got.Profile == "" {
		t.Fatalf("projection type %T profile %q", got.Projection, got.Profile)
	}
	return projection
}

func energyGoField(name string) string {
	switch name {
	case "voltage":
		return "VoltageCentivolts"
	case "current":
		return "CurrentDeciamps"
	case "soc":
		return "SOCPercent"
	case "soh_value":
		return "SOHValue"
	case "charge_voltage_limit":
		return "ChargeVoltageDecivolts"
	case "charge_current_limit":
		return "ChargeCurrentDeciamps"
	case "discharge_current_limit":
		return "DischargeCurrentDeciamps"
	case "soh_valid":
		return "SOHValid"
	case "maximum_cell_temperature":
		return "MaximumCellTemperatureDeciC"
	case "remaining_energy":
		return "RemainingEnergy"
	case "usable_capacity":
		return "UsableCapacity"
	case "nominal_capacity":
		return "NominalCapacity"
	case "charge_capacity_ampere_hours":
		return "ChargeCapacityAmpereHours"
	default:
		return ""
	}
}

func energySynthetic(name string) (any, bool) {
	switch name {
	case "voltage":
		return int16(0x1400), true
	case "current":
		return int16(-10), true
	case "soc":
		return uint8(80), true
	case "soh_value":
		return uint8(127), true
	case "charge_voltage_limit":
		return uint16(0x0238), true
	case "charge_current_limit":
		return uint16(250), true
	case "discharge_current_limit":
		return uint16(500), true
	case "soh_valid":
		return true, true
	case "maximum_cell_temperature":
		return int16(300), true
	default:
		return nil, false
	}
}

func energySet(samples []any, field string) (found, set bool, value any) {
	for _, sample := range samples {
		oneFound, oneSet, oneValue := energyField(sample, field)
		if oneSet {
			return true, true, oneValue
		}
		if oneFound {
			found = true
		}
	}
	return found, false, nil
}

func energyField(sample any, field string) (found, set bool, value any) {
	if sample == nil {
		return false, false, nil
	}
	return energyWalk(reflect.ValueOf(sample), field)
}

func energyWalk(v reflect.Value, field string) (found, set bool, value any) {
	if !v.IsValid() {
		return false, false, nil
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false, false, nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || !energyOwnType(v.Type()) {
		return false, false, nil
	}
	for i := 0; i < v.NumField(); i++ {
		structField := v.Type().Field(i)
		member := v.Field(i)
		if structField.Name == field {
			if (member.Kind() == reflect.Pointer || member.Kind() == reflect.Interface) && member.IsNil() {
				return true, false, nil
			}
			if !member.CanInterface() {
				return true, false, nil
			}
			return true, true, member.Interface()
		}
		memberType := structField.Type
		for memberType.Kind() == reflect.Pointer {
			memberType = memberType.Elem()
		}
		if memberType.Kind() != reflect.Struct {
			continue
		}
		if member.Kind() == reflect.Pointer && member.IsNil() {
			if energyTypeHasName(structField.Type, field) {
				return true, false, nil
			}
			continue
		}
		if nestedFound, nestedSet, nestedValue := energyWalk(member, field); nestedFound {
			return nestedFound, nestedSet, nestedValue
		}
	}
	return false, false, nil
}

func energyTypeHas(root reflect.Type, field, quantity string) bool {
	if energyTypeHasName(root, field) {
		return true
	}
	return energyTypeSnake(root, quantity)
}

func energyTypeHasName(root reflect.Type, field string) bool {
	return energyTypeMatch(root, func(name string) bool { return name == field })
}

func energyTypeSnake(root reflect.Type, quantity string) bool {
	return energyTypeMatch(root, func(name string) bool { return energySnake(name) == quantity })
}

func energyTypeMatch(root reflect.Type, match func(string) bool) bool {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type) bool
	walk = func(current reflect.Type) bool {
		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct || seen[current] || !energyOwnType(current) {
			return false
		}
		seen[current] = true
		for i := 0; i < current.NumField(); i++ {
			structField := current.Field(i)
			if match(structField.Name) || walk(structField.Type) {
				return true
			}
		}
		return false
	}
	return walk(root)
}

func energyOwnType(current reflect.Type) bool {
	path := current.PkgPath()
	return path == "" || path == reflect.TypeOf(GrowattLowVoltageBMSCommonProjection{}).PkgPath()
}

func energySnake(name string) string {
	var builder strings.Builder
	for index, char := range name {
		if index > 0 && char >= 'A' && char <= 'Z' {
			previous := rune(name[index-1])
			nextLower := index+1 < len(name) && name[index+1] >= 'a' && name[index+1] <= 'z'
			if previous >= 'a' && previous <= 'z' || previous >= 'A' && previous <= 'Z' && nextLower {
				builder.WriteByte('_')
			}
		}
		builder.WriteRune(unicode.ToLower(char))
	}
	return builder.String()
}
