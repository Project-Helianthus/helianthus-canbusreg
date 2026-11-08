package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"sync"
	"testing"
	"time"
)

func cx10Fragment(t *testing.T, c CX01SelectionSpec, fragment byte) CX01LINKInput {
	t.Helper()
	p := [8]byte{0x20 | fragment, 1 + fragment*7, 2 + fragment*7, 3 + fragment*7, 4 + fragment*7, 5 + fragment*7, 6 + fragment*7, 7 + fragment*7}
	i := cx10Input(t, c, 0x6c3, p)
	i.Evidence.Sequence = uint64(fragment) + 1
	i.Evidence.Monotonic = time.Duration(fragment+1) * time.Second
	return i
}
func TestCX10IdentityOrderedUnspecifiedTail(t *testing.T) {
	c := cx10Spec(t)
	a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
	for f := byte(0); f < 3; f++ {
		i := cx10Fragment(t, c, f)
		r := a.CX10Decode(i)
		if r.Status != "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal(r)
		}
		if a.CX10Snapshot().HasSerial != (f == 2) {
			t.Fatal("partial inventory")
		}
	}
	s := a.CX10Snapshot()
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
			c := cx10Spec(t)
			a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
			a.CX10Decode(cx10Fragment(t, c, 0))
			i := cx10Fragment(t, c, 1)
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
				a = CX10NewParallelIdentityAssembler(cx10Selection(t, cc), 2)
			case "project-code":
				cc := c
				cc.ProjectCode = "qaab"
				a = CX10NewParallelIdentityAssembler(cx10Selection(t, cc), 2)
			case "release":
				cc := c
				cc.Release = "0005"
				a = CX10NewParallelIdentityAssembler(cx10Selection(t, cc), 2)
			case "choice":
				cc := c
				cc.ProjectCode = "QAAa"
				cc.Release = "9011"
				cc.Choice = "legacy19"
				a = CX10NewParallelIdentityAssembler(cx10Selection(t, cc), 2)
			}
			r := a.CX10Decode(i)
			if r.Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() || a.CX10Snapshot().HasSerial {
				t.Fatal("mixed identity", r)
			}
			a.CX10Decode(cx10Fragment(t, c, 2))
			if a.CX10Snapshot().HasSerial {
				t.Fatal("cross-key completion")
			}
		})
	}
}
func TestCX10IdentityOrderingConflictsAndLastKnownGood(t *testing.T) {
	for _, sequence := range [][]byte{{1, 2}, {2}, {0, 2}, {0, 0, 1, 2}, {0, 1, 1, 2}, {0, 1, 3, 2}} {
		a := CX10NewParallelIdentityAssembler(cx10Selection(t, cx10Spec(t)), 2)
		for n, f := range sequence {
			i := cx10Fragment(t, cx10Spec(t), f)
			i.Evidence.Sequence = uint64(n + 1)
			i.Evidence.Monotonic = time.Duration(n+1) * time.Second
			a.CX10Decode(i)
		}
		if a.CX10Snapshot().HasSerial {
			t.Fatalf("unordered completion %v", sequence)
		}
	}
	c := cx10Spec(t)
	a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
	for f := byte(0); f < 3; f++ {
		a.CX10Decode(cx10Fragment(t, c, f))
	}
	good := a.CX10Snapshot()
	for f := byte(0); f < 3; f++ {
		i := cx10Fragment(t, c, f)
		p := i.Evidence.Frame.Bytes()
		p[1]++
		id, _ := canbus.NewStandardID(0x6c3)
		i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
		i.Evidence.Sequence += 3
		i.Evidence.Monotonic += 3 * time.Second
		r := a.CX10Decode(i)
		if f == 2 && r.Status != "conflict" {
			t.Fatal(r)
		}
	}
	if a.CX10Snapshot() != good {
		t.Fatal("lost last-known-good serial")
	}
}
func TestCX10IdentitySequenceAndTimeNegative(t *testing.T) {
	for _, gate := range []string{"sequence", "time"} {
		c := cx10Spec(t)
		a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
		a.CX10Decode(cx10Fragment(t, c, 0))
		i := cx10Fragment(t, c, 1)
		if gate == "sequence" {
			i.Evidence.Sequence = 1
		} else {
			i.Evidence.Monotonic = 0
		}
		r := a.CX10Decode(i)
		if r.Status != "stale" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal(r)
		}
	}
}
func TestCX10IdentityConcurrentSnapshot(t *testing.T) {
	c := cx10Spec(t)
	a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for n := 0; n < 50; n++ {
			a.CX10Snapshot()
		}
	}()
	go func() {
		defer wg.Done()
		for f := byte(0); f < 3; f++ {
			a.CX10Decode(cx10Fragment(t, c, f))
		}
	}()
	wg.Wait()
	if !a.CX10Snapshot().HasSerial {
		t.Fatal("missing serial")
	}
}

func TestCX10IdentityModuleAndFragmentBoundaries(t *testing.T) {
	for _, module := range []uint8{0, 15, 16} {
		c := cx10Spec(t)
		a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), module)
		for f := byte(0); f < 3; f++ {
			i := cx10Fragment(t, c, f)
			p := i.Evidence.Frame.Bytes()
			p[0] = (module << 4) | f
			id, _ := canbus.NewStandardID(0x6c3)
			i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			a.CX10Decode(i)
		}
		if a.CX10Snapshot().HasSerial != (module < 16) {
			t.Fatal("module boundary", module)
		}
	}
	for _, fragment := range []byte{2, 3, 15} {
		c := cx10Spec(t)
		a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
		a.CX10Decode(cx10Fragment(t, c, 0))
		a.CX10Decode(cx10Fragment(t, c, 1))
		r := a.CX10Decode(cx10Fragment(t, c, fragment))
		if a.CX10Snapshot().HasSerial != (fragment == 2) || (fragment > 2 && r.Status == "accepted") {
			t.Fatal("fragment boundary", fragment)
		}
	}
}
func TestCX10IdentityDuplicateConflict(t *testing.T) {
	c := cx10Spec(t)
	a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
	a.CX10Decode(cx10Fragment(t, c, 0))
	i := cx10Fragment(t, c, 0)
	p := i.Evidence.Frame.Bytes()
	p[2]++
	id, _ := canbus.NewStandardID(0x6c3)
	i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	i.Evidence.Sequence = 2
	i.Evidence.Monotonic = 2 * time.Second
	r := a.CX10Decode(i)
	if r.Status != "conflict" || r.Raw != p || a.CX10Snapshot().HasSerial {
		t.Fatal(r)
	}
}

func TestCX10IdentityNativeGroupAndSlotConfusion(t *testing.T) {
	c := cx10Spec(t)
	c.Slot = "4"
	a := CX10NewParallelIdentityAssembler(cx10Selection(t, c), 2)
	a.CX10Decode(cx10Fragment(t, c, 0))
	i := cx10Fragment(t, c, 1)
	p := i.Evidence.Frame.Bytes()
	id, _ := canbus.NewStandardID(0x6c4)
	i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	r := a.CX10Decode(i)
	if r.Status == "accepted" || r.Raw != p {
		t.Fatal("slot used as group", r)
	}
	a.CX10Decode(cx10Fragment(t, c, 2))
	if a.CX10Snapshot().HasSerial {
		t.Fatal("cross-address assembly")
	}
}
