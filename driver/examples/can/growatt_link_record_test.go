package can

import (
	"encoding/binary"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func TestGrowattLINKCurrentFromRecord(t *testing.T) {
	bus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: bus,
		Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006",
		Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02",
		RouteSet: true, Source: 2, Destination: 0xf4, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	// A packed current code of 3900 is below the zero-current bias of 4000.
	raw := []byte{0, 0, 0, 0, 8, 0, 0, 0, 0x23, 0x21, 0xcf, 0x0b, 0, 0, 0, 0}
	binary.NativeEndian.PutUint32(raw, 0x9805f402)
	r := receiver.Record{Interface: bus, Raw: raw, Sequence: 1}
	input := canbusreg.LINKInput{Epoch: 1, Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02"}
	ev, n, err := evaluateLINKCurrent(r, selected, input, signalSlice{14, 13, bitSlice, false}, roweval.Acquired[Selector]{})
	if err != nil || ev.Reason != roweval.ReasonNoBinding || ev.Native == nil || ev.Native.Value == nil || ev.Native.Value.Quantity == nil || ev.Native.Raw != 3900 {
		t.Fatalf("LINK current evaluation: %+v, %v", ev, err)
	}
	q := ev.Native.Value.Quantity
	if q.Unit != "unit.ampere" || q.Number.Coefficient != "-1" || q.Number.Exponent10 != 1 || n.Fields["current"].Grade != "Proven" || n.PhysicalQualified {
		t.Fatalf("LINK current: %+v, %+v", q, n.Fields["current"])
	}
}
