package canbusreg

import (
	"encoding/binary"
	"fmt"
	"github.com/Project-Helianthus/helianthus-canbus"
	"sync"
	"testing"
	"time"
)

func extendedTestSpec(t *testing.T) SelectionSpec {
	t.Helper()
	bus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return SelectionSpec{Interface: bus, Flavor: "pcs-extended19", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs", Scope: "local", Bank: "ordinary", Endpoint: "endpoint-a", Group: "group-a", Slot: "slot-a", RouteSet: true, Destination: 0xf2, Source: 2, Epoch: 1}
}
func extendedTestSelect(t *testing.T, c SelectionSpec) Selection {
	t.Helper()
	s, e := NewSelection(c)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func extendedTestInput(t *testing.T, c SelectionSpec, kind uint32, p [8]byte) LINKInput {
	t.Helper()
	id, e := canbus.NewExtendedID(kind<<16 | uint32(c.Destination)<<8 | uint32(c.Source))
	if e != nil {
		t.Fatal(e)
	}
	f, e := canbus.NewFrame(id, p[:])
	if e != nil {
		t.Fatal(e)
	}
	return LINKInput{Evidence: Evidence{Interface: c.Interface, Frame: f, Sequence: 7, Monotonic: time.Second}, Epoch: c.Epoch, Direction: c.Direction, Scope: c.Scope, Bank: c.Bank, Endpoint: c.Endpoint, Group: c.Group, Slot: c.Slot, FieldAge: time.Millisecond}
}
func extendedTestWithheld(t *testing.T, r NativeResult, p [8]byte) {
	t.Helper()
	if r.Raw != p || r.PhysicalQualified {
		t.Fatal(r)
	}
	for _, f := range r.Fields {
		if f.Status == "accepted" || f.Unit != "" || f.Meaning != "" {
			t.Fatal(f)
		}
	}
}

func TestCX09CurrentAndEvidence(t *testing.T) {
	c := extendedTestSpec(t)
	s := extendedTestSelect(t, c)
	for _, v := range []uint16{0, 1, 9990, 10000, 10010, 65534, 65535} {
		p := [8]byte{0x34, 0x12, 0, 0, 0x78, 0x56, 0xbc, 0x9a}
		binary.LittleEndian.PutUint16(p[2:4], v)
		i := extendedTestInput(t, c, 0x1902, p)
		r := DecodePCSExtended(s, i)
		for name, want := range map[string]int64{"pack_voltage": 0x1234, "current": int64(v) - 10000, "port_voltage": 0x5678, "soc": 0x9abc} {
			f := r.Fields[name]
			if f.Numerator != want || f.Denominator != 10 || f.Status != "accepted" || f.Grade != "Hypothesis" || f.Raw != p || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.Age != i.FieldAge || f.PhysicalQualified {
				t.Fatalf("%s: %#v", name, f)
			}
		}
		if r.Status != "accepted" || r.PhysicalQualified {
			t.Fatal(r)
		}
	}
}

func TestCX09EveryAdmissionKeyNegative(t *testing.T) {
	// Each mutation catches omission of the corresponding exact selection/evidence comparison.
	for _, key := range []string{"interface-name", "interface-index", "epoch-old", "epoch-new", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc", "raw-dlc", "age", "flavor", "route-set", "project-case", "release", "choice"} {
		t.Run(key, func(t *testing.T) {
			c := extendedTestSpec(t)
			p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
			i := extendedTestInput(t, c, 0x1902, p)
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch-old":
				i.Epoch--
			case "epoch-new":
				i.Epoch++
			case "direction":
				i.Direction = "pcs-to-bms"
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
			case "destination":
				other := c
				other.Destination++
				i = extendedTestInput(t, other, 0x1902, p)
			case "source":
				other := c
				other.Source++
				i = extendedTestInput(t, other, 0x1902, p)
			case "ide":
				id, _ := canbus.NewStandardID(0x102)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
				p = i.Evidence.Frame.Bytes()
			case "raw-dlc":
				record := [16]byte{}
				binary.LittleEndian.PutUint32(record[:4], 0x80000000|0x1902f202)
				record[4] = 8
				record[7] = 9
				copy(record[8:], p[:])
				f, e := canbus.DecodeSocketCANRecord(record[:])
				if e != nil {
					t.Fatal(e)
				}
				i.Evidence.Frame = f
			case "age":
				i.FieldAge = -1
			case "flavor":
				c.Flavor = "pcs-standard"
			case "route-set":
				c.RouteSet = false
			case "project-case":
				c.ProjectCode = "qAAA"
			case "release":
				c.Release = "9999"
			case "choice":
				c.Choice = "release11"
			}
			r := DecodePCSExtended(extendedTestSelect(t, c), i)
			extendedTestWithheld(t, r, p)
			if r.Status == "accepted" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX09EachFamilyRouteNegative(t *testing.T) {
	for _, kind := range []uint32{0x1900, 0x1901, 0x1902, 0x1903, 0x1904, 0x1905, 0x1906, 0x1907, 0x1908, 0x1909, 0x1910} {
		t.Run(fmt.Sprintf("%04x", kind), func(t *testing.T) {
			c := extendedTestSpec(t)
			if kind == 0x1910 {
				c.ProjectCode = "QCAA"
				c.Release = "0001"
				c.Destination = 0xff
				c.Source = 0xf4
			}
			p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
			other := c
			other.Source++
			i := extendedTestInput(t, other, kind, p)
			extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, c), i), p)
		})
	}
}

func TestCX09LegacyFeatureExclusions(t *testing.T) {
	for _, identity := range []string{"QAaT8252", "QAAA0002", "QAAB0006", "QAAA0011", "QAAa9011"} {
		for _, flavor := range []string{"pcs-standard", "pcs-parallel", "pcs-extended19"} {
			t.Run(identity+"/"+flavor, func(t *testing.T) {
				c := extendedTestSpec(t)
				c.ProjectCode = identity[:4]
				c.Release = identity[4:]
				c.Flavor = flavor
				if identity == "QAAa9011" {
					c.Choice = "release11"
				}
				p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
				r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1902, p))
				extendedTestWithheld(t, r, p)
				if r.Status != "unsupported" {
					t.Fatal(r)
				}
			})
		}
	}
	c := extendedTestSpec(t)
	c.ProjectCode = "QAAa"
	c.Release = "9011"
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1900, p)), p)
	c.Choice = "legacy19"
	r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1900, p))
	if r.Fields["version"].Numerator != 0x504 {
		t.Fatal(r)
	}
}

