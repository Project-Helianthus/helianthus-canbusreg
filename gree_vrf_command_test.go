package canbusreg

import "testing"

func TestGreeVRFCommandTableInventory(t *testing.T) {
	if got := len(greeVRFCommandRows); got != 88 {
		t.Fatalf("rows = %d, want 88", got)
	}
	counts := map[greeVRFCommandKind]int{}
	for _, row := range greeVRFCommandRows {
		counts[row.kind]++
	}
	if counts[greeVRFCommandBool]+counts[greeVRFCommandU8]+counts[greeVRFCommandU16LE] != 47 {
		t.Fatalf("supported rows = %d, want 47", counts[greeVRFCommandBool]+counts[greeVRFCommandU8]+counts[greeVRFCommandU16LE])
	}
	if counts[greeVRFCommandReserved] != 40 || counts[greeVRFCommandUnsupportedWidth] != 1 {
		t.Fatalf("rejected rows = reserved:%d unsupported:%d", counts[greeVRFCommandReserved], counts[greeVRFCommandUnsupportedWidth])
	}
}

func TestGreeVRFCommandEncoderBuildsSupportedFamilies(t *testing.T) {
	tests := []struct {
		name       string
		propertyID uint8
		value      uint16
		wantID     uint32
		wantData   []byte
	}{
		{name: "boolean", propertyID: 0x06, value: 2, wantID: 0x17ffc36f, wantData: []byte{0x00, 0x01, 0x01}},
		{name: "unsigned byte", propertyID: 0x0b, value: 0x7f, wantID: 0x17ffc371, wantData: []byte{0x00, 0x01, 0x7f}},
		{name: "little endian unsigned word", propertyID: 0x10, value: 0x1234, wantID: 0x17ffc373, wantData: []byte{0x00, 0x03, 0x34, 0x12}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := EncodeGreeVRFCommand(0x17e00000, 6, testCase.propertyID, testCase.value)
			if !ok {
				t.Fatal("EncodeGreeVRFCommand rejected a supported row")
			}
			if got.Identifier != testCase.wantID || string(got.Data) != string(testCase.wantData) {
				t.Fatalf("command = %#v, want id=%#x data=%x", got, testCase.wantID, testCase.wantData)
			}
		})
	}
}

func TestGreeVRFCommandEncoderRejectsUnsupportedInputs(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		unit7      uint8
		propertyID uint8
		value      uint16
	}{
		{name: "unit out of range", unit7: 0x80, propertyID: 0x06, value: 1},
		{name: "reserved row", unit7: 6, propertyID: 0x05, value: 1},
		{name: "unsigned byte overflow", unit7: 6, propertyID: 0x0b, value: 0x100},
		{name: "out of table", unit7: 6, propertyID: 0x58, value: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got, ok := EncodeGreeVRFCommand(0x17e00000, testCase.unit7, testCase.propertyID, testCase.value); ok || got.Identifier != 0 || got.Data != nil {
				t.Fatalf("command = %#v, ok = %t", got, ok)
			}
		})
	}
}

func TestGreeVRFTimeCommandEncodesPackedDecimal(t *testing.T) {
	got, ok := EncodeGreeVRFTimeCommand(GreeVRFTime{Year: 24, Month: 8, Day: 26, Hour: 13, Minute: 45, Second: 9, Weekday: 2})
	if !ok {
		t.Fatal("EncodeGreeVRFTimeCommand rejected bounded decimal fields")
	}
	if got.Identifier != 0x04820000 || string(got.Data) != string([]byte{0x24, 0x08, 0x26, 0x13, 0x45, 0x09, 0x02, 0x02}) {
		t.Fatalf("time command = %#v", got)
	}
	if got, ok := EncodeGreeVRFTimeCommand(GreeVRFTime{Month: 100}); ok || got.Identifier != 0 || got.Data != nil {
		t.Fatalf("out-of-range decimal result = %#v, %t", got, ok)
	}
}
