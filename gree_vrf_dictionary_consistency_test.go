package canbusreg

import (
	"testing"
)

func TestGreeVRFDictionaryCoversEveryMapRow(t *testing.T) {
	for _, table := range [][]greeVRFMapEntry{greeVRFMapM94Entries, greeVRFMapM115Entries} {
		for _, entry := range table {
			typ, coordinate := uint8(entry.sourceKey>>8), uint16(uint8(entry.sourceKey))
			if entry.sourceKind == greeVRFSourceBit {
				if _, ok := LookupGreeVRFDictionary(typ, coordinate, GreeVRFDictionaryBit); !ok {
					t.Errorf("row %d: %04x has no bit", entry.index, entry.sourceKey)
				}
				continue
			}
			matched := false
			for _, row := range greeVRFDictionary[typ] {
				if row.Kind == GreeVRFDictionaryByte && coordinate >= row.Coordinate && int(coordinate-row.Coordinate) < row.Width {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("row %d: %04x has no byte", entry.index, entry.sourceKey)
			}
		}
	}
}

func TestGreeVRFDictionaryCoversEncodableCommands(t *testing.T) {
	var supported, reserved, unsupported int
	for index, command := range greeVRFCommandRows {
		frame, ok := EncodeGreeVRFCommand(0x1ca00000, 0x20, uint8(index), 1)
		kind, width := GreeVRFDictionaryByte, 1
		switch command.kind {
		case greeVRFCommandReserved:
			reserved++
			if ok || len(frame.Data) != 0 {
				t.Fatal(index, frame)
			}
			continue
		case greeVRFCommandUnsupportedWidth:
			unsupported++
			if index != 0x57 || command.register != 0 || ok || len(frame.Data) != 0 {
				t.Fatal(index, frame)
			}
			continue
		case greeVRFCommandBool:
			kind = GreeVRFDictionaryBit
		case greeVRFCommandU16LE:
			width = 2
		}
		supported++
		typ, coordinate := uint8(command.register>>8), uint16(uint8(command.register))
		row, found := LookupGreeVRFDictionary(typ, coordinate, kind)
		if !found && kind == GreeVRFDictionaryByte && width == 2 && coordinate > 0 {
			// An odd command coordinate can name the second byte of a
			// dictionary value; this resolves the coordinate, not an
			// alternate typed value definition or write permission.
			row, found = LookupGreeVRFDictionary(typ, coordinate-1, kind)
		}
		if !found || row.Width != width {
			t.Errorf("command %02x: %04x has wrong or missing dictionary kind/width: %+v", index, command.register, row)
		}
		if !ok || frame.Identifier != 0x1cbfd000|uint32(typ) || len(frame.Data) != width+2 || frame.Data[0] != byte(coordinate) || frame.Data[1] != (1<<width)-1 || frame.Data[2] != 1 {
			t.Errorf("command %02x: bad frame: %+v", index, frame)
		}
	}
	if supported != 47 || reserved != 40 || unsupported != 1 {
		t.Fatalf("counts: %d, %d, %d", supported, reserved, unsupported)
	}
}
