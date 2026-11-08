package canbusreg

import (
	"encoding/binary"
	"fmt"
	"github.com/Project-Helianthus/helianthus-canbus"
	"sync"
	"testing"
	"time"
)

func cx12OtherInterface(t *testing.T, name string, index int) canbus.InterfaceIdentity {
	t.Helper()
	i, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestCX12VectorSpanUsesMeasurementAge(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	a := CX12NewLINKCells(cx01Select(t, spec), 4, true)
	g := cx12Gates()
	for j := range g {
		g[j].Age = time.Duration(j) * time.Second
	}
	a.CX12Decode(cx01Input(t, spec, 0x1810, cx12Words(3300, 3300, 3300, 3300)), g)
	v := a.CX12Snapshot(time.Second, 3*time.Second)
	if v.Span != 3*time.Second || v.ReceivedSpan != 0 || !v.CompleteCoverage {
		t.Fatal(v)
	}
	v = a.CX12Snapshot(time.Second+1, 3*time.Second)
	if v.CompleteCoverage || v.Fields[4].Status != "stale" || v.LastGood[4].Age != 3*time.Second+1 {
		t.Fatal(v)
	}
}

func TestCX12CellBlockMappingAndEvidence(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	spec.Group = "group-a"
	spec.Slot = "slot-a"
	s := cx01Select(t, spec)
	a := CX12NewLINKCells(s, 16, true)
	for b := 0; b < 4; b++ {
		p := cx12Words(uint16(3300+b*4), uint16(3301+b*4), uint16(3302+b*4), uint16(3303+b*4))
		i := cx01Input(t, spec, uint32(0x1810+b), p)
		i.Evidence.Sequence = uint64(b + 1)
		i.Evidence.RawRecord[0] = byte(b + 1)
		g := cx12Gates()
		g[0].Age = time.Second
		r := a.CX12Decode(i, g)
		for j := 0; j < 4; j++ {
			f := r.Fields[fmt.Sprintf("cell_%d", b*4+j+1)]
			if f.Numerator != int64(3300+b*4+j) || f.Denominator != 1 || f.Unit != "mV" || f.Grade != "Proven" || f.Raw != p || f.Evidence != i.Evidence || f.Epoch != spec.Epoch || f.Scope != spec.Scope || f.Bank != spec.Bank || f.Endpoint != spec.Endpoint || f.Group != spec.Group || f.Slot != spec.Slot || f.PhysicalQualified {
				t.Fatal(f)
			}
		}
	}
}

func TestCX12CellMeanStaleEachContributorAndLegacyNegative(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	s := cx01Select(t, spec)
	a := CX12NewLINKCells(s, 16, true)
	for b := 0; b < 4; b++ {
		i := cx01Input(t, spec, uint32(0x1810+b), cx12Words(3300, 3300, 3300, 3300))
		i.Evidence.Sequence = uint64(b + 1)
		a.CX12Decode(i, cx12Gates())
	}
	raw := cx12Words(0, 0, 0, 3300)
	i := cx01Input(t, spec, 0x1806, raw)
	i.Evidence.Sequence = 5
	for bad := 1; bad <= 16; bad++ {
		t.Run(fmt.Sprintf("stale-slot-%d", bad), func(t *testing.T) {
			v := a.CX12Snapshot(time.Second, time.Second)
			f := v.Fields[bad]
			f.Age = time.Second + 1
			v.Fields[bad] = f
			r := CX12DecodeLINKCellMean(s, i, cx12Gates()[0], v, false)
			if r.Fields["mean"].Status == "accepted" || r.Raw != raw {
				t.Fatal(r)
			}
		})
	}
	legacy := spec
	legacy.Release = "0010"
	ls := cx01Select(t, legacy)
	v := a.CX12Snapshot(time.Second, time.Second)
	v.Selection = ls
	r := CX12DecodeLINKCellMean(ls, cx01Input(t, legacy, 0x1806, raw), cx12Gates()[0], v, false)
	if r.Fields["mean"].Status == "accepted" || r.Raw != raw {
		t.Fatal(r)
	}
}

func TestCX12AdmissionEveryAssociationField(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	spec.Group = "group-a"
	spec.Slot = "slot-a"
	p := cx12Words(3300, 3301, 3302, 3303)
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc", "raw-dlc", "sequence", "monotonic", "field-age"} {
		t.Run(key, func(t *testing.T) {
			i := cx01Input(t, spec, 0x1810, p)
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "coordinator-to-module"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "bank-b"
			case "endpoint":
				i.Endpoint = "endpoint-b"
			case "group":
				i.Group = "group-b"
			case "slot":
				i.Slot = "slot-b"
			case "source", "destination":
				other := spec
				if key == "source" {
					other.Source++
				} else {
					other.Destination++
				}
				i = cx01Input(t, other, 0x1810, p)
			case "ide":
				id, _ := canbus.NewStandardID(0x110)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
			case "raw-dlc":
				record := i.Evidence.RawRecord
				binary.LittleEndian.PutUint32(record[:4], 0x80000000|i.Evidence.Frame.ID().Value())
				record[4] = 8
				record[7] = 9
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
			case "sequence":
				i.Evidence.Sequence = 0
			case "monotonic":
				i.Evidence.Monotonic = -1
			case "field-age":
				i.FieldAge = -1
			}
			a := CX12NewLINKCells(cx01Select(t, spec), 4, true)
			r := a.CX12Decode(i, cx12Gates())
			if r.Raw != i.Evidence.Frame.Bytes() || r.Evidence != i.Evidence || len(r.Fields) != 0 || len(a.CX12Snapshot(time.Second, time.Second).Fields) != 0 {
				t.Fatal(r)
			}
		})
	}
	for _, key := range []string{"project-case", "release", "choice", "flavor", "pcs-flavor", "route-set"} {
		t.Run(key, func(t *testing.T) {
			other := spec
			switch key {
			case "project-case":
				other.ProjectCode = "qAAA"
			case "release":
				other.Release = "0012"
			case "choice":
				other.ProjectCode = "QAAa"
				other.Release = "9011"
			case "flavor":
				other.Flavor = "pcs-standard"
			case "pcs-flavor":
				other.PCSFlavor = "invalid"
			case "route-set":
				other.RouteSet = false
			}
			s, err := NewCXSelection(other)
			if err != nil {
				if key == "route-set" {
					return
				}
				t.Fatal(err)
			}
			r := CX12NewLINKCells(s, 4, true).CX12Decode(cx01Input(t, other, 0x1810, p), cx12Gates())
			if r.Raw != p || len(r.Fields) != 0 {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12CellMeanCountAwareAndFixed16(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	s := cx01Select(t, spec)
	raw := cx12Words(0, 0, 0, 3300)
	for _, count := range []int{0, 1, 4, 16, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			a := CX12NewLINKCells(s, count, true)
			for b := 0; b < 4; b++ {
				i := cx01Input(t, spec, uint32(0x1810+b), cx12Words(3300, 3300, 3300, 3300))
				i.Evidence.Sequence = uint64(b + 1)
				a.CX12Decode(i, cx12Gates())
			}
			i := cx01Input(t, spec, 0x1806, raw)
			i.Evidence.Sequence = 5
			r := CX12DecodeLINKCellMean(s, i, cx12Gates()[0], a.CX12Snapshot(time.Second, time.Second), false)
			if (r.Fields["mean"].Status == "accepted") != (count >= 1 && count <= 16) || r.Raw != raw {
				t.Fatal(r)
			}
		})
	}
	a := CX12NewLINKFixed16Cells(s, 0, false)
	for b := 0; b < 4; b++ {
		i := cx01Input(t, spec, uint32(0x1810+b), cx12Words(3300, 3300, 3300, 3300))
		i.Evidence.Sequence = uint64(b + 1)
		a.CX12Decode(i, cx12Gates())
	}
	v := a.CX12Snapshot(time.Second, time.Second)
	i := cx01Input(t, spec, 0x1806, raw)
	i.Evidence.Sequence = 5
	for _, valid := range []bool{false, true} {
		r := CX12DecodeLINKCellMean(s, i, cx12Gates()[0], v, valid)
		if (r.Fields["mean"].Status == "accepted") != valid || v.ConfiguredCount != 0 || v.CompleteCoverage {
			t.Fatal(r, v)
		}
	}
	for bad := 1; bad <= 16; bad++ {
		t.Run(fmt.Sprintf("invalid-fixed-slot-%d", bad), func(t *testing.T) {
			w := a.CX12Snapshot(time.Second, time.Second)
			f := w.Fields[bad]
			f.Status = "invalid"
			w.Fields[bad] = f
			r := CX12DecodeLINKCellMean(s, i, cx12Gates()[0], w, true)
			if r.Fields["mean"].Status == "accepted" || r.Raw != raw {
				t.Fatal(r)
			}
		})
	}
}

func TestCX12CellHistoryReplayDetachAndConcurrentReads(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	a := CX12NewLINKCells(cx01Select(t, spec), 4, true)
	i := cx01Input(t, spec, 0x1810, cx12Words(3300, 3300, 3300, 3300))
	a.CX12Decode(i, cx12Gates())
	r := a.CX12Decode(i, cx12Gates())
	if r.Status != "stale" || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal(r)
	}
	v := a.CX12Snapshot(time.Second, time.Second)
	delete(v.Fields, 1)
	delete(v.LastGood, 1)
	v.ReceivedIndices[0] = 99
	if len(a.CX12Snapshot(time.Second, time.Second).Fields) != 4 {
		t.Fatal("snapshot alias")
	}
	var wg sync.WaitGroup
	for j := 0; j < 4; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				a.CX12Snapshot(time.Second, time.Second)
				a.CX12Decode(i, cx12Gates())
			}
		}()
	}
	wg.Wait()
}

