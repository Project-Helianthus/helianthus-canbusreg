package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func TestCX01IdentityAssemblyAndConflict(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	seq := uint64(0)
	feed := func(kind uint32, p [8]byte) NativeResult {
		seq++
		i := selectionTestInput(t, spec, kind, p)
		i.Evidence.Sequence = seq
		return a.DecodeLINKIdentity(i)
	}
	feed(0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	for j, p := range [][8]byte{{15, 1, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}} {
		if feed(0x18fb, p).Status != "accepted" {
			t.Fatalf("fragment %d", j)
		}
	}
	snap := a.SnapshotLINKIdentity()
	if !snap.HasSerial || snap.Serial[15] != 16 {
		t.Fatal(snap)
	}
	snap.Fields["fake"] = NativeField{}
	snap.Serial[0] = 99
	if a.SnapshotLINKIdentity().Serial[0] != 1 {
		t.Fatal("alias")
	}
	if _, ok := a.SnapshotLINKIdentity().Fields["fake"]; ok {
		t.Fatal("map alias")
	}
	feed(0x1805, [8]byte{1})
	prior := a.SnapshotLINKIdentity()
	if len(prior.Fields) == 0 {
		t.Fatal("no fields")
	}
	if feed(0x1802, [8]byte{'Q', 'A', 'A', 'a', '0', '0', '0', '6'}).Status != "conflict" {
		t.Fatal("case identity reuse")
	}
	after := a.SnapshotLINKIdentity()
	if after.HasSerial || after.HasIdentity || len(after.Fields) != 0 || len(prior.Fields) == 0 {
		t.Fatal("invalidation/history")
	}
}
func TestCX01SerialNegativeVectors(t *testing.T) {
	for _, gate := range []string{"orphan", "reorder", "interrupt", "length-low", "length-high", "padding", "duplicate", "conflicting-length", "l0", "unknown", "reserved-bit7"} {
		t.Run(gate, func(t *testing.T) {
			spec := selectionTestSpec(t, "QAAA", "0006")
			if gate == "l0" {
				spec.Release = "0002"
			}
			if gate == "unknown" {
				spec.ProjectCode = "QCAA"
				spec.Release = "0001"
			}
			a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
			seq := uint64(0)
			feed := func(k uint32, p [8]byte) NativeResult {
				seq++
				i := selectionTestInput(t, spec, k, p)
				i.Evidence.Sequence = seq
				return a.DecodeLINKIdentity(i)
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
			case "reserved-bit7":
				p[0] = 0xaf
			}
			r := feed(0x18fb, p)
			if a.SnapshotLINKIdentity().HasSerial || r.Raw != p || r.Status == "accepted" {
				t.Fatalf("admitted %#v", r)
			}
		})
	}
}
func TestCX01IdentityEpochRoleAndPartial(t *testing.T) {
	for _, gate := range []string{"epoch", "bad-ascii", "malformed", "source", "interface-name", "interface-index", "endpoint", "bank", "group", "slot", "scope", "direction"} {
		t.Run(gate, func(t *testing.T) {
			spec := selectionTestSpec(t, "QAAA", "0006")
			a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
			i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			a.DecodeLINKIdentity(i)
			i = selectionTestInput(t, spec, 0x1800, [8]byte{})
			i.Evidence.Sequence = 2
			a.DecodeLINKIdentity(i)
			i = selectionTestInput(t, spec, 0x1805, [8]byte{1})
			i.Evidence.Sequence = 3
			a.DecodeLINKIdentity(i)
			prior := a.SnapshotLINKIdentity()
			i.Evidence.Sequence = 4
			switch gate {
			case "epoch":
				i.Epoch++
			case "bad-ascii":
				i = selectionTestInput(t, spec, 0x1802, [8]byte{})
				i.Evidence.Sequence = 4
			case "malformed":
				id := i.Evidence.Frame.ID()
				i.Evidence.Frame, _ = selectionTestShortFrame(id)
			case "source":
				other := spec
				other.Source++
				i = selectionTestInput(t, other, 0x1805, [8]byte{1})
				i.Evidence.Sequence = 4
			case "interface-name":
				other := selectionTestSpec(t, "QAAA", "0006")
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
			r := a.DecodeLINKIdentity(i)
			if r.Status == "accepted" {
				t.Fatal("gate escaped")
			}
			if len(prior.Fields) == 0 {
				t.Fatal("history lost")
			}
			if gate == "bad-ascii" || gate == "malformed" || gate == "source" || gate == "interface-name" || gate == "interface-index" {
				if len(a.SnapshotLINKIdentity().Fields) == 0 {
					t.Fatal("last known good erased")
				}
			} else if len(a.SnapshotLINKIdentity().Fields) != 0 {
				t.Fatal("dependent fields not invalidated")
			}
		})
	}
}

func TestCX01ConcurrentSnapshots(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	done := make(chan bool, 4)
	for n := 0; n < 4; n++ {
		go func() {
			for j := 0; j < 50; j++ {
				a.SnapshotLINKIdentity()
				a.DecodeLINKIdentity(selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}))
			}
			done <- true
		}()
	}
	for n := 0; n < 4; n++ {
		<-done
	}
}
func TestCX01AssemblerBeforeIdentityAndStale(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	i := selectionTestInput(t, spec, 0x1805, [8]byte{1})
	r := a.DecodeLINKIdentity(i)
	if r.Fields["pack_voltage"].Status != "absent" || r.Fields["pack_voltage"].Unit != "" {
		t.Fatal("unidentified units")
	}
	i = selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	i.Evidence.Sequence = 2
	a.DecodeLINKIdentity(i)
	if a.DecodeLINKIdentity(i).Status != "stale" {
		t.Fatal("duplicate")
	}
	i.Evidence.Sequence = 3
	i.Evidence.Monotonic = 0
	if a.DecodeLINKIdentity(i).Status != "stale" {
		t.Fatal("time reversal")
	}
}

