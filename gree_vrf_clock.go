package canbusreg

// GreeVRFClockPoint retains the native clock report and its decimal fields.
type GreeVRFClockPoint struct {
	Evidence Evidence
	Time     GreeVRFTime
}

// EncodeGreeVRFClockCommand encodes the fixed register-model clock identifier.
// It does not change the separate Wi-Fi time encoder or assign calendar rules.
func EncodeGreeVRFClockCommand(value GreeVRFTime) (GreeVRFCommand, bool) {
	if value.Weekday > 6 {
		return GreeVRFCommand{}, false
	}
	command, ok := EncodeGreeVRFTimeCommand(value)
	if ok {
		command.Identifier = 0x04820084
	}
	return command, ok
}

// DecodeGreeVRFClock decodes either documented clock identifier without
// interpreting the fields as a date or a receipt for a transmitted command.
func DecodeGreeVRFClock(e Evidence) (GreeVRFClockPoint, bool) {
	id, ok := ClassifyGreeVRFIdentifier(e.Frame.ID())
	if !ok || id.Family != GreeVRFClock || e.Frame.DLC() != 8 || e.Frame.RawDLC() != 8 {
		return GreeVRFClockPoint{}, false
	}
	data := e.Frame.Bytes()
	if data[7] != 0x02 || e.Frame.ID().Value() == 0x04820084 && data[6] > 6 {
		return GreeVRFClockPoint{}, false
	}
	var parts [7]uint8
	for index, packed := range data[:7] {
		if packed>>4 > 9 || packed&0xf > 9 {
			return GreeVRFClockPoint{}, false
		}
		parts[index] = (packed>>4)*10 + (packed & 0xf)
	}
	return GreeVRFClockPoint{e, GreeVRFTime{parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]}}, true
}
