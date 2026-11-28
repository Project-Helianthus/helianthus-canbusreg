package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/periodic"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/operation"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

// The feed path publishes driver generation "1". A setting uses the same
// generation so a report from another generation cannot confirm it.
const (
	sendGeneration     uint64              = 1
	sendGenerationText semreg.Uint64       = "1"
	setTemperatureID   semreg.DefinitionID = "thermal.operation.set_temperature"
	setpointField      semreg.DefinitionID = "thermal.setpoint.temperature_setpoint"

	schemaGreeSetting  semdriver.SchemaRef = "native.can.gree.setting"
	schemaGreeProperty semdriver.SchemaRef = "native.can.gree.property"
	schemaGreeClock    semdriver.SchemaRef = "native.can.gree.clock"
	schemaPCS          semdriver.SchemaRef = "native.can.pcs.command"
	schemaLINK         semdriver.SchemaRef = "native.can.link.command"
	schemaV108         semdriver.SchemaRef = "native.can.v108.command"
	scopeNative        semdriver.ScopeID   = "native"

	kindGreeSetting  = "gree-setting"
	kindGreeOnce     = "gree-setting-once"
	kindGreeProperty = "gree-property"
	kindGreeClock    = "gree-clock"
	kindPCSClock     = "pcs-clock"
	kindPCSDiscovery = "pcs-discovery"
	kindPCSExtended  = "pcs-extended"
	kindLINKClock    = "link-clock"
	kindV108Event    = "v108-event"
	kindV108Serial   = "v108-serial"
	kindV108History  = "v108-history"
)

// Clock is the instance clock. Now is the receive-clock offset the publisher
// stamps. Wait blocks one step of a triggered cadence until the caller
// releases it; a profile without that cadence leaves Wait nil.
type Clock struct {
	Now  func() time.Duration
	Wait func(context.Context, time.Duration) error
}

// ResponseRecord is the correspondence of one offered frame with the newest
// outstanding query that accepted it. An unmatched frame leaves the exchanges
// unchanged. History body bytes stay raw.
type ResponseRecord struct {
	Accepted        bool
	SerialAccepted  bool
	HistoryAccepted bool
	Completed       bool
	BatteryID       uint8
	Serial          []byte
	HistoryBody     []byte
	BodyState       string
}
type heldExchange struct {
	serial  *growattlowvoltage.SerialExchange
	history *growattlowvoltage.HistoryExchange
}

// Path evaluates command rows for one configured instance and hands encoded
// frames to the transport publication API. Nothing is sent until a caller
// invokes a command or PublishDue runs a scheduled heartbeat. The instance
// receive loop does not call PublishDue, OfferFrame or Stop; the caller does.
// A Path must not be copied.
type Path struct {
	config   InstanceConfig
	identity canbus.InterfaceIdentity
	clock    Clock
	pub      *periodic.Publisher
	reports  *reportQueue

	events    *growattlowvoltage.Dispatcher
	serials   *growattlowvoltage.SerialQuerier
	histories *growattlowvoltage.HistoryQuerier

	mu          sync.Mutex
	stopped     bool
	outstanding []heldExchange

	units     GreeUnits
	targets   []gree.SettingTarget
	zoneNames map[semdriver.NodeRef]string
}

// NewPath builds a send path for one configured instance. Scheduling the
// heartbeat does not send it. A V1.08 path requires Clock.Wait and sends
// nothing until a caller triggers a row.
func NewPath(config InstanceConfig, identity canbus.InterfaceIdentity, sender periodic.Sender, clock Clock) (*Path, error) {
	if identity.Name() == "" || identity.Name() != config.Interface {
		return nil, fmt.Errorf("sender bound %q, configured %q", identity.Name(), config.Interface)
	}
	if clock.Now == nil {
		return nil, errors.New("send path requires the instance clock")
	}
	pub, err := periodic.New(config.Interface, sender, clock.Now)
	if err != nil {
		return nil, err
	}
	path := &Path{
		config: config, identity: identity, clock: clock, pub: pub,
		reports: newReportQueue(), zoneNames: map[semdriver.NodeRef]string{},
	}
	if err := path.arm(); err != nil {
		path.Stop()
		return nil, err
	}
	return path, nil
}

