package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCX15RawRetentionAndLastKnownGood(t *testing.T) {
	store, err := NewCX15OpaqueStore(2)
	if err != nil {
		t.Fatal(err)
	}
	s, c, i := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", 0x1800)
	if len(store.LastKnownGood()) != 0 || len(store.RawSnapshot()) != 0 {
		t.Fatal("initial absent state")
	}
	if !store.Observe(s, c, i).Admitted {
		t.Fatal("valid raw zero-safe record")
	}
	sibling := cx15Input(t, s, c, 0x1850, [8]byte{})
	sibling.Evidence.Sequence = 2
	if !store.Observe(s, c, sibling).Admitted {
		t.Fatal("zero sibling")
	}
	for _, gate := range []string{"stale-sequence", "stale-time", "invalid-dlc", "wrong-endpoint", "negative-age", "older-epoch", "newer-epoch"} {
		t.Run(gate, func(t *testing.T) {
			bad := i
			bad.Evidence.Sequence = 3
			switch gate {
			case "stale-sequence":
				bad.Evidence.Sequence = 1
			case "stale-time":
				bad.Evidence.Monotonic = 0
			case "invalid-dlc":
				p := i.Evidence.Frame.Bytes()
				bad.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
			case "wrong-endpoint":
				bad.Endpoint = "other"
			case "negative-age":
				bad.FieldAge = -1
			case "older-epoch":
				bad.Epoch--
			case "newer-epoch":
				bad.Epoch++
			}
			r := store.Observe(s, c, bad)
			if r.Admitted || r.Input != bad || r.Raw != bad.Evidence.Frame.Bytes() || r.PhysicalQualified {
				t.Fatal(r)
			}
			good := store.LastKnownGood()
			if len(good) != 2 || good[0].Input != i || good[1].Input != sibling {
				t.Fatal("rejection erased independent value", good)
			}
			if gate == "stale-sequence" || gate == "stale-time" {
				if r.Status != "stale" {
					t.Fatal(r)
				}
			}
		})
	}
	raw := store.RawSnapshot()
	if len(raw) != 2 {
		t.Fatal("unbounded journal")
	}
	raw[0].Raw[0] ^= 0xff
	if reflect.DeepEqual(raw, store.RawSnapshot()) {
		t.Fatal("snapshot alias")
	}
	good := store.LastKnownGood()
	good[0].Input.Endpoint = "edited"
	if store.LastKnownGood()[0].Input.Endpoint != i.Endpoint {
		t.Fatal("LKG alias")
	}
	newer := i
	newer.Evidence.Sequence = 4
	newer.Evidence.Monotonic += time.Second
	if !store.Observe(s, c, newer).Admitted || store.LastKnownGood()[0].Input != newer {
		t.Fatal("fresh record not retained")
	}
}

func TestCX15RetentionBoundsAndConcurrency(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 256, 257} {
		_, err := NewCX15OpaqueStore(n)
		if (err == nil) != (n >= 1 && n <= 256) {
			t.Fatal(n, err)
		}
	}
	store, _ := NewCX15OpaqueStore(2)
	s, c, i := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", 0x1800)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				ii := i
				ii.Evidence.Sequence = uint64(n*10 + k + 1)
				store.Observe(s, c, ii)
				store.RawSnapshot()
				store.LastKnownGood()
				key := CX15CapabilityKey{s, c, uint32(n*10 + k)}
				if err := store.AssertCapability(key, CX15Observed); err != nil {
					t.Error(err)
				}
				store.Capability(key)
			}
		}(worker)
	}
	wg.Wait()
	if len(store.RawSnapshot()) > 2 || len(store.LastKnownGood()) > 2 || len(store.capabilities) > 2 {
		t.Fatal("retention overflow")
	}
	for _, pid := range []uint32{0x1802, 0x1803, 0x1804} {
		ii := cx15Input(t, s, c, pid, [8]byte{})
		store.Observe(s, c, ii)
	}
	if len(store.LastKnownGood()) != 2 {
		t.Fatal("LKG not bounded")
	}
}

