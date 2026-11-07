package canbusreg

import "testing"

func TestCX01IdentityAssemblyAndConflict(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
	seq := uint64(0)
	feed := func(kind uint32, p [8]byte) CXNativeResult {
		seq++
		i := cx01Input(t, spec, kind, p)
		i.Evidence.Sequence = seq
		return a.CX01Decode(i)
	}
	feed(0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	for j, p := range [][8]byte{{15, 1, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}} {
		if feed(0x18fb, p).Status != "accepted" {
			t.Fatalf("fragment %d", j)
		}
	}
	snap := a.CX01Snapshot()
	if !snap.HasSerial || snap.Serial[15] != 16 {
		t.Fatal(snap)
	}
	snap.Fields["fake"] = CXNativeField{}
	snap.Serial[0] = 99
	if a.CX01Snapshot().Serial[0] != 1 {
		t.Fatal("alias")
	}
	if _, ok := a.CX01Snapshot().Fields["fake"]; ok {
		t.Fatal("map alias")
	}
	feed(0x1805, [8]byte{1})
	prior := a.CX01Snapshot()
	if len(prior.Fields) == 0 {
		t.Fatal("no fields")
	}
	if feed(0x1802, [8]byte{'Q', 'A', 'A', 'a', '0', '0', '0', '6'}).Status != "conflict" {
		t.Fatal("case identity reuse")
	}
	after := a.CX01Snapshot()
	if after.HasSerial || after.HasIdentity || len(after.Fields) != 0 || len(prior.Fields) == 0 {
		t.Fatal("invalidation/history")
	}
}
func TestCX01SerialNegativeVectors(t *testing.T) {
	for _, gate := range []string{"orphan", "reorder", "interrupt", "length-low", "length-high", "padding", "duplicate", "conflicting-length", "l0", "unknown"} {
		t.Run(gate, func(t *testing.T) {
			spec := cx01Spec(t, "QAAA", "0006")
			if gate == "l0" {
				spec.Release = "0002"
			}
			if gate == "unknown" {
				spec.ProjectCode = "QCAA"
				spec.Release = "0001"
			}
			a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
			seq := uint64(0)
			feed := func(k uint32, p [8]byte) CXNativeResult {
				seq++
				i := cx01Input(t, spec, k, p)
				i.Evidence.Sequence = seq
				return a.CX01Decode(i)
			}
			var id [8]byte
			copy(id[:], spec.ProjectCode+spec.Release)
			feed(0x1802, id)
			p := [8]byte{15, 1, 2, 3, 4, 5, 6, 7}
			if gate != "orphan" {
				feed(0x18fb, p)
			}
			switch gate {
			case "orphan":
				p[0] = 47
			case "reorder":
				p[0] = 79
			case "interrupt":
				feed(0x1800, [8]byte{})
				p[0] = 47
			case "length-low":
				p[0] = 14
			case "length-high":
				p[0] = 16
			case "padding":
				feed(0x18fb, [8]byte{47, 8, 9, 10, 11, 12, 13, 14})
				p = [8]byte{79, 15, 16, 1}
			case "duplicate":
				p[0] = 15
			case "conflicting-length":
				p[0] = 48
			}
			r := feed(0x18fb, p)
			if a.CX01Snapshot().HasSerial || r.Raw != p || r.Status == "accepted" {
				t.Fatalf("admitted %#v", r)
			}
		})
	}
}
func TestCX01IdentityEpochRoleAndPartial(t *testing.T) {
	for _, gate := range []string{"epoch", "role", "bad-ascii", "malformed", "source", "interface-name", "interface-index", "endpoint", "bank", "group", "slot", "scope", "direction"} {
		t.Run(gate, func(t *testing.T) {
			spec := cx01Spec(t, "QAAA", "0006")
			a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
			i := cx01Input(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			a.CX01Decode(i)
			i = cx01Input(t, spec, 0x1800, [8]byte{})
			i.Evidence.Sequence = 2
			a.CX01Decode(i)
			i = cx01Input(t, spec, 0x1805, [8]byte{1})
			i.Evidence.Sequence = 3
			a.CX01Decode(i)
			prior := a.CX01Snapshot()
			i.Evidence.Sequence = 4
			switch gate {
			case "epoch":
				i.Epoch++
			case "role":
				i = cx01Input(t, spec, 0x1800, [8]byte{0, 1})
				i.Evidence.Sequence = 4
			case "bad-ascii":
				i = cx01Input(t, spec, 0x1802, [8]byte{})
				i.Evidence.Sequence = 4
			case "malformed":
				id := i.Evidence.Frame.ID()
				i.Evidence.Frame, _ = cx01ShortFrame(id)
			case "source":
				other := spec
				other.Source++
				i = cx01Input(t, other, 0x1805, [8]byte{1})
				i.Evidence.Sequence = 4
			case "interface-name":
				other := cx01Spec(t, "QAAA", "0006")
				other.Interface = growattInterface(t, "other", 1)
				i.Evidence.Interface = other.Interface
			case "interface-index":
				i.Evidence.Interface = growattInterface(t, "synthetic", 2)
			case "endpoint":
				i.Endpoint = "b"
			case "bank":
				i.Bank = "b"
			case "group":
				i.Group = "b"
			case "slot":
				i.Slot = "b"
			case "scope":
				i.Scope = "aggregate"
			case "direction":
				i.Direction = "other"
			}
			r := a.CX01Decode(i)
			if r.Status == "accepted" {
				t.Fatal("gate escaped")
			}
			if len(prior.Fields) == 0 {
				t.Fatal("history lost")
			}
			if gate == "bad-ascii" || gate == "malformed" || gate == "source" || gate == "interface-name" || gate == "interface-index" {
				if len(a.CX01Snapshot().Fields) == 0 {
					t.Fatal("last known good erased")
				}
			} else if len(a.CX01Snapshot().Fields) != 0 {
				t.Fatal("dependent fields not invalidated")
			}
		})
	}
}

func TestCX01ConcurrentSnapshots(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
	done := make(chan bool, 4)
	for n := 0; n < 4; n++ {
		go func() {
			for j := 0; j < 50; j++ {
				a.CX01Snapshot()
				a.CX01Decode(cx01Input(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}))
			}
			done <- true
		}()
	}
	for n := 0; n < 4; n++ {
		<-done
	}
}
func TestCX01AssemblerBeforeIdentityAndStale(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
	i := cx01Input(t, spec, 0x1805, [8]byte{1})
	r := a.CX01Decode(i)
	if r.Fields["pack_voltage"].Status != "absent" || r.Fields["pack_voltage"].Unit != "" {
		t.Fatal("unidentified units")
	}
	i = cx01Input(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	i.Evidence.Sequence = 2
	a.CX01Decode(i)
	if a.CX01Decode(i).Status != "stale" {
		t.Fatal("duplicate")
	}
	i.Evidence.Sequence = 3
	i.Evidence.Monotonic = 0
	if a.CX01Decode(i).Status != "stale" {
		t.Fatal("time reversal")
	}
}

func TestCX01IdentityReleaseReuse(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	a := CX01NewLINKIdentityAssembler(cx01Select(t, spec))
	p := [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}
	a.CX01Decode(cx01Input(t, spec, 0x1802, p))
	before := a.CX01Snapshot()
	p[7] = '7'
	i := cx01Input(t, spec, 0x1802, p)
	i.Evidence.Sequence = 2
	r := a.CX01Decode(i)
	if r.Status != "conflict" || r.Raw != p || a.CX01Snapshot().HasIdentity || before.Release != "0006" {
		t.Fatal("release reassigned history")
	}
}
