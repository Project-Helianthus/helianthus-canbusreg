package canbusreg

import (
	"bytes"
	"testing"
)

func TestGreeVRFAddressedWriteForms(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, row := range []struct {
		typ                 uint8
		coordinate          uint16
		value               uint16
		id                  uint32
		data                []byte
		labelOnly, conflict bool
	}{
		{0x6f, 0, 1, 0x1cbfd06f, []byte{0, 1, 1}, false, false},
		{0x71, 0, 1, 0x1cbfd071, []byte{0, 1, 1}, false, false},
		{0x73, 0, 230, 0x1cbfd073, []byte{0, 3, 0xe6, 0}, false, false},
		{0x71, 0x15, 55, 0x1cbfd071, []byte{0x15, 1, 55}, true, false},
		{0x73, 4, 0x123, 0x1cbfd073, []byte{4, 3, 0x23, 1}, false, true},
	} {
		setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{iface, GreeVRFData, 0x20, row.typ}, Class: GreeVRFIndoorUnit, Generation: 1, Coordinate: row.coordinate, RawValue: row.value}
		frame, point, ok := EncodeGreeVRFAddressedWrite(setting)
		if !ok || frame.Identifier != row.id || !bytes.Equal(frame.Data, row.data) || point.Row.LabelOnly != row.labelOnly || (point.Row.Conflict != "") != row.conflict {
			t.Fatalf("%+v: %+v %+v %v", row, frame, point, ok)
		}
		got, ok := DecodeGreeVRFAddressedWrite(greeCandidateEvidence(t, iface, row.id, row.data))
		if !ok || got.Key.Family != GreeVRFAddressedWrite || got.Key.Unit != 0x20 || got.Key.Type != row.typ || got.Row.Coordinate != row.coordinate || !bytes.Equal(got.Value.Raw, row.data[2:]) {
			t.Fatalf("decode: %+v %v", got, ok)
		}
	}
}

func TestGreeVRFAddressedWriteRejectsUndefinedForms(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, data := range [][]byte{{0, 3, 1}, {0, 1, 2}, {0, 1, 1, 0}, {0, 0, 1}} {
		if point, ok := DecodeGreeVRFAddressedWrite(greeCandidateEvidence(t, iface, 0x1cbfd06f, data)); ok {
			t.Fatal(point)
		}
	}
	setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{iface, GreeVRFData, 0x20, 0x6f}, Class: GreeVRFIndoorUnit, Generation: 1, RawValue: 2}
	if frame, _, ok := EncodeGreeVRFAddressedWrite(setting); ok || len(frame.Data) != 0 {
		t.Fatal(frame)
	}
}
