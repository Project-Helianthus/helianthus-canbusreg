package canbusreg

import (
	"bytes"
	"testing"
)

func TestGreeVRFClockCommandIdentifiers(t *testing.T) {
	value := GreeVRFTime{24, 12, 31, 23, 59, 58, 6}
	modern, ok := EncodeGreeVRFClockCommand(value)
	if !ok || modern.Identifier != 0x04820084 || !bytes.Equal(modern.Data, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x06, 0x02}) {
		t.Fatal(modern, ok)
	}
	value.Weekday = 7
	legacy, ok := EncodeGreeVRFTimeCommand(value)
	if !ok || legacy.Identifier != 0x04820000 || !bytes.Equal(legacy.Data, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x07, 0x02}) {
		t.Fatal(legacy, ok)
	}
	if frame, ok := EncodeGreeVRFClockCommand(value); ok || len(frame.Data) != 0 {
		t.Fatal("accepted weekday 7 under the clock identifier")
	}
}

func TestGreeVRFClockDecodesTypedPoint(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, row := range []struct {
		id      uint32
		data    []byte
		weekday uint8
		ok      bool
	}{
		{0x04820084, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x06, 0x02}, 6, true},
		{0x04820000, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x07, 0x02}, 7, true},
		{0x04820084, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x07, 0x02}, 0, false},
		{0x04820084, []byte{0x24, 0x12, 0x31, 0x23, 0x5a, 0x58, 0x06, 0x02}, 0, false},
		{0x04820084, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x06, 0x01}, 0, false},
		{0x04820084, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x06}, 0, false},
		{0x04820085, []byte{0x24, 0x12, 0x31, 0x23, 0x59, 0x58, 0x06, 0x02}, 0, false},
	} {
		e := greeCandidateEvidence(t, iface, row.id, row.data)
		point, ok := DecodeGreeVRFClock(e)
		if ok != row.ok || ok && (point.Evidence != e || point.Time != (GreeVRFTime{24, 12, 31, 23, 59, 58, row.weekday})) {
			t.Errorf("%+v: %+v %v", row, point, ok)
		}
	}
}