func TestCX09UnitsScopeAndProjectNegative(t *testing.T) {
	for _, kind := range []uint32{0x1902, 0x1904} {
		for _, gate := range []string{"aggregate", "project", "release"} {
			t.Run(gate+"/"+fmt.Sprintf("%04x", kind), func(t *testing.T) {
				c := extendedTestSpec(t)
				switch gate {
				case "aggregate":
					c.Scope = "aggregate"
					c.Destination = 0xdf
					c.Source = 0xf2
				case "project":
					c.ProjectCode = "QAAa"
					c.Release = "2005"
				case "release":
					c.Release = "0007"
				}
				p := [8]byte{1, 2, 3, 4, 5, 6}
				r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, kind, p))
				for _, name := range []string{"pack_voltage", "current", "port_voltage", "soc", "full_capacity", "remaining_capacity"} {
					if f, ok := r.Fields[name]; ok && (f.Status != "raw" || f.Unit != "" || f.Meaning != "") {
						t.Fatal(name, f)
					}
				}
				if r.Raw != p || r.Status != "accepted" {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestCX09LayoutsAndNumericBoundaries(t *testing.T) {
	cases := []struct {
		kind    uint32
		offsets []int
		names   []string
	}{
		{0x1900, []int{0, 3}, []string{"product", "version"}},
		{0x1901, []int{4, 6}, []string{"configuration_4", "configuration_6"}},
		{0x1903, []int{0, 2}, []string{"limit_0", "limit_2"}},
		{0x1904, []int{0, 2, 4}, []string{"full_capacity", "remaining_capacity", "cycles"}},
		{0x1905, []int{0, 2, 4, 6}, []string{"maximum_index", "maximum_voltage", "minimum_index", "minimum_voltage"}},
		{0x1906, []int{0, 3}, []string{"maximum_index", "minimum_index"}},
		{0x1909, []int{0, 2, 4}, []string{"limit_0", "limit_2", "derived"}},
	}
	for _, tc := range cases {
		for j, at := range tc.offsets {
			for _, n := range []uint16{0, 1, 255, 256, 0x1234, 0x3412, 65534, 65535} {
				c := extendedTestSpec(t)
				p := [8]byte{}
				binary.LittleEndian.PutUint16(p[at:at+2], n)
				r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, tc.kind, p))
				f := r.Fields[tc.names[j]]
				if f.Numerator != int64(n) || f.Raw != p {
					t.Fatalf("%04x/%s/%d: %#v", tc.kind, tc.names[j], n, f)
				}
				if tc.kind == 0x1904 && j < 2 && f.Denominator != 10 {
					t.Fatal(f)
				}
				if tc.kind == 0x1904 && j == 2 && (f.Denominator != 1 || f.Unit != "cycles" || f.Meaning != "whole summary") {
					t.Fatal(f)
				}
			}
		}
	}
	for _, kind := range []uint32{0x1901, 0x1903, 0x1906, 0x1907, 0x1908, 0x1909} {
		for _, n := range []byte{0, 1, 254, 255} {
			c := extendedTestSpec(t)
			p := [8]byte{n, n, n, n, n, n, n, n}
			r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, kind, p))
			if r.Status != "accepted" || r.Raw != p {
				t.Fatal(r)
			}
			if kind == 0x1901 && r.Fields["count"].Numerator != int64(n) {
				t.Fatal(r)
			}
			for _, f := range r.Fields {
				if f.PhysicalQualified {
					t.Fatal(f)
				}
			}
		}
	}
}

