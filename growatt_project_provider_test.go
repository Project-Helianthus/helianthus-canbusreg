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

type providerTestEvent struct {
	observation Observation
	done        chan struct{}
}
type providerTestFake struct {
	events  chan providerTestEvent
	pending chan struct{}
}

func providerTestSource() *providerTestFake {
	return &providerTestFake{events: make(chan providerTestEvent)}
}
func (s *providerTestFake) ProjectProviderReceive(ctx context.Context) (Observation, error) {
	if s.pending != nil {
		close(s.pending)
		s.pending = nil
	}
	select {
	case e := <-s.events:
		s.pending = e.done
		return e.observation, nil
	case <-ctx.Done():
		return Observation{}, errors.New("synthetic cancellation")
	}
}
func providerTestSend(t *testing.T, s *providerTestFake, p *Provider, g uint64, i ReplayInput) {
	t.Helper()
	e := providerTestEvent{observation: Observation{Generation: g, Input: i}, done: make(chan struct{})}
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
	s, i := replayTestFixture(t)
	p, err := NewPCSProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProjectProviderSnapshot(time.Second, time.Second).State != "stopped" {
		t.Fatal("constructor active")
	}
	a := providerTestSource()
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
	providerTestSend(t, a, p, 1, i)
	before := p.ProjectProviderSnapshot(time.Second, time.Second)
	if before.Current["core/voltage"].Numerator != 5310 || !before.Promotable {
		t.Fatal(before)
	}
	if p.Start(a, 1) == nil {
		t.Fatal("reused generation")
	}
	b := providerTestSource()
	if err = p.Start(b, 2); err != nil {
		t.Fatal(err)
	}
	if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.Promotable || got.State != "awaiting" {
		t.Fatal(got)
	}
	providerTestSend(t, b, p, 1, i)
	if p.ProjectProviderSnapshot(time.Second, time.Second).Rejected != 1 {
		t.Fatal("retired generation")
	}
	providerTestSend(t, b, p, 2, i)
	if !p.ProjectProviderSnapshot(time.Second, time.Second).Promotable {
		t.Fatal("successor not active")
	}
	p.Stop()
	p.Stop()
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || len(got.Raw) == 0 || got.State != "stopped" {
		t.Fatal(got)
	}
}
func TestCX17IndependentAgesAndCopies(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	i.PCS.FieldAges = map[string]time.Duration{"voltage": 0, "current": time.Second}
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(2*time.Second, time.Second)
	if got.Current["core/voltage"].Status != "accepted" || got.Current["core/current"].Status != "stale" {
		t.Fatal(got)
	}
	got.Current["core/voltage"] = NativeField{}
	got.LastGood["core/voltage"] = NativeField{}
	got.Raw[0].Status = "altered"
	got = p.ProjectProviderSnapshot(2*time.Second, time.Second)
	if got.Current["core/voltage"].Numerator != 5310 || got.LastGood["core/voltage"].Numerator != 5310 || got.Raw[0].Status == "altered" {
		t.Fatal("alias")
	}
	i.PCS.Evidence.Sequence = 2
	i.PCS.Evidence.Monotonic = 2 * time.Second
	i.PCS.FieldStates = map[string]string{"voltage": "invalid"}
	providerTestSend(t, a, p, 1, i)
	got = p.ProjectProviderSnapshot(2*time.Second, 2*time.Second)
	if got.Current["core/voltage"].Status != "invalid" || got.LastGood["core/voltage"].Evidence.Sequence != 1 || got.Current["core/current"].Evidence.Sequence != 2 {
		t.Fatal("partial retention", got)
	}
}
func TestCX17IdentityInvalidationNegatives(t *testing.T) {
	for _, name := range []string{"epoch", "epoch-zero", "role", "profile", "publisher", "bank", "endpoint", "group", "slot", "scope"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
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
				c := s.SelectionSpecValue()
				c.Release = "0011"
				bad.Selection, _ = NewSelection(c)
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
			providerTestSend(t, a, p, 1, bad)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || got.Raw[len(got.Raw)-1].Input.Evidence.Frame != i.PCS.Evidence.Frame {
				t.Fatal(got)
			}
			i.PCS.Evidence.Sequence = 3
			providerTestSend(t, a, p, 1, i)
			if p.ProjectProviderSnapshot(time.Second, time.Second).Promotable {
				t.Fatal("identity auto-recovered")
			}
		})
	}
}
func TestCX17SequenceAndClockBoundaries(t *testing.T) {
	for _, name := range []string{"zero", "equal", "lower", "clock", "interface-name", "interface-index"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			i.PCS.Evidence.Sequence = 2
			providerTestSend(t, a, p, 1, i)
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
				bad.PCS.Evidence.Interface = providerTestInterface("other", 1)
			case "interface-index":
				bad.PCS.Evidence.Interface = providerTestInterface("synthetic", 2)
			}
			providerTestSend(t, a, p, 1, bad)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.Rejected != 1 || got.LastGood["core/voltage"].Evidence.Sequence != 2 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17BoundsAndConcurrentSnapshots(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				snap := p.ProjectProviderSnapshot(time.Second, time.Second)
				snap.Current["invented"] = NativeField{}
			}
		}()
	}
	for n := 1; n <= 257; n++ {
		i.PCS.Evidence.Sequence = uint64(n)
		providerTestSend(t, a, p, 1, i)
	}
	wg.Wait()
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if len(got.Raw) != 256 || got.EvictedRaw != 1 || got.Raw[0].Input.Evidence.Sequence != 2 || len(got.LastGood) > 256 {
		t.Fatal("bound", len(got.Raw), got.EvictedRaw)
	}
}
func TestCX17LINKCountsAndValidityNegatives(t *testing.T) {
	for _, count := range []int{0, 1, 16, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1810, [8]byte{1, 0, 2, 0, 3, 0, 4, 0})
			p, err := NewLINKProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			i.CellCount = count
			i.CellCountValid = true
			for n := range i.CellGates {
				i.CellGates[n] = MeasurementGate{Configured: true, Valid: true}
			}
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if (got.Current["cells/cell_1"].Status == "accepted") != (count >= 1 && count <= 16) || got.PhysicalQualified || len(got.Raw) != 1 {
				t.Fatal(got)
			}
		})
	}
}

func providerTestInterface(name string, index int) canbus.InterfaceIdentity {
	i, _ := canbus.NewInterfaceIdentity(name, index)
	return i
}

type providerTestLateSource struct {
	entered, canceled, release chan struct{}
	fail                       bool
}

func (s *providerTestLateSource) ProjectProviderReceive(ctx context.Context) (Observation, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	if s.fail {
		return Observation{}, errors.New("late synthetic failure")
	}
	return Observation{}, nil
}
func TestCX17CancelJoinAndLateCallback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := &providerTestLateSource{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), fail: fail}
			p.Start(a, 1)
			<-a.entered
			b := providerTestSource()
			finished := make(chan error, 1)
			go func() { finished <- p.Start(b, 2) }()
			select {
			case <-a.canceled:
			case <-time.After(time.Second):
				t.Fatal("not canceled")
			}
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "stopped" || got.Promotable {
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
			providerTestSend(t, b, p, 2, i)
			defer p.Stop()
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || !got.Promotable {
				t.Fatal("late callback affected successor", got)
			}
		})
	}
}

type providerTestErrorSource struct{}

