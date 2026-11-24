package canbusreg

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GreeVRFDictionaryKind distinguishes byte positions, named bit positions and packed bit containers.
type GreeVRFDictionaryKind uint8

const (
	// GreeVRFDictionaryByte is a byte coordinate, possibly starting a multi-byte value.
	GreeVRFDictionaryByte GreeVRFDictionaryKind = iota + 1
	// GreeVRFDictionaryBit is a single bit coordinate.
	GreeVRFDictionaryBit
	// GreeVRFDictionaryContainer describes the raw view of eight consecutive bit coordinates.
	GreeVRFDictionaryContainer
)

// GreeVRFDictionaryRow describes a native register position without assigning a semantic name.
// Coordinate is a bit coordinate for bit and container rows.
type GreeVRFDictionaryRow struct {
	Type                                                    uint8
	Coordinate                                              uint16
	Kind                                                    GreeVRFDictionaryKind
	Width                                                   int
	ValueType, Label, Unit, PresentationConverter, Conflict string
	LabelOnly                                               bool
	CandidateLabels                                         map[string][]string
	Enumeration                                             map[int64]string
}

// GreeVRFTypedPoint is a decoded value with its native identity, or an absent position.
type GreeVRFTypedPoint struct {
	Key              GreeVRFRegisterKey
	Row              GreeVRFDictionaryRow
	Present          bool
	Received         time.Duration
	Value            GreeVRFValue
	EnumerationLabel string
}

var greeVRFDictionaryShards = map[string][]string{
	"identity":                     greeVRFDictionaryIdentity[:],
	"temperatures-operating-state": greeVRFDictionaryTemperaturesOperatingState[:],
	"status-bits":                  greeVRFDictionaryStatusBits[:],
	"electrical-system":            greeVRFDictionaryElectricalSystem[:],
	"settings":                     greeVRFDictionarySettings[:],
	"remaining":                    greeVRFDictionaryRemaining[:],
}

var greeVRFDictionary = loadGreeVRFDictionary()

type greeVRFDictionarySourceRow struct {
	Type, Coordinate, Container, Kind, Label string
	LabelEnglish                             string `json:"label_en"`
	ValueType                                string `json:"value_type"`
	Width                                    int
	Unit, Conflict                           string
	PresentationConverter                    string              `json:"presentation_converter"`
	LabelOnly                                bool                `json:"label_only"`
	CandidateLabels                          map[string][]string `json:"candidate_labels"`
	Enumeration                              []struct{ Value, Label string }
	Bits                                     []struct {
		Coordinate, Label string
		LabelEnglish      string `json:"label_en"`
		SetLabel          string `json:"set_label"`
		ClearLabel        string `json:"clear_label"`
	}
}

func loadGreeVRFDictionary() [128][]GreeVRFDictionaryRow {
	var rows [128][]GreeVRFDictionaryRow
	for _, shard := range greeVRFDictionaryShards {
		for _, data := range shard {
			var source greeVRFDictionarySourceRow
			if err := json.Unmarshal([]byte(data), &source); err != nil {
				panic(err)
			}
			typ := uint8(parseGreeVRFHex(source.Type))
			rows[typ] = source.appendRows(rows[typ], typ)
		}
	}
	return rows
}

func (s greeVRFDictionarySourceRow) appendRows(rows []GreeVRFDictionaryRow, typ uint8) []GreeVRFDictionaryRow {
	if s.Kind == "bits" {
		rows = append(rows, GreeVRFDictionaryRow{Type: typ, Coordinate: parseGreeVRFHex(s.Container) * 8, Kind: GreeVRFDictionaryContainer, Width: 1, ValueType: "bits"})
		for _, bit := range s.Bits {
			rows = append(rows, GreeVRFDictionaryRow{Type: typ, Coordinate: parseGreeVRFHex(bit.Coordinate), Kind: GreeVRFDictionaryBit, Width: 1, ValueType: "bits", Label: greeVRFNativeLabel(bit.Label, bit.LabelEnglish), Enumeration: map[int64]string{0: bit.ClearLabel, 1: bit.SetLabel}})
		}
		return rows
	}
	row := GreeVRFDictionaryRow{Type: typ, Coordinate: parseGreeVRFHex(s.Coordinate), Kind: GreeVRFDictionaryByte, Width: s.Width,
		ValueType: s.ValueType, Label: greeVRFNativeLabel(s.Label, s.LabelEnglish), Unit: s.Unit, PresentationConverter: s.PresentationConverter,
		Conflict: s.Conflict, LabelOnly: s.LabelOnly, CandidateLabels: s.CandidateLabels}
	if s.Kind == "label_only_bit" {
		row.Kind, row.Width = GreeVRFDictionaryBit, 1
	}
	if len(s.Enumeration) > 0 {
		row.Enumeration = make(map[int64]string, len(s.Enumeration))
		for _, item := range s.Enumeration {
			row.Enumeration[int64(parseGreeVRFHex(item.Value))] = item.Label
		}
	}
	return append(rows, row)
}

