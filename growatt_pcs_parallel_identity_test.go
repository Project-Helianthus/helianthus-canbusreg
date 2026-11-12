package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"sync"
	"testing"
	"time"
)

func parallelIdentityTestFragment(t *testing.T, c SelectionSpec, fragment byte) LINKInput {
	t.Helper()
	p := [8]byte{0x20 | fragment, 1 + fragment*7, 2 + fragment*7, 3 + fragment*7, 4 + fragment*7, 5 + fragment*7, 6 + fragment*7, 7 + fragment*7}
	i := parallelIdentityTestInput(t, c, 0x6c3, p)
	i.Evidence.Sequence = uint64(fragment) + 1
	i.Evidence.Monotonic = time.Duration(fragment+1) * time.Second
	return i
}
func TestCX10IdentityOrderedUnspecifiedTail(t *testing.T) {
	c := parallelIdentityTestSpec(t)
	a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
	for f := byte(0); f < 3; f++ {
		i := parallelIdentityTestFragment(t, c, f)
		r := a.DecodeParallelIdentity(i)
		if r.Status != "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal(r)
		}
		if a.SnapshotParallelIdentity().HasSerial != (f == 2) {
			t.Fatal("partial inventory")
		}
	}
	s := a.SnapshotParallelIdentity()
	for j, b := range s.Serial {
		if b != byte(j+1) {
			t.Fatal(s)
		}
	}
	if s.Grade != "Hypothesis" || s.PhysicalQualified || s.Fragments[2].Evidence.Frame.Bytes()[7] == 0 {
		t.Fatal(s)
	}
}
func TestCX10IdentityKeyIsolationNegatives(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "group", "module", "epoch", "direction", "scope", "bank", "endpoint", "slot", "profile", "project-code", "release", "choice"} {
		t.Run(key, func(t *testing.T) {
			c := parallelIdentityTestSpec(t)
			a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
			a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 0))
			i := parallelIdentityTestFragment(t, c, 1)
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "group":
				i.Group = "4"
			case "module":
				p := i.Evidence.Frame.Bytes()
				p[0] = 0x31
				id, _ := canbus.NewStandardID(0x6c3)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "pcs-to-bms"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "other"
			case "endpoint":
				i.Endpoint = "other"
			case "slot":
				i.Slot = "2"
			case "profile":
				cc := c
				cc.Flavor = "pcs-standard"
				a = NewParallelIdentityAssembler(parallelIdentityTestSelection(t, cc), 2)
			case "project-code":
				cc := c
				cc.ProjectCode = "qaab"
				a = NewParallelIdentityAssembler(parallelIdentityTestSelection(t, cc), 2)
			case "release":
				cc := c
				cc.Release = "0005"
				a = NewParallelIdentityAssembler(parallelIdentityTestSelection(t, cc), 2)
			case "choice":
				cc := c
				cc.ProjectCode = "QAAa"
				cc.Release = "9011"
				cc.Choice = "legacy19"
				a = NewParallelIdentityAssembler(parallelIdentityTestSelection(t, cc), 2)
			}
			r := a.DecodeParallelIdentity(i)
			if r.Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() || a.SnapshotParallelIdentity().HasSerial {
				t.Fatal("mixed identity", r)
			}
			a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 2))
			if a.SnapshotParallelIdentity().HasSerial {
				t.Fatal("cross-key completion")
			}
		})
	}
}
func TestCX10IdentityOrderingConflictsAndLastKnownGood(t *testing.T) {
	for _, sequence := range [][]byte{{1, 2}, {2}, {0, 2}, {0, 0, 1, 2}, {0, 1, 1, 2}, {0, 1, 3, 2}} {
		a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, parallelIdentityTestSpec(t)), 2)
		for n, f := range sequence {
			i := parallelIdentityTestFragment(t, parallelIdentityTestSpec(t), f)
			i.Evidence.Sequence = uint64(n + 1)
			i.Evidence.Monotonic = time.Duration(n+1) * time.Second
			a.DecodeParallelIdentity(i)
		}
		if a.SnapshotParallelIdentity().HasSerial {
			t.Fatalf("unordered completion %v", sequence)
		}
	}
	c := parallelIdentityTestSpec(t)
	a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
	for f := byte(0); f < 3; f++ {
		a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, f))
	}
	good := a.SnapshotParallelIdentity()
	for f := byte(0); f < 3; f++ {
		i := parallelIdentityTestFragment(t, c, f)
		p := i.Evidence.Frame.Bytes()
		p[1]++
		id, _ := canbus.NewStandardID(0x6c3)
		i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
		i.Evidence.Sequence += 3
		i.Evidence.Monotonic += 3 * time.Second
		r := a.DecodeParallelIdentity(i)
		if f == 2 && r.Status != "conflict" {
			t.Fatal(r)
		}
	}
	if a.SnapshotParallelIdentity() != good {
		t.Fatal("lost last-known-good serial")
	}
}
func TestCX10IdentitySequenceAndTimeNegative(t *testing.T) {
	for _, gate := range []string{"sequence", "time"} {
		c := parallelIdentityTestSpec(t)
		a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
		a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 0))
		i := parallelIdentityTestFragment(t, c, 1)
		if gate == "sequence" {
			i.Evidence.Sequence = 1
		} else {
			i.Evidence.Monotonic = 0
		}
		r := a.DecodeParallelIdentity(i)
		if r.Status != "stale" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal(r)
		}
	}
}
func TestCX10IdentityConcurrentSnapshot(t *testing.T) {
	c := parallelIdentityTestSpec(t)
	a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for n := 0; n < 50; n++ {
			a.SnapshotParallelIdentity()
		}
	}()
	go func() {
		defer wg.Done()
		for f := byte(0); f < 3; f++ {
			a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, f))
		}
	}()
	wg.Wait()
	if !a.SnapshotParallelIdentity().HasSerial {
		t.Fatal("missing serial")
	}
}

