package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
)

func TestCX08CounterArithmetic(t *testing.T) {
	for _, c := range []uint32{0, 1, 7, 8, 9, 15, 16, 17, 167608480, 167608481, 0xfffffffe, 0xffffffff} {
		want := uint32((uint64(c)*25+uint64(c)/2+uint64(c)/8)&0xffffffff) >> 8
		if got := CX08ExplainCounter(c); got != want {
			t.Fatal(c, got, want)
		}
	}
	if CX08ExplainCounter(8) != 0 || CX08ExplainCounter(16) != 1 {
		t.Fatal("boundary")
	}
	for _, state := range []int64{-32769, -32768, -6554, -6553, -1, 0, 1, 6553, 6554, 32767, 32768} {
		if got := CX08ExplainTail(state); got != uint16(uint64(state)*10) {
			t.Fatal(state, got)
		}
	}
}
func TestCX08CounterPreservesCachedCode(t *testing.T) {
	s, i := cx08Fixture(t, 0x329, [8]byte{2, 0, 0, 16})
	r := CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i.CX06PCSInput})
	cx08Want(t, r, "counter", "accepted", 16, 1, "")
	c := s.CX01Spec()
	c.Flavor = "link"
	c.PCSFlavor = "pcs-standard"
	c.RouteSet = true
	c.Destination = 0xf4
	c.Source = 1
	c.Direction = "module-to-coordinator"
	link, err := NewCXSelection(c)
	if err != nil {
		t.Fatal(err)
	}
	j := CX06PCSInput{CX05PCSInput: CX05PCSInput{CX01LINKInput: cx01Input(t, c, 0x180a, [8]byte{2, 16}), PublisherResolved: true, MeasurementValid: true}}
	r = CX08RefineCounter(link, CX08CounterInput{CX06PCSInput: j})
	cx08Want(t, r, "counter", "accepted", 16, 1, "")
	c.Destination = 0xf5
	bad, _ := NewCXSelection(c)
	if r = CX08RefineCounter(bad, CX08CounterInput{CX06PCSInput: j}); r.Status != "invalid" {
		t.Fatal(r)
	}
}
func TestCX08TailHypothesisNegativeFeature(t *testing.T) {
	for _, tail := range [][2]byte{{0, 0}, {0xff, 0xf6}} {
		s, i := cx08Fixture(t, 0x329, [8]byte{2, 0, 0, 16, 0, 0, tail[0], tail[1]})
		j := CX08CounterInput{CX06PCSInput: i.CX06PCSInput, TailSelected: true}
		r := CX08RefineCounter(s, j)
		n := int64(tail[0])<<8 | int64(tail[1])
		cx08Want(t, r, "tail", "accepted", n, 1, "")
		if r.Fields["tail"].Grade != "Hypothesis" {
			t.Fatal(r)
		}
		j.TailSelected = false
		r = CX08RefineCounter(s, j)
		cx08Want(t, r, "tail", "raw", n, 1, "")
		cx08Want(t, r, "counter", "accepted", 16, 1, "")
	}
}
func TestCX08TailHypothesisNegativeScopeAndValidity(t *testing.T) {
	for _, gate := range []string{"scope", "validity", "release", "choice", "direction"} {
		t.Run(gate, func(t *testing.T) {
			s, i := cx08Fixture(t, 0x329, [8]byte{2, 0, 0, 16, 0, 0, 0xff, 0xf6})
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) {
				switch gate {
				case "scope":
					c.Scope = "aggregate"
				case "release":
					c.Release = "0099"
				case "choice":
					c.ProjectCode = "QAAa"
					c.Release = "9011"
				case "direction":
					c.Direction = "pcs-to-bms"
				}
			})
			if gate == "validity" {
				i.MeasurementValid = false
			}
			r := CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i.CX06PCSInput, TailSelected: true})
			if r.Raw != i.Evidence.Frame.Bytes() || r.Fields["tail"].Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX08CounterPaddingAndFieldIsolation(t *testing.T) {
	s, i := cx08Fixture(t, 0x329, [8]byte{2, 0, 0, 16, 0, 0, 0xff, 0xf6})
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict"} {
		i.FieldStates = map[string]string{"tail": state}
		r := CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i.CX06PCSInput, TailSelected: true})
		cx08Want(t, r, "tail", state, 65526, 1, "")
		cx08Want(t, r, "counter", "accepted", 16, 1, "")
	}
	for _, at := range []int{4, 5} {
		p := i.Evidence.Frame.Bytes()
		p[at] = 1
		id := i.Evidence.Frame.ID()
		var err error
		i.Evidence.Frame, err = canbus.NewFrame(id, p[:])
		if err != nil {
			t.Fatal(err)
		}
		r := CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i.CX06PCSInput, TailSelected: true})
		if r.Fields["counter"].Status == "accepted" || r.Fields["tail"].Status == "accepted" {
			t.Fatal(r)
		}
	}
}

