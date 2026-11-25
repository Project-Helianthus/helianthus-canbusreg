package can

import (
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const pcsVoltageID = 0x1902F202

func pcsVoltageRows() roweval.Table[Selector] {
	pack := field(pcsVoltageID, true, signalSlice{0, 16, littleEndian, false}, "pack_voltage", "unit.volt")
	port := field(pcsVoltageID, true, signalSlice{32, 16, littleEndian, false}, "port_voltage", "unit.volt")
	pack.Decode, port.Decode = scale(10), scale(10)
	return roweval.Table[Selector]{Native: []roweval.NativeRow[Selector]{pack, port}, Bindings: []roweval.BindingRow{
		packRead(pack.ID, "CAN 1902 B0–B1", "storage.pack.voltage", "unit.volt", 1),
	}}
}

func evaluatePCSVoltage(record receiver.Record, selected canbusreg.Selection, input canbusreg.LINKInput, slice signalSlice, a roweval.Acquired[Selector]) (roweval.Evaluation, canbusreg.NativeResult, error) {
	e, err := decodeRecord(record)
	input.Evidence = e
	native := canbusreg.NativeResult{Selection: selected, Evidence: e, Epoch: input.Epoch, Status: "invalid"}
	copy(native.Raw[:], e.RawRecord[8:])
	noMatch := roweval.Evaluation{Reason: roweval.ReasonNoMatch}
	if err != nil || e.Frame.ID().Value() != pcsVoltageID {
		return noMatch, native, nil
	}
	c := selected.SelectionSpecValue()
	if c.ProjectCode != "QAAA" || c.Release != "0006" || c.Bank != "ordinary" || c.Scope != "local" || c.Endpoint != "participant" {
		return noMatch, native, nil
	}
	native = canbusreg.DecodePCSExtended(selected, input)
	if native.Status != "accepted" {
		return noMatch, native, nil
	}
	ev, err := evaluateSlice(pcsVoltageRows(), e.Frame, slice, a)
	return ev, native, err
}
