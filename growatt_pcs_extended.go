package canbusreg

import (
	"encoding/binary"
	"fmt"
	"sync"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: Extended PCS 19xx; Gateway interpretation rules.
// CX09DecodePCSExtended decodes candidate receive evidence only. Unspecified
// units and validity rules remain raw; no field is physically qualified.
func DecodePCSExtended(s CXSelection, i LINKInput) CXNativeResult {
	r := selectionResult(s, i, "invalid")
	c := s.SelectionSpecValue()
	f := i.Evidence.Frame
	id := f.ID().Value()
	kind := uint16(id >> 16)
	if i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot || i.FieldAge < 0 || !f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 || !c.RouteSet || uint8(id>>8) != c.Destination || uint8(id) != c.Source {
		return r
	}
	r.Status = "unsupported"
	if _, err := NewCXSelection(c); err != nil {
		return r
	}
	if c.Flavor != "pcs-extended19" || c.Direction != "bms-to-pcs" || c.Choice == "release11" {
		return r
	}
	gateway := c.ProjectCode == "QCAA" && c.Release == "0001"
	// Selection alone never enables legacy extended19 on known excluded families.
	if !gateway && (s.SelectionGroup() == "" || s.SelectionGroup() == "L0" || s.SelectionGroup() == "L7" || s.SelectionGroup() == "L8") {
		return r
	}
	gatewayState := kind == 0x1910 && c.Destination == 0xff && c.Source == 0xf4
	if !gatewayState {
		if kind >= 0x1910 && kind <= 0x1919 && c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2 {
			kind -= 0x10
		}
		if kind < 0x1900 || kind > 0x1909 {
			return r
		}
		endpoint := c.Scope == "local" && c.Destination == 0xf2 && c.Source != 0 && (!gateway || c.Source <= 8)
		coordinator := c.Scope == "aggregate" && c.Destination == 0xdf && c.Source == 0xf2
		if !endpoint && !coordinator {
			return r
		}
	}
	p := r.Raw
	add := func(name string, n, d int64, unit, meaning, status, grade string) {
		r.Fields[name] = CXNativeField{Numerator: n, Denominator: d, Unit: unit, Meaning: meaning, Status: status, Grade: grade, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
	}
	raw := func(name string, n int64) { add(name, n, 1, "", "", "raw", "Unknown") }
	known := func(name string, n int64) { add(name, n, 1, "", name, "accepted", "Hypothesis") }
	scaled := func(name string, n, d int64, unit, meaning string) {
		add(name, n, d, unit, meaning, "accepted", "Hypothesis")
	}
	le := func(at int) int64 { return int64(binary.LittleEndian.Uint16(p[at : at+2])) }
	padding := func(at int) {
		name := fmt.Sprintf("padding_%d", at)
		raw(name, int64(p[at]))
		if p[at] != 0 {
			v := r.Fields[name]
			v.Status = "invalid"
			v.Grade = "Hypothesis"
			r.Fields[name] = v
		}
	}
	localUnits := c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"
	if gatewayState {
		known("state", int64(p[0]))
		raw("flag", int64(p[1]))
		for at := 2; at <= 6; at++ {
			padding(at)
		}
		raw("downstream_address", int64(p[7]))
		if gateway {
			known("downstream_address", int64(p[7]))
		}
		if gateway && (p[7] < 1 || p[7] > 8) {
			v := r.Fields["downstream_address"]
			v.Status = "invalid"
			v.Meaning = ""
			r.Fields["downstream_address"] = v
		}
		r.Status = "accepted"
		return r
	}
	switch kind {
	case 0x1900:
		known("product", le(0))
		known("hardware", int64(p[2]))
		known("version", le(3))
		for at := 5; at < 8; at++ {
			padding(at)
		}
		if c.Scope == "aggregate" && le(0) == 65535 {
			v := r.Fields["product"]
			v.Status = "invalid"
			v.Meaning = ""
			r.Fields["product"] = v
		}
	case 0x1901:
		known("count", int64(p[0]))
		raw("configuration_1", int64(p[1]))
		raw("configuration_2", int64(p[2]))
		known("state", int64(p[3]))
		raw("configuration_4", le(4))
		raw("configuration_6", le(6))
	case 0x1902:
		for at, name := range []string{"pack_voltage", "current", "port_voltage", "soc"} {
			raw(name, le(at*2))
		}
		if localUnits {
			scaled("pack_voltage", le(0), 10, "V", "pack voltage")
			scaled("current", le(2)-10000, 10, "A", "positive charging")
			scaled("port_voltage", le(4), 10, "V", "port voltage")
			scaled("soc", le(6), 10, "percent", "SOC")
		}
	case 0x1903:
		raw("limit_0", le(0))
		raw("limit_2", le(2))
		raw("permission_4", int64(p[4]))
		raw("permission_5", int64(p[5]))
		known("state", int64(p[6]))
		known("role", int64(p[7]))
	case 0x1904:
		raw("full_capacity", le(0))
		raw("remaining_capacity", le(2))
		raw("cycles", le(4))
		if c.ProjectCode == "QAAA" && c.Release == "0006" {
			scaled("cycles", le(4), 1, "cycles", "whole summary")
		}
		padding(6)
		padding(7)
		if localUnits {
			scaled("full_capacity", le(0), 10, "Ah", "full capacity")
			scaled("remaining_capacity", le(2), 10, "Ah", "remaining capacity")
		}
	case 0x1905:
		known("maximum_index", le(0))
		raw("maximum_voltage", le(2))
		known("minimum_index", le(4))
		raw("minimum_voltage", le(6))
		if localUnits {
			scaled("maximum_voltage", le(2), 1, "mV", "maximum cell voltage")
			scaled("minimum_voltage", le(6), 1, "mV", "minimum cell voltage")
		}
	case 0x1906:
		known("maximum_index", le(0))
		raw("maximum_temperature_code", int64(p[2]))
		known("minimum_index", le(3))
		raw("minimum_temperature_code", int64(p[5]))
		raw("extra", int64(p[6]))
		raw("soh_like", int64(p[7]))
	case 0x1907, 0x1908:
		start := int(kind-0x1907) * 8
		for at, b := range p {
			known(fmt.Sprintf("current_fault_%d", start+at), int64(b))
		}
	case 0x1909:
		raw("limit_0", le(0))
		raw("limit_2", le(2))
		raw("derived", le(4))
		raw("flag_6", int64(p[6]))
		raw("flag_7", int64(p[7]))
	}
	r.Status = "accepted"
	return r
}

// PCSExtendedDecoder retains independent last-known-good fields for one
// immutable selection. Rejected input never replaces retained evidence.
// A changed epoch or address requires a new decoder and explicit selection.
type PCSExtendedDecoder struct {
	mu        sync.Mutex
	selection CXSelection
	fields    map[string]CXNativeField
	sequence  uint64
	monotonic int64
	seen      bool
}

func NewPCSExtendedDecoder(s CXSelection) *PCSExtendedDecoder {
	return &PCSExtendedDecoder{selection: s, fields: make(map[string]CXNativeField)}
}
func (d *PCSExtendedDecoder) DecodePCSExtendedFrame(i LINKInput) CXNativeResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := DecodePCSExtended(d.selection, i)
	if r.Status != "accepted" {
		return r
	}
	if d.seen && (i.Evidence.Sequence <= d.sequence || int64(i.Evidence.Monotonic) < d.monotonic) {
		r.Status = "stale"
		for k, v := range r.Fields {
			v.Status = "stale"
			v.Unit = ""
			v.Meaning = ""
			r.Fields[k] = v
		}
		return r
	}
	d.seen = true
	d.sequence = i.Evidence.Sequence
	d.monotonic = int64(i.Evidence.Monotonic)
	kind := uint16(i.Evidence.Frame.ID().Value() >> 16)
	for name, v := range r.Fields {
		if v.Status == "accepted" || v.Status == "raw" {
			d.fields[fmt.Sprintf("%04x/%s", kind, name)] = v
		}
	}
	return r
}
func (d *PCSExtendedDecoder) SnapshotPCSExtended() map[string]CXNativeField {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := make(map[string]CXNativeField, len(d.fields))
	for k, v := range d.fields {
		r[k] = v
	}
	return r
}
