package canbusreg

import (
	"context"
	"errors"
	"fmt"
	"github.com/Project-Helianthus/helianthus-canbus"
	"sync"
	"testing"
	"time"
)

type cx17Event struct {
	observation CX17Observation
	done        chan struct{}
}
type cx17Fake struct {
	events  chan cx17Event
	pending chan struct{}
}

func cx17Source() *cx17Fake { return &cx17Fake{events: make(chan cx17Event)} }
func (s *cx17Fake) CX17Receive(ctx context.Context) (CX17Observation, error) {
	if s.pending != nil {
		close(s.pending)
		s.pending = nil
	}
	select {
	case e := <-s.events:
		s.pending = e.done
		return e.observation, nil
	case <-ctx.Done():
		return CX17Observation{}, errors.New("synthetic cancellation")
	}
}
func cx17Send(t *testing.T, s *cx17Fake, p *CX17Provider, g uint64, i CX16ReplayInput) {
	t.Helper()
	e := cx17Event{observation: CX17Observation{Generation: g, Input: i}, done: make(chan struct{})}
	select {
	case s.events <- e:
	case <-time.After(time.Second):
		t.Fatal("receive blocked")
	}
	select {
	case <-e.done:
	case <-time.After(time.Second):
		t.Fatal("publication blocked")
	}
}
func TestCX17Lifecycle(t *testing.T) {
	s, i := cx16Fixture(t)
	p, err := CX17NewPCSProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	if p.CX17Snapshot(time.Second, time.Second).State != "stopped" {
		t.Fatal("constructor active")
	}
	a := cx17Source()
	for _, g := range []uint64{0} {
		if p.Start(a, g) == nil {
			t.Fatal("zero generation")
		}
	}
	if p.Start(nil, 1) == nil {
		t.Fatal("nil source")
	}
	if err = p.Start(a, 1); err != nil {
		t.Fatal(err)
	}
	cx17Send(t, a, p, 1, i)
	before := p.CX17Snapshot(time.Second, time.Second)
	if before.Current["core/voltage"].Numerator != 5310 || !before.Promotable {
		t.Fatal(before)
	}
	if p.Start(a, 1) == nil {
		t.Fatal("reused generation")
	}
	b := cx17Source()
	if err = p.Start(b, 2); err != nil {
		t.Fatal(err)
	}
	if got := p.CX17Snapshot(time.Second, time.Second); got.Promotable || got.State != "awaiting" {
		t.Fatal(got)
	}
	cx17Send(t, b, p, 1, i)
	if p.CX17Snapshot(time.Second, time.Second).Rejected != 1 {
		t.Fatal("retired generation")
	}
	cx17Send(t, b, p, 2, i)
	if !p.CX17Snapshot(time.Second, time.Second).Promotable {
		t.Fatal("successor not active")
	}
	p.Stop()
	p.Stop()
	got := p.CX17Snapshot(time.Second, time.Second)
	if got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || len(got.Raw) == 0 || got.State != "stopped" {
		t.Fatal(got)
	}
}
func TestCX17IndependentAgesAndCopies(t *testing.T) {
	s, i := cx16Fixture(t)
	p, _ := CX17NewPCSProvider(s)
	a := cx17Source()
	p.Start(a, 1)
	defer p.Stop()
	i.PCS.FieldAges = map[string]time.Duration{"voltage": 0, "current": time.Second}
	cx17Send(t, a, p, 1, i)
	got := p.CX17Snapshot(2*time.Second, time.Second)
	if got.Current["core/voltage"].Status != "accepted" || got.Current["core/current"].Status != "stale" {
		t.Fatal(got)
	}
	got.Current["core/voltage"] = CXNativeField{}
	got.LastGood["core/voltage"] = CXNativeField{}
	got.Raw[0].Status = "altered"
	got = p.CX17Snapshot(2*time.Second, time.Second)
	if got.Current["core/voltage"].Numerator != 5310 || got.LastGood["core/voltage"].Numerator != 5310 || got.Raw[0].Status == "altered" {
		t.Fatal("alias")
	}
	i.PCS.Evidence.Sequence = 2
	i.PCS.Evidence.Monotonic = 2 * time.Second
	i.PCS.FieldStates = map[string]string{"voltage": "invalid"}
	cx17Send(t, a, p, 1, i)
	got = p.CX17Snapshot(2*time.Second, 2*time.Second)
	if got.Current["core/voltage"].Status != "invalid" || got.LastGood["core/voltage"].Evidence.Sequence != 1 || got.Current["core/current"].Evidence.Sequence != 2 {
		t.Fatal("partial retention", got)
	}
}
func TestCX17IdentityInvalidationNegatives(t *testing.T) {
	for _, name := range []string{"epoch", "epoch-zero", "role", "profile", "publisher", "bank", "endpoint", "group", "slot", "scope"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			p, _ := CX17NewPCSProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			cx17Send(t, a, p, 1, i)
			bad := i
			bad.PCS.Evidence.Sequence = 2
			switch name {
			case "epoch":
				bad.PCS.Epoch++
			case "epoch-zero":
				bad.PCS.Epoch = 0
			case "role":
				bad.PCS.Direction = "other"
			case "profile":
				c := s.CX01Spec()
				c.Release = "0011"
				bad.Selection, _ = NewCXSelection(c)
			case "publisher":
				bad.PCS.PublisherResolved = false
			case "bank":
				bad.PCS.Bank = "other"
			case "endpoint":
				bad.PCS.Endpoint = "other"
			case "group":
				bad.PCS.Group = "other"
			case "slot":
				bad.PCS.Slot = "other"
			case "scope":
				bad.PCS.Scope = "aggregate"
			}
			cx17Send(t, a, p, 1, bad)
			got := p.CX17Snapshot(time.Second, time.Second)
			if got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || got.Raw[len(got.Raw)-1].Input.Evidence.Frame != i.PCS.Evidence.Frame {
				t.Fatal(got)
			}
			i.PCS.Evidence.Sequence = 3
			cx17Send(t, a, p, 1, i)
			if p.CX17Snapshot(time.Second, time.Second).Promotable {
				t.Fatal("identity auto-recovered")
			}
		})
	}
}
func TestCX17SequenceAndClockBoundaries(t *testing.T) {
	for _, name := range []string{"zero", "equal", "lower", "clock", "interface-name", "interface-index"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			p, _ := CX17NewPCSProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			i.PCS.Evidence.Sequence = 2
			cx17Send(t, a, p, 1, i)
			bad := i
			switch name {
			case "zero":
				bad.PCS.Evidence.Sequence = 0
			case "equal":
			case "lower":
				bad.PCS.Evidence.Sequence = 1
			case "clock":
				bad.PCS.Evidence.Sequence = 3
				bad.PCS.Evidence.Monotonic = 0
			case "interface-name":
				bad.PCS.Evidence.Interface = cx17Interface("other", 1)
			case "interface-index":
				bad.PCS.Evidence.Interface = cx17Interface("synthetic", 2)
			}
			cx17Send(t, a, p, 1, bad)
			got := p.CX17Snapshot(time.Second, time.Second)
			if got.Rejected != 1 || got.LastGood["core/voltage"].Evidence.Sequence != 2 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17BoundsAndConcurrentSnapshots(t *testing.T) {
	s, i := cx16Fixture(t)
	p, _ := CX17NewPCSProvider(s)
	a := cx17Source()
	p.Start(a, 1)
	defer p.Stop()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				snap := p.CX17Snapshot(time.Second, time.Second)
				snap.Current["invented"] = CXNativeField{}
			}
		}()
	}
	for n := 1; n <= 257; n++ {
		i.PCS.Evidence.Sequence = uint64(n)
		cx17Send(t, a, p, 1, i)
	}
	wg.Wait()
	got := p.CX17Snapshot(time.Second, time.Second)
	if len(got.Raw) != 256 || got.EvictedRaw != 1 || got.Raw[0].Input.Evidence.Sequence != 2 || len(got.LastGood) > 256 {
		t.Fatal("bound", len(got.Raw), got.EvictedRaw)
	}
}
func TestCX17LINKCountsAndValidityNegatives(t *testing.T) {
	for _, count := range []int{0, 1, 16, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, i := cx16LINK(t, 0x1810, [8]byte{1, 0, 2, 0, 3, 0, 4, 0})
			p, err := CX17NewLINKProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			i.CellCount = count
			i.CellCountValid = true
			for n := range i.CellGates {
				i.CellGates[n] = CX12MeasurementGate{Configured: true, Valid: true}
			}
			cx17Send(t, a, p, 1, i)
			got := p.CX17Snapshot(time.Second, time.Second)
			if (got.Current["cells/cell_1"].Status == "accepted") != (count >= 1 && count <= 16) || got.PhysicalQualified || len(got.Raw) != 1 {
				t.Fatal(got)
			}
		})
	}
}

