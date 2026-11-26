package gree

import (
	"bytes"
	"testing"
	"time"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
)

func TestCompressorFrequencyStaysNativeAndNamesTheMissingShaftRateStatement(t *testing.T) {
	const missing = "compressor: statement that operating frequency in Hz is shaft rotation rate"
	for _, tc := range []unboundReadVector{
		{0x28, 0x89, []byte{47}, "Hz", 47, 1, missing},
		{0x28, 0xb3, []byte{58}, "Hz", 58, 1, missing},
		{0x28, 0xc1, []byte{69}, "Hz", 69, 1, missing},
	} {
		checkUnboundRead(t, tc)
	}
}

func TestElectricalPowerAndEnergyStayNativeAndNameTheMissingElement(t *testing.T) {
	const power = "heat generator module: electrical power element in the pinned catalog"
	const energy = "heat generator module: electrical energy element in the pinned catalog"
	for _, tc := range []unboundReadVector{
		{0x14, 0x04, []byte{0x34, 0x12}, "W", 0x1234, 1, power},
		{0x14, 0x06, []byte{0x45, 0x23}, "kWh", 0x2345, 10, energy},
		{0x14, 0x08, []byte{0x56, 0x34}, "kWh", 0x3456, 1, energy},
		{0x14, 0x0a, []byte{0x67, 0x45}, "kWh", 0x4567, 1, energy},
		{0x14, 0x0c, []byte{0x78, 0x56, 0x34, 0x12}, "kWh", 0x12345678, 1, energy},
		{0x14, 0x10, []byte{0x89, 0x67, 0x45, 0x23}, "kWh", 0x23456789, 1, energy},
		{0x14, 0x14, []byte{0x9a, 0x78}, "kW", 0x789a, 100, power},
	} {
		checkUnboundRead(t, tc)
	}
}

type unboundReadVector struct {
	typ, start uint8
	raw        []byte
	unit       string
	num, den   int64
	missing    string
}

func checkUnboundRead(t *testing.T, tc unboundReadVector) {
	t.Helper()
	c := Coordinate{tc.typ, uint16(tc.start), bytePoint}
	t.Run(c.String(), func(t *testing.T) {
		key := reg.GreeVRFRegisterKey{Interface: bus(t, "synthetic", 1), Family: reg.GreeVRFData, Unit: 0x08, Type: tc.typ}
		img := reg.NewGreeVRFRegisterImage(key)
		if !img.Apply(fragment(t, key, append([]byte{tc.start}, tc.raw...), time.Second), false) {
			t.Fatal("complete value frame rejected")
		}
		p := pointAt(t, img, c.Start, c.Kind)
		if !p.Present || p.Row.Unit != tc.unit || p.Value.Status != reg.GreeVRFValueDecoded || p.Value.Numerator != tc.num || p.Value.Denominator != tc.den || !bytes.Equal(p.Value.Raw, tc.raw) {
			t.Fatalf("decoded native point: got %+v, want %d/%d %s from %x", p, tc.num, tc.den, tc.unit, tc.raw)
		}
		assertNativeOnly(t, p, pointAt(t, img, c.Start, c.Kind), readInput(p, outdoor, circuit), tc.missing, false)
	})
}

func TestUnlistedPointsKeepTheGenericReason(t *testing.T) {
	const generic = "no established semantic binding"
	iface := bus(t, "synthetic", 1)
	for _, c := range []Coordinate{{0x20, 0x04, bytePoint}, {0x14, 0x00, bytePoint}} {
		img, p := imagePoint(t, iface, 0x08, c.Type, c.Start, false, 125)
		assertNativeOnly(t, p, pointAt(t, img, c.Start, c.Kind), readInput(p, outdoor, circuit), generic, false)
	}

	// An otherwise identical synthetic point of a different kind must not match.
	_, p := imagePoint(t, iface, 0x08, 0x14, 0x04, false, 125)
	p.Row.Kind = bitPoint
	assertNativeOnly(t, p, p, readInput(p, outdoor, circuit), generic, false)
}

func TestWaterAndValvePointsKeepTheirReasons(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	for _, tc := range []struct {
		typ     uint8
		start   uint16
		missing string
	}{
		{0x20, 0x0e, "exact water sensor to DHW subject association"},
		{0x78, 0x59, "terminal-unit valve subject and ratio scale"},
	} {
		img, p := imagePoint(t, iface, 0x20, tc.typ, tc.start, false, 125)
		assertNativeOnly(t, p, pointAt(t, img, tc.start, bytePoint), readInput(p, indoor, zone), tc.missing, false)
	}
}