func TestCX01IdentityReleaseReuse(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	p := [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}
	a.DecodeLINKIdentity(selectionTestInput(t, spec, 0x1802, p))
	before := a.SnapshotLINKIdentity()
	p[7] = '7'
	i := selectionTestInput(t, spec, 0x1802, p)
	i.Evidence.Sequence = 2
	r := a.DecodeLINKIdentity(i)
	if r.Status != "conflict" || r.Raw != p || a.SnapshotLINKIdentity().HasIdentity || before.Release != "0006" {
		t.Fatal("release reassigned history")
	}
}

func TestCX01UnroutedFrameDoesNotInvalidate(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	a.DecodeLINKIdentity(i)
	i = selectionTestInput(t, spec, 0x1805, [8]byte{1})
	i.Evidence.Sequence = 2
	a.DecodeLINKIdentity(i)
	if len(a.SnapshotLINKIdentity().Fields) == 0 {
		t.Fatal("setup")
	}
	id, err := canbus.NewStandardID(uint32(spec.Source))
	if err != nil {
		t.Fatal(err)
	}
	f, err := canbus.NewFrame(id, make([]byte, 8))
	if err != nil {
		t.Fatal(err)
	}
	u := i
	u.Evidence.Frame = f
	u.Evidence.Sequence = 3
	u.Scope = "aggregate"
	u.Direction = "other"
	if r := a.DecodeLINKIdentity(u); r.Status == "accepted" {
		t.Fatal("unrelated frame admitted")
	}
	snap := a.SnapshotLINKIdentity()
	if snap.Status != "accepted" || !snap.HasIdentity || len(snap.Fields) == 0 {
		t.Fatal("unrelated frame erased state", snap.Status)
	}
}
func TestCX01DecoderConflictIsSticky(t *testing.T) {
	spec := selectionTestSpec(t, "QAAa", "9011")
	spec.Choice = "legacy19"
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'a', '9', '0', '1', '1'})
	a.DecodeLINKIdentity(i)
	if a.SnapshotLINKIdentity().Status != "accepted" {
		t.Fatal("setup")
	}
	i = selectionTestInput(t, spec, 0x1853, [8]byte{})
	i.Evidence.Sequence = 2
	if r := a.DecodeLINKIdentity(i); r.Status != "conflict" {
		t.Fatal(r.Status)
	}
	snap := a.SnapshotLINKIdentity()
	if snap.Status != "conflict" || snap.HasIdentity || len(snap.Fields) != 0 {
		t.Fatal("not invalidated", snap.Status)
	}
	i = selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'a', '9', '0', '1', '1'})
	i.Evidence.Sequence = 3
	if r := a.DecodeLINKIdentity(i); r.Status != "conflict" || a.SnapshotLINKIdentity().Status != "conflict" {
		t.Fatal("not sticky")
	}
}