func TestCX09AggregateRoutesAndIdentitySentinel(t *testing.T) {
	for _, prefix := range []uint32{0x1900, 0x1910} {
		c := extendedTestSpec(t)
		c.Scope = "aggregate"
		c.Destination = 0xdf
		c.Source = 0xf2
		s := extendedTestSelect(t, c)
		for n := uint32(0); n < 10; n++ {
			p := [8]byte{0x34, 0x12, 0x78, 0x56, 0xbc, 0x9a, 0xfe, 0xdc}
			r := DecodePCSExtended(s, extendedTestInput(t, c, prefix+n, p))
			if r.Status != "accepted" || r.Raw != p {
				t.Fatal(r)
			}
			if n == 5 && (r.Fields["maximum_index"].Numerator != 0x1234 || r.Fields["minimum_index"].Numerator != 0x9abc) {
				t.Fatal(r)
			}
			other := c
			other.Scope = "local"
			extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, other), extendedTestInput(t, other, prefix+n, p)), p)
		}
		for _, product := range []uint16{65534, 65535} {
			p := [8]byte{}
			binary.LittleEndian.PutUint16(p[:2], product)
			r := DecodePCSExtended(s, extendedTestInput(t, c, prefix, p))
			want := "accepted"
			if product == 65535 {
				want = "invalid"
			}
			if r.Fields["product"].Status != want || r.Fields["hardware"].Status != "accepted" {
				t.Fatal(r)
			}
		}
	}
	c := extendedTestSpec(t)
	p := [8]byte{255, 255}
	r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1900, p))
	if r.Fields["product"].Status != "accepted" {
		t.Fatal(r)
	}
}