func (providerTestErrorSource) ProjectProviderReceive(context.Context) (Observation, error) {
	return Observation{}, errors.New("synthetic failure")
}
func TestCX17ReceiveFailureRetainsHistory(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	providerTestSend(t, a, p, 1, i)
	p.Start(providerTestErrorSource{}, 2)
	defer p.Stop()
	deadline := time.After(time.Second)
	for {
		got := p.ProjectProviderSnapshot(time.Second, time.Second)
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
	s, i := replayTestFixture(t)
	i.PCS.FieldAge = 0
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	i.PCS.Evidence.Sequence = 2
	i.PCS.Evidence.Monotonic = 3 * time.Second
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(3*time.Second, time.Second)
	if got.Current["core/voltage"].Status != "stale" || got.Current["identity/product_0"].Status != "accepted" {
		t.Fatal(got)
	}
	for _, now := range []time.Duration{0, time.Second, 2 * time.Second, 2*time.Second + 1} {
		got = p.ProjectProviderSnapshot(now, time.Second)
		if (got.Current["core/voltage"].Status == "accepted") != (now >= time.Second && now <= 2*time.Second) {
			t.Fatal("age boundary", now, got.Current["core/voltage"])
		}
	}
}

// Catches: a changed LINK project/release (0x1802) or peer version byte (0x1801/version) within one generation
// updating the identity without a sticky conflict, so fields retained under the previous peer stayed promotable.
func TestCX17WireIdentityReuseAndRoleChange(t *testing.T) {
	for _, kind := range []uint32{0x1801, 0x1802} {
		t.Run(fmt.Sprintf("%x", kind), func(t *testing.T) {
			payload := [8]byte{5, 0, 1, 2, 3, 4, 6, 0}
			if kind == 0x1802 {
				payload = [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'}
			}
			s, i := replayTestLINK(t, kind, payload)
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" {
				t.Fatal("first observation", got.State)
			}
			if kind == 0x1802 {
				payload[7] = '7'
			} else {
				payload[6] = 7
			}
			replayTestFrame(t, &i, kind<<16|0xf401, true, payload)
			i.PCS.Evidence.Sequence = 2
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 || len(got.Raw) != 2 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17LINKSensorAndGateNegatives(t *testing.T) {
	for _, name := range []string{"count-zero", "count-one", "count-four", "count-five", "count-invalid", "gate-invalid", "gate-stale", "gate-unconfigured", "age-negative", "age-zero", "age-max", "age-over"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1830, [8]byte{0x40, 1, 0x41, 1, 0x42, 1, 0x43, 1})
			i.SensorCount = 4
			i.SensorCountValid = true
			for j := range i.SensorGates {
				i.SensorGates[j] = MeasurementGate{Configured: true, Valid: true}
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
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
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
	mutations := map[string]func(*SelectionSpec){
		"interface-name":  func(c *SelectionSpec) { c.Interface = providerTestInterface("other", 1) },
		"interface-index": func(c *SelectionSpec) { c.Interface = providerTestInterface("synthetic", 2) },
		"flavor":          func(c *SelectionSpec) { c.Flavor = "pcs-parallel" },
		"pcs-flavor":      func(c *SelectionSpec) { c.PCSFlavor = "pcs-extended19" },
		"project":         func(c *SelectionSpec) { c.ProjectCode = "QAAa" },
		"release":         func(c *SelectionSpec) { c.Release = "0011" },
		"choice":          func(c *SelectionSpec) { c.Choice = "legacy19" },
		"direction":       func(c *SelectionSpec) { c.Direction = "other" },
		"scope":           func(c *SelectionSpec) { c.Scope = "aggregate" },
		"bank":            func(c *SelectionSpec) { c.Bank = "parallel" },
		"endpoint":        func(c *SelectionSpec) { c.Endpoint = "other" },
		"group":           func(c *SelectionSpec) { c.Group = "1" },
		"slot":            func(c *SelectionSpec) { c.Slot = "2" },
		"route-set":       func(c *SelectionSpec) { c.RouteSet = true },
		"destination":     func(c *SelectionSpec) { c.Destination = 0xf5 },
		"source":          func(c *SelectionSpec) { c.Source = 2 },
		"epoch":           func(c *SelectionSpec) { c.Epoch = 2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			c := s.SelectionSpecValue()
			mutate(&c)
			var err error
			i.Selection, err = NewSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			i.PCS.Evidence.Sequence = 2
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.Selection != s || got.Promotable || len(got.Current) != 0 || len(got.LastGood) == 0 || got.Raw[1].Input.Evidence.Frame != i.PCS.Evidence.Frame {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17ConstructorFailClosed(t *testing.T) {
	s, i := replayTestFixture(t)
	if _, err := NewLINKProvider(s); err == nil {
		t.Fatal("PCS used as LINK")
	}
	link, _ := replayTestLINK(t, 0x1805, [8]byte{})
	if _, err := NewPCSProvider(link); err == nil {
		t.Fatal("LINK used as PCS")
	}
	c := s.SelectionSpecValue()
	c.ProjectCode = "QAAa"
	c.Release = "9011"
	ambiguous, _ := NewSelection(c)
	if _, err := NewPCSProvider(ambiguous); err == nil {
		t.Fatal("ambiguous selection")
	}
	// The gateway-state selection has no native group, so its 0x1910 downstream address never reaches the fence.
	c = s.SelectionSpecValue()
	c.Flavor, c.ProjectCode, c.Release, c.Scope, c.Destination, c.Source = "pcs-extended19", "QCAA", "0001", "aggregate", 0xdf, 0xf2
	gateway, err := NewSelection(c)
	if err != nil || gateway.SelectionGroup() != "" {
		t.Fatal(err, gateway.SelectionGroup())
	}
	if _, err := NewPCSProvider(gateway); err == nil {
		t.Fatal("gateway-state selection admitted")
	}
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	if p.Start(nil, 2) == nil || p.Start(a, 0) == nil || p.Start(a, 1) == nil {
		t.Fatal("invalid replacement")
	}
	if !p.ProjectProviderSnapshot(time.Second, time.Second).Promotable {
		t.Fatal("failed replacement stopped predecessor")
	}
}
func TestCX17LINKRouteNegative(t *testing.T) {
	for _, name := range []string{"destination", "source", "ide"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1805, [8]byte{})
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
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
			replayTestFrame(t, &i, id, extended, [8]byte{})
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.Promotable || len(got.Current) != 0 || len(got.Raw) != 1 {
				t.Fatal(got)
			}
		})
	}
}
func TestCX17RawRecordBoundsAndDetachedInput(t *testing.T) {
	for _, size := range []int{0, 72, 73} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			i.PCS.NativeRecord = make([]byte, size)
			providerTestSend(t, a, p, 1, i)
			if size > 0 {
				i.PCS.NativeRecord[0] = 9
			}
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
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
				if p.ProjectProviderSnapshot(time.Second, time.Second).Raw[0].NativeRecord[0] != 0 {
					t.Fatal("snapshot alias")
				}
			}
		})
	}
}
func TestCX17FieldEvictionAcrossGenerations(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	// Populate the bounded historical dictionary to exercise deterministic eviction
	// independently of the finite selected protocol dictionary's current size.
	p.mu.Lock()
	for n := 0; n < 256; n++ {
		key := fmt.Sprintf("history/%03d", n)
		p.order = append(p.order, key)
		p.snapshot.LastGood[key] = NativeField{Status: "accepted"}
		p.snapshot.LastGoodGeneration[key] = 1
	}
	p.mu.Unlock()
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if len(got.LastGood) > 256 || got.EvictedFields == 0 {
		t.Fatal("unbounded fields")
	}
	if _, exists := got.LastGood["history/000"]; exists {
		t.Fatal("not oldest eviction")
	}
	p.Stop()
	b := providerTestSource()
	p.Start(b, 2)
	providerTestSend(t, b, p, 2, i)
	got = p.ProjectProviderSnapshot(time.Second, time.Second)
	if len(p.order) != 256 || got.LastGoodGeneration["core/voltage"] != 2 {
		t.Fatal("generation retention", got)
	}
}

func TestCX17InvalidIdentityFieldDoesNotConflict(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	i.PCS.Evidence.Sequence = 1
	providerTestSend(t, a, p, 1, i)
	if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.Current["identity/product_0"].Status != "accepted" {
		t.Fatal(got)
	}
	i.PCS.Evidence.Sequence = 2
	replayTestFrame(t, &i, 0x320, false, [8]byte{9, 2, 3, 4, 5, 6, 7, 0})
	i.PCS.FieldStates = map[string]string{"product_0": "invalid"}
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.State == "conflict" || len(got.Current) == 0 || got.Current["identity/product_0"].Status != "invalid" {
		t.Fatal("withheld field caused conflict", got)
	}
	i.PCS.Evidence.Sequence = 3
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	i.PCS.FieldStates = nil
	providerTestSend(t, a, p, 1, i)
	if got = p.ProjectProviderSnapshot(time.Second, time.Second); got.State == "conflict" || got.Current["identity/product_0"].Status != "accepted" {
		t.Fatal("baseline was overwritten by withheld field", got)
	}
	i.PCS.Evidence.Sequence = 4
	replayTestFrame(t, &i, 0x320, false, [8]byte{9, 2, 3, 4, 5, 6, 7, 0})
	providerTestSend(t, a, p, 1, i)
	if got = p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "conflict" || len(got.Current) != 0 {
		t.Fatal("accepted identity change must still conflict", got)
	}
}

func TestCX17LINKCountDropDemotesRetainedCells(t *testing.T) {
	s, i := replayTestLINK(t, 0x1811, [8]byte{5, 0, 6, 0, 7, 0, 8, 0})
	p, _ := NewLINKProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	i.CellCount, i.CellCountValid = 16, true
	for n := range i.CellGates {
		i.CellGates[n] = MeasurementGate{Configured: true, Valid: true}
	}
	providerTestSend(t, a, p, 1, i)
	if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.Current["cells/cell_5"].Status != "accepted" {
		t.Fatal(got.Current)
	}
	i.PCS.Evidence.Sequence++
	i.PCS.Evidence.Monotonic += time.Millisecond
	replayTestFrame(t, &i, 0x1810<<16|0xf401, true, [8]byte{1, 0, 2, 0, 3, 0, 4, 0})
	i.CellCount = 4
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.Current["cells/cell_5"].Status != "unconfigured" || got.Current["cells/cell_5"].Unit != "" || got.LastGood["cells/cell_5"].Status != "accepted" {
		t.Fatal(got.Current["cells/cell_5"], got.LastGood["cells/cell_5"])
	}
}

func TestCX17EqualMemberValueDoesNotConflict(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	i.PCS.Member = &s
	providerTestSend(t, a, p, 1, i)
	if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || len(got.Current) == 0 {
		t.Fatal(got)
	}
	other := s
	changed := i
	changed.PCS.Member = &other
	changed.PCS.Evidence.Sequence = 2
	providerTestSend(t, a, p, 1, changed)
	if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || len(got.Current) == 0 {
		t.Fatal("same member value must not conflict", got.State)
	}
}

func TestCX17MemberAssociationChangeConflicts(t *testing.T) {
	for _, first := range []bool{false, true} {
		s, i := replayTestFixture(t)
		p, _ := NewPCSProvider(s)
		a := providerTestSource()
		p.Start(a, 1)
		if first {
			i.PCS.Member = &s
		}
		providerTestSend(t, a, p, 1, i)
		next := i
		next.PCS.Evidence.Sequence = 2
		if first {
			next.PCS.Member = nil
		} else {
			next.PCS.Member = &s
		}
		providerTestSend(t, a, p, 1, next)
		got := p.ProjectProviderSnapshot(time.Second, time.Second)
		if got.State != "conflict" || got.Promotable || len(got.Current) != 0 {
			t.Fatal(first, got)
		}
		p.Stop()
	}
}

func TestCX17ReplacementDTCDemotesOmittedIndex(t *testing.T) {
	s, pcs := slotTestFixture(t, 0x333, [8]byte{3, 2})
	word, ok := EncodeDTC(s, 0)
	if !ok {
		t.Fatal("no member dictionary entry")
	}
	pcs.NativeSlot, pcs.PublisherResolved, pcs.Member = 2, true, &s
	p, err := NewPCSProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	i := ReplayInput{Selection: s, PCS: pcs}
	replayTestFrame(t, &i, 0x333, false, [8]byte{3, 2, byte(word >> 8), byte(word), 0x10, 1})
	providerTestSend(t, a, p, 1, i)
	const name = "slots/dtc_a_index"
	got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
	if got.Current[name].Status != "accepted" {
		t.Fatal(got.State, got.Current)
	}
	replayTestFrame(t, &i, 0x333, false, [8]byte{3, 2, 0xff, 0xfe, 0x10, 1})
	i.PCS.Evidence.Sequence++
	providerTestSend(t, a, p, 1, i)
	got = p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
	if got.Current[name].Status == "accepted" || got.LastGood[name].Status != "accepted" {
		t.Fatal(got.Current[name], got.LastGood[name])
	}
}
func TestCX17ParallelReplacementDTCDemotesOmittedIndex(t *testing.T) {
	c := parallelReleaseTestSpec(t)
	c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "release11"
	s := parallelIdentityTestSelection(t, c)
	p, err := NewPCSProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	send := func(seq uint64, payload [8]byte) ProjectProviderSnapshot {
		in := parallelIdentityTestInput(t, c, 0x523, payload)
		in.Evidence.Sequence = seq
		pcs := PCSSlotPCSInput{PCSIdentityPCSInput: PCSIdentityPCSInput{PCSStandardPCSInput: PCSStandardPCSInput{LINKInput: in, PublisherResolved: true}}}
		providerTestSend(t, a, p, 1, ReplayInput{Selection: s, PCS: pcs})
		return p.ProjectProviderSnapshot(time.Second, time.Hour)
	}
	got := send(1, [8]byte{2, 4, 0xa1, 0, 0, 0, 0, 0})
	const name = "release11/523/dtc_a_index"
	if got.Current[name].Status != "accepted" {
		t.Fatal(got.State, got.Current)
	}
	got = send(2, [8]byte{2, 4, 0xff, 0xfe, 0, 0, 0, 0})
	if got.Current[name].Status == "accepted" || got.LastGood[name].Status != "accepted" {
		t.Fatal(got.Current[name], got.LastGood[name])
	}
}
func TestCX17NonzeroReservedByteDoesNotConflict(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	i.PCS.Evidence.Sequence = 1
	providerTestSend(t, a, p, 1, i)
	i.PCS.Evidence.Sequence = 2
	replayTestFrame(t, &i, 0x320, false, [8]byte{1, 2, 3, 4, 5, 6, 7, 9})
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.State == "conflict" || len(got.Current) == 0 || got.Current["identity/product_0"].Status != "accepted" {
		t.Fatal("reserved byte must not fence identity", got)
	}
	i.PCS.Evidence.Sequence = 3
	replayTestFrame(t, &i, 0x320, false, [8]byte{9, 2, 3, 4, 5, 6, 7, 9})
	providerTestSend(t, a, p, 1, i)
	if got = p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "conflict" {
		t.Fatal("identity change must still conflict", got)
	}
}

// Catches: LINK record kinds that share a field name (0x1806 cell indexes, 0x1808 temperature indexes)
// collapsing into one retention key, so the later record overwrote the earlier record's evidence.
func TestCX17InterleavedLINKKindsKeepIndependentIndexes(t *testing.T) {
	s, i := replayTestLINK(t, 0x1806, [8]byte{1, 0, 2, 7, 0, 3, 0, 4})
	p, _ := NewLINKProvider(s)
	a := providerTestSource()
	p.Start(a, 1)
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	cells := p.ProjectProviderSnapshot(time.Second, time.Second)
	if cells.Current["link/1806/minimum_index"].Numerator != 7 {
		t.Fatalf("cell minimum index: %#v", cells.Current["link/1806/minimum_index"])
	}
	i.PCS.Evidence.Sequence++
	i.PCS.Evidence.Monotonic += time.Millisecond
	replayTestFrame(t, &i, 0x1808<<16|0xf401, true, [8]byte{9, 1, 3, 2, 0, 0, 0, 0})
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.Current["link/1808/minimum_index"].Numerator != 3 || got.Current["link/1806/minimum_index"].Numerator != 7 {
		t.Fatalf("indexes collided: cells %#v temperatures %#v", got.Current["link/1806/minimum_index"], got.Current["link/1808/minimum_index"])
	}
	if _, collided := got.Current["link/minimum_index"]; collided {
		t.Fatal("kind-less LINK retention key still present")
	}
}

// Catches: the native slot or group association changing under identical wire bytes within one generation,
// which would decode the same record into another slot's or group's current and last-good fields.
func TestCX17NativeAssociationChangeConflicts(t *testing.T) {
	cases := map[string]func(pcs *PCSSlotPCSInput){
		"slot":           func(pcs *PCSSlotPCSInput) { pcs.NativeSlot = 3 },
		"group":          func(pcs *PCSSlotPCSInput) { pcs.NativeGroup = 2 },
		"slot validity":  func(pcs *PCSSlotPCSInput) { pcs.SlotValid = false },
		"group validity": func(pcs *PCSSlotPCSInput) { pcs.GroupValid = false },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			i.PCS.SlotValid, i.PCS.NativeSlot, i.PCS.GroupValid, i.PCS.NativeGroup = true, 2, true, 1
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State == "conflict" {
				t.Fatalf("first observation conflicted: %#v", got)
			}
			next := i
			next.PCS.Evidence.Sequence = 2
			change(&next.PCS)
			providerTestSend(t, a, p, 1, next)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 {
				t.Fatal(got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: aggregate extended identity records (kind 0x1910 from the coordinator) decoding as the normalized
// 0x1900 identity fields while the fence looked only at wire kind 0x1900, so changed product, hardware or
// version values on 0x1910 overwrote current and last-good fields without a sticky conflict.
func TestCX17AggregateExtendedIdentityChangeConflicts(t *testing.T) {
	for _, kind := range []uint32{0x1900, 0x1910} {
		t.Run(fmt.Sprintf("%x", kind), func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.Flavor, c.Scope, c.Destination, c.Source = "pcs-extended19", "aggregate", 0xdf, 0xf2
			c.Bank, c.Endpoint, c.Group, c.Slot, c.RouteSet = "ordinary", "endpoint-a", "group-a", "slot-a", true
			s = replayTestReselect(t, &i, c)
			payload := [8]byte{0x34, 0x12, 2, 6, 0, 0, 0, 0}
			replayTestFrame(t, &i, kind<<16|0xdf<<8|0xf2, true, payload)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			p.Start(a, 1)
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			key := fmt.Sprintf("extended/%04x/product", kind)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.Current[key].Numerator != 0x1234 || got.Current[key].Status != "accepted" {
				t.Fatalf("identity record not accepted: %#v", got.Current[key])
			}
			payload[0] = 0x35
			replayTestFrame(t, &i, kind<<16|0xdf<<8|0xf2, true, payload)
			i.PCS.Evidence.Sequence = 2
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 {
				t.Fatal(got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: the opaque LINK 0x1800 flags byte fencing the generation. Its unspecified bits cannot establish a role
// change, so a different role_flags value is retained raw (latest value) and the generation keeps receiving.
func TestCX17OpaqueRoleFlagsDoNotConflict(t *testing.T) {
	payload := [8]byte{0, 0x01, 16, 4, 0, 0, 0, 0}
	s, i := replayTestLINK(t, 0x1800, payload)
	p, _ := NewLINKProvider(s)
	a := providerTestSource()
	if err := p.Start(a, 1); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	payload[1] = 0x81
	replayTestFrame(t, &i, 0x1800<<16|0xf401, true, payload)
	i.PCS.Evidence.Sequence = 2
	providerTestSend(t, a, p, 1, i)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	f := got.Current["link/1800/role_flags"]
	if got.State != "receiving" || f.Status != "raw" || f.Numerator != 0x81 || f.Unit != "" || f.Meaning != "" {
		t.Fatal(got.State, f)
	}
	// A real identity change still fences after opaque flag changes.
	replayTestFrame(t, &i, 0x1802<<16|0xf401, true, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
	i.PCS.Evidence.Sequence = 3
	providerTestSend(t, a, p, 1, i)
	if got = p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" {
		t.Fatal("matching identity conflicted", got.State)
	}
}

// Catches: an accepted PCS 0x321 family changing within one generation without a sticky conflict, so telemetry
// retained under the previous family stayed promotable. A raw family on an unlabelled selection is not identity.
func TestCX17PCSFamilyChangeConflicts(t *testing.T) {
	for _, release := range []string{"0011", "0006"} {
		t.Run(release, func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.Release = release
			s = replayTestReselect(t, &i, c)
			p, err := NewPCSProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			replayTestFrame(t, &i, 0x321, false, [8]byte{0, 'A', 'B', 'C', 'D', 0, 1, 2})
			providerTestSend(t, a, p, 1, i)
			want := map[string]string{"0011": "accepted", "0006": "raw"}[release]
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || got.Current["identity/family"].Status != want {
				t.Fatal(got.State, got.Current["identity/family"])
			}
			i.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &i, 0x321, false, [8]byte{0, 'A', 'B', 'C', 'E', 0, 1, 2})
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if release == "0011" && (got.State != "conflict" || got.Promotable || len(got.Current) != 0 || got.LastGood["identity/family"].Numerator != 0x41424344) {
				t.Fatal("accepted family change", got.State, got.Promotable, got.LastGood["identity/family"])
			}
			if release == "0006" && (got.State != "receiving" || got.Current["identity/family"].Numerator != 0x41424345) {
				t.Fatal("raw family fenced", got.State, got.Current["identity/family"])
			}
		})
	}
}

// Catches: the extended 0x1903 role (and the coordinator's aggregate 0x1913, normalized to the same role) changing
// within one generation without a sticky conflict. A changed state byte on the same record is not identity.
func TestCX17ExtendedRoleChangeConflicts(t *testing.T) {
	for _, kind := range []uint32{0x1903, 0x1913} {
		t.Run(fmt.Sprintf("%x", kind), func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.Flavor, c.Bank, c.Endpoint, c.Group, c.Slot, c.RouteSet = "pcs-extended19", "ordinary", "endpoint-a", "group-a", "slot-a", true
			c.Destination, c.Source = 0xf2, 2
			if kind == 0x1913 {
				c.Scope, c.Destination, c.Source = "aggregate", 0xdf, 0xf2
			}
			s = replayTestReselect(t, &i, c)
			route := uint32(c.Destination)<<8 | uint32(c.Source)
			payload := [8]byte{1, 0, 2, 0, 3, 4, 5, 1}
			replayTestFrame(t, &i, kind<<16|route, true, payload)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			key := fmt.Sprintf("extended/%04x/role", kind)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || got.Current[key].Status != "accepted" || got.Current[key].Numerator != 1 {
				t.Fatal(got.State, got.Current[key])
			}
			payload[6] = 6
			replayTestFrame(t, &i, kind<<16|route, true, payload)
			i.PCS.Evidence.Sequence = 2
			providerTestSend(t, a, p, 1, i)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" {
				t.Fatal("state change fenced", got.State)
			}
			payload[7] = 2
			replayTestFrame(t, &i, kind<<16|route, true, payload)
			i.PCS.Evidence.Sequence = 3
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 || got.LastGood[key].Numerator != 1 {
				t.Fatal(got.State, got.Promotable, len(got.Current), got.LastGood[key])
			}
		})
	}
}

// Catches: a replacement LINK peer announcing a different complete 0x18fb serial under the same project, release
// and route without a conflict, because no serial was assembled and retained across observations. The same serial
// repeated, or a fresh generation, does not conflict.
func TestCX17LINKSerialReplacementConflicts(t *testing.T) {
	serial := func(first byte) [][8]byte {
		return [][8]byte{{15, first, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}}
	}
	for _, name := range []string{"same", "different", "new-generation"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			generation := uint64(1)
			providerTestSend(t, a, p, generation, i)
			feed := func(frames [][8]byte) {
				for _, payload := range frames {
					i.PCS.Evidence.Sequence++
					replayTestFrame(t, &i, 0x18fb<<16|0xf401, true, payload)
					providerTestSend(t, a, p, generation, i)
				}
			}
			feed(serial(1))
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" {
				t.Fatal("first serial", got.State)
			}
			next := serial(1)
			if name != "same" {
				next = serial(9)
			}
			if name == "new-generation" {
				b := providerTestSource()
				generation = 2
				if err := p.Start(b, generation); err != nil {
					t.Fatal(err)
				}
				a = b
				// Sequences above the predecessor's keep a retained predecessor assembly from rejecting them as stale.
				i.PCS.Evidence.Sequence = 10
				replayTestFrame(t, &i, 0x1802<<16|0xf401, true, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
				providerTestSend(t, a, p, generation, i)
			}
			feed(next)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if (got.State == "conflict") != (name == "different") || (name == "different" && (got.Promotable || len(got.Current) != 0)) {
				t.Fatal(name, got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: a later 0x1801 whose balance gate is invalid, whose hardware age is unknown, or whose cell count is
// smaller or invalid omitting enable fields while the earlier accepted enables stayed current and promotable.
// LastGood keeps the earlier enables.
func TestCX17BalanceEnablesDemotedWhenWithheld(t *testing.T) {
	for _, name := range []string{"gate-invalid", "hardware-age-unknown", "count-smaller", "count-invalid", "unchanged"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1801, [8]byte{0x0f, 0, 1, 2, 3, 4, 6, 0})
			i.CellCount, i.CellCountValid = 4, true
			i.BalanceGate = MeasurementGate{Configured: true, Valid: true}
			i.HardwareAgeKnown = true
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			now := i.PCS.Evidence.Monotonic
			if got := p.ProjectProviderSnapshot(now, time.Second); got.Current["balance/enable_3"].Status != "accepted" || !got.Promotable {
				t.Fatal(got.State, got.Current["balance/enable_3"])
			}
			i.PCS.Evidence.Sequence = 2
			switch name {
			case "gate-invalid":
				i.BalanceGate.Valid = false
			case "hardware-age-unknown":
				i.HardwareAgeKnown = false
			case "count-smaller":
				i.CellCount = 2
			case "count-invalid":
				i.CellCountValid = false
			}
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(now, time.Second)
			want := map[string]string{"gate-invalid": "absent", "hardware-age-unknown": "absent", "count-smaller": "unconfigured", "count-invalid": "absent", "unchanged": "accepted"}[name]
			for _, slot := range []int{3, 4} {
				key := fmt.Sprintf("balance/enable_%d", slot)
				f := got.Current[key]
				if f.Status != want || (want != "accepted" && (f.Unit != "" || f.Meaning != "")) || got.LastGood[key].Status != "accepted" {
					t.Fatal(key, f, got.LastGood[key])
				}
			}
			if f := got.Current["balance/enable_1"]; (name == "count-smaller" || name == "unchanged") != (f.Status == "accepted" && f.Evidence.Sequence == 2) {
				t.Fatal("enable_1", f)
			}
			if name != "unchanged" && name != "count-smaller" && got.Promotable {
				t.Fatal("withheld enables promotable")
			}
		})
	}
}

// Catches: a PCS-standard peer announcing a second, different complete 0x324 serial (or the same serial under a
// changed native serial slot) within one generation without a conflict, because the serial was never assembled and
// retained. The same serial repeated, an interrupted sequence, or a fresh generation does not conflict.
func TestCX17PCSStandardSerialReplacementConflicts(t *testing.T) {
	serial := func(slot, first byte) [][8]byte {
		return [][8]byte{{0, slot, first, 2, 3, 4, 5, 6}, {1, 7, 8, 9, 10, 11, 12, 13}, {2, 14, 15, 16, 0, 0, 0, 0}}
	}
	for _, name := range []string{"same", "different", "slot-change", "interrupted", "new-generation"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			i.PCS.SerialSlotValid, i.PCS.SerialSlot = true, 1
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			generation := uint64(1)
			providerTestSend(t, a, p, generation, i)
			feed := func(frames [][8]byte) {
				for _, payload := range frames {
					i.PCS.Evidence.Sequence++
					i.PCS.Evidence.Monotonic += time.Millisecond
					replayTestFrame(t, &i, 0x324, false, payload)
					providerTestSend(t, a, p, generation, i)
				}
			}
			feed(serial(1, 1))
			if got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour); got.State != "receiving" || !got.Promotable {
				t.Fatal("first serial", got.State, got.Promotable)
			}
			next := serial(1, 1)
			switch name {
			case "different", "new-generation":
				next = serial(1, 9)
			case "slot-change":
				i.PCS.SerialSlot = 2
				next = serial(2, 1)
			case "interrupted":
				next = serial(1, 9)
				next = [][8]byte{next[0], next[2]}
			}
			if name == "new-generation" {
				b := providerTestSource()
				generation = 2
				if err := p.Start(b, generation); err != nil {
					t.Fatal(err)
				}
				a = b
				i.PCS.Evidence.Sequence = 10
				replayTestFrame(t, &i, 0x313, false, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0, 10, 50, 0xe4})
				providerTestSend(t, a, p, generation, i)
			}
			feed(next)
			got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
			conflict := name == "different" || name == "slot-change"
			if (got.State == "conflict") != conflict || (conflict && (got.Promotable || len(got.Current) != 0)) {
				t.Fatal(name, got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: a PCS-parallel peer announcing a replacement 0x6C0 serial for the same module under unchanged product,
// hardware and version without a conflict. Another module's serial is a separate identity and never merges; a
// reordered fragment sequence, the same serial, or a fresh generation does not conflict.
func TestCX17ParallelSerialReplacementConflicts(t *testing.T) {
	serial := func(module, first byte) [][8]byte {
		m := module << 4
		return [][8]byte{{m, first, 2, 3, 4, 5, 6, 7}, {m | 1, 8, 9, 10, 11, 12, 13, 14}, {m | 2, 15, 16, 0, 0, 0, 0, 0}}
	}
	for _, name := range []string{"same", "different", "other-module", "reordered", "new-generation"} {
		t.Run(name, func(t *testing.T) {
			c := parallelIdentityTestSpec(t)
			s := parallelIdentityTestSelection(t, c)
			p, err := NewPCSProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			generation, sequence := uint64(1), uint64(0)
			feed := func(frames [][8]byte) {
				for _, payload := range frames {
					sequence++
					in := parallelIdentityTestInput(t, c, 0x6c3, payload)
					in.Evidence.Sequence, in.Evidence.Monotonic = sequence, time.Duration(sequence)*time.Millisecond
					pcs := PCSSlotPCSInput{PCSIdentityPCSInput: PCSIdentityPCSInput{PCSStandardPCSInput: PCSStandardPCSInput{LINKInput: in, PublisherResolved: true}}}
					providerTestSend(t, a, p, generation, ReplayInput{Selection: s, PCS: pcs})
				}
			}
			feed([][8]byte{{1, 2, 3, 4, 5, 6, 7, 8}})
			feed(serial(2, 1))
			if got := p.ProjectProviderSnapshot(time.Second, time.Hour); got.State != "receiving" {
				t.Fatal("first serial", got.State)
			}
			next := serial(2, 1)
			switch name {
			case "different", "new-generation":
				next = serial(2, 9)
			case "other-module":
				next = serial(5, 9)
			case "reordered":
				next = serial(2, 9)
				next = [][8]byte{next[0], next[2], next[1]}
			}
			if name == "new-generation" {
				b := providerTestSource()
				generation = 2
				if err := p.Start(b, generation); err != nil {
					t.Fatal(err)
				}
				a = b
			}
			feed(next)
			got := p.ProjectProviderSnapshot(time.Second, time.Hour)
			if (got.State == "conflict") != (name == "different") || (name == "different" && (got.Promotable || len(got.Current) != 0)) {
				t.Fatal(name, got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: a later accepted non-balance LINK record carrying a smaller or invalid configured cell count while the
// enables accepted from an earlier 0x1801 beyond that count stayed current. LastGood keeps the earlier enables.
func TestCX17CellCountChangeDemotesBalanceEnables(t *testing.T) {
	for _, name := range []string{"count-smaller", "count-invalid", "unchanged"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1801, [8]byte{0xff, 0xff, 1, 2, 3, 4, 6, 0})
			i.CellCount, i.CellCountValid = 16, true
			i.BalanceGate = MeasurementGate{Configured: true, Valid: true}
			i.HardwareAgeKnown = true
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			now := i.PCS.Evidence.Monotonic
			if got := p.ProjectProviderSnapshot(now, time.Second); got.Current["balance/enable_16"].Status != "accepted" {
				t.Fatal(got.State, got.Current["balance/enable_16"])
			}
			i.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &i, 0x1806<<16|0xf401, true, [8]byte{1, 0, 2, 7, 0, 3, 0, 4})
			switch name {
			case "count-smaller":
				i.CellCount = 4
			case "count-invalid":
				i.CellCountValid = false
			}
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(now, time.Second)
			if got.State != "receiving" {
				t.Fatal(got.State)
			}
			for slot := 1; slot <= 16; slot++ {
				key := fmt.Sprintf("balance/enable_%d", slot)
				want := "accepted"
				if name == "count-invalid" {
					want = "absent"
				} else if name == "count-smaller" && slot > 4 {
					want = "unconfigured"
				}
				f := got.Current[key]
				if f.Status != want || (want != "accepted" && (f.Unit != "" || f.Meaning != "")) || got.LastGood[key].Status != "accepted" {
					t.Fatal(key, f, got.LastGood[key])
				}
			}
		})
	}
}

// Catches: a changed serial slot, or a serial slot validity changed in either direction, after the generation's
// baseline leaving the predecessor's telemetry current and promotable until (or forever, once the assembler rejects
// every serial) another complete serial arrives. An unchanged serial slot association keeps receiving.
func TestCX17SerialSlotAssociationChangeConflicts(t *testing.T) {
	serial := [][8]byte{{0, 1, 1, 2, 3, 4, 5, 6}, {1, 7, 8, 9, 10, 11, 12, 13}, {2, 14, 15, 16, 0, 0, 0, 0}}
	telemetry := [8]byte{0x14, 0xbe, 0xff, 0xf6, 0, 10, 50, 0xe4}
	for _, name := range []string{"slot-after-serial", "validity-cleared", "validity-set", "unchanged"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			i.PCS.SerialSlotValid, i.PCS.SerialSlot = name != "validity-set", 1
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			send := func(id uint32, payload [8]byte) {
				i.PCS.Evidence.Sequence++
				i.PCS.Evidence.Monotonic += time.Millisecond
				replayTestFrame(t, &i, id, false, payload)
				providerTestSend(t, a, p, 1, i)
			}
			send(0x313, telemetry)
			if name != "validity-set" {
				for _, payload := range serial {
					send(0x324, payload)
				}
			}
			if got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour); got.State != "receiving" || !got.Promotable {
				t.Fatal("baseline", got.State, got.Promotable)
			}
			switch name {
			case "slot-after-serial":
				i.PCS.SerialSlot = 2
			case "validity-cleared":
				i.PCS.SerialSlotValid = false
			case "validity-set":
				i.PCS.SerialSlotValid = true
			}
			send(0x313, telemetry)
			got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
			conflict := name != "unchanged"
			if (got.State == "conflict") != conflict || got.Promotable == conflict || (conflict && len(got.Current) != 0) {
				t.Fatal(name, got.State, got.Promotable, len(got.Current))
			}
			if last := got.Raw[len(got.Raw)-1]; conflict && last.Status != "conflict" {
				t.Fatal("raw", last.Status)
			}
		})
	}
}

// Catches: complete 0x18fb serial announcements being ignored by the LINK identity assembly when no 0x1802 record
// was received, so a replacement peer announcing a different complete serial under the same configured selection
// never raised the sticky conflict. The same serial repeated does not conflict.
func TestCX17LINKSerialWithoutIdentityRecordConflicts(t *testing.T) {
	serial := func(first byte) [][8]byte {
		return [][8]byte{{15, first, 2, 3, 4, 5, 6, 7}, {47, 8, 9, 10, 11, 12, 13, 14}, {79, 15, 16, 0, 0, 0, 0, 0}}
	}
	for _, name := range []string{"same", "different"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x18fb, serial(1)[0])
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			i.PCS.Evidence.Sequence = 0
			feed := func(frames [][8]byte) {
				for _, payload := range frames {
					i.PCS.Evidence.Sequence++
					replayTestFrame(t, &i, 0x18fb<<16|0xf401, true, payload)
					providerTestSend(t, a, p, 1, i)
				}
			}
			feed(serial(1))
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" {
				t.Fatal("first serial", got.State)
			}
			next := serial(1)
			if name == "different" {
				next = serial(9)
			}
			feed(next)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if (got.State == "conflict") != (name == "different") || (name == "different" && (got.Promotable || len(got.Current) != 0)) {
				t.Fatal(name, got.State, got.Promotable, len(got.Current))
			}
		})
	}
}

// Catches: a malformed first observation (negative field age, so admission withholds it) establishing the member,
// native association and serial slot baselines, so the following valid observation carrying its correct
// associations permanently conflicted the generation. The malformed raw record is still retained.
func TestCX17MalformedFirstObservationDoesNotSetBaseline(t *testing.T) {
	cases := map[string]func(s *Selection, pcs *PCSSlotPCSInput){
		"member": func(s *Selection, pcs *PCSSlotPCSInput) { pcs.Member = s },
		"association": func(_ *Selection, pcs *PCSSlotPCSInput) {
			pcs.SlotValid, pcs.NativeSlot, pcs.GroupValid, pcs.NativeGroup = true, 3, true, 2
		},
		"serial-slot": func(_ *Selection, pcs *PCSSlotPCSInput) { pcs.SerialSlotValid, pcs.SerialSlot = true, 5 },
	}
	for name, poison := range cases {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			bad := i
			bad.PCS.FieldAge = -time.Second
			poison(&s, &bad.PCS)
			providerTestSend(t, a, p, 1, bad)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State == "conflict" || len(got.Raw) != 1 || got.Raw[0].Status != "invalid" || len(got.Current) != 0 {
				t.Fatal("malformed first", got.State, got.Raw, len(got.Current))
			}
			for sequence := uint64(2); sequence <= 3; sequence++ {
				i.PCS.Evidence.Sequence = sequence
				providerTestSend(t, a, p, 1, i)
				if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || !got.Promotable || got.Current["core/voltage"].Evidence.Sequence != sequence {
					t.Fatal("valid after malformed", sequence, got.State, got.Promotable)
				}
			}
		})
	}
}

// Catches: a local 0x321 announcement switching between an accepted family and a valid all-zero family_omitted
// announcement (either direction) within one generation without a sticky conflict, leaving telemetry promotable
// under contradictory identity states. A repeated omitted announcement keeps receiving, and on a selection whose
// family is raw an omitted-then-labelled change stays unfenced.
func TestCX17PCSFamilyOmittedTransitionConflicts(t *testing.T) {
	labelled := [8]byte{0, 'A', 'B', 'C', 'D', 0, 1, 2}
	omitted := [8]byte{0, 0, 0, 0, 0, 0, 1, 2}
	cases := []struct {
		name, release   string
		first, second   [8]byte
		firstStatusName string
		conflict        bool
	}{
		{"labelled-to-omitted", "0011", labelled, omitted, "identity/family", true},
		{"omitted-to-labelled", "0011", omitted, labelled, "identity/family_omitted", true},
		{"omitted-repeat", "0011", omitted, omitted, "identity/family_omitted", false},
		{"raw-family-unfenced", "0006", omitted, labelled, "identity/family_omitted", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			c := s.SelectionSpecValue()
			c.Release = tc.release
			s = replayTestReselect(t, &i, c)
			p, err := NewPCSProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			replayTestFrame(t, &i, 0x321, false, tc.first)
			providerTestSend(t, a, p, 1, i)
			if got := p.ProjectProviderSnapshot(time.Second, time.Second); got.State != "receiving" || got.Current[tc.firstStatusName].Status != "accepted" {
				t.Fatal("first", got.State, got.Current[tc.firstStatusName])
			}
			i.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &i, 0x321, false, tc.second)
			providerTestSend(t, a, p, 1, i)
			got := p.ProjectProviderSnapshot(time.Second, time.Second)
			if (got.State == "conflict") != tc.conflict || (tc.conflict && (got.Promotable || len(got.Current) != 0 || got.LastGood[tc.firstStatusName].Status != "accepted")) {
				t.Fatal(tc.name, got.State, got.Promotable, len(got.Current), got.LastGood[tc.firstStatusName])
			}
			if !tc.conflict && (got.State != "receiving" || !got.Promotable) {
				t.Fatal("unfenced", got.State, got.Promotable)
			}
		})
	}
}

// Catches: a wrong-route or malformed LINK observation carrying smaller or invalid cell and sensor counts demoting
// the retained cells, sensors and balance enables of an accepted cycle with rejected evidence. Only an admitted
// observation reconciles; the admitted control still demotes.
func TestCX17RejectedLINKObservationDoesNotReconcile(t *testing.T) {
	for _, name := range []string{"wrong-route-smaller", "wrong-route-invalid", "malformed-smaller", "malformed-invalid", "admitted-smaller"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestLINK(t, 0x1801, [8]byte{0xff, 0xff, 1, 2, 3, 4, 6, 0})
			i.CellCount, i.CellCountValid = 16, true
			i.SensorCount, i.SensorCountValid = 4, true
			i.BalanceGate = MeasurementGate{Configured: true, Valid: true}
			i.HardwareAgeKnown = true
			for n := range i.CellGates {
				i.CellGates[n] = MeasurementGate{Configured: true, Valid: true}
			}
			for n := range i.SensorGates {
				i.SensorGates[n] = MeasurementGate{Configured: true, Valid: true}
			}
			p, _ := NewLINKProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			send := func(id uint32, payload [8]byte) {
				i.PCS.Evidence.Sequence++
				i.PCS.Evidence.Monotonic += time.Millisecond
				replayTestFrame(t, &i, id, true, payload)
				providerTestSend(t, a, p, 1, i)
			}
			i.PCS.Evidence.Sequence = 0
			send(0x1801<<16|0xf401, [8]byte{0xff, 0xff, 1, 2, 3, 4, 6, 0})
			send(0x1811<<16|0xf401, [8]byte{5, 0, 6, 0, 7, 0, 8, 0})
			send(0x1830<<16|0xf401, [8]byte{0x40, 1, 0x41, 1, 0x42, 1, 0x43, 1})
			keys := []string{"cells/cell_5", "cells/cell_8", "sensors/sensor_2", "sensors/sensor_4", "balance/enable_3", "balance/enable_16"}
			got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
			for _, key := range keys {
				if got.State != "receiving" || got.Current[key].Status != "accepted" {
					t.Fatal("accepted cycle", key, got.State, got.Current[key])
				}
			}
			id := uint32(0x1810<<16 | 0xf401)
			switch name {
			case "wrong-route-smaller", "wrong-route-invalid":
				id = 0x1810<<16 | 0xf402
			case "malformed-smaller", "malformed-invalid":
				i.PCS.FieldAge = -time.Second
			}
			switch name {
			case "wrong-route-invalid", "malformed-invalid":
				i.CellCountValid, i.SensorCountValid = false, false
			default:
				i.CellCount, i.SensorCount = 2, 1
			}
			send(id, [8]byte{1, 0, 2, 0, 3, 0, 4, 0})
			got = p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Hour)
			admitted := name == "admitted-smaller"
			if last := got.Raw[len(got.Raw)-1]; got.State != "receiving" || (last.Status == "invalid") == admitted {
				t.Fatal("raw", got.State, last.Status)
			}
			for _, key := range keys {
				f := got.Current[key]
				if want := map[bool]string{false: "accepted", true: "unconfigured"}[admitted]; f.Status != want || got.LastGood[key].Status != "accepted" {
					t.Fatal(name, key, f, got.LastGood[key])
				}
			}
		})
	}
}