func cx12Words(v ...uint16) (p [8]byte) {
	for j, n := range v {
		binary.LittleEndian.PutUint16(p[j*2:], n)
	}
	return
}
func cx12Gates() (g [4]CX12MeasurementGate) {
	for j := range g {
		g[j] = CX12MeasurementGate{Configured: true, Valid: true}
	}
	return
}
func TestCX12CellsCountBoundaries(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	s := cx01Select(t, spec)
	for _, count := range []int{0, 1, 4, 16, 17} {
		t.Run(string(rune('A'+count)), func(t *testing.T) {
			a := CX12NewLINKCells(s, count, true)
			for b := 0; b < 4; b++ {
				i := cx01Input(t, spec, uint32(0x1810+b), cx12Words(3300, 3301, 3302, 3303))
				i.Evidence.Sequence = uint64(b + 1)
				i.Evidence.Monotonic = time.Duration(b+1) * time.Second
				a.CX12Decode(i, cx12Gates())
			}
			v := a.CX12Snapshot(4*time.Second, 10*time.Second)
			if v.ConfiguredCount != count || v.AddressableSlots != 16 || len(v.ReceivedIndices) != 16 || v.CompleteCoverage != (count > 0 && count <= 16) {
				t.Fatal(v)
			}
			if count > 0 && v.LastGood[1].Numerator != 3300 {
				t.Fatal(v)
			}
			wantSpan := time.Duration(0)
			if count >= 16 {
				wantSpan = 3 * time.Second
			}
			if v.ReceivedSpan != 3*time.Second || v.Span != wantSpan {
				t.Fatal("frames became atomic", v.Span)
			}
		})
	}
}
func TestCX12CellsOmittedAndIndependentRefresh(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	a := CX12NewLINKCells(cx01Select(t, spec), 16, true)
	p := cx12Words(3300, 3301, 3302, 3303)
	i := cx01Input(t, spec, 0x1810, p)
	a.CX12Decode(i, cx12Gates())
	v := a.CX12Snapshot(time.Second, time.Second)
	if v.CompleteCoverage || len(v.ReceivedIndices) != 4 || len(v.LastGood) != 4 {
		t.Fatal(v)
	}
	g := cx12Gates()
	g[1].Valid = false
	i.Evidence.Sequence++
	i.Evidence.Monotonic = 3 * time.Second
	r := a.CX12Decode(i, g)
	v = a.CX12Snapshot(3*time.Second, time.Second)
	if r.Raw != p || r.Fields["cell_2"].Status != "invalid" || r.Fields["cell_1"].Status != "accepted" || v.LastGood[2].Evidence.Sequence != 1 || v.Fields[2].Status != "invalid" || v.LastGood[1].Evidence.Sequence != 2 {
		t.Fatal(r, v)
	}
	if v.LastGood[2].Age != 2*time.Second || v.LastGood[1].Age != 0 {
		t.Fatal("channel ages coupled", v)
	}
}
func TestCX12CellsNoExtremaSubstitution(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	a := CX12NewLINKCells(cx01Select(t, spec), 1, true)
	for _, kind := range []uint32{0x1806, 0x1801} {
		p := cx12Words(3300, 3300, 3300, 3300)
		r := a.CX12Decode(cx01Input(t, spec, kind, p), cx12Gates())
		if r.Raw != p || len(a.CX12Snapshot(time.Second, time.Second).ReceivedIndices) != 0 {
			t.Fatal(r)
		}
	}
}