func TestCX09RawIndependentValidityAndFaultBlocks(t *testing.T) {
	c := extendedTestSpec(t)
	s := extendedTestSelect(t, c)
	p := [8]byte{0x34, 0x12, 65, 0x78, 0x56, 66, 255, 255}
	r := DecodePCSExtended(s, extendedTestInput(t, c, 0x1906, p))
	if r.Fields["minimum_index"].Numerator != 0x5678 {
		t.Fatal(r)
	}
	for _, name := range []string{"maximum_temperature_code", "minimum_temperature_code", "extra", "soh_like"} {
		f := r.Fields[name]
		if f.Status != "raw" || f.Unit != "" || f.Meaning != "" {
			t.Fatal(f)
		}
	}
	for _, kind := range []uint32{0x1903, 0x1909} {
		for _, scope := range []string{"local", "aggregate"} {
			other := c
			other.Scope = scope
			if scope == "aggregate" {
				other.Destination = 0xdf
				other.Source = 0xf2
			}
			r := DecodePCSExtended(extendedTestSelect(t, other), extendedTestInput(t, other, kind, [8]byte{}))
			for _, name := range []string{"limit_0", "limit_2"} {
				f := r.Fields[name]
				if f.Numerator != 0 || f.Status != "raw" || f.Unit != "" {
					t.Fatal(f)
				}
			}
			if kind == 0x1909 {
				for _, f := range r.Fields {
					if f.Status != "raw" {
						t.Fatal(f)
					}
				}
			}
		}
	}
	for _, kind := range []uint32{0x1907, 0x1908} {
		r := DecodePCSExtended(s, extendedTestInput(t, c, kind, p))
		if len(r.Fields) != 8 {
			t.Fatal(r)
		}
		for at := 0; at < 8; at++ {
			name := fmt.Sprintf("current_fault_%d", int(kind-0x1907)*8+at)
			if r.Fields[name].Numerator != int64(p[at]) {
				t.Fatal(r)
			}
		}
	}
	for _, kind := range []uint32{0x190a, 0x191a, 0x1861} {
		extendedTestWithheld(t, DecodePCSExtended(s, extendedTestInput(t, c, kind, p)), p)
	}
	for _, tc := range []struct {
		kind  uint32
		start int
	}{{0x1900, 5}, {0x1904, 6}} {
		for at := tc.start; at < 8; at++ {
			p := [8]byte{}
			p[at] = 1
			r := DecodePCSExtended(s, extendedTestInput(t, c, tc.kind, p))
			if r.Fields[fmt.Sprintf("padding_%d", at)].Status != "invalid" {
				t.Fatal(r)
			}
			name := "hardware"
			if tc.kind == 0x1904 {
				name = "full_capacity"
			}
			if r.Fields[name].Status != "accepted" {
				t.Fatal(r)
			}
		}
	}
}