// OpenSend builds the send path of this instance on the receiver identity.
// Start does not publish, and instance Stop does not stop the path.
func (i *instance) OpenSend(identity canbus.InterfaceIdentity, sender periodic.Sender, clock Clock) (*Path, error) {
	if i == nil {
		return nil, errors.New("nil instance")
	}
	return NewPath(i.config, identity, sender, clock)
}
func (p *Path) arm() error {
	if p.config.Options.Profile == ProfileV104 && p.config.Options.V104Role == RoleInverter {
		frame, err := growattlowvoltage.EncodeV104InverterHeartbeat(growattlowvoltage.V104Selection{
			Profile: growattlowvoltage.V104Profile, Role: growattlowvoltage.V104InverterRole,
		})
		if err != nil {
			return err
		}
		if _, err := p.pub.Add(frame, time.Second); err != nil {
			return err
		}
	}
	if p.config.Options.Profile != ProfileV108 {
		return nil
	}
	if p.clock.Wait == nil {
		return errors.New("event cadence requires the instance wait")
	}
	sel := growattlowvoltage.Selection{Profile: growattlowvoltage.Profile}
	events, err := growattlowvoltage.NewDispatcher(sel, p.sendTransport, p.clock.Wait)
	if err != nil {
		return err
	}
	serials, err := growattlowvoltage.NewSerialQuerier(sel, p.sendTransport)
	if err != nil {
		return err
	}
	histories, err := growattlowvoltage.NewHistoryQuerier(sel, p.sendTransport)
	if err != nil {
		return err
	}
	p.events, p.serials, p.histories = events, serials, histories
	return nil
}

// PublishDue sends heartbeat frames that are due on Clock.Now. A path with no
// scheduled heartbeat sends nothing.
func (p *Path) PublishDue() error {
	if p == nil || p.pub == nil {
		return errors.New("nil send path")
	}
	return p.pub.PublishDue()
}

// Stop ends publication for this path. Later commands send nothing. It does
// not stop the instance receive loop, and instance Stop does not call it.
func (p *Path) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopped = true
	pub, reports := p.pub, p.reports
	p.mu.Unlock()
	if pub != nil {
		pub.Supersede()
	}
	if reports != nil {
		reports.stop()
	}
}
func (p *Path) callerRefused() error {
	if p == nil {
		return errors.New("nil send path")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return periodic.ErrSuperseded
	}
	if p.config.Options.Profile == ProfileMonitor {
		return errors.New("monitor profile documents no command frame")
	}
	return nil
}
func (p *Path) requireV108() error {
	if err := p.callerRefused(); err != nil {
		return err
	}
	if p.config.Options.Profile != ProfileV108 || p.events == nil {
		return growattlowvoltage.ErrNotV108
	}
	return nil
}
func (p *Path) emit(frame canbus.Frame, encodeErr error) (periodic.Submission, error) {
	if err := p.callerRefused(); err != nil {
		return periodic.Submission{}, err
	}
	if encodeErr != nil {
		return periodic.Submission{}, encodeErr
	}
	sub, err := p.pub.Command(frame)
	if err != nil {
		return sub, err
	}
	if !sub.Accepted {
		return sub, errors.New("transport did not accept the frame")
	}
	return sub, nil
}
func (p *Path) sendTransport(frame canbus.Frame) error {
	_, err := p.emit(frame, nil)
	return err
}

// TriggerClockEvent sends the V1.08 event cadence for the supplied clock
// event. Any other profile, a denied event and a path that has not been
// triggered send nothing.
func (p *Path) TriggerClockEvent(ctx context.Context, event growattlowvoltage.ClockEvent) error {
	if err := p.requireV108(); err != nil {
		return err
	}
	return p.events.Trigger(ctx, event)
}

// ObserveHour triggers the V1.08 event cadence only when now falls in a later
// calendar hour than previous. The event bytes are the caller's. A clock that
// stays in the same hour sends nothing.
func (p *Path) ObserveHour(ctx context.Context, previous, now time.Time, event growattlowvoltage.ClockEvent) error {
	if !growattlowvoltage.ClockSyncDue(previous, now) {
		return nil
	}
	return p.TriggerClockEvent(ctx, event)
}

