package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
)

// Profile names an instance's configured CAN protocol profile.
type Profile string

// The configurable profiles. The common projection is selected by its own name
// and never stands in for a revision.
const (
	ProfileV104    Profile = "growatt-bms-can-v104"
	ProfileV108    Profile = "growatt-bms-can-v108"
	ProfileCommon  Profile = "growatt-bms-can-common"
	ProfileMonitor Profile = "growatt-bms-can-monitor"
	ProfilePCS     Profile = "growatt-bms-can-pcs"
	ProfileLINK    Profile = "growatt-bms-can-link"
)

// V104Role is the configured transmit role of a V1.04 instance. The only
// documented selected transmitter role is RoleInverter; an absent role leaves
// the instance observing only.
type V104Role string

// RoleInverter selects the documented V1.04 inverter side.
const RoleInverter V104Role = "inverter"

// TransportConfig is the strict transport operand of an instance.
type TransportConfig struct {
	Interface string `json:"interface"`
}

// CommonSelection is the asserted scope context of a separately selected
// common projection. Its members mirror canbusreg.SelectionSpec without the
// interface, which is the instance's own.
type CommonSelection struct {
	Flavor      string `json:"flavor,omitempty"`
	PCSFlavor   string `json:"pcs_flavor,omitempty"`
	ProjectCode string `json:"project_code,omitempty"`
	Release     string `json:"release,omitempty"`
	Choice      string `json:"choice,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Bank        string `json:"bank,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Group       string `json:"group,omitempty"`
	Slot        string `json:"slot,omitempty"`
	RouteSet    bool   `json:"route_set,omitempty"`
	Destination uint8  `json:"destination,omitempty"`
	Source      uint8  `json:"source,omitempty"`
	Epoch       uint64 `json:"epoch,omitempty"`
}

// Options is the strict options operand of an instance.
// CacheAge is the configured freshness window of a PCS or LINK instance.
// PCS is that instance's one selection. Sources is a LINK instance's ordered
// per-source selections, including the coordinator route when one is configured.
// A value that is already a member of the typed selection is not repeated here.
type Options struct {
	Profile   Profile               `json:"profile"`
	V104Role  V104Role              `json:"v104_role,omitempty"`
	Selection *CommonSelection      `json:"selection,omitempty"`
	CacheAge  time.Duration         `json:"cache_age,omitempty"`
	PCS       *PCSSelection         `json:"pcs,omitempty"`
	Sources   []LINKSourceSelection `json:"sources,omitempty"`
}

// InstanceConfig is one validated configured instance.
type InstanceConfig struct {
	Interface string
	Options   Options
}

func (s CommonSelection) spec(identity canbus.InterfaceIdentity) canbusreg.SelectionSpec {
	return canbusreg.SelectionSpec{
		Interface: identity, Flavor: s.Flavor, PCSFlavor: s.PCSFlavor, ProjectCode: s.ProjectCode,
		Release: s.Release, Choice: s.Choice, Direction: s.Direction, Scope: s.Scope, Bank: s.Bank,
		Endpoint: s.Endpoint, Group: s.Group, Slot: s.Slot, RouteSet: s.RouteSet,
		Destination: s.Destination, Source: s.Source, Epoch: s.Epoch,
	}
}

func decodeStrict(raw json.RawMessage, into any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("operand is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after the operand")
	}
	return nil
}

// Owns reports whether a configuration record is addressed to this driver: its
// transport operand names a CAN interface, or its options name a profile of
// this driver. A record of another driver is not owned and is claimed by no
// plan; an owned record that is invalid is still reported by ParseConfig.
func Owns(transport, options json.RawMessage) bool {
	var t map[string]json.RawMessage
	if json.Unmarshal(transport, &t) == nil {
		if _, ok := t["interface"]; ok {
			return true
		}
	}
	var o struct {
		Profile string `json:"profile"`
	}
	if json.Unmarshal(options, &o) == nil {
		return strings.HasPrefix(o.Profile, "growatt-bms-can-")
	}
	return false
}

// ParseConfig decodes and validates the transport and options operands of one
// instance. It dials nothing.
func ParseConfig(transport, options json.RawMessage) (InstanceConfig, error) {
	var t TransportConfig
	if err := decodeStrict(transport, &t); err != nil {
		return InstanceConfig{}, fmt.Errorf("transport: %w", err)
	}
	if !receiver.ValidInterfaceName(t.Interface) {
		return InstanceConfig{}, fmt.Errorf("transport: invalid CAN interface name %q", t.Interface)
	}
	var o Options
	if err := decodeStrict(options, &o); err != nil {
		return InstanceConfig{}, fmt.Errorf("options: %w", err)
	}
	switch o.Profile {
	case ProfileV104, ProfileV108, ProfileCommon, ProfileMonitor, ProfilePCS, ProfileLINK:
	default:
		return InstanceConfig{}, fmt.Errorf("options: unsupported profile %q", o.Profile)
	}
	switch o.V104Role {
	case "":
	case RoleInverter:
		if o.Profile != ProfileV104 {
			return InstanceConfig{}, fmt.Errorf("options: v104_role applies only to profile %q", ProfileV104)
		}
	default:
		return InstanceConfig{}, fmt.Errorf("options: unrecognized v104_role %q", o.V104Role)
	}
	if o.Selection != nil {
		if o.Profile != ProfileCommon {
			return InstanceConfig{}, fmt.Errorf("options: selection applies only to profile %q", ProfileCommon)
		}
		if _, err := o.commonProjector(canbus.InterfaceIdentity{}, t.Interface); err != nil {
			return InstanceConfig{}, fmt.Errorf("options: %w", err)
		}
	}
	return finishParse(t.Interface, o)
}

func (o Options) commonProjector(identity canbus.InterfaceIdentity, name string) (*canbusreg.GrowattLowVoltageBMSCommonProjector, error) {
	if identity.Name() == "" {
		probe, err := canbus.NewInterfaceIdentity(name, 1)
		if err != nil {
			return nil, err
		}
		identity = probe
	}
	if o.Selection == nil {
		projector, ok := canbusreg.NewGrowattLowVoltageBMSCommonProjector(identity)
		if !ok {
			return nil, errors.New("common projection requires a valid interface")
		}
		return projector, nil
	}
	selection, err := canbusreg.NewSelection(o.Selection.spec(identity))
	if err != nil {
		return nil, err
	}
	projector, ok := canbusreg.NewGrowattLowVoltageBMSCommonProjectorFromSelection(selection)
	if !ok {
		return nil, errors.New("common projection selection requires scope local or aggregate")
	}
	return projector, nil
}
