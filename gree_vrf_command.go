package canbusreg

// GreeVRFCommand is an in-memory candidate CAN command encoding. It carries
// no transport handle and does not imply a live operation.
type GreeVRFCommand struct {
	Identifier uint32
	Data       []byte
}

// GreeVRFTime contains bounded decimal components for the dedicated time
// payload. Their calendar interpretation remains outside this codec.
type GreeVRFTime struct {
	Year    uint8
	Month   uint8
	Day     uint8
	Hour    uint8
	Minute  uint8
	Second  uint8
	Weekday uint8
}

type greeVRFCommandKind uint8

const (
	greeVRFCommandReserved greeVRFCommandKind = iota
	greeVRFCommandUnsupportedWidth
	greeVRFCommandBool
	greeVRFCommandU8
	greeVRFCommandU16LE
)

type greeVRFCommandRow struct {
	kind     greeVRFCommandKind
	register uint16
}

// EncodeGreeVRFCommand encodes a supported command-table row entirely in
// memory. The caller supplies the opaque seed and bounded unit field.
func EncodeGreeVRFCommand(seed uint32, unit7 uint8, propertyID uint8, value uint16) (GreeVRFCommand, bool) {
	if unit7 > 0x7f || int(propertyID) >= len(greeVRFCommandRows) {
		return GreeVRFCommand{}, false
	}
	row := greeVRFCommandRows[propertyID]
	if row.kind == greeVRFCommandReserved || row.kind == greeVRFCommandUnsupportedWidth {
		return GreeVRFCommand{}, false
	}

	identifier := ((seed & 0xffffc000) | 0x001fc000 | (uint32(unit7) << 7) | uint32(row.register>>8)) & 0x1fffffff
	switch row.kind {
	case greeVRFCommandBool:
		encoded := byte(0)
		if value != 0 {
			encoded = 1
		}
		return GreeVRFCommand{Identifier: identifier, Data: []byte{byte(row.register), 0x01, encoded}}, true
	case greeVRFCommandU8:
		if value > 0xff {
			return GreeVRFCommand{}, false
		}
		return GreeVRFCommand{Identifier: identifier, Data: []byte{byte(row.register), 0x01, byte(value)}}, true
	case greeVRFCommandU16LE:
		return GreeVRFCommand{Identifier: identifier, Data: []byte{byte(row.register), 0x03, byte(value), byte(value >> 8)}}, true
	default:
		return GreeVRFCommand{}, false
	}
}

// EncodeGreeVRFTimeCommand encodes the dedicated packed-decimal time payload
// as a bounded in-memory candidate command.
func EncodeGreeVRFTimeCommand(value GreeVRFTime) (GreeVRFCommand, bool) {
	parts := [...]uint8{value.Year, value.Month, value.Day, value.Hour, value.Minute, value.Second, value.Weekday}
	data := make([]byte, 8)
	for index, part := range parts {
		encoded, ok := greeVRFPackedDecimal(part)
		if !ok {
			return GreeVRFCommand{}, false
		}
		data[index] = encoded
	}
	data[7] = 0x02
	return GreeVRFCommand{Identifier: 0x04820000, Data: data}, true
}

func greeVRFPackedDecimal(value uint8) (byte, bool) {
	if value > 99 {
		return 0, false
	}
	return value/10<<4 | value%10, true
}
