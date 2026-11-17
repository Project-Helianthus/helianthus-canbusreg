package canbusreg

import (
	"encoding/binary"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// GrowattLowVoltageBMSCommonProjection is the caller-selected, receive-only
// field projection defined by
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-common-projection-v1.md.
// It is not a protocol-revision profile and does not derive one from firmware.
//
// Scope is the publisher scope of every value in the projection. It is
// "unknown" unless the projector was selected with an explicit selection
// scope: "aggregate" for a stack, gateway or parallel-group summary and
// "member" for one reporting BMS. No frame carries a scope selector, so a pack
// count under "unknown" or "aggregate" is never a single-BMS count.
type GrowattLowVoltageBMSCommonProjection struct {
	Interface    canbus.InterfaceIdentity
	RawEvidence  Evidence
	Limits       *GrowattLowVoltageBMSCommonLimits
	Status       *GrowattLowVoltageBMSCommonStatus
	Measurements *GrowattLowVoltageBMSCommonMeasurements
	Scope        string
}

// GrowattLowVoltageBMSCommonLimits contains only the shared 0x311 limits.
// The status word remains available solely through RawEvidence.
type GrowattLowVoltageBMSCommonLimits struct {
	ChargeVoltageDecivolts   uint16
	ChargeCurrentDeciamps    uint16
	DischargeCurrentDeciamps uint16
}

// GrowattLowVoltageBMSCommonStatus preserves only the shared raw 0x312 bytes
// and pack count. The remaining frame bytes remain available through RawEvidence.
type GrowattLowVoltageBMSCommonStatus struct {
	Protection [2]byte
	Warning    [2]byte
	PackCount  uint8
}

// GrowattLowVoltageBMSCommonMeasurements contains only the shared 0x313 fields.
// Bytes 4--5 and byte 7's revision-local high bit remain available solely
// through RawEvidence.
type GrowattLowVoltageBMSCommonMeasurements struct {
	VoltageCentivolts int16
	CurrentDeciamps   int16
	SOCPercent        uint8
	SOHValue          uint8
}

// GrowattLowVoltageBMSCommonProjector holds one explicitly selected source.
// It is deliberately not a Registry Profile: registering it with V1.04 would
// turn two caller-selected interpretations into a generic match collision.
type GrowattLowVoltageBMSCommonProjector struct {
	source canbus.InterfaceIdentity
	scope  string
}

// NewGrowattLowVoltageBMSCommonProjector selects the common projection for one
// source interface. It neither identifies equipment nor selects a Growatt
// protocol revision.
func NewGrowattLowVoltageBMSCommonProjector(source canbus.InterfaceIdentity) (*GrowattLowVoltageBMSCommonProjector, bool) {
	if source.Name() == "" || source.Index() <= 0 {
		return nil, false
	}
	return &GrowattLowVoltageBMSCommonProjector{source: source, scope: "unknown"}, true
}

// NewGrowattLowVoltageBMSCommonProjectorFromSelection selects the common
// projection for the interface of a validated selection and carries its
// asserted scope: selection scope "aggregate" projects as "aggregate" and
// "local" as "member". A selection without either scope is rejected.
func NewGrowattLowVoltageBMSCommonProjectorFromSelection(s Selection) (*GrowattLowVoltageBMSCommonProjector, bool) {
	c := s.SelectionSpecValue()
	scope := ""
	switch c.Scope {
	case "aggregate":
		scope = "aggregate"
	case "local":
		scope = "member"
	default:
		return nil, false
	}
	p, ok := NewGrowattLowVoltageBMSCommonProjector(c.Interface)
	if !ok {
		return nil, false
	}
	p.scope = scope
	return p, true
}

// Apply retains one source-local raw observation. A valid shared frame exposes
// only its documented common fields. Unknown or malformed frames remain raw,
// and an observation from another source is rejected without creating state.
func (p *GrowattLowVoltageBMSCommonProjector) Apply(evidence Evidence) (GrowattLowVoltageBMSCommonProjection, bool) {
	if p == nil || !growattCommonSameInterface(p.source, evidence.Interface) {
		return GrowattLowVoltageBMSCommonProjection{}, false
	}

	projection := GrowattLowVoltageBMSCommonProjection{
		Interface:   evidence.Interface,
		RawEvidence: evidence,
		Scope:       p.scope,
	}
	if !growattCommonGeometry(evidence.Frame) {
		return projection, true
	}

	data := evidence.Frame.Bytes()
	switch evidence.Frame.ID().Value() {
	case 0x311:
		projection.Limits = &GrowattLowVoltageBMSCommonLimits{
			ChargeVoltageDecivolts:   binary.BigEndian.Uint16(data[0:2]),
			ChargeCurrentDeciamps:    binary.BigEndian.Uint16(data[2:4]),
			DischargeCurrentDeciamps: binary.BigEndian.Uint16(data[4:6]),
		}
	case 0x312:
		projection.Status = &GrowattLowVoltageBMSCommonStatus{
			Protection: [2]byte{data[0], data[1]},
			Warning:    [2]byte{data[2], data[3]},
			PackCount:  data[4],
		}
	case 0x313:
		projection.Measurements = &GrowattLowVoltageBMSCommonMeasurements{
			VoltageCentivolts: int16(binary.BigEndian.Uint16(data[0:2])),
			CurrentDeciamps:   int16(binary.BigEndian.Uint16(data[2:4])),
			SOCPercent:        data[6],
			SOHValue:          data[7] & 0x7f,
		}
	}
	return projection, true
}

func growattCommonSameInterface(left, right canbus.InterfaceIdentity) bool {
	return left.Name() == right.Name() && left.Index() == right.Index()
}

func growattCommonGeometry(frame canbus.Frame) bool {
	return !frame.ID().Extended() && frame.DLC() == 8 && frame.RawDLC() == 8
}

// GrowattEnergyCoverage reports which storage quantities the common projection
// and the V1.04 projection set. The results are "common" and "v104". Each
// maps a quantity name to "supported" or "unsupported". Voltage, current,
// state of charge, the state-of-health value and the charge and discharge
// limits are supported on both. Remaining energy, usable capacity, nominal
// capacity and charge capacity in ampere-hours are unsupported on both. The
// state-of-health valid bit and maximum cell temperature are supported only
// on the V1.04 result. The report reads no frame and does not change admission.
func GrowattEnergyCoverage() map[string]map[string]string {
	common := map[string]string{
		"voltage":                      "supported",
		"current":                      "supported",
		"soc":                          "supported",
		"soh_value":                    "supported",
		"charge_voltage_limit":         "supported",
		"charge_current_limit":         "supported",
		"discharge_current_limit":      "supported",
		"remaining_energy":             "unsupported",
		"usable_capacity":              "unsupported",
		"nominal_capacity":             "unsupported",
		"charge_capacity_ampere_hours": "unsupported",
		"soh_valid":                    "unsupported",
		"maximum_cell_temperature":     "unsupported",
	}
	revision := map[string]string{
		"voltage":                      "supported",
		"current":                      "supported",
		"soc":                          "supported",
		"soh_value":                    "supported",
		"charge_voltage_limit":         "supported",
		"charge_current_limit":         "supported",
		"discharge_current_limit":      "supported",
		"remaining_energy":             "unsupported",
		"usable_capacity":              "unsupported",
		"nominal_capacity":             "unsupported",
		"charge_capacity_ampere_hours": "unsupported",
		"soh_valid":                    "supported",
		"maximum_cell_temperature":     "supported",
	}
	return map[string]map[string]string{"common": common, "v104": revision}
}