// QuerySerial sends one V1.08 serial query for batteryID and retains the
// exchange. Battery 0 is a target. Any other profile sends nothing.
func (p *Path) QuerySerial(batteryID uint8) (*growattlowvoltage.SerialExchange, error) {
	if err := p.requireV108(); err != nil {
		return nil, err
	}
	ex, err := p.serials.Query(batteryID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.outstanding = append(p.outstanding, heldExchange{serial: ex})
	p.mu.Unlock()
	return ex, nil
}

// QueryHistory sends one V1.08 history query for batteryID and retains the
// exchange. Any other profile sends nothing.
func (p *Path) QueryHistory(batteryID uint8) (*growattlowvoltage.HistoryExchange, error) {
	if err := p.requireV108(); err != nil {
		return nil, err
	}
	ex, err := p.histories.Query(batteryID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.outstanding = append(p.outstanding, heldExchange{history: ex})
	p.mu.Unlock()
	return ex, nil
}

// OfferFrame offers one received frame to outstanding V1.08 query exchanges,
// newest first, and stops at the first exchange that accepts it so one frame
// is not consumed by two queries. The observation rows of the low-voltage BMS
// profile stay on the receive path; this exchange records query correspondence
// only. The serial query exists on this path, and a history body stays raw
// with the pending state the history row reports. A refused offer does not
// clear an exchange that already completed.
func (p *Path) OfferFrame(frame canbus.Frame) ResponseRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	var newest string
	if n := len(p.outstanding); n > 0 && p.outstanding[n-1].history != nil {
		newest = p.outstanding[n-1].history.BodyState()
	}
	for i := len(p.outstanding) - 1; i >= 0; i-- {
		item := p.outstanding[i]
		if item.serial != nil {
			if err := item.serial.Offer(frame); err != nil {
				continue
			}
			return ResponseRecord{
				Accepted: true, SerialAccepted: true, Completed: item.serial.Completed(),
				BatteryID: item.serial.BatteryID(), Serial: item.serial.SerialBytes(),
			}
		}
		if item.history == nil {
			continue
		}
		if err := item.history.Offer(frame); err != nil {
			continue
		}
		return ResponseRecord{
			Accepted: true, HistoryAccepted: true, Completed: item.history.Completed(),
			BatteryID: item.history.BatteryID(), HistoryBody: item.history.Body(),
			BodyState: item.history.BodyState(),
		}
	}
	return ResponseRecord{BodyState: newest}
}

// SendPCSClock encodes the standard PCS clock row and submits it once.
func (p *Path) SendPCSClock(in pcs.ClockInput) error {
	frame, err := pcs.EncodeClockInput(in)
	_, err = p.emit(frame, err)
	return err
}

// SendPCSDiscovery encodes the standard PCS discovery row and submits it once.
func (p *Path) SendPCSDiscovery(in pcs.DiscoveryControlInput) error {
	frame, err := pcs.EncodeDiscoveryControlInput(in)
	_, err = p.emit(frame, err)
	return err
}

// SendPCSUndocumented reports an identifier that has no PCS command encoder
// and submits nothing. A documented clock or discovery row is not sent here.
func (p *Path) SendPCSUndocumented(id uint32) error {
	if err := p.callerRefused(); err != nil {
		return err
	}
	for _, row := range pcs.Commands() {
		if row.Identifier == id {
			return fmt.Errorf("identifier %#x is a documented command row", id)
		}
	}
	for _, row := range pcs.IncompleteInputs() {
		if row.Identifier == id {
			return fmt.Errorf("identifier %#x has no command encoder", id)
		}
	}
	for _, row := range pcs.ExtendedCommands() {
		if row.Identifier == id {
			return fmt.Errorf("identifier %#x is an extended command row", id)
		}
	}
	return fmt.Errorf("identifier %#x has no command row", id)
}

// SendPCSExtendedTime encodes one extended PCS time row for the stated contract.
func (p *Path) SendPCSExtendedTime(contract pcs.ExtendedContract, in pcs.ExtendedTimeInput) error {
	frame, err := pcs.EncodeExtendedTimeInput(contract, in)
	_, err = p.emit(frame, err)
	return err
}

// SendPCSStackState encodes one extended PCS stack-state row for the stated contract.
func (p *Path) SendPCSStackState(contract pcs.ExtendedContract, in pcs.StackStateInput) error {
	frame, err := pcs.EncodeStackStateInput(contract, in)
	_, err = p.emit(frame, err)
	return err
}

// SendPCSSecondaryQuantity encodes one extended PCS secondary-quantity row for the stated contract.
func (p *Path) SendPCSSecondaryQuantity(contract pcs.ExtendedContract, in pcs.SecondaryQuantityInput) error {
	frame, err := pcs.EncodeSecondaryQuantityInput(contract, in)
	_, err = p.emit(frame, err)
	return err
}

// SendPCSUpdateCoordination encodes one extended PCS update-coordination row for the stated contract.
func (p *Path) SendPCSUpdateCoordination(contract pcs.ExtendedContract, in pcs.UpdateCoordinationInput) error {
	frame, err := pcs.EncodeUpdateCoordinationInput(contract, in)
	_, err = p.emit(frame, err)
	return err
}

// SendLINKClock encodes the LINK clock row and submits it once.
func (p *Path) SendLINKClock(in link.ClockInput) error {
	frame, err := link.EncodeClockInput(in)
	_, err = p.emit(frame, err)
	return err
}
func (p *Path) sendGree(cmd canbusreg.GreeVRFCommand) error {
	frame, err := publicationFrame(cmd)
	_, err = p.emit(frame, err)
	return err
}

// SendGreeProperty encodes one Gree property row and submits it once.
func (p *Path) SendGreeProperty(property uint8, seed uint32, unit uint8, value uint16) error {
	return gree.SendProperty(property, seed, unit, value, p.sendGree)
}

// SendGreeClock encodes one Gree time or clock row and submits it once.
func (p *Path) SendGreeClock(name string, arg canbusreg.GreeVRFTime) error {
	return gree.SendClock(name, arg, p.sendGree)
}

// SendSetTemperature encodes the set-temperature operation for the unique
// declared owner of the zone and submits one frame. No owner, more than one
// owner, or a denied argument sends nothing. Local acceptance is not a
// protocol acknowledgement and does not update readback.
func (p *Path) SendSetTemperature(zone semdriver.NodeRef, argument semreg.Quantity, limits *semdriver.ValueLimits) error {
	if err := p.callerRefused(); err != nil {
		return err
	}
	p.mu.Lock()
	name, known := p.zoneNames[zone]
	units, targets := append(GreeUnits(nil), p.units...), append([]gree.SettingTarget(nil), p.targets...)
	p.mu.Unlock()
	if !known {
		return errors.New("zone is not configured")
	}
	owner, ok := units.SettingOwner(name)
	if !ok {
		return errors.New("no unique declared setting owner")
	}
	return gree.SendTemperature(zone, owner, targets, argument, limits, p.sendGree)
}

// ExecuteGreeSetting submits one addressed setting with the caller's deadline
// and waits on the report queue installed before any send. The deadline is
// converted with the transport receive clock. A missing or expired deadline
// sends nothing.
func (p *Path) ExecuteGreeSetting(ctx context.Context, deadline time.Time, class canbusreg.GreeVRFDeviceClass, unit, typ uint8, coordinate, raw uint16) (canbusreg.GreeVRFWriteOutcome, error) {
	if err := p.callerRefused(); err != nil {
		return canbusreg.GreeVRFWriteOutcome{}, err
	}
	call := gree.SettingCall{Setting: p.settingFrom(class, unit, typ, coordinate, raw), Deadline: deadline}
	return call.Execute(ctx, p.reports.out, p.submitSetting, receiver.MonotonicAt)
}
func (p *Path) settingFrom(class canbusreg.GreeVRFDeviceClass, unit, typ uint8, coordinate, raw uint16) canbusreg.GreeVRFAddressedSetting {
	return canbusreg.GreeVRFAddressedSetting{
		Key:        canbusreg.GreeVRFRegisterKey{Interface: p.identity, Family: canbusreg.GreeVRFData, Unit: unit, Type: typ},
		Class:      class,
		Generation: sendGeneration,
		Coordinate: coordinate,
		RawValue:   raw,
	}
}
func (p *Path) submitSetting(ctx context.Context, cmd canbusreg.GreeVRFCommand) (canbusreg.GreeVRFSubmission, error) {
	if err := ctx.Err(); err != nil {
		return canbusreg.GreeVRFSubmission{}, err
	}
	frame, err := publicationFrame(cmd)
	sub, err := p.emit(frame, err)
	return canbusreg.GreeVRFSubmission{Accepted: sub.Accepted, Monotonic: sub.Monotonic}, err
}
func publicationFrame(cmd canbusreg.GreeVRFCommand) (canbus.Frame, error) {
	var id canbus.Identifier
	var err error
	if cmd.Identifier > 0x7ff {
		id, err = canbus.NewExtendedID(cmd.Identifier)
	} else {
		id, err = canbus.NewStandardID(cmd.Identifier)
	}
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(id, cmd.Data)
}

// OfferGreeReport queues one decoded setting report. The queue is installed
// before any send and does not drop a report offered while a submit callback
// is still running.
func (p *Path) OfferGreeReport(report canbusreg.GreeVRFSettingReport) {
	if p == nil || p.reports == nil {
		return
	}
	p.reports.offer(report)
}

// BindGree stores the configured units of this instance and the native class
// of each anchor. A later call replaces the set. SettingTargets are built
// from those classes; a unit without a class has no target.
func (p *Path) BindGree(bridge semdriver.BridgeID, instance semdriver.InstanceKey, units GreeUnits, classes map[string]canbusreg.GreeVRFDeviceClass) error {
	if err := p.callerRefused(); err != nil {
		return err
	}
	if err := units.Validate(); err != nil {
		return err
	}
	targets, err := units.SettingTargets(bridge, instance, p.identity, classes)
	if err != nil {
		return err
	}
	names := map[semdriver.NodeRef]string{}
	for _, unit := range units {
		for _, zone := range unit.Zones {
			ref, err := ZoneKey(bridge, instance, zone.Zone).Ref()
			if err != nil {
				return err
			}
			names[ref] = zone.Zone
		}
	}
	p.mu.Lock()
	p.units, p.targets, p.zoneNames = units, targets, names
	p.mu.Unlock()
	return nil
}

type commandArguments struct {
	Year             *uint16    `json:"year,omitempty"`
	Month            *uint8     `json:"month,omitempty"`
	Day              *uint8     `json:"day,omitempty"`
	Hour             *uint8     `json:"hour,omitempty"`
	Minute           *uint8     `json:"minute,omitempty"`
	Second           *uint8     `json:"second,omitempty"`
	Weekday          *uint8     `json:"weekday,omitempty"`
	Extra            *uint8     `json:"extra,omitempty"`
	BatteryID        *uint8     `json:"battery_id,omitempty"`
	Contract         string     `json:"contract,omitempty"`
	FrequencyEnable  *bool      `json:"frequency_enable,omitempty"`
	ClearFaultEnable *bool      `json:"clear_fault_enable,omitempty"`
	Unit             *uint8     `json:"unit,omitempty"`
	Class            *uint8     `json:"class,omitempty"`
	Value            *uint16    `json:"value,omitempty"`
	Deadline         *time.Time `json:"deadline,omitempty"`
	Seed             *uint32    `json:"seed,omitempty"`
	Property         *uint8     `json:"property,omitempty"`
	Phase            *uint8     `json:"phase,omitempty"`
	Parameter        *uint8     `json:"parameter,omitempty"`
	Destination      *uint8     `json:"destination,omitempty"`
	Source           *uint8     `json:"source,omitempty"`
	CurrentCode      *uint16    `json:"current_code,omitempty"`
	StateOfCharge    *uint16    `json:"state_of_charge,omitempty"`
	Percentage       *uint16    `json:"percentage,omitempty"`
	ByteFour         *uint8     `json:"byte_four,omitempty"`
}
type commandResult struct {
	Accepted  bool   `json:"accepted"`
	Monotonic int64  `json:"monotonic,omitempty"`
	Status    string `json:"status,omitempty"`
}

func byteOf(v *uint8) uint8 {
	if v == nil {
		return 0
	}
	return *v
}
func boolOf(v *bool) bool {
	return v != nil && *v
}
func (a commandArguments) year8() (uint8, error) {
	if a.Year != nil && *a.Year > 255 {
		return 0, fmt.Errorf("year %d does not fit one byte", *a.Year)
	}
	if a.Year == nil {
		return 0, nil
	}
	return uint8(*a.Year), nil
}
func (a commandArguments) year16() uint16 {
	if a.Year == nil {
		return 0
	}
	return *a.Year
}
func (a commandArguments) battery() (uint8, error) {
	if a.BatteryID == nil {
		return 0, errors.New("battery id is required")
	}
	return *a.BatteryID, nil
}
func resultOf(sub periodic.Submission, status string) ([]byte, error) {
	out := commandResult{Accepted: sub.Accepted, Status: status}
	if sub.Monotonic > 0 {
		out.Monotonic = int64(sub.Monotonic)
	}
	return json.Marshal(out)
}

// RegisterMethods registers every native command row of this path. A monitor
// profile registers nothing and sends nothing. Registration itself sends nothing.
func (p *Path) RegisterMethods(reg semdriver.NativeRegistry, node semdriver.NodeRef) error {
	if reg == nil {
		return errors.New("nil native registry")
	}
	if p.config.Options.Profile == ProfileMonitor {
		return nil
	}
	if p.config.Options.Profile == ProfileV108 {
		for _, method := range []struct{ local, kind string }{
			{growattlowvoltage.ClockEventRowID, kindV108Event},
			{growattlowvoltage.SerialQueryRowID, kindV108Serial},
			{growattlowvoltage.HistoryQueryRowID, kindV108History},
		} {
			if err := p.add(reg, node, method.local, schemaV108, method.kind, nil); err != nil {
				return err
			}
		}
	}
	for _, row := range pcs.Commands() {
		kind := kindPCSClock
		if row.Identifier == 0x6F0 {
			kind = kindPCSDiscovery
		}
		if err := p.add(reg, node, row.ID, schemaPCS, kind, row.Identifier); err != nil {
			return err
		}
	}
	for _, row := range pcs.ExtendedCommands() {
		if err := p.add(reg, node, row.ID, schemaPCS, kindPCSExtended, row.Identifier); err != nil {
			return err
		}
	}
	for _, row := range link.Commands() {
		if err := p.add(reg, node, row.ID, schemaLINK, kindLINKClock, row); err != nil {
			return err
		}
	}
	for _, row := range gree.CommandRows() {
		kind, schema := kindGreeClock, schemaGreeClock
		switch {
		case row.Setting != nil && row.SynchronousReadback:
			kind, schema = kindGreeSetting, schemaGreeSetting
		case row.Setting != nil:
			kind, schema = kindGreeOnce, schemaGreeSetting
		case row.Property != nil:
			kind, schema = kindGreeProperty, schemaGreeProperty
		}
		if err := p.add(reg, node, row.Name, schema, kind, row); err != nil {
			return err
		}
	}
	return nil
}
func (p *Path) add(reg semdriver.NativeRegistry, node semdriver.NodeRef, local string, schema semdriver.SchemaRef, kind string, row any) error {
	spec := semdriver.MethodSpec{Local: semdriver.MemberID(local), Schema: schema, Scope: scopeNative}
	return reg.Method(node, spec, func(ctx context.Context, raw []byte) ([]byte, error) {
		return p.dispatch(ctx, kind, row, raw)
	})
}
func (p *Path) dispatch(ctx context.Context, kind string, row any, raw []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var args commandArguments
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	switch kind {
	case kindGreeSetting, kindGreeOnce:
		return p.invokeGreeSetting(ctx, row.(gree.CommandRow), args, kind == kindGreeSetting)
	case kindGreeProperty:
		return p.invokeGreeProperty(row.(gree.CommandRow), args)
	case kindGreeClock:
		return p.invokeGreeClock(row.(gree.CommandRow), args)
	case kindPCSClock:
		return p.invokePCSClock(args)
	case kindPCSDiscovery:
		return p.invokePCSDiscovery(args)
	case kindPCSExtended:
		return p.invokeExtended(row.(uint32), args)
	case kindLINKClock:
		return p.invokeLINK(args)
	case kindV108Event, kindV108Serial, kindV108History:
		return p.invokeV108(ctx, kind, args)
	default:
		return nil, fmt.Errorf("unknown command kind %q", kind)
	}
}
func (p *Path) invokeGreeSetting(ctx context.Context, row gree.CommandRow, args commandArguments, sync bool) ([]byte, error) {
	if row.Setting == nil {
		return nil, errors.New("setting row has no coordinate")
	}
	var deadline time.Time
	if args.Deadline != nil {
		deadline = *args.Deadline
	}
	var raw uint16
	if args.Value != nil {
		raw = *args.Value
	}
	class := canbusreg.GreeVRFDeviceClass(byteOf(args.Class))
	if !sync {
		err := gree.SendSetting(gree.SettingCall{Setting: p.settingFrom(class, byteOf(args.Unit), row.Setting.Type, row.Setting.Start, raw), Deadline: deadline}, p.sendGree)
		if err != nil {
			return nil, err
		}
		return resultOf(periodic.Submission{Accepted: true}, "")
	}
	out, err := p.ExecuteGreeSetting(ctx, deadline, class, byteOf(args.Unit), row.Setting.Type, row.Setting.Start, raw)
	if err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: out.Submission.Accepted, Monotonic: out.Submission.Monotonic}, string(out.Status))
}
func (p *Path) invokeGreeProperty(row gree.CommandRow, args commandArguments) ([]byte, error) {
	if row.Property == nil {
		return nil, errors.New("property row has no property")
	}
	if args.Property != nil && *args.Property != *row.Property {
		return nil, errors.New("property does not match the registered row")
	}
	var value uint16
	if args.Value != nil {
		value = *args.Value
	}
	var seed uint32
	if args.Seed != nil {
		seed = *args.Seed
	}
	if err := p.SendGreeProperty(*row.Property, seed, byteOf(args.Unit), value); err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func (p *Path) invokeGreeClock(row gree.CommandRow, args commandArguments) ([]byte, error) {
	year, err := args.year8()
	if err != nil {
		return nil, err
	}
	arg := canbusreg.GreeVRFTime{Year: year, Month: byteOf(args.Month), Day: byteOf(args.Day), Hour: byteOf(args.Hour), Minute: byteOf(args.Minute), Second: byteOf(args.Second), Weekday: byteOf(args.Weekday)}
	if err := p.SendGreeClock(row.Name, arg); err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func (p *Path) invokePCSClock(args commandArguments) ([]byte, error) {
	year, err := args.year8()
	if err != nil {
		return nil, err
	}
	if err := p.SendPCSClock(pcs.ClockInput{YearOffset: year, Month: byteOf(args.Month), Day: byteOf(args.Day), Hour: byteOf(args.Hour), Minute: byteOf(args.Minute), Second: byteOf(args.Second)}); err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func (p *Path) invokePCSDiscovery(args commandArguments) ([]byte, error) {
	if err := p.SendPCSDiscovery(pcs.DiscoveryControlInput{Phase: byteOf(args.Phase), Parameter: byteOf(args.Parameter)}); err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func (p *Path) invokeExtended(id uint32, args commandArguments) ([]byte, error) {
	contract := pcs.ExtendedContract(args.Contract)
	year, err := args.year8()
	if err != nil {
		return nil, err
	}
	switch id {
	case 0x190BDFF2:
		err = p.SendPCSExtendedTime(contract, pcs.ExtendedTimeInput{YearOffset: year, Month: byteOf(args.Month), Day: byteOf(args.Day), Hour: byteOf(args.Hour), Minute: byteOf(args.Minute), Second: byteOf(args.Second)})
	case 0x191ADFF2:
		err = p.SendPCSStackState(contract, pcs.StackStateInput{CurrentCode: valueOf(args.CurrentCode), StateOfCharge: valueOf(args.StateOfCharge)})
	case 0x191CDFF2:
		err = p.SendPCSSecondaryQuantity(contract, pcs.SecondaryQuantityInput{Percentage: valueOf(args.Percentage), ByteFour: byteOf(args.ByteFour)})
	case 0x191EDFF2:
		err = p.SendPCSUpdateCoordination(contract, pcs.UpdateCoordinationInput{Parameter: byteOf(args.Parameter)})
	default:
		err = fmt.Errorf("identifier %#x has no extended encoder", id)
	}
	if err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func valueOf(v *uint16) uint16 {
	if v == nil {
		return 0
	}
	return *v
}
func (p *Path) invokeLINK(args commandArguments) ([]byte, error) {
	if err := p.SendLINKClock(link.ClockInput{
		Destination: byteOf(args.Destination), Source: byteOf(args.Source), Year: args.year16(),
		Month: byteOf(args.Month), Day: byteOf(args.Day), Hour: byteOf(args.Hour),
		Minute: byteOf(args.Minute), Second: byteOf(args.Second), Extra: byteOf(args.Extra),
	}); err != nil {
		return nil, err
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}
func (p *Path) invokeV108(ctx context.Context, kind string, args commandArguments) ([]byte, error) {
	switch kind {
	case kindV108Serial:
		battery, err := args.battery()
		if err != nil {
			return nil, err
		}
		if _, err := p.QuerySerial(battery); err != nil {
			return nil, err
		}
	case kindV108History:
		battery, err := args.battery()
		if err != nil {
			return nil, err
		}
		if _, err := p.QueryHistory(battery); err != nil {
			return nil, err
		}
	default:
		year, err := args.year8()
		if err != nil {
			return nil, err
		}
		event := growattlowvoltage.ClockEvent{
			FrequencyEnable: boolOf(args.FrequencyEnable), Year: year, Month: byteOf(args.Month), Day: byteOf(args.Day),
			Hour: byteOf(args.Hour), Minute: byteOf(args.Minute), Second: byteOf(args.Second), ClearFaultEnable: boolOf(args.ClearFaultEnable),
		}
		if err := p.TriggerClockEvent(ctx, event); err != nil {
			return nil, err
		}
	}
	return resultOf(periodic.Submission{Accepted: true}, "")
}

// settingRegistrar is the session port that records one setting executor.
type settingRegistrar interface {
	RegisterExecutor(semdriver.SettingRoute, semdriver.Executor) (func(), error)
}
type temperatureExec struct {
	path   *Path
	zone   semdriver.NodeRef
	owner  semdriver.AnchorKey
	limits *semdriver.ValueLimits
}

// Execute rechecks the caller context and the driver generation immediately
// before the set-temperature send. A denied call sends nothing. Local
// acceptance is recorded as acknowledged without a confirming readback.
func (e temperatureExec) Execute(ctx context.Context, d semdriver.Dispatch) (semdriver.Completion, error) {
	rejected := semdriver.Completion{Outcome: operation.OutcomeRejected}
	if err := ctx.Err(); err != nil {
		return rejected, err
	}
	if d.Guard.DriverGeneration != sendGenerationText || d.Request.Operation.ID != setTemperatureID {
		return rejected, errors.New("set temperature admission does not match this instance")
	}
	var qty *semreg.Quantity
	for _, arg := range d.Request.Arguments {
		if arg.ID == setpointField && arg.Value.Kind == semreg.ValueQuantity && arg.Value.Quantity != nil {
			qty = arg.Value.Quantity
			break
		}
	}
	if qty == nil {
		return rejected, errors.New("set temperature argument is absent")
	}
	if err := ctx.Err(); err != nil {
		return rejected, err
	}
	if d.Guard.DriverGeneration != sendGenerationText {
		return rejected, errors.New("driver generation changed before send")
	}
	e.path.mu.Lock()
	name, known := e.path.zoneNames[e.zone]
	units := append(GreeUnits(nil), e.path.units...)
	e.path.mu.Unlock()
	current, ok := units.SettingOwner(name)
	if !known || !ok || current != e.owner {
		return rejected, errors.New("no unique declared setting owner")
	}
	if err := e.path.SendSetTemperature(e.zone, *qty, e.limits); err != nil {
		return rejected, err
	}
	return semdriver.Completion{Outcome: operation.OutcomeAcknowledgedUnverified}, nil
}

// RegisterSetTemperature registers the set-temperature executor for a route
// whose subject and owner are a configured zone and unit. The executor reads
// the current owner at call time, so removing the declaration sends nothing.
func (p *Path) RegisterSetTemperature(reg settingRegistrar, route semdriver.SettingRoute) (func(), error) {
	if reg == nil {
		return nil, errors.New("nil setting registrar")
	}
	if err := p.callerRefused(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	targets := append([]gree.SettingTarget(nil), p.targets...)
	_, known := p.zoneNames[route.For.Subject]
	p.mu.Unlock()
	if !known {
		return nil, errors.New("route subject is not a configured zone")
	}
	var owner semdriver.AnchorKey
	matched := false
	for _, target := range targets {
		if target.Zone == route.For.Subject && target.Equipment.Producer == route.Owner.Producer {
			owner = target.Equipment.Anchor
			matched = true
			break
		}
	}
	if !matched {
		return nil, errors.New("route owner is not a configured unit of the zone")
	}
	return reg.RegisterExecutor(route, temperatureExec{path: p, zone: route.For.Subject, owner: owner, limits: route.Limits})
}

type reportQueue struct {
	mu      sync.Mutex
	pending []canbusreg.GreeVRFSettingReport
	wake    chan struct{}
	stopc   chan struct{}
	out     chan canbusreg.GreeVRFSettingReport
	done    chan struct{}
	closed  bool
}

func newReportQueue() *reportQueue {
	q := &reportQueue{
		wake:  make(chan struct{}),
		stopc: make(chan struct{}),
		out:   make(chan canbusreg.GreeVRFSettingReport),
		done:  make(chan struct{}),
	}
	go q.loop()
	return q
}
func (q *reportQueue) offer(report canbusreg.GreeVRFSettingReport) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pending = append(q.pending, report)
	close(q.wake)
	q.wake = make(chan struct{})
}
func (q *reportQueue) stop() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.stopc)
	}
	q.mu.Unlock()
	<-q.done
}
func (q *reportQueue) loop() {
	defer close(q.done)
	for {
		q.mu.Lock()
		var report canbusreg.GreeVRFSettingReport
		has := len(q.pending) > 0
		if has {
			report = q.pending[0]
		}
		wake := q.wake
		q.mu.Unlock()
		if has {
			select {
			case q.out <- report:
				q.mu.Lock()
				if len(q.pending) > 0 {
					q.pending = q.pending[1:]
				}
				q.mu.Unlock()
			case <-q.stopc:
				return
			}
			continue
		}
		select {
		case <-wake:
		case <-q.stopc:
			return
		}
	}
}