// Catches: a malformed observation (negative field age, rejected by admission) that also carries a changed
// member or native association conflicting the generation before admission rejects it, so rejected evidence
// cleared the current fields and a later valid observation could never recover.
func TestCX17MalformedObservationWithChangedAssociationDoesNotConflict(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	if err := p.Start(a, 1); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	baseline := p.ProjectProviderSnapshot(time.Second, time.Second)
	if baseline.State == "conflict" || len(baseline.Current) == 0 {
		t.Fatalf("baseline not established: %s %d", baseline.State, len(baseline.Current))
	}
	bad := i
	bad.PCS.Evidence.Sequence = 2
	bad.PCS.FieldAge = -time.Second
	bad.PCS.Member = &s
	bad.PCS.SlotValid, bad.PCS.NativeSlot = true, 9
	providerTestSend(t, a, p, 1, bad)
	got := p.ProjectProviderSnapshot(time.Second, time.Second)
	if got.State == "conflict" || len(got.Current) != len(baseline.Current) || got.Raw[len(got.Raw)-1].Status != "invalid" {
		t.Fatalf("rejected evidence changed the generation: state %s current %d raw %s", got.State, len(got.Current), got.Raw[len(got.Raw)-1].Status)
	}
	good := i
	good.PCS.Evidence.Sequence = 3
	providerTestSend(t, a, p, 1, good)
	after := p.ProjectProviderSnapshot(time.Second, time.Second)
	if after.State == "conflict" || len(after.Current) == 0 {
		t.Fatalf("valid observation after a rejected one did not keep receiving: %s %d", after.State, len(after.Current))
	}
}

