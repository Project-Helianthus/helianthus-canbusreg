package can

import (
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func linkCurrentRows() roweval.Table[Selector] {
	current := field(0x1805f402, true, signalSlice{14, 13, bitSlice, false}, "current", "unit.ampere")
	// The wire code is unsigned; the bias gives the engineering value its sign.
	current.Decode.Numeric = &roweval.Affine{Num: 1, Den: 10, Add: semreg.Decimal{Coefficient: "-4", Exponent10: 2}}
	return roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{current}}
}

func evaluateLINKCurrent(record receiver.Record, selected canbusreg.Selection, input canbusreg.LINKInput, slice signalSlice, a roweval.Acquired[Selector]) (roweval.Evaluation, canbusreg.NativeResult, error) {
	e, err := decodeRecord(record)
	noMatch := roweval.Evaluation{Reason: roweval.ReasonNoMatch}
	if err != nil {
		native := canbusreg.NativeResult{Selection: selected, Evidence: e, Epoch: input.Epoch, Status: "invalid"}
		copy(native.Raw[:], e.RawRecord[8:])
		return noMatch, native, nil
	}
	input.Evidence = e
	native := canbusreg.DecodeLINKTelemetry(selected, input)
	if native.Fields["current"].Status != "accepted" {
		return noMatch, native, nil
	}
	ev, err := evaluateSlice(linkCurrentRows(), e.Frame, slice, a)
	return ev, native, err
}