func cx17Interface(name string, index int) canbus.InterfaceIdentity {
	i, _ := canbus.NewInterfaceIdentity(name, index)
	return i
}

type cx17LateSource struct {
	entered, canceled, release chan struct{}
	fail                       bool
}

func (s *cx17LateSource) CX17Receive(ctx context.Context) (CX17Observation, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	if s.fail {
		return CX17Observation{}, errors.New("late synthetic failure")
	}
	return CX17Observation{}, nil
}
func TestCX17CancelJoinAndLateCallback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, i := cx16Fixture(t)
			p, _ := CX17NewPCSProvider(s)
			a := &cx17LateSource{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), fail: fail}
			p.Start(a, 1)
			<-a.entered
			b := cx17Source()
			finished := make(chan error, 1)
			go func() { finished <- p.Start(b, 2) }()
			select {
			case <-a.canceled:
			case <-time.After(time.Second):
				t.Fatal("not canceled")
			}
			if got := p.CX17Snapshot(time.Second, time.Second); got.State != "stopped" || got.Promotable {
				t.Fatal(got)
			}
			select {
			case err := <-finished:
				t.Fatal("replacement before join", err)
			default:
			}
			close(a.release)
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("not joined")
			}
			cx17Send(t, b, p, 2, i)
			defer p.Stop()
			if got := p.CX17Snapshot(time.Second, time.Second); got.State != "receiving" || !got.Promotable {
				t.Fatal("late callback affected successor", got)
			}
		})
	}
}