// Catches: a changed index scope, a raised minimum or a lowered maximum after the generation's baseline leaving
// extrema indexes accepted under bounds or a scope the current input no longer supports. Unchanged bounds keep
// receiving.
func TestCX17IndexBoundsAssociationChangeConflicts(t *testing.T) {
	for _, name := range []string{"scope", "minimum-raised", "maximum-lowered", "unchanged"} {
		t.Run(name, func(t *testing.T) {
			s, i := replayTestFixture(t)
			i.PCS.IndexValid, i.PCS.IndexScope, i.PCS.IndexMinimum, i.PCS.IndexMaximum = true, "local", 0, 15
			replayTestFrame(t, &i, 0x319, false, [8]byte{0, 0x0c, 0, 0x0b, 0, 12, 3, 0})
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			now := i.PCS.Evidence.Monotonic
			if got := p.ProjectProviderSnapshot(now, time.Second); got.Current["core/maximum_index"].Status != "accepted" || got.Current["core/maximum_index"].Numerator != 12 {
				t.Fatal(got.State, got.Current["core/maximum_index"])
			}
			next := i
			next.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &next, 0x314, false, [8]byte{0x27, 0x10, 0, 0, 0, 0, 0, 0})
			switch name {
			case "scope":
				next.PCS.IndexScope = "aggregate"
			case "minimum-raised":
				next.PCS.IndexMinimum = 13
			case "maximum-lowered":
				next.PCS.IndexMaximum = 8
			}
			providerTestSend(t, a, p, 1, next)
			got := p.ProjectProviderSnapshot(now, time.Second)
			if name == "unchanged" {
				if got.State != "receiving" || !got.Promotable || got.Current["core/maximum_index"].Status != "accepted" {
					t.Fatal(got.State, got.Promotable, got.Current["core/maximum_index"])
				}
				return
			}
			if got.State != "conflict" || got.Promotable || len(got.Current) != 0 || got.LastGood["core/maximum_index"].Status != "accepted" {
				t.Fatal(got.State, got.Promotable, len(got.Current), got.LastGood["core/maximum_index"])
			}
		})
	}
}