func TestCX09GatewayBoundAndNoIdentity(t *testing.T) {
	for _, address := range []byte{0, 1, 8, 9, 254, 255} {
		c := extendedTestSpec(t)
		c.ProjectCode = "QCAA"
		c.Release = "0001"
		c.Destination = 0xff
		c.Source = 0xf4
		p := [8]byte{3, 7, 0, 0, 0, 0, 0, address}
		r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1910, p))
		want := "accepted"
		if address < 1 || address > 8 {
			want = "invalid"
		}
		if r.Fields["downstream_address"].Status != want || r.Fields["state"].Status != "accepted" || r.Raw != p {
			t.Fatal(r)
		}
		for _, name := range []string{"identity", "release", "version", "cell_inventory"} {
			if _, ok := r.Fields[name]; ok {
				t.Fatal(name)
			}
		}
		for _, gate := range []string{"project", "release", "scope-route", "epoch", "address-route"} {
			t.Run(fmt.Sprintf("%d/%s", address, gate), func(t *testing.T) {
				other := c
				i := extendedTestInput(t, c, 0x1910, p)
				switch gate {
				case "project":
					other.ProjectCode = "QAAA"
				case "release":
					other.Release = "0002"
				case "scope-route":
					other.Destination = 0xf2
					other.Source = 2
					i = extendedTestInput(t, other, 0x1910, p)
				case "epoch":
					i.Epoch++
				case "address-route":
					other.Source++
					i = extendedTestInput(t, other, 0x1910, p)
				}
				if gate != "project" && gate != "release" {
					extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, c), i), p)
				}
				if gate == "project" || gate == "release" {
					extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, other), extendedTestInput(t, other, 0x1910, p)), p)
				}
			})
		}
		for at := 2; at < 7; at++ {
			bad := p
			bad[at] = 1
			r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1910, bad))
			if r.Fields[fmt.Sprintf("padding_%d", at)].Status != "invalid" || r.Fields["state"].Status != "accepted" {
				t.Fatal(r)
			}
		}
	}
}

func TestCX09RetainedPartialAndStale(t *testing.T) {
	c := extendedTestSpec(t)
	s := extendedTestSelect(t, c)
	d := NewPCSExtendedDecoder(s)
	i := extendedTestInput(t, c, 0x1904, [8]byte{10, 0, 20, 0, 30})
	if d.DecodePCSExtendedFrame(i).Status != "accepted" {
		t.Fatal("first")
	}
	i.Evidence.Sequence++
	i.Evidence.Monotonic += time.Second
	id := i.Evidence.Frame.ID()
	i.Evidence.Frame, _ = canbus.NewFrame(id, []byte{40, 0, 50, 0, 60, 0, 1, 0})
	r := d.DecodePCSExtendedFrame(i)
	snap := d.SnapshotPCSExtended()
	if r.Fields["padding_6"].Status != "invalid" || snap["1904/full_capacity"].Numerator != 40 || snap["1904/padding_6"].Numerator != 0 {
		t.Fatal(r, snap)
	}
	snap["1904/full_capacity"] = NativeField{}
	if d.SnapshotPCSExtended()["1904/full_capacity"].Numerator != 40 {
		t.Fatal("snapshot aliased")
	}
	for _, gate := range []string{"sequence", "time", "epoch", "address", "scope"} {
		bad := i
		bad.Evidence.Sequence++
		bad.Evidence.Monotonic += time.Second
		switch gate {
		case "sequence":
			bad.Evidence.Sequence--
		case "time":
			bad.Evidence.Monotonic = 0
		case "epoch":
			bad.Epoch++
		case "address":
			other := c
			other.Source++
			bad = extendedTestInput(t, other, 0x1904, [8]byte{})
		case "scope":
			bad.Scope = "aggregate"
		}
		r := d.DecodePCSExtendedFrame(bad)
		if r.Status == "accepted" || d.SnapshotPCSExtended()["1904/full_capacity"].Numerator != 40 {
			t.Fatal(gate, r)
		}
		extendedTestWithheld(t, r, bad.Evidence.Frame.Bytes())
	}
}

func TestCX09NativeRouteGatesWithMatchingSelection(t *testing.T) {
	for _, kind := range []uint32{0x1900, 0x1901, 0x1902, 0x1903, 0x1904, 0x1905, 0x1906, 0x1907, 0x1908, 0x1909} {
		for _, gate := range []string{"endpoint-destination", "coordinator-source", "coordinator-destination", "direction"} {
			t.Run(fmt.Sprintf("%04x/%s", kind, gate), func(t *testing.T) {
				c := extendedTestSpec(t)
				switch gate {
				case "endpoint-destination":
					c.Destination = 0xf3
				case "coordinator-source":
					c.Scope = "aggregate"
					c.Destination = 0xdf
					c.Source = 0xf3
				case "coordinator-destination":
					c.Scope = "aggregate"
					c.Destination = 0xde
					c.Source = 0xf2
				case "direction":
					c.Direction = "pcs-to-bms"
				}
				p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
				extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, kind, p)), p)
			})
		}
	}
}

