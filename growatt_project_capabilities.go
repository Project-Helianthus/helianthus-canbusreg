package canbusreg

import "fmt"

// ReceiverContext supplies independent receiver direction, role and capability context.
// Native contracts, Receive-handler classes / Native receiver capability matrix /
// Legacy receive behavior / Configuration and service channels:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// PCS host input / Maintenance and firmware coordination channels:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// These candidate descriptions never authorize an operation or qualify hardware.
type ReceiverContext struct {
	Direction, Role, Profile, Session, Form       string
	Assigned, CommunicationKnown, AddressingKnown bool
	CommunicationState                            uint8
	LocalCount                                    uint8
	LocalCountKnown                               bool
	NativeCount                                   uint32
}

// ReceiverResult retains receiver admission status and raw evidence without implying receiver capability qualification.
type ReceiverResult struct {
	Input                             LINKInput
	Context                           ReceiverContext
	Raw                               [8]byte
	Class, Grade, Status, Description string
	Admitted, PhysicalQualified       bool
	NativeFreshnessCount              uint32
}

type receiverCapabilityRule struct {
	pid                                                       uint32
	profile, direction, role, class, route, gate, description string
	groups                                                    uint16
}

func receiverCapabilityPIDName(pid uint32) string { return fmt.Sprintf("%04X", pid) }

// The finite table is private and is never extended by observed traffic.
var receiverRules = receiverCapabilityRules()

