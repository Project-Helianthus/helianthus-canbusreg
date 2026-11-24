package canbusreg

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// GrowattMonitorAddressed decodes only the addressed monitor contract of its configured interface.
type GrowattMonitorAddressed struct{ source canbus.InterfaceIdentity }

// GrowattMonitorReport preserves the full observation and distinct controller/module addresses.
type GrowattMonitorReport struct {
	Evidence   Evidence
	Base       uint32
	BCU, BMU   uint8
	HasBMU     bool
	Unresolved string
	Fields     map[string]GrowattReportField
}

// NewGrowattMonitorAddressed explicitly selects the monitor contract on one interface.
func NewGrowattMonitorAddressed(source canbus.InterfaceIdentity) (*GrowattMonitorAddressed, bool) {
	if source.Name() == "" || source.Index() <= 0 {
		return nil, false
	}
	return &GrowattMonitorAddressed{source}, true
}

// Apply decodes an addressed publication or retains a diagnostic fragment as raw.
func (p *GrowattMonitorAddressed) Apply(e Evidence) (GrowattMonitorReport, bool) {
	frame := e.Frame
	if p == nil || !growattV104SameInterface(p.source, e.Interface) || !frame.ID().Extended() {
		return GrowattMonitorReport{}, false
	}
	id := frame.ID().Value()
	result := GrowattMonitorReport{Evidence: e, Base: id & 0xffffff00, BCU: uint8(id)}
	switch id >> 24 {
	case 0x08, 0x10, 0x14:
		result.Base = id & 0xff0fff00
		result.HasBMU, result.BMU = true, uint8(id>>20&15)
	}
	if layout, ok := growattMonitorLiveFields[result.Base]; ok {
		if frame.DLC() != 8 || frame.RawDLC() != 8 {
			return GrowattMonitorReport{}, false
		}
		raw := frame.Bytes()
		result.Fields = decodeGrowattMonitorFields(layout, raw[:])
		return result, true
	}
	family := id & 0xffff0000
	if _, ok := growattMonitorRecordFields[family]; ok && uint8(id) == 0 {
		result.Base, result.BCU = family, uint8(id>>8)
	} else if _, ok := growattMonitorRecordFields[family-0x00200000]; ok && uint8(id>>8) == 0 && frame.DLC() >= 2 {
		result.Base = family
	} else {
		return GrowattMonitorReport{}, false
	}
	result.Unresolved = "diagnostic record requires framed completion"
	return result, true
}

// DecodeRecord decodes a caller-reassembled addressed diagnostic response, not
// individual frames. The caller owns record boundaries and native fragment evidence.
func (p *GrowattMonitorAddressed) DecodeRecord(family uint32, record []byte) (map[string]GrowattReportField, bool) {
	layout, ok := growattMonitorRecordFields[family-0x00200000]
	if p == nil || !ok {
		return nil, false
	}
	last := layout[len(layout)-1]
	if len(record) < last.at+last.width {
		return nil, false
	}
	return decodeGrowattMonitorFields(layout, record), true
}

type growattMonitorEncoding uint8

const (
	monitorOctet growattMonitorEncoding = iota
	monitorSignedOctet
	monitorWord
	monitorSignedWord
	monitorBits
	monitorRunMode
	monitorWorkMode
	monitorBytes
	monitorASCII
	monitorRaw
	monitorDate
	monitorVersion
	monitorRelease
	monitorWideVersion
	monitorSOCVersion
)

type growattMonitorField struct {
	name                string
	at, width           int
	kind                growattMonitorEncoding
	shift, bits         uint8
	offset, denominator int64
	unit, unresolved    string
}

func decodeGrowattMonitorFields(layout []growattMonitorField, raw []byte) growattReportFields {
	fields := make(growattReportFields, len(layout))
	for _, f := range layout {
		b := raw[f.at : f.at+f.width]
		if decodeGrowattMonitorStructured(fields, f, b) {
			continue
		}
		n := int64(b[0])
		den := f.denominator
		if den == 0 {
			den = 1
		}
		switch f.kind {
		case monitorSignedOctet:
			n = int64(int8(b[0]))
		case monitorBits:
			n = int64(b[0] >> f.shift & (1<<f.bits - 1))
		case monitorWord:
			n = int64(binary.LittleEndian.Uint16(b))
		case monitorSignedWord:
			n = int64(int16(binary.LittleEndian.Uint16(b)))
		case monitorRunMode:
			fields.enum(f.name, b[0], "default", "system_init", "system_wait", "hv_enable", "hv_output", "fault", "diagnostic", "calibration", "debug", "shut_down", "pcs_control", "factory", "sleep", "upgrade", "power_off", "maintenance")
			continue
		case monitorWorkMode:
			fields.enum(f.name, b[0], "static", "discharge", "charge")
			continue
		}
		fields.number(f.name, n+f.offset, den, f.unit)
	}
	return fields
}

func decodeGrowattMonitorStructured(fields growattReportFields, f growattMonitorField, b []byte) bool {
	var label string
	reason := f.unresolved
	switch f.kind {
	case monitorRaw, monitorBytes:
	case monitorASCII:
		label = string(b)
	case monitorDate:
		year, month, day := 2000+int(b[0]), time.Month(b[1]), int(b[2])
		date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if date.Year() == year && date.Month() == month && date.Day() == day {
			label = date.Format("2006-01-02")
		} else {
			reason = "invalid calendar date"
		}
	case monitorVersion:
		label = fmt.Sprintf("V%d.%d", b[0], b[1])
	case monitorRelease:
		label = fmt.Sprintf("R%d.%d", b[0], b[1])
	case monitorWideVersion:
		label = fmt.Sprintf("V%d.%d.%d", b[0], b[1], binary.BigEndian.Uint16(b[2:]))
	case monitorSOCVersion:
		label = fmt.Sprintf("V%d.%d.%d", b[0]>>4, b[0]&15, b[1])
	default:
		return false
	}
	fields.raw(f.name, b, reason)
	field := fields[f.name]
	field.Label = label
	fields[f.name] = field
	return true
}