// providerTestGroupFixture is the default PCS-standard fixture moved to another project release.
func providerTestGroupFixture(t *testing.T, release, group string) (Selection, ReplayInput) {
	t.Helper()
	s, i := replayTestFixture(t)
	c := s.SelectionSpecValue()
	c.Release = release
	s, err := NewSelection(c)
	if err != nil || s.SelectionGroup() != group {
		t.Fatal(err, s.SelectionGroup())
	}
	i.Selection = s
	return s, i
}

// Catches: early-layout 0x316 cells 5..8 accepted under a count of eight staying current after an admitted record
// whose count no longer configures them (smaller count) or is invalid. Fields freshly decoded keep their own status,
// LastGood is untouched and an unchanged count keeps the cells accepted.
func TestCX17PCSGaugeCountChangeDemotesEarlyCells(t *testing.T) {
	for _, name := range []string{"count-smaller", "count-invalid", "count-zero", "unchanged"} {
		t.Run(name, func(t *testing.T) {
			s, i := providerTestGroupFixture(t, "0005", "L1")
			i.PCS.CountValid, i.PCS.Count = true, 8
			replayTestFrame(t, &i, 0x316, false, [8]byte{0x0c, 0x80, 0x0c, 0x81, 0x0c, 0x82, 0x0c, 0x83})
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			now := i.PCS.Evidence.Monotonic
			before := p.ProjectProviderSnapshot(now, time.Second)
			for pos := 5; pos <= 8; pos++ {
				if f := before.Current[fmt.Sprintf("gauge/316/cell_%d", pos)]; f.Status != "accepted" {
					t.Fatal(pos, before.State, f)
				}
			}
			next := i
			next.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &next, 0x314, false, [8]byte{0x27, 0x10, 0, 0, 0, 0, 0, 0})
			switch name {
			case "count-smaller":
				next.PCS.Count = 6
			case "count-invalid":
				next.PCS.CountValid = false
			case "count-zero":
				next.PCS.Count = 0
			}
			providerTestSend(t, a, p, 1, next)
			got := p.ProjectProviderSnapshot(now, time.Second)
			if got.State != "receiving" {
				t.Fatal(got.State)
			}
			for pos := 5; pos <= 8; pos++ {
				key := fmt.Sprintf("gauge/316/cell_%d", pos)
				want := "accepted"
				switch {
				case name == "count-invalid":
					want = "absent"
				case name == "count-zero", name == "count-smaller" && pos > 6:
					want = "unconfigured"
				}
				f := got.Current[key]
				if f.Status != want || (want != "accepted" && (f.Unit != "" || f.Meaning != "")) || got.LastGood[key].Status != "accepted" {
					t.Fatal(key, f, got.LastGood[key])
				}
			}
		})
	}
}