func receiverCapabilityRules() []receiverCapabilityRule {
	var rows []receiverCapabilityRule
	add := func(pid uint32, profile, direction, role, class, route, gate, description string, groups uint16) {
		rows = append(rows, receiverCapabilityRule{pid, profile, direction, role, class, route, gate, description, groups})
	}
	const all uint16 = 0x1ff
	for _, pid := range []uint32{0x1800, 0x1802, 0x1803, 0x1804, 0x1805, 0x1806, 0x1807, 0x1808, 0x180a, 0x180b, 0x180c, 0x1850, 0x1801, 0x1809, 0x1810, 0x1811, 0x1812, 0x1813, 0x1830, 0x1840, 0x1851, 0x1852, 0x1853, 0x1861, 0x1862, 0x18fb} {
		groups := all
		switch pid {
		case 0x1801, 0x1809, 0x1810, 0x1811, 0x1812, 0x1813, 0x1830, 0x1840:
			groups = 0xff
		case 0x1851, 0x18fb:
			groups = 0x1fe
		case 0x1852:
			groups = (1 << 3) | (1 << 4) | (1 << 5) | (1 << 7) | (1 << 8)
		case 0x1853:
			groups = 1 << 8
		case 0x1861:
			groups = (1 << 7) | (1 << 8)
		case 0x1862:
			groups = (1 << 4) | (1 << 8)
		}
		add(pid, "link", "module-to-coordinator", "coordinator", "T", "module", "assigned", "profile-specific raw retention; no forwarding inferred", groups)
	}
	for _, pid := range []uint32{0x1800, 0x1805, 0x180a, 0x1860, 0x18fe, 0x1869, 0x18b0, 0x18b1} {
		groups := all
		class, gate := "C", ""
		switch pid {
		case 0x1860:
			groups = 0x1fe
			gate = "legacy-sync"
		case 0x18fe, 0x18b1:
			groups = 0x1fe
		case 0x1869:
			groups = (1 << 7) | (1 << 8)
		}
		if pid == 0x18b0 || pid == 0x18b1 {
			class = "Q"
			gate = "maintenance"
		}
		add(pid, "link", "coordinator-to-module", "module", class, "coordinator", gate, "coordinator input; not a local measurement", groups)
	}
	add(0x18b0, "link", "service-to-module", "module", "Q", "service-broadcast", "maintenance", "distinct service update session route", all)
	for pid := uint32(0x1870); pid <= 0x1889; pid++ {
		groups := uint16(0x5f)
		if pid == 0x1871 || pid == 0x1877 || pid == 0x187b {
			groups |= 1 << 5
		}
		add(pid, "link", "service-to-module", "module", "Q", "matched", "maintenance", "configuration/service input; never an identity query", groups)
	}
	for _, pid := range []uint32{0x18f0, 0x188a, 0x188b, 0x188c, 0x18a0, 0x18fd, 0x18df, 0x1892, 0x1894, 0x1899, 0x18fc} {
		groups := all
		gate := "maintenance"
		class := "Q"
		description := "raw service input"
		switch pid {
		case 0x18f0:
			class = "C"
			gate = "addressing"
			description = "addressing-state-dependent bytes; no allocation or discovery"
		case 0x188a:
			description = "clock-shaped LE16 year B0, calendar B2-B6, extra B7; setter"
		case 0x188b:
			groups = 0x5f
			description = "LE16 configuration B0 and byte B2; maintenance only"
		case 0x18df:
			groups = 0x5f
		case 0x188c:
			description = "identity-overwriting fragments; maintenance only; no identity selection"
		case 0x18a0:
			description = "direction/session-dependent service input"
		case 0x18fd:
			description = "control B0 and desired LE16 mask B1; raw only; no clearing"
		case 0x1892:
			groups = 0x1fe
		case 0x1894:
			groups = 0x5e
		case 0x1899:
			groups = 0x1e
		case 0x18fc:
			groups = 1 << 4
			gate = "legacy-aging"
			description = "legacy configuration B3 and LE16 B4; output does not imply input"
		}
		add(pid, "link", "service-to-module", "module", class, "matched", gate, description, groups)
	}
	for _, pid := range []uint32{0x211, 0x212, 0x301} {
		gate := ""
		description := "standard-profile stateful heartbeat input"
		if pid == 0x211 {
			description = "battery calendar year-offset B1/calendar B2-B6; B0/B7 unused"
		}
		if pid == 0x212 {
			gate = "legacy-noop"
			description = "defined legacy/QAAB no-op; periodic 324 is not a response"
		}
		add(pid, "pcs-standard", "pcs-to-bms", "battery", "Q", "standard", gate, description, all)
	}
	for _, pid := range []uint32{0x6f0, 0x6ff} {
		description := "parallel stateful B0/B1 with zero tail"
		if pid == 0x6ff {
			description = "parallel count B0/BE16 peer B2; remaining bytes raw"
		}
		direction, role := "controller-to-participant", "participant"
		if pid == 0x6ff {
			direction = "participant-to-coordinator"
			role = "coordinator"
		}
		add(pid, "pcs-parallel", direction, role, "C", "standard", "parallel", description, 0x1e0)
	}
	for _, pid := range []uint32{0x190a, 0x190b, 0x190c, 0x191a, 0x191c, 0x191e} {
		description := map[uint32]string{0x190a: "profile/state input", 0x190b: "calendar input", 0x190c: "defined legacy no-op", 0x191a: "current/SOC setters LE16 B2/B4", 0x191c: "setters LE16 B2 and byte B4", 0x191e: "update coordination may propagate 18B1; no propagation here"}[pid]
		add(pid, "pcs-extended19", "coordinator-to-battery", "battery", "Q", "extended-pcs", "maintenance", description, 0x7e)
	}
	for _, pid := range []uint32{0x190c, 0x190d, 0x190e, 0x190f, 0x191b, 0x191c} {
		description := map[uint32]string{0x190c: "aggregate fault block 0", 0x190d: "aggregate fault block 1", 0x190e: "fault/status B0-B4/B6", 0x190f: "topology/count B6; not enumeration", 0x191b: "output LE16 limits B0/B2 and cell extrema B4/B6", 0x191c: "output temperatures B0/B1, secondary LE16 B2, zero B4-B7"}[pid]
		add(pid, "pcs-extended19", "gateway-to-pcs", "pcs", "A", "extended-pcs", "", description, 0x7e)
	}
	for _, pid := range []uint32{0x1918, 0x1919} {
		add(pid, "pcs-extended19", "service-to-gateway", "gateway", "Q", "gateway-service", "maintenance", "local gateway fragments/service; not slave enumeration", 0x7e)
	}
	for _, pid := range []uint32{0x782, 0x728, 0x7f8, 0x78f} {
		profile, direction, role := "pcs-standard", "service-to-battery", "battery"
		if pid == 0x7f8 || pid == 0x78f {
			profile = "pcs-parallel"
			role = "participant"
			direction = "service-to-participant"
		}
		if pid == 0x728 {
			direction = "battery-to-service"
			role = "service"
		}
		if pid == 0x78f {
			direction = "participant-to-service"
			role = "service"
		}
		add(pid, profile, direction, role, "Q", "standard", "maintenance", "separate release-11 maintenance route; no image compatibility inference", 1<<8)
	}
	for _, profile := range []string{"pcs-standard", "pcs-parallel"} {
		add(0x37f, profile, "controller-to-bms", "battery", "Q", "standard", "release11", "role/profile effects; no operation performed", 1<<8)
		add(0x3f7, profile, "bms-to-controller", "controller", "C", "standard", "status-form", "periodic and direct status remain distinct", 1<<8)
	}
	for _, pid := range []uint32{0x510, 0x511, 0x512, 0x513, 0x514, 0x515, 0x516, 0x517, 0x518, 0x519, 0x51a, 0x51b, 0x51c, 0x51d, 0x51e, 0x51f, 0x590} {
		add(pid, "pcs-parallel", "participant-to-coordinator", "coordinator", "C", "standard", "release11", "input B7 variant gate; not native 590 output", 1<<8)
	}
	return rows
}

