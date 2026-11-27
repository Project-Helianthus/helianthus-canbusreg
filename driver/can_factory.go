// Package driver is the public constructor package of the CAN driver. It
// exports one factory that plans one instance per configured CAN interface and
// profile and opens each interface only through the CAN transport receiver.
package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
)

// Kind is the driver kind of every instance this factory plans.
const Kind semdriver.DriverKind = "canbusreg"

const (
	transportProvider     = "helianthus-canbus"
	transportAccessDomain = "socketcan"
)

type openFunc func(string) (receiver.Receiver, error)

// Factory is the SemReg driver factory of the CAN driver.
type Factory struct {
	mu      sync.Mutex
	planned map[semdriver.InstanceKey]InstanceConfig
	open    openFunc
}

// NewFactory returns the CAN driver factory. Instances open their interface
// through receiver.Open.
func NewFactory() *Factory {
	return &Factory{planned: map[semdriver.InstanceKey]InstanceConfig{}, open: receiver.Open}
}

// Descriptor returns the families the factory declares.
func (f *Factory) Descriptor() semdriver.Descriptor { return semdriver.Descriptor{} }

// Plan plans one instance for one enabled configuration. It dials nothing.
func (f *Factory) Plan(c semdriver.Config) ([]semdriver.InstancePlan, error) {
	if err := semdriver.InstanceKey(c.Instance).Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, nil
	}
	if !Owns(c.Transport, c.Options) {
		return nil, nil
	}
	config, err := ParseConfig(c.Transport, c.Options)
	if err != nil {
		return nil, err
	}
	plan := semdriver.InstancePlan{
		Driver:   Kind,
		Instance: semdriver.InstanceKey(c.Instance),
		Transports: []semdriver.TransportClaim{{
			Provider: transportProvider, ConfigKey: config.Interface,
			AccessDomain: transportAccessDomain, Share: semdriver.ShareSerialized,
		}},
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.planned[plan.Instance] = config
	f.mu.Unlock()
	return []semdriver.InstancePlan{plan}, nil
}

// Construct builds one planned instance. It opens nothing.
func (f *Factory) Construct(_ context.Context, plan semdriver.InstancePlan) (semdriver.Instance, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if plan.Driver != Kind {
		return nil, fmt.Errorf("plan is for driver %q, not %q", plan.Driver, Kind)
	}
	f.mu.Lock()
	config, ok := f.planned[plan.Instance]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("instance %q was not planned by this factory", plan.Instance)
	}
	return &instance{key: plan.Instance, config: config, open: f.open}, nil
}

type decoder func(canbusreg.Evidence) (any, bool)

type instance struct {
	key    semdriver.InstanceKey
	config InstanceConfig
	open   openFunc

	mu       sync.Mutex
	started  bool
	fenced   bool
	session  semdriver.Session
	cancel   context.CancelFunc
	recv     receiver.Receiver
	done     chan struct{}
	latest   any
	received uint64
	skipped  uint64
	err      error
}

func (i *instance) newDecoder(identity canbus.InterfaceIdentity) (decoder, error) {
	switch i.config.Options.Profile {
	case ProfileV104:
		replay, ok := canbusreg.NewGrowattLowVoltageBMSV104QualificationReplay(identity)
		if !ok {
			return nil, errors.New("invalid interface for the V1.04 profile")
		}
		return func(e canbusreg.Evidence) (any, bool) { return replay.Apply(e) }, nil
	case ProfileV108:
		profile, err := canbusreg.NewGrowattBMSProfile(identity, canbusreg.BMSRevisionV108)
		if err != nil {
			return nil, err
		}
		registry := canbusreg.NewRegistry(profile)
		return func(e canbusreg.Evidence) (any, bool) {
			c := registry.Classify(e)
			return c.Projection, c.Projection != nil
		}, nil
	case ProfileCommon:
		projector, err := i.config.Options.commonProjector(identity, i.config.Interface)
		if err != nil {
			return nil, err
		}
		return func(e canbusreg.Evidence) (any, bool) { return projector.Apply(e) }, nil
	case ProfileMonitor:
		monitor, ok := canbusreg.NewGrowattMonitorAddressed(identity)
		if !ok {
			return nil, errors.New("invalid interface for the monitor profile")
		}
		return func(e canbusreg.Evidence) (any, bool) { return monitor.Apply(e) }, nil
	}
	return nil, fmt.Errorf("unsupported profile %q", i.config.Options.Profile)
}

// Start opens the configured interface through the CAN transport receiver and
// feeds every received record to the instance's own decoder and row publisher.
func (i *instance) Start(ctx context.Context, session semdriver.Session) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.started {
		return errors.New("instance already started")
	}
	if i.fenced {
		return errors.New("instance withdrawn")
	}
	recv, err := i.open(i.config.Interface)
	if err != nil {
		return fmt.Errorf("open %s: %w", i.config.Interface, err)
	}
	identity := recv.Identity()
	if identity.Name() != i.config.Interface {
		_ = recv.Close()
		return fmt.Errorf("receiver bound %q, configured %q", identity.Name(), i.config.Interface)
	}
	decode, err := i.newDecoder(identity)
	if err != nil {
		_ = recv.Close()
		return err
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	i.started, i.cancel, i.recv, i.done, i.session = true, cancel, recv, make(chan struct{}), session
	go i.run(loopCtx, recv, decode, i.done)
	return nil
}

func (i *instance) run(ctx context.Context, recv receiver.Receiver, decode decoder, done chan struct{}) {
	defer close(done)
	for {
		record, err := recv.Receive(ctx)
		if err != nil {
			if ctx.Err() == nil {
				i.mu.Lock()
				i.err = err
				i.mu.Unlock()
			}
			return
		}
		frame, ferr := canbus.DecodeSocketCANRecord(record.Raw)
		i.mu.Lock()
		if i.fenced {
			i.mu.Unlock()
			continue
		}
		if ferr != nil {
			i.skipped++
			i.mu.Unlock()
			continue
		}
		evidence := canbusreg.Evidence{Frame: frame, Interface: record.Interface, Sequence: record.Sequence, Monotonic: record.Monotonic}
		copy(evidence.RawRecord[:], record.Raw)
		i.received++
		if out, ok := decode(evidence); ok {
			i.latest = out
		}
		session := i.session
		i.mu.Unlock()
		i.publishReceived(ctx, session, evidence)
	}
}

// FenceWithdrawal stops further decoding without blocking or calling back.
func (i *instance) FenceWithdrawal() {
	i.mu.Lock()
	i.fenced = true
	i.mu.Unlock()
}

// Stop ends the receive loop, closes the interface and reports the close.
func (i *instance) Stop(ctx context.Context) semdriver.CloseReport {
	i.mu.Lock()
	cancel, recv, done := i.cancel, i.recv, i.done
	i.cancel, i.recv = nil, nil
	i.mu.Unlock()
	if recv == nil {
		return semdriver.CloseReport{Confirmed: true}
	}
	cancel()
	closeErr := recv.Close()
	select {
	case <-done:
	case <-ctx.Done():
		return semdriver.CloseReport{Err: ctx.Err()}
	}
	return semdriver.CloseReport{Confirmed: closeErr == nil, Err: closeErr}
}