func TestCX15CapabilityEveryKeyFieldIndependent(t *testing.T) {
	s, c, _ := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", 0x1800)
	base := CX15CapabilityKey{s, c, 0x1800}
	// Each named field mutation catches a production equality that omits that
	// field from the capability key. No input/output inference is permitted.
	cases := map[string]func(*CX15CapabilityKey){
		"PID":              func(k *CX15CapabilityKey) { k.PID++ },
		"selection.group":  func(k *CX15CapabilityKey) { k.Selection.group = "L7" },
		"selection.layout": func(k *CX15CapabilityKey) { k.Selection.layout = "B" },
		"selection.grade":  func(k *CX15CapabilityKey) { k.Selection.grade = "Unknown" },
		"interface.name":   func(k *CX15CapabilityKey) { k.Selection.spec.Interface, _ = canbus.NewInterfaceIdentity("other", 1) },
		"interface.index": func(k *CX15CapabilityKey) {
			k.Selection.spec.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
		},
	}
	// Walk every exported identity and context field so new fields cannot silently
	// escape this test. Interface identity has separate name and index vectors.
	specType := reflect.TypeOf(s.spec)
	for n := 0; n < specType.NumField(); n++ {
		field := specType.Field(n)
		if field.Name == "Interface" {
			continue
		}
		name := field.Name
		cases["selection."+name] = func(k *CX15CapabilityKey) {
			cx15MutateField(t, reflect.ValueOf(&k.Selection.spec).Elem().FieldByName(name))
		}
	}
	contextType := reflect.TypeOf(c)
	for n := 0; n < contextType.NumField(); n++ {
		name := contextType.Field(n).Name
		cases["context."+name] = func(k *CX15CapabilityKey) { cx15MutateField(t, reflect.ValueOf(&k.Context).Elem().FieldByName(name)) }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store, _ := NewCX15OpaqueStore(4)
			for _, state := range []CX15CapabilityState{CX15Unknown, CX15Observed, CX15Qualified, CX15Unsupported} {
				if err := store.AssertCapability(base, state); err != nil {
					t.Fatal(err)
				}
				if store.Capability(base) != state {
					t.Fatal(state)
				}
			}
			if err := store.AssertCapability(base, CX15Qualified); err != nil {
				t.Fatal(err)
			}
			other := base
			mutate(&other)
			if other == base || store.Capability(other) != CX15Unknown {
				t.Fatal("identity key collapsed", name)
			}
			if err := store.AssertCapability(other, CX15Unsupported); err != nil {
				t.Fatal(err)
			}
			if store.Capability(base) != CX15Qualified || store.Capability(other) != CX15Unsupported {
				t.Fatal("capability crossed key", name)
			}
			if store.AssertCapability(base, "invented") == nil {
				t.Fatal("invalid capability accepted")
			}
		})
	}
}
func cx15MutateField(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-other")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Uint8, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	default:
		t.Fatal("uncovered key kind", v.Kind())
	}
}

func TestCX15AdmissionEveryEvidenceField(t *testing.T) {
	s, c, i := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", 0x1800)
	for _, field := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "IDE", "DLC", "raw-DLC", "age", "sequence", "monotonic"} {
		t.Run(field, func(t *testing.T) {
			ii := i
			switch field {
			case "interface-name":
				ii.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				ii.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				ii.Epoch++
			case "direction":
				ii.Direction = "opposite"
			case "scope":
				ii.Scope = "aggregate"
			case "bank":
				ii.Bank = "other"
			case "endpoint":
				ii.Endpoint = "other"
			case "group":
				ii.Group = "other"
			case "slot":
				ii.Slot = "other"
			case "destination", "source":
				spec := s.spec
				if field == "destination" {
					spec.Destination++
				} else {
					spec.Source++
				}
				ss := cx01Select(t, spec)
				ii = cx15Input(t, ss, c, 0x1800, i.Evidence.Frame.Bytes())
			case "IDE":
				id, _ := canbus.NewStandardID(0x301)
				p := i.Evidence.Frame.Bytes()
				ii.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "DLC":
				p := i.Evidence.Frame.Bytes()
				ii.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
			case "raw-DLC":
				p := i.Evidence.Frame.Bytes()
				record := [16]byte{}
				binary.NativeEndian.PutUint32(record[:4], i.Evidence.Frame.ID().Value()|0x80000000)
				record[4] = 8
				record[7] = 9
				copy(record[8:], p[:])
				var err error
				ii.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
			case "age":
				ii.FieldAge = -1
			case "sequence":
				ii.Evidence.Sequence = 0
			case "monotonic":
				ii.Evidence.Monotonic = -1
			}
			r := CX15ClassifyReceiver(s, c, ii)
			if r.Admitted || r.Input != ii || r.Raw != ii.Evidence.Frame.Bytes() {
				t.Fatal("evidence gate omitted", r)
			}
		})
	}
}

func TestCX15MONRequiresIndependentAssociationAndNoDecoder(t *testing.T) {
	// These arithmetic examples are raw ID constructions, never native admission.
	for _, bcu := range []uint32{0, 1, 254, 255} {
		for _, bmu := range []uint32{0, 1, 14, 15} {
			system := (uint32(0x188abcde) & 0xffffff00) | bcu
			module := (uint32(0x188abcde) & 0xff0fff00) | (bmu << 20) | bcu
			for _, idValue := range []uint32{system, module} {
				s, c, i := cx15Fixture(t, 8, "link", "service-to-module", "module", 0x188a)
				id, _ := canbus.NewExtendedID(idValue)
				p := [8]byte{0x30, 0x75, 0x90, 1, 0xff, 0, 0x80, 1}
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
				c.Profile = "mon"
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted || r.Class != "U" || r.Grade != "Unknown" || r.Raw != p {
					t.Fatal("MON inferred from native identity/shared PID", r)
				}
			}
		}
	}
	for _, family := range []uint32{0x0400, 0x0800, 0x1000, 0x1400} {
		t.Run(cx15PIDName(family), func(t *testing.T) {
			s, c, i := cx15Fixture(t, 8, "link", "service-to-module", "module", family)
			if CX15ClassifyReceiver(s, c, i).Admitted {
				t.Fatal("MON decoder selected")
			}
		})
	}
}