// ClassifyReceiver only describes an explicitly selected receiver. No payload
// is decoded into measurements, identity, a client timeout, or a bus operation.
func ClassifyReceiver(s CXSelection, c ReceiverContext, i LINKInput) ReceiverResult {
	r := ReceiverResult{Input: i, Context: c, Raw: i.Evidence.Frame.Bytes(), Class: "U", Grade: "Unknown", Status: "invalid"}
	spec := s.spec
	f := i.Evidence.Frame
	id := f.ID().Value()
	if i.Epoch < spec.Epoch {
		r.Status = "stale"
		return r
	}
	if i.Epoch > spec.Epoch {
		r.Status = "conflict"
		return r
	}
	if i.Evidence.Interface != spec.Interface || i.Scope != spec.Scope || i.Bank != spec.Bank || i.Endpoint != spec.Endpoint || i.Group != spec.Group || i.Slot != spec.Slot || i.Direction != c.Direction || i.FieldAge < 0 || i.Evidence.Monotonic < 0 || i.Evidence.Sequence == 0 {
		return r
	}
	if s.group == "" || c.Profile != spec.Flavor {
		r.Status = "unknown"
		return r
	}
	if c.Profile == "link" && (!spec.RouteSet || !f.ID().Extended() || id>>24 != 0x18 || uint8(id>>8) != spec.Destination || uint8(id) != spec.Source) {
		return r
	}
	if c.Profile != "link" && f.ID().Extended() != (c.Profile == "pcs-extended19") {
		return r
	}
	pid := id
	if f.ID().Extended() {
		pid = id >> 16
	}
	r.Status = "unsupported"
	for _, row := range receiverRules {
		if row.pid != pid || row.profile != c.Profile || row.direction != c.Direction || row.role != c.Role {
			continue
		}
		if row.groups&(1<<uint(s.group[1]-'0')) == 0 {
			return r
		}
		r.Class = row.class
		r.Grade = "Hypothesis"
		r.Description = row.description
		if row.class == "Q" {
			r.Status = "raw"
		}
		if f.DLC() != 8 || f.RawDLC() != 8 {
			r.Status = "invalid"
			return r
		}
		switch row.route {
		case "module":
			if spec.Scope != "local" || spec.Destination != 0xf4 || spec.Source == 0xff || spec.Source == 0xf4 {
				return r
			}
		case "coordinator":
			if spec.Source != 0xf4 || spec.Destination != 0xdf {
				return r
			}
		case "service-broadcast":
			if spec.Source != 0xf5 || spec.Destination != 0xff {
				return r
			}
		case "matched":
			if !spec.RouteSet || spec.Source != 0xf5 || spec.Destination == 0xff {
				return r
			}
		case "extended-pcs":
			if !spec.RouteSet || uint16(id) != 0xdff2 || uint8(id>>8) != spec.Destination || uint8(id) != spec.Source {
				return r
			}
		case "gateway-service":
			if !spec.RouteSet || uint16(id) != 0xf2f5 || uint8(id>>8) != spec.Destination || uint8(id) != spec.Source {
				return r
			}
		}
		switch row.gate {
		case "assigned":
			if !c.Assigned {
				return r
			}
		case "maintenance":
			if c.Session != "maintenance" {
				return r
			}
		case "addressing":
			if c.Session != "addressing" || !c.AddressingKnown {
				return r
			}
		case "legacy-aging":
			if spec.ProjectCode != "QAAa" || spec.Release != "9011" || spec.Choice != "legacy19" || c.Session != "maintenance" {
				return r
			}
		case "legacy-noop":
			if s.group == "L8" {
				r.Status = "unsupported"
				return r
			}
		case "parallel":
			if c.Session != "parallel" {
				return r
			}
			if pid == 0x6f0 {
				for _, b := range r.Raw[2:] {
					if b != 0 {
						r.Status = "invalid"
						return r
					}
				}
			}
		case "release11":
			if s.group != "L8" {
				return r
			}
		case "status-form":
			if c.Form != "periodic" && c.Form != "direct" {
				return r
			}
		case "legacy-sync":
			if !c.CommunicationKnown || (c.CommunicationState != 3 && c.CommunicationState != 4) || r.Raw[0] != 0xaa {
				return r
			}
		}
		r.Admitted = true
		r.Status = "raw"
		receiverCapabilityDescribe(s, &r, pid)
		return r
	}
	return r
}