type cx17ErrorSource struct{}

func (cx17ErrorSource) CX17Receive(context.Context) (CX17Observation, error) {
	return CX17Observation{}, errors.New("synthetic failure")
}
func TestCX17ReceiveFailureRetainsHistory(t *testing.T) {
	s, i := cx16Fixture(t)
	p, _ := CX17NewPCSProvider(s)
	a := cx17Source()
	p.Start(a, 1)
	cx17Send(t, a, p, 1, i)
	p.Start(cx17ErrorSource{}, 2)
	defer p.Stop()
	deadline := time.After(time.Second)
	for {
		got := p.CX17Snapshot(time.Second, time.Second)
		if got.State == "degraded" {
			if got.Promotable || len(got.LastGood) == 0 || len(got.Raw) == 0 {
				t.Fatal(got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("failure not published")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
func TestCX17FreshFrameDoesNotRenewSibling(t *testing.T) {
	s, i := cx16Fixture(t)
	i.PCS.FieldAge = 0
	p, _ := CX17NewPCSProvider(s)
	a := cx17Source()
	p.Start(a, 1)
	defer p.Stop()
	cx17Send(t, a, p, 1, i)
	i.PCS.Evidence.Sequence = 2
	i.PCS.Evidence.Monotonic = 3 * time.Second
	cx16Frame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	cx17Send(t, a, p, 1, i)
	got := p.CX17Snapshot(3*time.Second, time.Second)
	if got.Current["core/voltage"].Status != "stale" || got.Current["identity/product_0"].Status != "accepted" {
		t.Fatal(got)
	}
	for _, now := range []time.Duration{0, time.Second, 2 * time.Second, 2*time.Second + 1} {
		got = p.CX17Snapshot(now, time.Second)
		if (got.Current["core/voltage"].Status == "accepted") != (now >= time.Second && now <= 2*time.Second) {
			t.Fatal("age boundary", now, got.Current["core/voltage"])
		}
	}
}
func TestCX17WireIdentityReuseAndRoleChange(t *testing.T) {
	for _, kind := range []uint32{0x1800, 0x1802} {
		t.Run(fmt.Sprintf("%x", kind), func(t *testing.T) {
			payload := [8]byte{}
			if kind == 0x1802 {
				payload = [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}
			}
			s, i := cx16LINK(t, kind, payload)
			p, _ := CX17NewLINKProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			cx17Send(t, a, p, 1, i)
			if kind == 0x1802 {
				payload[7] = '7'
			} else {
				payload[1] = 1
			}
			cx16Frame(t, &i, kind<<16|0xf401, true, payload)
			i.PCS.Evidence.Sequence = 2
			cx17Send(t, a, p, 1, i)
			got := p.CX17Snapshot(time.Second, time.Second)
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 || len(got.Raw) != 2 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17LINKSensorAndGateNegatives(t *testing.T) {
	for _, name := range []string{"count-zero", "count-one", "count-four", "count-five", "count-invalid", "gate-invalid", "gate-stale", "gate-unconfigured", "age-negative", "age-zero", "age-max", "age-over"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16LINK(t, 0x1830, [8]byte{0x40, 1, 0x41, 1, 0x42, 1, 0x43, 1})
			i.SensorCount = 4
			i.SensorCountValid = true
			for j := range i.SensorGates {
				i.SensorGates[j] = CX12MeasurementGate{Configured: true, Valid: true}
			}
			accepted := false
			switch name {
			case "count-zero":
				i.SensorCount = 0
			case "count-one":
				i.SensorCount = 1
				accepted = true
			case "count-four":
				accepted = true
			case "count-five":
				i.SensorCount = 5
			case "count-invalid":
				i.SensorCountValid = false
			case "gate-invalid":
				i.SensorGates[0].Valid = false
			case "gate-stale":
				i.SensorGates[0].Status = "stale"
			case "gate-unconfigured":
				i.SensorGates[0].Configured = false
			case "age-negative":
				i.SensorGates[0].Age = -1
			case "age-zero":
				accepted = true
			case "age-max":
				i.SensorGates[0].Age = time.Second
				accepted = true
			case "age-over":
				i.SensorGates[0].Age = time.Second + 1
			}
			p, _ := CX17NewLINKProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			cx17Send(t, a, p, 1, i)
			got := p.CX17Snapshot(time.Second, time.Second)
			if (got.Current["sensors/sensor_1"].Status == "accepted") != accepted || len(got.Raw) != 1 || got.PhysicalQualified {
				t.Fatal(got)
			}
			if (name == "gate-invalid" || name == "gate-stale" || name == "age-over" || name == "age-negative") && got.Current["sensors/sensor_2"].Status != "accepted" {
				t.Fatal("invalid sibling erased valid sensor")
			}
		})
	}
}

func TestCX17EverySelectionFieldNegative(t *testing.T) {
	mutations := map[string]func(*CX01SelectionSpec){
		"interface-name":  func(c *CX01SelectionSpec) { c.Interface = cx17Interface("other", 1) },
		"interface-index": func(c *CX01SelectionSpec) { c.Interface = cx17Interface("synthetic", 2) },
		"flavor":          func(c *CX01SelectionSpec) { c.Flavor = "pcs-parallel" },
		"pcs-flavor":      func(c *CX01SelectionSpec) { c.PCSFlavor = "pcs-extended19" },
		"project":         func(c *CX01SelectionSpec) { c.ProjectCode = "QAAa" },
		"release":         func(c *CX01SelectionSpec) { c.Release = "0011" },
		"choice":          func(c *CX01SelectionSpec) { c.Choice = "legacy19" },
		"direction":       func(c *CX01SelectionSpec) { c.Direction = "other" },
		"scope":           func(c *CX01SelectionSpec) { c.Scope = "aggregate" },
		"bank":            func(c *CX01SelectionSpec) { c.Bank = "other" },
		"endpoint":        func(c *CX01SelectionSpec) { c.Endpoint = "other" },
		"group":           func(c *CX01SelectionSpec) { c.Group = "1" },
		"slot":            func(c *CX01SelectionSpec) { c.Slot = "2" },
		"route-set":       func(c *CX01SelectionSpec) { c.RouteSet = true },
		"destination":     func(c *CX01SelectionSpec) { c.Destination = 0xf5 },
		"source":          func(c *CX01SelectionSpec) { c.Source = 2 },
		"epoch":           func(c *CX01SelectionSpec) { c.Epoch = 2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			p, _ := CX17NewPCSProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			cx17Send(t, a, p, 1, i)
			c := s.CX01Spec()
			mutate(&c)
			var err error
			i.Selection, err = NewCXSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			i.PCS.Evidence.Sequence = 2
			cx17Send(t, a, p, 1, i)
			got := p.CX17Snapshot(time.Second, time.Second)
			if got.Selection != s || got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || got.Raw[1].Input.Evidence.Frame != i.PCS.Evidence.Frame {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17ConstructorFailClosed(t *testing.T) {
	s, i := cx16Fixture(t)
	if _, err := CX17NewLINKProvider(s); err == nil {
		t.Fatal("PCS used as LINK")
	}
	link, _ := cx16LINK(t, 0x1805, [8]byte{})
	if _, err := CX17NewPCSProvider(link); err == nil {
		t.Fatal("LINK used as PCS")
	}
	c := s.CX01Spec()
	c.ProjectCode = "QAAa"
	c.Release = "9011"
	ambiguous, _ := NewCXSelection(c)
	if _, err := CX17NewPCSProvider(ambiguous); err == nil {
		t.Fatal("ambiguous selection")
	}
	p, _ := CX17NewPCSProvider(s)
	a := cx17Source()
	p.Start(a, 1)
	defer p.Stop()
	cx17Send(t, a, p, 1, i)
	if p.Start(nil, 2) == nil || p.Start(a, 0) == nil || p.Start(a, 1) == nil {
		t.Fatal("invalid replacement")
	}
	if !p.CX17Snapshot(time.Second, time.Second).Promotable {
		t.Fatal("failed replacement stopped predecessor")
	}
}
func TestCX17LINKRouteNegative(t *testing.T) {
	for _, name := range []string{"destination", "source", "ide"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16LINK(t, 0x1805, [8]byte{})
			p, _ := CX17NewLINKProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			id := uint32(0x1805f401)
			extended := true
			switch name {
			case "destination":
				id = 0x1805f501
			case "source":
				id = 0x1805f402
			case "ide":
				id = 0x313
				extended = false
			}
			cx16Frame(t, &i, id, extended, [8]byte{})
			cx17Send(t, a, p, 1, i)
			got := p.CX17Snapshot(time.Second, time.Second)
			if got.Promotable || len(got.Current) != 0 || len(got.Raw) != 1 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17RawRecordBoundsAndDetachedInput(t *testing.T) {
	for _, size := range []int{0, 72, 73} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, i := cx16Fixture(t)
			p, _ := CX17NewPCSProvider(s)
			a := cx17Source()
			p.Start(a, 1)
			defer p.Stop()
			i.PCS.NativeRecord = make([]byte, size)
			cx17Send(t, a, p, 1, i)
			if size > 0 {
				i.PCS.NativeRecord[0] = 9
			}
			got := p.CX17Snapshot(time.Second, time.Second)
			expected := size
			if expected > 72 {
				expected = 72
			}
			if len(got.Raw[0].NativeRecord) != expected {
				t.Fatal("raw bound")
			}
			if size > 0 && got.Raw[0].NativeRecord[0] != 0 {
				t.Fatal("input alias")
			}
			if size > 72 && got.TruncatedRecords != 1 {
				t.Fatal("unreported loss")
			}
			if size > 0 && got.Promotable {
				t.Fatal("malformed native record promoted")
			}
			if size > 0 {
				got.Raw[0].NativeRecord[0] = 8
				if p.CX17Snapshot(time.Second, time.Second).Raw[0].NativeRecord[0] != 0 {
					t.Fatal("snapshot alias")
				}
			}
		})
	}
}
func TestCX17FieldEvictionAcrossGenerations(t *testing.T) {
	s, i := cx16Fixture(t)
	p, _ := CX17NewPCSProvider(s)
	// Populate the bounded historical dictionary to exercise deterministic eviction
	// independently of the finite selected protocol dictionary's current size.
	p.mu.Lock()
	for n := 0; n < 256; n++ {
		key := fmt.Sprintf("history/%03d", n)
		p.order = append(p.order, key)
		p.snapshot.LastGood[key] = CXNativeField{Status: "accepted"}
		p.snapshot.LastGoodGeneration[key] = 1
	}
	p.mu.Unlock()
	a := cx17Source()
	p.Start(a, 1)
	defer p.Stop()
	cx17Send(t, a, p, 1, i)
	got := p.CX17Snapshot(time.Second, time.Second)
	if len(got.LastGood) > 256 || got.EvictedFields == 0 {
		t.Fatal("unbounded fields")
	}
	if _, exists := got.LastGood["history/000"]; exists {
		t.Fatal("not oldest eviction")
	}
	p.Stop()
	b := cx17Source()
	p.Start(b, 2)
	cx17Send(t, b, p, 2, i)
	got = p.CX17Snapshot(time.Second, time.Second)
	if len(p.order) != 256 || got.LastGoodGeneration["core/voltage"] != 2 {
		t.Fatal("generation retention", got)
	}
}
