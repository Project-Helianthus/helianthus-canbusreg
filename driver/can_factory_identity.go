package driver

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

// IdentityKind names the kind of node an identity key addresses.
type IdentityKind string

// The identity kinds of the CAN driver.
const (
	IdentityInterface IdentityKind = "interface"
	IdentityBattery   IdentityKind = "battery"
	IdentityPack      IdentityKind = "pack"
	IdentityModule    IdentityKind = "module"
	IdentityCell      IdentityKind = "cell"
	IdentityGree      IdentityKind = "gree-equipment"
	IdentityZone      IdentityKind = "thermal-system"
)

var identityPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]*$`)

// IdentityKey is the key of one published node: the bridge, the instance's
// configured key, the kind and the native or configured anchor parts. No
// member records arrival order, timing or a counter.
type IdentityKey struct {
	Bridge   semdriver.BridgeID
	Instance semdriver.InstanceKey
	Kind     IdentityKind
	Anchor   []string
}

// Ref returns the node reference of the key. Every part must be a non-empty
// token without the separator.
func (k IdentityKey) Ref() (semdriver.NodeRef, error) {
	if err := k.Bridge.Validate(); err != nil {
		return semdriver.NodeRef{}, err
	}
	parts := append([]string{string(k.Instance), string(k.Kind)}, k.Anchor...)
	for _, p := range parts {
		if !identityPart.MatchString(p) {
			return semdriver.NodeRef{}, fmt.Errorf("identity part %q is not a valid token", p)
		}
	}
	ref := semdriver.NodeRef{Bridge: k.Bridge, ID: semreg.ResourceID("can/" + strings.Join(parts, "/"))}
	return ref, ref.Validate()
}

func hex2(v uint8) string { return fmt.Sprintf("%02X", v) }

// InterfaceKey keys the node of the instance's CAN interface by its name.
func InterfaceKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey, name string) IdentityKey {
	return IdentityKey{bridge, instance, IdentityInterface, []string{name}}
}

// BatteryKey keys the battery of one instance.
func BatteryKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey) IdentityKey {
	return IdentityKey{bridge, instance, IdentityBattery, nil}
}

// PackKey keys a pack by its native pack anchor.
func PackKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey, pack string) IdentityKey {
	return IdentityKey{bridge, instance, IdentityPack, []string{pack}}
}

// ModuleKey keys a module by its source address and, only where the frame
// carries a slot byte, that slot. A nil slot adds nothing.
func ModuleKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey, source uint8, slot *uint8) IdentityKey {
	anchor := []string{hex2(source)}
	if slot != nil {
		anchor = append(anchor, "slot"+fmt.Sprint(*slot))
	}
	return IdentityKey{bridge, instance, IdentityModule, anchor}
}

// CellKey keys a cell by its module key's anchor and the native cell anchor
// the caller supplies. A position in a vector is not an anchor.
func CellKey(module IdentityKey, cell string) IdentityKey {
	return IdentityKey{module.Bridge, module.Instance, IdentityCell, append(append([]string{}, module.Anchor...), cell)}
}

// GreeEquipmentKey keys Gree equipment by its configured equipment anchor.
func GreeEquipmentKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey, anchor string) IdentityKey {
	return IdentityKey{bridge, instance, IdentityGree, []string{anchor}}
}

// ZoneKey keys a configured thermal system by its configured name.
func ZoneKey(bridge semdriver.BridgeID, instance semdriver.InstanceKey, zone string) IdentityKey {
	return IdentityKey{bridge, instance, IdentityZone, []string{zone}}
}

// SerialClaim returns the unbound match claim of a reported serial for one
// node. A changed serial is a different claim; the node keeps its key and
// nothing is treated as a replacement.
func SerialClaim(member semdriver.NodeRef, serial string, proof []semreg.EvidenceRef) (semdriver.MatchClaim, error) {
	if serial == "" {
		return semdriver.MatchClaim{}, fmt.Errorf("serial is empty")
	}
	claim := semdriver.MatchClaim{
		Member: member, Scheme: "serial",
		Key:   semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(serial)))),
		Proof: proof,
	}
	return claim, claim.Validate()
}
