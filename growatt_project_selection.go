package canbusreg

import (
	"fmt"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// SelectionSpec is an explicit caller assertion, never inferred from traffic.
// Native rules: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: PCS feature availability by version; Same-identity variants and native-contract selection.
type SelectionSpec struct {
	Interface                                                         canbus.InterfaceIdentity
	Flavor, PCSFlavor, ProjectCode, Release, Choice, Direction, Scope string
	Bank, Endpoint, Group, Slot                                       string
	RouteSet                                                          bool
	Destination, Source                                               uint8
	Epoch                                                             uint64
}

// Selection holds a validated caller assertion and its candidate contract group, layout and grade.
type Selection struct {
	spec                 SelectionSpec
	group, layout, grade string
}

// NewSelection validates explicit identity and context; unsupported combinations remain Unknown rather than being inferred from traffic.
func NewSelection(spec SelectionSpec) (Selection, error) {
	s := Selection{spec: spec, grade: "Unknown"}
	if spec.Interface.Name() == "" || spec.Interface.Index() <= 0 || len(spec.ProjectCode) != 4 || len(spec.Release) != 4 || !selectionASCII([]byte(spec.ProjectCode+spec.Release)) {
		return s, fmt.Errorf("invalid explicit identity")
	}
	for _, b := range []byte(spec.Release) {
		if b < '0' || b > '9' {
			return s, fmt.Errorf("invalid four-digit release")
		}
	}
	if spec.Choice != "" && spec.Choice != "legacy19" && spec.Choice != "release11" {
		return s, fmt.Errorf("invalid native-contract choice")
	}
	if spec.Scope != "local" && spec.Scope != "aggregate" {
		return s, fmt.Errorf("explicit scope required")
	}
	if spec.Bank == "" || spec.Endpoint == "" || spec.Epoch == 0 {
		return s, fmt.Errorf("explicit bank, endpoint and epoch required")
	}
	flavor := spec.Flavor
	if flavor == "link" {
		if !spec.RouteSet {
			return s, fmt.Errorf("explicit LINK route required")
		}
		flavor = spec.PCSFlavor
		if spec.Direction != "module-to-coordinator" {
			return s, nil
		}
	} else if spec.Direction != "bms-to-pcs" {
		return s, nil
	}
	if flavor != "pcs-standard" && flavor != "pcs-extended19" && flavor != "pcs-parallel" {
		return s, nil
	}
	identity := spec.ProjectCode + spec.Release
	group := ""
	switch identity {
	case "QAaT8252", "QAAA0002":
		group = "L0"
	case "QAAA0005", "QAAa2005", "QAAa3005", "QAAa5005", "QAAa6005", "QAAa9005", "QAAa9006":
		group = "L1"
	case "QAAA0006":
		group = "L2"
	case "QAAA0007", "QAAA0008", "QAAa2007", "QAAa6008", "QAAa6009", "QAAa7004", "QAAa8008":
		group = "L3"
	case "QAAA0009", "QAAA0010", "QAAa1011", "QAAa4010", "QAAa4011", "QAAa9010":
		group = "L4"
	case "QAAb5004", "QAAB0004":
		group = "L5"
	case "QAAB0002", "QAAB0003":
		group = "L6"
	case "QAAB0006":
		group = "L7"
	case "QAAA0011":
		group = "L8"
	case "QAAa9011":
		switch spec.Choice {
		case "legacy19":
			group = "L4"
		case "release11":
			group = "L8"
		default:
			if flavor != "pcs-parallel" {
				s.layout = "W"
			}
			return s, nil
		}
	}
	if (group == "L0" && flavor != "pcs-standard") || ((group == "L1" || group == "L2" || group == "L3" || group == "L4") && flavor == "pcs-parallel") || ((group == "L7" || group == "L8") && flavor == "pcs-extended19") {
		return s, nil
	}
	s.group = group
	switch group {
	case "L0", "L1":
		s.layout = "C"
	case "L2", "L3", "L4", "L6", "L8":
		s.layout = "W"
	case "L5", "L7":
		s.layout = "B"
	}
	if group != "" {
		s.grade = "Hypothesis"
	}
	if group == "L2" || group == "L8" {
		s.grade = "Proven"
	}
	return s, nil
}

// SelectionSpecValue returns the caller assertion by value.
func (s Selection) SelectionSpecValue() SelectionSpec { return s.spec }

// SelectionGroup returns the selected contract group, or empty when unresolved.
func (s Selection) SelectionGroup() string { return s.group }

// SelectionLayout returns the selected layout code, or empty when unresolved.
func (s Selection) SelectionLayout() string { return s.layout }

// SelectionGrade returns Proven, Hypothesis or Unknown for the contract selection, never physical qualification.
func (s Selection) SelectionGrade() string { return s.grade }
func selectionASCII(p []byte) bool {
	for _, b := range p {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

// NativeField separates candidate protocol meaning from physical qualification.
// Raw and Evidence are value copies; unknown units remain empty.
type NativeField struct {
	Numerator, Denominator             int64
	Unit, Meaning, Grade, Status       string
	Raw                                [8]byte
	Evidence                           Evidence
	Epoch                              uint64
	Scope, Bank, Endpoint, Group, Slot string
	Age                                time.Duration
	PhysicalQualified                  bool
}

// NativeResult retains native evidence and per-field candidate results separately from physical qualification.
type NativeResult struct {
	Selection         Selection
	Evidence          Evidence
	Raw               [8]byte
	Epoch             uint64
	Status            string
	Fields            map[string]NativeField
	PhysicalQualified bool
	// Member records the reporting-member dictionary selection used to decode
	// member-dependent fields; HasMember is false when none was supplied.
	Member    Selection
	HasMember bool
}

// SelectionNativeIndication requires a caller-supplied topology association for PCS
// evidence. Timing, matching identifiers and ordinary LINK frames supply none.
type SelectionNativeIndication struct {
	Input              LINKInput
	TopologyAssociated bool
}

// ConfirmNativeSelection checks associated evidence against an explicit choice; it cannot choose a contract from traffic.
func ConfirmNativeSelection(s Selection, indication SelectionNativeIndication) NativeResult {
	i := indication.Input
	r := selectionResult(s, i, "unsupported")
	c := s.spec
	if i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot {
		r.Status = "invalid"
		return r
	}
	f := i.Evidence.Frame
	if f.DLC() != 8 || f.RawDLC() != 8 {
		r.Status = "invalid"
		return r
	}
	contract := ""
	id := f.ID().Value()
	family := uint16(id >> 16)
	if f.ID().Extended() {
		switch family {
		case 0x1853, 0x1861:
			if lINKAdmission(s, i) {
				contract = "release11"
			}
		case 0x1869:
			if lINKReportAdmission(s, i) {
				contract = "release11"
			}
		}
	}
	if indication.TopologyAssociated && i.Direction == "bms-to-pcs" {
		if f.ID().Extended() {
			if family >= 0x1900 && family <= 0x1909 {
				contract = "legacy19"
			}
		} else {
			switch id {
			case 0x326, 0x327, 0x333, 0x334:
				contract = "release11"
			}
		}
	}
	if contract == "" {
		return r
	}
	// Evidence confirms or conflicts with an explicit choice; it cannot resolve one.
	if c.ProjectCode != "QAAa" || c.Release != "9011" || c.Choice == "" {
		return r
	}
	if contract != c.Choice {
		r.Status = "conflict"
	} else {
		r.Status = "confirmed"
	}
	return r
}

// SelectionFeatureGrade reports a candidate contract gate, never qualification.
func (s Selection) SelectionFeatureGrade(feature string) string {
	switch feature {
	case "three-word-pcs-gauge":
		if s.group == "L2" {
			return "Proven"
		}
	case "slots", "dtc-extensions":
		if s.group == "L8" {
			return "Proven"
		}
	case "reduced-configuration":
		if s.group == "L5" {
			return "Hypothesis"
		}
	case "broad-configuration":
		if s.group == "L6" {
			return "Hypothesis"
		}
	}
	return "Unknown"
}
