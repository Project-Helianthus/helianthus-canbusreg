package canbusreg

// GreeVRFAddressedSetting is a caller-selected unit, setting coordinate and
// exact native wire value. RawValue is not a presentation-converted number.
type GreeVRFAddressedSetting struct {
	Key        GreeVRFRegisterKey
	Class      GreeVRFDeviceClass
	Generation uint64
	Coordinate uint16
	RawValue   uint16
}

// EncodeGreeVRFAddressedWrite encodes one native setting and describes the
// selected dictionary row. The unit address must belong to the selected
// class. A frame is not a claim that a device accepts that setting.
func EncodeGreeVRFAddressedWrite(setting GreeVRFAddressedSetting) (GreeVRFCommand, GreeVRFTypedPoint, bool) {
	key := setting.Key
	if key.Interface.Name() == "" || key.Interface.Index() <= 0 ||
		key.Family != GreeVRFData && key.Family != GreeVRFAddressedData ||
		!greeVRFClassUnit(setting.Class, key.Unit) {
		return GreeVRFCommand{}, GreeVRFTypedPoint{}, false
	}
	row, ok := greeVRFSettingRow(key.Type, setting.Coordinate)
	if !ok || row.Kind == GreeVRFDictionaryBit && setting.RawValue > 1 || row.Width == 1 && setting.RawValue > 0xff {
		return GreeVRFCommand{}, GreeVRFTypedPoint{}, false
	}
	value := []byte{byte(setting.RawValue)}
	if row.Width == 2 {
		value = append(value, byte(setting.RawValue>>8))
	}
	frame := GreeVRFCommand{Identifier: 0x1cbfc000 | uint32(key.Unit)<<7 | uint32(key.Type), Data: append([]byte{byte(row.Coordinate), (1 << row.Width) - 1}, value...)}
	key.Family = GreeVRFAddressedWrite
	point := GreeVRFTypedPoint{Key: key, Row: row, Present: true}
	point.decodeRaw(value)
	return frame, point, true
}

func greeVRFSettingRow(typ uint8, coordinate uint16) (GreeVRFDictionaryRow, bool) {
	if coordinate > 0xff {
		return GreeVRFDictionaryRow{}, false
	}
	switch typ {
	case 0x6d, 0x6e, 0x6f, 0x71, 0x72, 0x73, 0x78, 0x79, 0x7b, 0x7c, 0x7d:
	default:
		return GreeVRFDictionaryRow{}, false
	}
	for _, row := range greeVRFDictionary[typ] {
		if row.Coordinate == coordinate && row.Kind != GreeVRFDictionaryContainer && (row.Width == 1 || row.Width == 2) {
			return row.detached(), true
		}
	}
	return GreeVRFDictionaryRow{}, false
}

// DecodeGreeVRFAddressedWrite returns native evidence of an addressed write,
// never a data-family observation of the requested setting.
func DecodeGreeVRFAddressedWrite(e Evidence) (GreeVRFTypedPoint, bool) {
	id, ok := ClassifyGreeVRFIdentifier(e.Frame.ID())
	if !ok || id.Family != GreeVRFAddressedWrite || e.Frame.RawDLC() != e.Frame.DLC() || e.Frame.DLC() < 3 {
		return GreeVRFTypedPoint{}, false
	}
	data := e.Frame.Data()
	row, ok := greeVRFSettingRow(id.Type, uint16(data[0]))
	if !ok || len(data) != 2+row.Width || data[1] != (1<<row.Width)-1 || row.Kind == GreeVRFDictionaryBit && data[2] > 1 {
		return GreeVRFTypedPoint{}, false
	}
	point := GreeVRFTypedPoint{Key: GreeVRFRegisterKey{e.Interface, id.Family, id.Unit, id.Type}, Row: row, Present: true, Received: e.Monotonic}
	point.decodeRaw(data[2:])
	return point, true
}