func TestCX01UnrelatedTrafficKeepsSerialAssembly(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	seq := uint64(0)
	feed := func(kind uint32, p [8]byte) NativeResult {
		seq++
		i := selectionTestInput(t, spec, kind, p)
		i.Evidence.Sequence = seq
		return a.DecodeLINKIdentity(i)
	}
	feed(0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	frags := [][8]byte{{15, 1, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}}
	for j, p := range frags {
		if j == 1 {
			id, err := canbus.NewStandardID(uint32(spec.Source))
			if err != nil {
				t.Fatal(err)
			}
			f, err := canbus.NewFrame(id, make([]byte, 8))
			if err != nil {
				t.Fatal(err)
			}
			u := selectionTestInput(t, spec, 0x18fb, frags[0])
			u.Evidence.Frame = f
			u.Evidence.Sequence = seq + 1
			seq++
			a.DecodeLINKIdentity(u)
		}
		if feed(0x18fb, p).Status != "accepted" {
			t.Fatalf("fragment %d", j)
		}
	}
	if !a.SnapshotLINKIdentity().HasSerial {
		t.Fatal("serial lost to unrelated traffic")
	}
}

func TestCX01SerialChangeConflicts(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	seq := uint64(0)
	feed := func(kind uint32, p [8]byte) NativeResult {
		seq++
		i := selectionTestInput(t, spec, kind, p)
		i.Evidence.Sequence = seq
		return a.DecodeLINKIdentity(i)
	}
	feed(0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	for _, p := range [][8]byte{{15, 1, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}} {
		feed(0x18fb, p)
	}
	feed(0x1805, [8]byte{1})
	if len(a.SnapshotLINKIdentity().Fields) == 0 {
		t.Fatal("setup")
	}
	var last NativeResult
	for _, p := range [][8]byte{{15, 9, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}} {
		last = feed(0x18fb, p)
	}
	if last.Status != "conflict" {
		t.Fatal(last.Status)
	}
	s := a.SnapshotLINKIdentity()
	if s.HasSerial || s.HasIdentity || len(s.Fields) != 0 || s.Status != "conflict" {
		t.Fatal("stale state survived serial change")
	}
}

func TestCX01DecoderIgnoresF4ReportFrameForLegacySelection(t *testing.T) {
	spec := selectionTestSpec(t, "QAAa", "9011")
	spec.Choice = "legacy19"
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'a', '9', '0', '1', '1'})
	a.DecodeLINKIdentity(i)
	i = selectionTestInput(t, spec, 0x1869, [8]byte{})
	i.Evidence.Sequence = 2
	if r := a.DecodeLINKIdentity(i); r.Status == "conflict" {
		t.Fatal("F4 report frame raised conflict")
	}
	snap := a.SnapshotLINKIdentity()
	if snap.Status != "accepted" || !snap.HasIdentity {
		t.Fatal("identity erased", snap.Status)
	}
}

