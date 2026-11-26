package driver

import (
	"crypto/sha256"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

// GreeZoneRelation is one configured association of a unit with a zone.
// Priority is the unit's declared priority as a source of that zone, and
// SettingOwner declares the unit the owner of the zone's setting,
// independently of any read score.
type GreeZoneRelation struct {
	Zone         string `json:"zone"`
	Basis        string `json:"basis"`
	Priority     *int32 `json:"priority,omitempty"`
	SettingOwner bool   `json:"setting_owner,omitempty"`
}

// GreeUnit is one configured unit: the operator's equipment anchor, the
// documented unit coordinate of its register image and addressed-write target,
// and its optional zone relations.
type GreeUnit struct {
	Anchor string             `json:"anchor"`
	Unit   uint8              `json:"unit"`
	Zones  []GreeZoneRelation `json:"zones,omitempty"`
}

// GreeUnits is the instance-local set of configured units.
type GreeUnits []GreeUnit

// Validate rejects an entry without an anchor, a repeated anchor, a repeated
// coordinate, and a relation without a zone or a basis.
func (u GreeUnits) Validate() error {
	anchors, coords := map[string]bool{}, map[uint8]string{}
	for _, unit := range u {
		if !identityPart.MatchString(unit.Anchor) {
			return fmt.Errorf("gree unit anchor %q is not a valid token", unit.Anchor)
		}
		if anchors[unit.Anchor] {
			return fmt.Errorf("gree unit anchor %q is configured twice", unit.Anchor)
		}
		anchors[unit.Anchor] = true
		if other, dup := coords[unit.Unit]; dup {
			return fmt.Errorf("gree units %q and %q claim the same coordinate %d", other, unit.Anchor, unit.Unit)
		}
		coords[unit.Unit] = unit.Anchor
		zones := map[string]bool{}
		for _, z := range unit.Zones {
			if !identityPart.MatchString(z.Zone) || z.Basis == "" {
				return fmt.Errorf("gree unit %q has a relation without a valid zone and basis", unit.Anchor)
			}
			if zones[z.Zone] {
				return fmt.Errorf("gree unit %q relates to zone %q twice", unit.Anchor, z.Zone)
			}
			zones[z.Zone] = true
		}
	}
	return nil
}

func (u GreeUnits) find(anchor string) (GreeUnit, bool) {
	for _, unit := range u {
		if unit.Anchor == anchor {
			return unit, true
		}
	}
	return GreeUnit{}, false
}

// Equipment returns the row input for one configured unit: its anchor, the
// native class the register image decoded, its data-family routing key on the
// interface, and its producer node. The class is native evidence, not
// identity.
func (u GreeUnits) Equipment(bridge semdriver.BridgeID, instance semdriver.InstanceKey, iface canbus.InterfaceIdentity, anchor string, class canbusreg.GreeVRFDeviceClass) (gree.Equipment, error) {
	unit, ok := u.find(anchor)
	if !ok {
		return gree.Equipment{}, fmt.Errorf("gree unit %q is not configured", anchor)
	}
	producer, err := GreeEquipmentKey(bridge, instance, anchor).Ref()
	if err != nil {
		return gree.Equipment{}, err
	}
	return gree.Equipment{
		Anchor: semdriver.AnchorKey{Slot: anchor}, Class: class, Producer: producer,
		Key: canbusreg.GreeVRFRegisterKey{Interface: iface, Family: canbusreg.GreeVRFData, Unit: unit.Unit},
	}, nil
}

func relationBasis(unit GreeUnit, z GreeZoneRelation) []semreg.EvidenceRef {
	raw := fmt.Sprintf("%s|%d|%s|%s", unit.Anchor, unit.Unit, z.Zone, z.Basis)
	return []semreg.EvidenceRef{{
		Owner: "driver.canbusreg", Kind: "evidence.configuration.gree_zone",
		Digest:   semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw)))),
		Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone,
	}}
}

// Relations returns the configured air-supply link of every unit and zone
// relation, in configuration order, with the configured basis.
func (u GreeUnits) Relations(bridge semdriver.BridgeID, instance semdriver.InstanceKey) ([]semdriver.LinkClaim, error) {
	var out []semdriver.LinkClaim
	for _, unit := range u {
		from, err := GreeEquipmentKey(bridge, instance, unit.Anchor).Ref()
		if err != nil {
			return nil, err
		}
		for _, z := range unit.Zones {
			to, err := ZoneKey(bridge, instance, z.Zone).Ref()
			if err != nil {
				return nil, err
			}
			link, ok := gree.Relation("air_supply", from, to, semreg.RelationConfiguration, relationBasis(unit, z))
			if !ok {
				return nil, fmt.Errorf("gree unit %q relation to zone %q is not admissible", unit.Anchor, z.Zone)
			}
			out = append(out, link)
		}
	}
	return out, nil
}

// SettingTargets returns the configured indoor-unit targets for the row roles,
// given the native class each configured anchor decoded. A unit with no
// decoded class has no target.
func (u GreeUnits) SettingTargets(bridge semdriver.BridgeID, instance semdriver.InstanceKey, iface canbus.InterfaceIdentity, classes map[string]canbusreg.GreeVRFDeviceClass) ([]gree.SettingTarget, error) {
	var out []gree.SettingTarget
	for _, unit := range u {
		class, ok := classes[unit.Anchor]
		if !ok {
			continue
		}
		eq, err := u.Equipment(bridge, instance, iface, unit.Anchor, class)
		if err != nil {
			return nil, err
		}
		for _, z := range unit.Zones {
			out = append(out, gree.SettingTarget{Equipment: eq, Zone: mustZone(bridge, instance, z.Zone), ZoneBasis: relationBasis(unit, z)})
		}
	}
	return out, nil
}

func mustZone(bridge semdriver.BridgeID, instance semdriver.InstanceKey, zone string) semdriver.NodeRef {
	ref, _ := ZoneKey(bridge, instance, zone).Ref()
	return ref
}

// SettingOwner returns the declared owner of a zone's setting. With no
// declaration or more than one, no owner resolves and no route exists.
func (u GreeUnits) SettingOwner(zone string) (semdriver.AnchorKey, bool) {
	var owner semdriver.AnchorKey
	for _, unit := range u {
		for _, z := range unit.Zones {
			if z.Zone == zone && z.SettingOwner {
				if owner.Slot != "" {
					return semdriver.AnchorKey{}, false
				}
				owner = semdriver.AnchorKey{Slot: unit.Anchor}
			}
		}
	}
	return owner, owner.Slot != ""
}

// Priority returns the declared priority of the unit as a source of the zone.
func (u GreeUnits) Priority(anchor, zone string) (*int32, bool) {
	unit, ok := u.find(anchor)
	if !ok {
		return nil, false
	}
	for _, z := range unit.Zones {
		if z.Zone == zone && z.Priority != nil {
			p := *z.Priority
			return &p, true
		}
	}
	return nil, false
}