func TestCX08CounterWireBoundaries(t *testing.T) {
	for _, code := range []uint32{0, 16, 0x010210, 0xffffff} {
		s, i := cx08Fixture(t, 0x329, [8]byte{2, byte(code >> 16), byte(code >> 8), byte(code)})
		cx08Want(t, CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i.CX06PCSInput}), "counter", "accepted", int64(code), 1, "")
		c := s.CX01Spec()
		c.Flavor = "link"
		c.PCSFlavor = "pcs-standard"
		c.Direction = "module-to-coordinator"
		c.RouteSet = true
		c.Destination = 0xf4
		c.Source = 2
		link, err := NewCXSelection(c)
		if err != nil {
			t.Fatal(err)
		}
		i.CX01LINKInput = cx01Input(t, c, 0x180a, [8]byte{2, byte(code), byte(code >> 8), byte(code >> 16)})
		cx08Want(t, CX08RefineCounter(link, CX08CounterInput{CX06PCSInput: i.CX06PCSInput}), "counter", "accepted", int64(code), 1, "")
	}
}
func TestCX08CounterLINKAdmissionKeys(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc", "raw-dlc", "age", "publisher", "native-record", "raw-record", "pid"} {
		t.Run(key, func(t *testing.T) {
			c := cx01Spec(t, "QAAA", "0011")
			c.Group = "group"
			c.Slot = "slot"
			s := cx01Select(t, c)
			i := CX06PCSInput{CX05PCSInput: CX05PCSInput{CX01LINKInput: cx01Input(t, c, 0x180a, [8]byte{2, 16}), PublisherResolved: true, MeasurementValid: true}}
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
				i.Bank = "other"
			case "endpoint":
				i.Endpoint = "other"
			case "group":
				i.Group = "other"
			case "slot":
				i.Slot = "other"
			case "destination", "source", "pid":
				id := i.Evidence.Frame.ID().Value()
				if key == "destination" {
					id ^= 0x100
				} else if key == "source" {
					id ^= 1
				} else {
					id ^= 0x10000
				}
				changed, err := canbus.NewExtendedID(id)
				if err != nil {
					t.Fatal(err)
				}
				i.Evidence.Frame, err = canbus.NewFrame(changed, i.Evidence.Frame.Data())
				if err != nil {
					t.Fatal(err)
				}
			case "ide":
				id, _ := canbus.NewStandardID(0x329)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data())
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), []byte{2})
			case "raw-dlc":
				var record [16]byte
				binary.NativeEndian.PutUint32(record[:4], i.Evidence.Frame.ID().Value()|0x80000000)
				record[4], record[7] = 8, 9
				p := i.Evidence.Frame.Bytes()
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
			case "age":
				i.FieldAge = -1
			case "publisher":
				i.PublisherResolved = false
			case "native-record":
				i.NativeRecord = []byte{1}
			case "raw-record":
				i.Evidence.RawRecord[0] = 1
			}
			r := CX08RefineCounter(s, CX08CounterInput{CX06PCSInput: i})
			if r.Status == "decoded" || len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}