func TestCX01FlagsByteChangeRetainsState(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	seq := uint64(0)
	feed := func(kind uint32, p [8]byte) NativeResult {
		seq++
		i := selectionTestInput(t, spec, kind, p)
		i.Evidence.Sequence = seq
		return a.DecodeLINKIdentity(i)
	}
	feed(0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	for _, p := range [][8]byte{{15, 1, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}} {
		feed(0x18fb, p)
	}
	feed(0x1800, [8]byte{})
	feed(0x1805, [8]byte{1})
	r := feed(0x1800, [8]byte{0, 0x81})
	if r.Status != "accepted" {
		t.Fatal(r.Status)
	}
	if f := r.Fields["role_flags"]; f.Status != "raw" || f.Numerator != 0x81 || f.Unit != "" || f.Meaning != "" {
		t.Fatalf("flags not raw %#v", f)
	}
	s := a.SnapshotLINKIdentity()
	if s.Status != "accepted" || !s.HasIdentity || !s.HasSerial || s.Serial[15] != 16 {
		t.Fatal("flags change invalidated identity", s.Status)
	}
	if _, ok := s.Fields["1805/pack_voltage"]; !ok {
		t.Fatal("flags change erased telemetry")
	}
	if f := s.Fields["1800/role_flags"]; f.Numerator != 0x81 || f.Status != "raw" {
		t.Fatalf("raw flags not retained %#v", f)
	}
}

func TestCX01ObsoleteEvidenceDoesNotInvalidate(t *testing.T) {
	for _, gate := range []string{"older-epoch", "older-sequence", "reversed-time"} {
		t.Run(gate, func(t *testing.T) {
			spec := selectionTestSpec(t, "QAAA", "0006")
			spec.Epoch = 5
			a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
			i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			i.Evidence.Sequence = 10
			i.Evidence.Monotonic = 10 * time.Second
			a.DecodeLINKIdentity(i)
			i = selectionTestInput(t, spec, 0x1805, [8]byte{1})
			i.Evidence.Sequence = 11
			i.Evidence.Monotonic = 11 * time.Second
			a.DecodeLINKIdentity(i)
			if len(a.SnapshotLINKIdentity().Fields) == 0 {
				t.Fatal("setup")
			}
			d := selectionTestInput(t, spec, 0x1805, [8]byte{1})
			d.Evidence.Sequence = 12
			d.Evidence.Monotonic = 12 * time.Second
			switch gate {
			case "older-epoch":
				d.Epoch = 4
			case "older-sequence":
				d.Epoch = 4
				d.Evidence.Sequence = 3
			case "reversed-time":
				d.Bank = "b"
				d.Evidence.Monotonic = time.Second
			}
			if r := a.DecodeLINKIdentity(d); r.Status != "stale" {
				t.Fatal(r.Status)
			}
			s := a.SnapshotLINKIdentity()
			if s.Status != "accepted" || !s.HasIdentity || len(s.Fields) == 0 {
				t.Fatal("obsolete evidence invalidated state", s.Status)
			}
			n := selectionTestInput(t, spec, 0x1805, [8]byte{2})
			n.Evidence.Sequence = 13
			n.Evidence.Monotonic = 13 * time.Second
			if r := a.DecodeLINKIdentity(n); r.Status != "accepted" {
				t.Fatal("current evidence rejected", r.Status)
			}
			n.Epoch = 6
			n.Evidence.Sequence = 14
			if r := a.DecodeLINKIdentity(n); r.Status != "conflict" || len(a.SnapshotLINKIdentity().Fields) != 0 {
				t.Fatal("newer epoch did not invalidate", r.Status)
			}
		})
	}
}

func TestCX01IdentityAssemblerRejectsCoordinatorSource(t *testing.T) {
	spec := selectionTestSpec(t, "QAAA", "0006")
	spec.Source = 0xf4
	a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
	a.DecodeLINKIdentity(selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}))
	i := selectionTestInput(t, spec, 0x1805, [8]byte{1})
	i.Evidence.Sequence = 2
	r := a.DecodeLINKIdentity(i)
	if r.Status == "accepted" || len(r.Fields) != 0 {
		t.Fatal("coordinator source admitted as module telemetry")
	}
	if s := a.SnapshotLINKIdentity(); s.HasIdentity || len(s.Fields) != 0 {
		t.Fatal("coordinator source established identity")
	}
}

func TestCX01NewerEpochWithResetCountersInvalidates(t *testing.T) {
	for _, reset := range []string{"sequence", "monotonic", "both"} {
		t.Run(reset, func(t *testing.T) {
			spec := selectionTestSpec(t, "QAAA", "0006")
			spec.Epoch = 5
			a := NewLINKIdentityAssembler(selectionTestSelect(t, spec))
			i := selectionTestInput(t, spec, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			i.Evidence.Sequence = 10
			i.Evidence.Monotonic = 10 * time.Second
			a.DecodeLINKIdentity(i)
			i = selectionTestInput(t, spec, 0x1805, [8]byte{1})
			i.Evidence.Sequence = 11
			i.Evidence.Monotonic = 11 * time.Second
			a.DecodeLINKIdentity(i)
			if len(a.SnapshotLINKIdentity().Fields) == 0 {
				t.Fatal("setup")
			}
			// A new session restarts its counters: the newer epoch must not be
			// mistaken for a replayed sequence or reversed time.
			n := selectionTestInput(t, spec, 0x1805, [8]byte{2})
			n.Epoch = 6
			n.Evidence.Sequence = 12
			n.Evidence.Monotonic = 12 * time.Second
			switch reset {
			case "sequence":
				n.Evidence.Sequence = 1
			case "monotonic":
				n.Evidence.Monotonic = time.Second
			case "both":
				n.Evidence.Sequence = 1
				n.Evidence.Monotonic = time.Second
			}
			r := a.DecodeLINKIdentity(n)
			s := a.SnapshotLINKIdentity()
			if r.Status != "conflict" || s.Status != "conflict" || s.HasIdentity || len(s.Fields) != 0 {
				t.Fatalf("newer epoch with reset counters retained state: result %s, snapshot %s", r.Status, s.Status)
			}
		})
	}
}