func TestCX10IdentityModuleAndFragmentBoundaries(t *testing.T) {
	for _, module := range []uint8{0, 15, 16} {
		c := parallelIdentityTestSpec(t)
		a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), module)
		for f := byte(0); f < 3; f++ {
			i := parallelIdentityTestFragment(t, c, f)
			p := i.Evidence.Frame.Bytes()
			p[0] = (module << 4) | f
			id, _ := canbus.NewStandardID(0x6c3)
			i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			a.DecodeParallelIdentity(i)
		}
		if a.SnapshotParallelIdentity().HasSerial != (module < 16) {
			t.Fatal("module boundary", module)
		}
	}
	for _, fragment := range []byte{2, 3, 15} {
		c := parallelIdentityTestSpec(t)
		a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
		a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 0))
		a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 1))
		r := a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, fragment))
		if a.SnapshotParallelIdentity().HasSerial != (fragment == 2) || (fragment > 2 && r.Status == "accepted") {
			t.Fatal("fragment boundary", fragment)
		}
	}
}
func TestCX10IdentityDuplicateConflict(t *testing.T) {
	c := parallelIdentityTestSpec(t)
	a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
	a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 0))
	i := parallelIdentityTestFragment(t, c, 0)
	p := i.Evidence.Frame.Bytes()
	p[2]++
	id, _ := canbus.NewStandardID(0x6c3)
	i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	i.Evidence.Sequence = 2
	i.Evidence.Monotonic = 2 * time.Second
	r := a.DecodeParallelIdentity(i)
	if r.Status != "conflict" || r.Raw != p || a.SnapshotParallelIdentity().HasSerial {
		t.Fatal(r)
	}
}

func TestCX10IdentityNativeGroupAndSlotConfusion(t *testing.T) {
	c := parallelIdentityTestSpec(t)
	c.Slot = "4"
	a := NewParallelIdentityAssembler(parallelIdentityTestSelection(t, c), 2)
	a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 0))
	i := parallelIdentityTestFragment(t, c, 1)
	p := i.Evidence.Frame.Bytes()
	id, _ := canbus.NewStandardID(0x6c4)
	i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	r := a.DecodeParallelIdentity(i)
	if r.Status == "accepted" || r.Raw != p {
		t.Fatal("slot used as group", r)
	}
	a.DecodeParallelIdentity(parallelIdentityTestFragment(t, c, 2))
	if a.SnapshotParallelIdentity().HasSerial {
		t.Fatal("cross-address assembly")
	}
}
