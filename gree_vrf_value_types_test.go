package canbusreg

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGreeVRFValueTypeVectors(t *testing.T) {
	var fixture struct {
		Rules []struct {
			Names   []string
			Bytes   int
			Vectors []struct {
				Bytes       string
				Result      json.RawMessage
				DefinedBits []uint8 `json:"defined_bits"`
			}
		}
		WithoutRule []struct {
			Name  string
			Bytes int
		} `json:"without_rule"`
	}
	data, err := os.ReadFile("testdata/gree_vrf_value_types.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, rule := range fixture.Rules {
		for _, name := range rule.Names {
			for _, v := range rule.Vectors {
				raw, err := hex.DecodeString(strings.ReplaceAll(v.Bytes, " ", ""))
				if err != nil {
					t.Fatal(err)
				}
				got := DecodeGreeVRFValue(name, raw, v.DefinedBits...)
				if !reflect.DeepEqual(got.Raw, raw) {
					t.Fatalf("%s: lost raw bytes: %v", name, got)
				}
				checkGreeVRFValue(t, name, v.Result, got)
				if short := DecodeGreeVRFValue(name, raw[:rule.Bytes-1]); short.Status != GreeVRFValueIncomplete {
					t.Errorf("%s: short input: %+v", name, short)
				}
			}
		}
	}
	for _, row := range fixture.WithoutRule {
		raw := []byte{0x19}
		got := DecodeGreeVRFValue(row.Name, raw)
		if got.Status != GreeVRFValueRaw || !reflect.DeepEqual(got.Raw, raw) || got.Denominator != 0 {
			t.Errorf("%s: %+v", row.Name, got)
		}
		if got := DecodeGreeVRFValue(row.Name, raw[:row.Bytes-1]); got.Status != GreeVRFValueIncomplete {
			t.Errorf("%s: short input: %+v", row.Name, got)
		}
	}
}

func checkGreeVRFValue(t *testing.T, name string, want json.RawMessage, got GreeVRFValue) {
	t.Helper()
	switch name {
	case "bits":
		var bits map[uint8]bool
		if err := json.Unmarshal(want, &bits); err != nil {
			t.Fatal(err)
		}
		if got.Status != GreeVRFValueDecoded || !reflect.DeepEqual(got.Bits, bits) {
			t.Errorf("%s: %+v != %v", name, got, bits)
		}
	case "ubytes6":
		if got.Status != GreeVRFValueRaw || got.Denominator != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	default:
		var decimal string
		if err := json.Unmarshal(want, &decimal); err != nil {
			t.Fatal(err)
		}
		exact, ok := new(big.Rat).SetString(decimal)
		if !ok || got.Status != GreeVRFValueDecoded || got.Denominator == 0 || exact.Cmp(big.NewRat(got.Numerator, got.Denominator)) != 0 {
			t.Errorf("%s: %+v != %s", name, got, decimal)
		}
	}
}
