package canbusreg

import (
	"math/big"
	"testing"
)

func TestGreeVRFPresentationConverters(t *testing.T) {
	for _, row := range []struct {
		name         string
		inNum, inDen int64
		options      GreeVRFPresentationOptions
		want, unit   string
	}{
		{"CeVsFa", 45, 2, GreeVRFPresentationOptions{Active: true}, "73", "°F"},
		{"CeVsFa", -45, 2, GreeVRFPresentationOptions{}, "-23", ""},
		{"CeVsFa", 73, 1, GreeVRFPresentationOptions{Active: true, Inverse: true}, "22", ""},
		{"kWVskBtuh", 5, 2, GreeVRFPresentationOptions{Active: true}, "8.53", "kBtu/h"},
		{"kWVskBtuh", 5, 4, GreeVRFPresentationOptions{Active: true}, "4.26", "kBtu/h"},
		{"kWVskBtuh", 853, 100, GreeVRFPresentationOptions{Active: true, Inverse: true}, "2", ""},
		{"kPaVsinWG", 25, 1, GreeVRFPresentationOptions{Active: true}, "100", "inWG"},
		{"kPaVsinWG", 101, 1, GreeVRFPresentationOptions{Active: true, Inverse: true}, "25.25", ""},
		{"kPaVsinWG", 203, 2, GreeVRFPresentationOptions{Active: true, Inverse: true, IntegerInverse: true}, "101", ""},
		{"Blur", -1279, 10, GreeVRFPresentationOptions{Masked: true}, "-120", ""},
		{"Blur", 1279, 10, GreeVRFPresentationOptions{Masked: true}, "120", ""},
		{"Blur", 1279, 10, GreeVRFPresentationOptions{Active: true}, "127.9", ""},
		{"CeVskPa", -70, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R410A"}, "36", "kPa"},
		{"CeVskPa", 71, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R410A"}, "4893", "kPa"},
		{"CeVskPa", 72, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R410A"}, "", ""},
		{"CeVskPa", -56, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R22"}, "45.86", "kPa"},
		{"CeVskPa", 70, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R22"}, "2995.9", "kPa"},
		{"CeVskPa", 71, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "R22"}, "", ""},
		{"CeVskPa", -56, 1, GreeVRFPresentationOptions{Active: true, Pressure: true, Refrigerant: "R22"}, "183.44", "inWG"},
		{"CeVskPa", 249, 10, GreeVRFPresentationOptions{Active: true}, "75.2", "°F"},
		{"CeVskPa", 0, 1, GreeVRFPresentationOptions{Pressure: true, Refrigerant: "unknown"}, "", ""},
	} {
		in := GreeVRFValue{Status: GreeVRFValueDecoded, Numerator: row.inNum, Denominator: row.inDen}
		var got GreeVRFPresentationValue
		got, ok := ConvertGreeVRFPresentation(row.name, in, row.options)
		want, hasWant := new(big.Rat).SetString(row.want)
		if ok != hasWant || ok && (got.Value.Cmp(want) != 0 || got.Unit != row.unit) {
			t.Errorf("%+v: got %+v, %v", row, got, ok)
		}
		if in.Numerator != row.inNum || in.Denominator != row.inDen {
			t.Fatal("mutated native value")
		}
	}
}
