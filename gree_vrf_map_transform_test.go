package canbusreg

import "testing"

func TestGreeVRFMapTransformsReplayCanonicalVectors(t *testing.T) {
	tests := []struct {
		name            string
		entry           greeVRFMapEntry
		state           GreeVRFMapState
		raw             uint8
		initialSlotFlag uint8
		wantCells       map[uint8]uint8
		wantAggregate   bool
		wantMode        bool
		wantSlotFlag    uint8
	}{
		{
			name:            "boolean_magic_true",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformBooleanToMagicAa55},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0xaa},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "boolean_magic_false",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformBooleanToMagicAa55},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0xff}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x00,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x55},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "swing_two",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformSwingValueRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x09}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x02,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "swing_passthrough",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformSwingValueRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x09}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x03,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x03},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_range_four",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeRangeToBoolean},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x04,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_range_passthrough",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeRangeToBoolean},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x05,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x05},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "nonzero_one_cleared",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformNonzeroBooleanWithOneCleared},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x09}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x00},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "nonzero_two_set",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformNonzeroBooleanWithOneCleared},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x02,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "aggregate_part_set",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformAggregateErrorLatchPart},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   true,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "aggregate_finalize_restore",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformAggregateErrorLatchFinalize},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x01}, AggregateErrorLatch: true, ModeLatch: false},
			raw:             0x00,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   true,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "aggregate_finalize_nonzero",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformAggregateErrorLatchFinalize},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: true, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "run_mode_forced",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformRunModeCoupledRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00, 0x07: 0x01}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x04,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x05, 0x07: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "run_mode_sets_coupling",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformRunModeCoupledRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00, 0x07: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x05,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x05, 0x07: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "global_mode_set",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformGlobalModeLatchUpdate},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x07},
			wantAggregate:   false,
			wantMode:        true,
			wantSlotFlag:    0x00,
		},
		{
			name:            "global_mode_clear",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformGlobalModeLatchUpdate},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x01}, AggregateErrorLatch: false, ModeLatch: true},
			raw:             0x00,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x00},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_flags_guard",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeUpdatesAll100SlotFlags},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00, 0x15: 0x00, 0x17: 0x01}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x03,
			initialSlotFlag: 0x08,
			wantCells:       map[uint8]uint8{0x04: 0x03, 0x15: 0x02, 0x17: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x08,
		},
		{
			name:            "mode_flags_clear_all",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeUpdatesAll100SlotFlags},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x00: 0x01, 0x04: 0x00, 0x17: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x01,
			wantCells:       map[uint8]uint8{0x00: 0x01, 0x04: 0x01, 0x17: 0x00},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_flags_set_all",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeUpdatesAll100SlotFlags},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x00: 0x01, 0x04: 0x00, 0x17: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x04,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x00: 0x01, 0x04: 0x04, 0x17: 0x00},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x01,
		},
		{
			name:            "global_mode_projection_latched",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformGlobalModeLatchProjection},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: true},
			raw:             0x03,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x07},
			wantAggregate:   false,
			wantMode:        true,
			wantSlotFlag:    0x00,
		},
		{
			name:            "global_mode_projection_raw",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformGlobalModeLatchProjection},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x03,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x03},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "coupled_state_set",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 0, sourceKind: greeVRFSourceBit, transform: greeVRFTransformCoupledStateRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x04: 0x00, 0x07: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x04: 0x01, 0x07: 0x06},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_value_forced",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeValueRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x01: 0x01, 0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x02,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x01: 0x01, 0x04: 0x06},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_value_one",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeValueRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x01: 0x00, 0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x01,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x01: 0x00, 0x04: 0x07},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
		{
			name:            "mode_value_two",
			entry:           greeVRFMapEntry{destinationCell: 4, destinationBit: 255, sourceKind: greeVRFSourceU8, transform: greeVRFTransformModeValueRemap},
			state:           GreeVRFMapState{Cells: map[uint8]uint8{0x01: 0x00, 0x04: 0x00}, AggregateErrorLatch: false, ModeLatch: false},
			raw:             0x02,
			initialSlotFlag: 0x00,
			wantCells:       map[uint8]uint8{0x01: 0x00, 0x04: 0x01},
			wantAggregate:   false,
			wantMode:        false,
			wantSlotFlag:    0x00,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			state := testCase.state
			for index := range state.SlotFlags {
				state.SlotFlags[index] = testCase.initialSlotFlag
			}
			greeVRFApplyEntry(&state, testCase.entry, testCase.raw)
			for cell, want := range testCase.wantCells {
				if got := state.Cells[cell]; got != want {
					t.Fatalf("cell 0x%02x = 0x%02x, want 0x%02x", cell, got, want)
				}
			}
			if state.AggregateErrorLatch != testCase.wantAggregate || state.ModeLatch != testCase.wantMode {
				t.Fatalf("latches = aggregate:%t mode:%t", state.AggregateErrorLatch, state.ModeLatch)
			}
			for _, value := range state.SlotFlags {
				if value != testCase.wantSlotFlag {
					t.Fatalf("slot flag = 0x%02x, want 0x%02x", value, testCase.wantSlotFlag)
				}
			}
		})
	}
}