func TestCX15ReceiveOnlySurface(t *testing.T) {
	for _, path := range []string{"growatt_project_capabilities.go", "growatt_project_opaque.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if name != "fmt" && name != "sync" {
				t.Fatal("receive-only product gained an I/O dependency", name)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.SendStmt, *ast.GoStmt, *ast.ChanType, *ast.InterfaceType:
				t.Fatal("receive-only product gained an active/callback surface", path)
			}
			return true
		})
	}
}

func TestCX15ExplicitUnsupportedCannotOverrideRawRetention(t *testing.T) {
	store, _ := NewCX15OpaqueStore(4)
	s, c, i := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", 0x1800)
	key := CX15CapabilityKey{s, c, 0x1800}
	if !store.Observe(s, c, i).Admitted || store.Capability(key) != CX15Observed {
		t.Fatal("observation missing")
	}
	if err := store.AssertCapability(key, CX15Unsupported); err != nil {
		t.Fatal(err)
	}
	i.Evidence.Sequence++
	if r := store.Observe(s, c, i); r.Admitted || r.Status != "unsupported" || r.Input != i {
		t.Fatal(r)
	}
	if len(store.LastKnownGood()) != 1 || store.LastKnownGood()[0].Input.Evidence.Sequence != 1 {
		t.Fatal("unsupported erased raw LKG")
	}
	// An external qualified assertion cannot override an absent table entry.
	key.PID = 0x1801
	if err := store.AssertCapability(key, CX15Qualified); err != nil {
		t.Fatal(err)
	}
	ii := cx15Input(t, s, c, 0x1801, [8]byte{})
	if store.Observe(s, c, ii).Admitted {
		t.Fatal("qualification manufactured a receiver")
	}
	var zero CX15OpaqueStore
	if zero.Observe(s, c, i).Admitted || zero.AssertCapability(key, CX15Observed) == nil {
		t.Fatal("zero store admitted without a bound")
	}
}

func TestCX15DirectStatusPreservesPeriodicAlarmEvidence(t *testing.T) {
	store, _ := NewCX15OpaqueStore(4)
	s, c, _ := cx15Fixture(t, 8, "pcs-standard", "bms-to-controller", "controller", 0x3f7)
	periodic := cx15Input(t, s, c, 0x3f7, [8]byte{1, 2, 0, 0, 0, 1, 7, 8})
	if !store.Observe(s, c, periodic).Admitted {
		t.Fatal("periodic")
	}
	c.Form = "direct"
	direct := cx15Input(t, s, c, 0x3f7, [8]byte{1, 2})
	direct.Evidence.Sequence = 2
	if !store.Observe(s, c, direct).Admitted {
		t.Fatal("direct")
	}
	good := store.LastKnownGood()
	if len(good) != 2 || good[0].Raw[5] != 1 || good[0].Input != periodic || good[1].Input != direct {
		t.Fatal("direct response erased independent periodic alarm evidence", good)
	}
}

func TestCX15ParallelZeroTailAndOpaqueCountBoundaries(t *testing.T) {
	s, c, i := cx15Fixture(t, 8, "pcs-parallel", "controller-to-participant", "participant", 0x6f0)
	c.Session = "parallel"
	if !CX15ClassifyReceiver(s, c, i).Admitted {
		t.Fatal("zero tail")
	}
	for at := 2; at < 8; at++ {
		p := i.Evidence.Frame.Bytes()
		p[at] = 1
		ii := cx15Input(t, s, c, 0x6f0, p)
		if r := CX15ClassifyReceiver(s, c, ii); r.Admitted || r.Status != "invalid" || r.Raw != p {
			t.Fatal("6F0 nonzero tail interpreted", at, r)
		}
	}
	s, c, _ = cx15Fixture(t, 8, "pcs-parallel", "participant-to-coordinator", "coordinator", 0x6ff)
	c.Session = "parallel"
	for _, count := range []byte{0, 1, 254, 255} {
		for _, peer := range []uint16{0, 1, 65534, 65535} {
			p := [8]byte{count, 9, byte(peer >> 8), byte(peer), 10, 11, 12, 13}
			ii := cx15Input(t, s, c, 0x6ff, p)
			r := CX15ClassifyReceiver(s, c, ii)
			if !r.Admitted || r.Raw != p || r.Input != ii || r.PhysicalQualified {
				t.Fatal("raw count/peer was changed", r)
			}
		}
	}
}
