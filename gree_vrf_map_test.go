package canbusreg

import "testing"

func TestGreeVRFMapTableInventory(t *testing.T) {
	if got := len(greeVRFMapM94Entries); got != 94 {
		t.Fatalf("M94 entries = %d, want 94", got)
	}
	if got := len(greeVRFMapM115Entries); got != 115 {
		t.Fatalf("M115 entries = %d, want 115", got)
	}
}

func TestGreeVRFMapDecoderReplaysCanonicalPipelines(t *testing.T) {
	tests := []struct {
		name    string
		decoder GreeVRFMapDecoder
		initial GreeVRFMapState
		opcode  uint8
		data    []byte
		rows    []int
		cells   map[uint8]uint8
		latch   bool
	}{
		{
			name:    "M94 duplicate source applies both rows",
			decoder: NewGreeVRFMapDecoder(GreeVRFMapM94, 0),
			initial: GreeVRFMapState{},
			opcode:  0x22,
			data:    []byte{0x0d, 0x01},
			rows:    []int{59, 60, 61, 62, 63},
			cells:   map[uint8]uint8{0x34: 0x18},
		},
		{
			name:    "M115 6098 post decode cleanup follows rows",
			decoder: NewGreeVRFMapDecoder(GreeVRFMapM115, 0x6098),
			initial: GreeVRFMapState{Cells: map[uint8]uint8{0x06: 0, 0x09: 0, 0x11: 9, 0x42: 0xff, 0x44: 8}},
			opcode:  0x7b,
			data:    []byte{0x01, 0x01},
			rows:    []int{108, 109},
			cells:   map[uint8]uint8{0x06: 0, 0x09: 6, 0x11: 0, 0x1d: 0, 0x42: 0xfd, 0x44: 0},
		},
		{
			name:    "M115 cleanup applies without matching row",
			decoder: NewGreeVRFMapDecoder(GreeVRFMapM115, 0x60a4),
			initial: GreeVRFMapState{Cells: map[uint8]uint8{0x06: 7, 0x11: 9, 0x42: 0xff, 0x44: 8}},
			opcode:  0x01,
			data:    []byte{0x00},
			rows:    []int{},
			cells:   map[uint8]uint8{0x06: 0, 0x11: 0, 0x42: 0xfd, 0x44: 0},
		},
		{
			name:    "M115 resets aggregate latch before zero finalize",
			decoder: NewGreeVRFMapDecoder(GreeVRFMapM115, 0),
			initial: GreeVRFMapState{Cells: map[uint8]uint8{0x53: 0xfe}, AggregateErrorLatch: true},
			opcode:  0x3d,
			data:    []byte{0x00, 0x00, 0x00, 0x00, 0x00},
			rows:    []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23},
			cells:   map[uint8]uint8{0x00: 0, 0x53: 0, 0x54: 0},
			latch:   false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, rows, ok := testCase.decoder.Apply(testCase.initial, testCase.opcode, testCase.data)
			if !ok {
				t.Fatal("Apply rejected a canonical replay")
			}
			if len(rows) != len(testCase.rows) {
				t.Fatalf("rows = %v, want %v", rows, testCase.rows)
			}
			for index, want := range testCase.rows {
				if rows[index] != want {
					t.Fatalf("rows = %v, want %v", rows, testCase.rows)
				}
			}
			for cell, want := range testCase.cells {
				if value := got.Cells[cell]; value != want {
					t.Fatalf("cell 0x%02x = 0x%02x, want 0x%02x", cell, value, want)
				}
			}
			if got.AggregateErrorLatch != testCase.latch {
				t.Fatalf("AggregateErrorLatch = %t, want %t", got.AggregateErrorLatch, testCase.latch)
			}
		})
	}
}

func TestGreeVRFMapDecoderFailsClosedForUnalignedU16Stream(t *testing.T) {
	decoder := NewGreeVRFMapDecoder(GreeVRFMapM94, 0)
	initial := GreeVRFMapState{Cells: map[uint8]uint8{0x34: 0x55}}
	got, rows, ok := decoder.Apply(initial, 0x73, []byte{0x01, 0xaa})
	if ok || len(rows) != 0 || got.Cells[0x34] != 0x55 {
		t.Fatalf("invalid frame result = %#v, %v, %t", got, rows, ok)
	}
}