func receiverCapabilityDescribe(s CXSelection, r *ReceiverResult, pid uint32) {
	identity := s.spec.ProjectCode + s.spec.Release
	if r.Context.Direction == "coordinator-to-module" && (pid == 0x18fe || pid == 0x1869) {
		r.Grade = "Proven"
	}
	if pid == 0x18fe {
		if s.group == "L8" {
			r.Description += "; native output has zero B0, state nibble B1, zero B2-B7"
		} else {
			r.Description += "; earlier output carries retained PCS state B0"
		}
	}
	if r.Context.Direction == "module-to-coordinator" {
		if identity == "QAAA0010" {
			r.NativeFreshnessCount = map[uint32]uint32{0x1800: 5000, 0x1850: 1000, 0x1862: 5000}[pid]
		}
		if identity == "QAAa9011" && s.spec.Choice == "legacy19" {
			r.NativeFreshnessCount = map[uint32]uint32{0x1800: 1000, 0x1850: 5000, 0x1862: 10000}[pid]
		}
	}
	if pid == 0x18b1 {
		r.Description += "; parameter B0"
	}
	if s.group == "L8" {
		if pid == 0x1869 || pid == 0x1861 || pid == 0x590 || pid >= 0x510 && pid <= 0x51f {
			r.Grade = "Proven"
			if identity == "QAAA0011" {
				r.Description += "; input B7 ignored"
			} else {
				r.Description += "; input B7 retained"
			}
		}
		if pid == 0x18fd && identity == "QAAa9011" {
			r.Description += "; native branch also clears extra coordination state"
		}
		if pid == 0x18b0 && r.Context.Direction == "coordinator-to-module" {
			r.Description += "; coordinator parameters B6/B7"
		}
		if pid == 0x301 {
			r.Grade = "Proven"
			switch {
			case identity == "QAAA0011":
				r.Description += "; B7 ignored"
			case r.Raw[7] == 0 || r.Raw[7] == 0x58:
				r.Description += "; B7 normalizes to zero"
			default:
				r.Description += "; B7 retained"
			}
		}
		if pid == 0x37f {
			r.Grade = "Proven"
			r.Description += "; count branch withheld"
			count := r.Context.LocalCount
			if r.Context.LocalCountKnown && r.Raw[0]&1 != 0 && count >= 1 && count <= 8 && ((identity == "QAAA0011" && r.Raw[7] != 1) || (identity == "QAAa9011" && r.Raw[7] > 1)) {
				r.Description = "role/profile input; native count branch; no operation performed"
			}
		}
		if pid == 0x3f7 {
			r.Grade = "Proven"
			if r.Context.Form == "direct" {
				r.Description = "direct status B0/B1 only; cannot clear alarms"
			} else {
				r.Description = "periodic state B0/B1, zero B2-B4, alarm B5, extra B6, address/state B7"
			}
		}
	}
}