func TestCX09GatewayBoundIsNotUniversalAndUpstreamStaysRaw(t *testing.T) {
	c := extendedTestSpec(t)
	c.Destination = 0xff
	c.Source = 0xf4
	p := [8]byte{1, 2, 0, 0, 0, 0, 0, 9}
	r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1910, p))
	if r.Fields["downstream_address"].Status != "raw" || r.Fields["downstream_address"].Numerator != 9 || r.Fields["state"].Status != "accepted" {
		t.Fatal(r)
	}
	c.ProjectCode = "QCAA"
	c.Release = "0001"
	// No byte layout or independent aggregation/unit gate was supplied for these
	// upstream records. They cannot become local measurements or identity evidence.
	for _, kind := range []uint32{0x313, 0x314, 0x319, 0x320, 0x330} {
		t.Run(fmt.Sprintf("%03x", kind), func(t *testing.T) {
			i := extendedTestInput(t, c, 0x1910, p)
			id, _ := canbus.NewStandardID(kind)
			i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			r := DecodePCSExtended(extendedTestSelect(t, c), i)
			extendedTestWithheld(t, r, p)
			if len(r.Fields) != 0 {
				t.Fatal(r)
			}
		})
	}
}

func TestCX09ConcurrentRetention(t *testing.T) {
	c := extendedTestSpec(t)
	d := NewPCSExtendedDecoder(extendedTestSelect(t, c))
	base := extendedTestInput(t, c, 0x1902, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	var wg sync.WaitGroup
	for seq := uint64(1); seq <= 32; seq++ {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			i := base
			i.Evidence.Sequence = seq
			i.Evidence.Monotonic = time.Duration(seq)
			d.DecodePCSExtendedFrame(i)
			snap := d.SnapshotPCSExtended()
			delete(snap, "1902/current")
		}(seq)
	}
	wg.Wait()
	snap := d.SnapshotPCSExtended()
	if len(snap) != 4 || snap["1902/current"].Evidence.Sequence != 32 {
		t.Fatal(snap)
	}
}

func TestCX09EndpointSourceRangeAndCellVoltageUnits(t *testing.T) {
	p := [8]byte{1, 0, 2, 0, 3, 0, 4, 0}
	for _, src := range []uint8{0, 9, 0xf2} {
		c := extendedTestSpec(t)
		c.ProjectCode = "QCAA"
		c.Release = "0001"
		c.Source = src
		extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1902, p)), p)
	}
	// The 1-8 receiver bound belongs to the gateway contract only; an explicitly
	// selected non-gateway endpoint at source 9 is decoded.
	nc := extendedTestSpec(t)
	nc.Source = 9
	if r := DecodePCSExtended(extendedTestSelect(t, nc), extendedTestInput(t, nc, 0x1902, p)); r.Status != "accepted" || r.Fields["pack_voltage"].Status != "accepted" {
		t.Fatal(r)
	}
	// An unassigned source of zero is rejected for every selected family.
	zc := extendedTestSpec(t)
	zc.Source = 0
	extendedTestWithheld(t, DecodePCSExtended(extendedTestSelect(t, zc), extendedTestInput(t, zc, 0x1902, p)), p)
	c := extendedTestSpec(t)
	c.Release = "0007"
	r := DecodePCSExtended(extendedTestSelect(t, c), extendedTestInput(t, c, 0x1905, p))
	for _, n := range []string{"maximum_voltage", "minimum_voltage"} {
		if f := r.Fields[n]; f.Status != "raw" || f.Unit != "" || f.Meaning != "" {
			t.Fatal(n, f)
		}
	}
}
