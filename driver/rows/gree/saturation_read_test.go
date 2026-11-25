package gree

import (
	"fmt"
	"reflect"
	"testing"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
)

func TestIndoorOverallFaultComesFromTheOverallFlagNotTheProtectionFlag(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	for _, tc := range []struct {
		name, symbol string
		raw          int
	}{
		{"overall fault only", "present", 1},
		{"protection only", "clear", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, overall := imagePoint(t, iface, 0x20, 0x1e, 0, true, tc.raw)
			protection := pointAt(t, img, 1, bitPoint)
			if !overall.Present || !protection.Present || overall.Value.Bits[0] != (tc.raw == 1) || protection.Value.Bits[0] != (tc.raw == 2) {
				t.Fatalf("decoded flags: overall=%+v protection=%+v", overall, protection)
			}
			in := readInput(overall, indoor, terminal)
			want := pointAt(t, img, 0, bitPoint)
			got, err := Evaluate(overall, in)
			cand := got.Evaluation.Candidate
			const field = "thermal.status.terminal_unit_fault"
			if err != nil || got.Missing != "" || got.Source == nil || !reflect.DeepEqual(got.Point, want) || cand == nil || cand.Key.FactID != field || cand.Value.Symbol == nil || !cand.Value.Symbol.Known || cand.Value.Symbol.Token != field+"."+tc.symbol {
				t.Fatalf("overall fault: got %+v (%v), want %s", got, err, tc.symbol)
			}
			assertNativeOnly(t, protection, pointAt(t, img, 1, bitPoint), in, "no established semantic binding", false)
		})
	}
}

type saturationVector struct {
	Start       uint16
	Unit        uint8
	Raw         int
	Celsius     int64
	Field       semreg.DefinitionID
	Coefficient string
}

type saturationVectors struct {
	Temperatures []saturationVector
	Averages     []saturationVector
}

func TestSaturationTemperaturesBindAsKelvinOnTheRefrigerantCircuit(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	for _, v := range vectors[saturationVectors](t, "saturation").Temperatures {
		t.Run(fmt.Sprintf("20:%02x", v.Start), func(t *testing.T) {
			img, p := imagePoint(t, iface, v.Unit, 0x20, v.Start, false, v.Raw)
			want := pointAt(t, img, v.Start, bytePoint)
			if !want.Present || want.Value.Status != reg.GreeVRFValueDecoded || want.Value.Numerator != v.Celsius || want.Value.Denominator != 1 {
				t.Fatalf("decoded temperature: got %+v, want %d °C", want.Value, v.Celsius)
			}
			in := readInput(p, outdoor, circuit)
			got, err := Evaluate(p, in)
			cand := got.Evaluation.Candidate
			if err != nil || got.Missing != "" || got.Source == nil || !reflect.DeepEqual(got.Point, want) || cand == nil {
				t.Fatalf("saturation reading: %+v (%v)", got, err)
			}
			dims := cand.Key.Dimensions
			if cand.Key.FactID != v.Field || len(dims) != 1 || dims[0].ID != "thermal.dimension.refrigerant_circuit" || dims[0].Value.Text == nil || *dims[0].Value.Text != in.Dimension || got.Source.For.Subject != in.Subject {
				t.Fatalf("circuit binding: %+v, source %+v", cand.Key, got.Source)
			}
			q := semreg.Quantity{Number: semreg.Decimal{Coefficient: v.Coefficient, Exponent10: -2}, Unit: "unit.kelvin"}
			if !reflect.DeepEqual(cand.Value.Quantity, &q) {
				t.Fatalf("quantity: got %+v, want %+v", cand.Value.Quantity, q)
			}

			t.Run("absent reading", func(t *testing.T) {
				empty := reg.NewGreeVRFRegisterImage(p.Key)
				absent := pointAt(t, empty, v.Start, bytePoint)
				assertNativeOnly(t, absent, pointAt(t, empty, v.Start, bytePoint), in, "native point absent", true)
			})
			switch v.Start {
			case 0x51, 0x52:
				t.Run("non-master system source", func(t *testing.T) {
					other, reading := imagePoint(t, iface, 0x09, 0x20, v.Start, false, v.Raw)
					assertNativeOnly(t, reading, pointAt(t, other, v.Start, bytePoint), readInput(reading, outdoor, circuit), "master outdoor system source", false)
				})
			case 0x69, 0x6a:
				t.Run("module without circuit membership", func(t *testing.T) {
					unassociated := in
					unassociated.CircuitBasis = nil
					assertNativeOnly(t, p, want, unassociated, "module-to-refrigerant-circuit association", false)
				})
			}
		})
	}
}

func TestAverageHighAndLowReadingsStayNative(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	for _, v := range vectors[saturationVectors](t, "saturation").Averages {
		t.Run(fmt.Sprintf("20:%02x", v.Start), func(t *testing.T) {
			img, p := imagePoint(t, iface, v.Unit, 0x20, v.Start, false, v.Raw)
			want := pointAt(t, img, v.Start, bytePoint)
			if !want.Present || want.Value.Status != reg.GreeVRFValueDecoded || want.Value.Numerator != v.Celsius || want.Value.Denominator != 1 {
				t.Fatalf("decoded average: got %+v, want %d °C", want.Value, v.Celsius)
			}
			assertNativeOnly(t, p, want, readInput(p, outdoor, circuit), "no established semantic binding", false)
		})
	}
}

func assertNativeOnly(t *testing.T, p, want reg.GreeVRFTypedPoint, in ReadInput, missing string, declared bool) {
	t.Helper()
	got, err := Evaluate(p, in)
	if err != nil || got.Missing != missing || got.Evaluation.Candidate != nil || (got.Source != nil) != declared || !reflect.DeepEqual(got.Point, want) {
		t.Fatalf("native-only reading: got %+v (%v), want missing %q and declared=%t", got, err, missing, declared)
	}
}
