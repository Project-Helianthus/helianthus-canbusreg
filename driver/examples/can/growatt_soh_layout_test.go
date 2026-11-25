package can

import (
	"testing"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func TestGrowattV104ValidityLayout(t *testing.T) {
	set := []byte{0, 0, 0, 0, 0, 0, 0, 0x80}
	clear := []byte{255, 255, 255, 255, 255, 255, 255, 0x7f}
	for _, tc := range []struct {
		name    string
		payload []byte
		raw     uint64
		reason  roweval.Reason
		flipBit bool
	}{
		{"bit-seven-only", set, 0, "", false},
		{"all-but-bit-seven", clear, 127, "not valid", false},
		{"descriptor-controls-set-bit", set, 0, "not valid", true},
		{"descriptor-controls-clear-bit", clear, 127, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptors := canDescriptors()
			if tc.flipBit {
				order := descriptors.ByteOrders[bitSlice]
				order.Decode = func(raw uint64, width uint16) (uint64, error) {
					if width == 1 {
						return raw ^ 1, nil
					}
					return raw, nil
				}
				descriptors.ByteOrders[bitSlice] = order
			}
			table := v104Rows(descriptors)
			table.Bindings = nil // Exercise native validity independently of the read binding.
			ev, err := roweval.Evaluate(table, roweval.Acquired[Selector]{
				Selector: Selector{Identifier: 0x313, DLC: 8, RawDLC: 8, Slice: signalSlice{56, 7, bitSlice, false}},
				Payload:  tc.payload, Descriptors: descriptors,
			})
			if err != nil || ev.Native == nil || ev.Native.Raw != tc.raw || ev.Native.Reason != tc.reason || (ev.Native.Value == nil) != (tc.reason != "") {
				t.Fatalf("SOH native validity: %+v, error: %v", ev.Native, err)
			}
		})
	}
}