// Catches: the release-11 count-aware fine average temperature staying current after an admitted record whose count
// is invalid or zero. LastGood is untouched and a still-valid nonzero count keeps the temperature accepted.
func TestCX17Release11TemperatureCountChangeDemotes(t *testing.T) {
	for _, name := range []string{"count-invalid", "count-zero", "count-smaller"} {
		t.Run(name, func(t *testing.T) {
			s, i := providerTestGroupFixture(t, "0011", "L8")
			i.PCS.CountValid, i.PCS.Count = true, 8
			p, _ := NewPCSProvider(s)
			a := providerTestSource()
			if err := p.Start(a, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			providerTestSend(t, a, p, 1, i)
			now := i.PCS.Evidence.Monotonic
			if got := p.ProjectProviderSnapshot(now, time.Second); got.Current["core/temperature"].Status != "accepted" {
				t.Fatal(got.State, got.Current["core/temperature"])
			}
			next := i
			next.PCS.Evidence.Sequence = 2
			replayTestFrame(t, &next, 0x314, false, [8]byte{0x27, 0x10, 0, 0, 0, 0, 0, 0})
			want := "accepted"
			switch name {
			case "count-invalid":
				next.PCS.CountValid, want = false, "absent"
			case "count-zero":
				next.PCS.Count, want = 0, "unconfigured"
			case "count-smaller":
				next.PCS.Count = 1
			}
			providerTestSend(t, a, p, 1, next)
			got := p.ProjectProviderSnapshot(now, time.Second)
			f := got.Current["core/temperature"]
			if got.State != "receiving" || f.Status != want || (want != "accepted" && (f.Unit != "" || f.Meaning != "")) || got.LastGood["core/temperature"].Status != "accepted" {
				t.Fatal(got.State, f, got.LastGood["core/temperature"])
			}
		})
	}
}

// Catches: a gauge or temperature count reconciliation on a group whose decoder has no count gate. The default
// layout keeps its accepted core temperature when an admitted record carries an invalid count.
func TestCX17CountReconciliationOnlyForCountGatedGroups(t *testing.T) {
	s, i := replayTestFixture(t)
	p, _ := NewPCSProvider(s)
	a := providerTestSource()
	if err := p.Start(a, 1); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	providerTestSend(t, a, p, 1, i)
	next := i
	next.PCS.Evidence.Sequence = 2
	replayTestFrame(t, &next, 0x314, false, [8]byte{0x27, 0x10, 0, 0, 0, 0, 0, 0})
	providerTestSend(t, a, p, 1, next)
	got := p.ProjectProviderSnapshot(i.PCS.Evidence.Monotonic, time.Second)
	if got.State != "receiving" || got.Current["core/temperature"].Status != "accepted" {
		t.Fatal(got.State, got.Current["core/temperature"])
	}
}