func parseGreeVRFHex(text string) uint16 {
	n, err := strconv.ParseUint(strings.TrimPrefix(text, "0x"), 16, 16)
	if err != nil {
		panic(err)
	}
	return uint16(n)
}

func greeVRFNativeLabel(native, rendition string) string {
	if native != "" {
		return native
	}
	return rendition
}

func (row GreeVRFDictionaryRow) detached() GreeVRFDictionaryRow {
	row.CandidateLabels = maps.Clone(row.CandidateLabels)
	for key, labels := range row.CandidateLabels {
		row.CandidateLabels[key] = slices.Clone(labels)
	}
	row.Enumeration = maps.Clone(row.Enumeration)
	return row
}

// LookupGreeVRFDictionary looks up an exact native position and coordinate kind.
func LookupGreeVRFDictionary(typ uint8, coordinate uint16, kind GreeVRFDictionaryKind) (GreeVRFDictionaryRow, bool) {
	if typ >= uint8(len(greeVRFDictionary)) {
		return GreeVRFDictionaryRow{}, false
	}
	for _, row := range greeVRFDictionary[typ] {
		if row.Coordinate == coordinate && row.Kind == kind {
			return row.detached(), true
		}
	}
	return GreeVRFDictionaryRow{}, false
}

// TypedPoints returns the dictionary points for the image's type in table order.
// It does not apply presentation converters or turn labels into capability claims.
func (i *GreeVRFRegisterImage) TypedPoints() []GreeVRFTypedPoint {
	if i.key.Type >= uint8(len(greeVRFDictionary)) {
		return nil
	}
	rows := greeVRFDictionary[i.key.Type]
	points := make([]GreeVRFTypedPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, i.typedPoint(row))
	}
	return points
}

func (i *GreeVRFRegisterImage) typedPoint(row GreeVRFDictionaryRow) GreeVRFTypedPoint {
	p := GreeVRFTypedPoint{Key: i.key, Row: row.detached()}
	var raw []byte
	switch row.Kind {
	case GreeVRFDictionaryContainer:
		raw, p.Received, p.Present = i.readBitContainer(row.Coordinate)
	case GreeVRFDictionaryBit, GreeVRFDictionaryByte:
		if i.bits == (row.Kind == GreeVRFDictionaryBit) {
			raw, p.Received, p.Present = i.Read(row.Coordinate, row.Width)
		}
	}
	if !p.Present {
		p.Value.Status = GreeVRFValueIncomplete
		return p
	}
	p.decodeRaw(raw)
	return p
}

func (p *GreeVRFTypedPoint) decodeRaw(raw []byte) {
	row := p.Row
	if row.Kind == GreeVRFDictionaryBit && !row.LabelOnly {
		p.Value = DecodeGreeVRFValue("bits", raw, 0)
		p.EnumerationLabel = row.Enumeration[int64(raw[0])]
	} else {
		p.Value = DecodeGreeVRFValue(row.ValueType, raw)
		if p.Value.Denominator != 0 && p.Value.Numerator%p.Value.Denominator == 0 {
			p.EnumerationLabel = row.Enumeration[p.Value.Numerator/p.Value.Denominator]
		}
	}
}

func (i *GreeVRFRegisterImage) readBitContainer(coordinate uint16) ([]byte, time.Duration, bool) {
	if !i.bits {
		return nil, 0, false
	}
	fragment, offset, ok := i.fragmentAt(coordinate, 8)
	if !ok {
		return nil, 0, false
	}
	data := fragment.evidence.Frame.Data()[1:]
	var raw byte
	for bit := 0; bit < 8; bit++ {
		at := offset + bit
		raw |= (data[at/8] >> (at % 8) & 1) << bit
	}
	return []byte{raw}, fragment.evidence.Monotonic, true
}
