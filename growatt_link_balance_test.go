package canbusreg

import (
	"fmt"
	"testing"
	"time"
)

func TestCX12BalanceCountValidityAndEveryMaskBit(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	for bit := 0; bit < 16; bit++ {
		t.Run(fmt.Sprintf("bit-%d", bit), func(t *testing.T) {
			p := cx12Words(1<<bit, 0, 0, 0)
			r := CX12DecodeLINKBalance(s, cx01Input(t, spec, 0x1801, p), 16, true, cx12Gates()[0], true, 3*time.Second)
			for slot := 1; slot <= 16; slot++ {
				want := int64(0)
				if slot == bit+1 {
					want = 1
				}
				f := r.Fields[fmt.Sprintf("enable_%d", slot)]
				if f.Numerator != want || f.Age != 3*time.Second || f.Status != "accepted" || f.Grade != "Hypothesis" || f.PhysicalQualified {
					t.Fatal(f)
				}
			}
		})
	}
	for _, count := range []int{0, 1, 16, 17} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			p := cx12Words(65535, 0, 0, 0)
			r := CX12DecodeLINKBalance(s, cx01Input(t, spec, 0x1801, p), count, true, cx12Gates()[0], true, 0)
			want := 1
			if count >= 1 && count <= 16 {
				want += count
			}
			if r.Raw != p || len(r.Fields) != want {
				t.Fatal(r)
			}
		})
	}
	for _, bad := range []string{"count-invalid", "measurement-invalid", "age-unknown", "age-negative", "age-stale", "direction", "scope", "source", "epoch"} {
		t.Run(bad, func(t *testing.T) {
			p := cx12Words(5, 0, 0, 0)
			i := cx01Input(t, spec, 0x1801, p)
			g := cx12Gates()[0]
			countValid, known, age := true, true, time.Duration(0)
			switch bad {
			case "count-invalid":
				countValid = false
			case "measurement-invalid":
				g.Valid = false
			case "age-unknown":
				known = false
			case "age-negative":
				age = -1
			case "age-stale":
				g.Valid = false
				g.Status = "stale"
			case "direction":
				i.Direction = "coordinator-to-module"
			case "scope":
				i.Scope = "aggregate"
			case "source":
				other := spec
				other.Source++
				i = cx01Input(t, other, 0x1801, p)
			case "epoch":
				i.Epoch++
			}
			r := CX12DecodeLINKBalance(s, i, 3, countValid, g, known, age)
			if r.Raw != p || r.Fields["enable_1"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12BalanceSelectorAndHardwareAge(t *testing.T) {
	for _, id := range []struct {
		code, release string
		valid         bool
	}{{"QAAA", "0005", true}, {"QAAA", "0006", true}, {"QAAB", "0002", true}, {"QAAB", "0003", true}, {"QAAB", "0004", true}, {"QAAB", "0006", true}, {"QAAA", "0007", false}, {"QAAA", "0011", false}, {"QAAa", "0006", false}} {
		t.Run(id.code+id.release, func(t *testing.T) {
			spec := cx01Spec(t, id.code, id.release)
			p := cx12Words(5, 0, 0, 0)
			for _, known := range []bool{false, true} {
				r := CX12DecodeLINKBalance(cx01Select(t, spec), cx01Input(t, spec, 0x1801, p), 3, true, cx12Gates()[0], known, time.Second)
				if r.Raw != p || r.Fields["mask"].Numerator != 5 || (r.Fields["enable_1"].Status == "accepted") != (id.valid && known) {
					t.Fatal(r)
				}
				if id.valid && known {
					if r.Fields["enable_2"].Numerator != 0 || r.Fields["enable_3"].Numerator != 1 || r.Fields["enable_1"].Age != time.Second || r.Fields["enable_1"].Meaning != "retained register enable state" {
						t.Fatal(r)
					}
				}
				if _, ok := r.Fields["current"]; ok {
					t.Fatal("invented current")
				}
				if _, ok := r.Fields["desired_mask"]; ok {
					t.Fatal("invented request")
				}
			}
		})
	}
}
