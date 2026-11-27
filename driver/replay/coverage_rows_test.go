package replay_test

import (
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/bms"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

func catalogue() (replay.Catalog, error) {
	var frames []replay.Frame
	seen := map[string]bool{}
	measurementFrames(&frames, seen)
	projectFrames(&frames, seen)
	if err := commandFrames(&frames, seen); err != nil {
		return replay.Catalog{}, err
	}
	if err := requirePackages(frames); err != nil {
		return replay.Catalog{}, err
	}
	return finish(frames), nil
}

func measurementFrames(into *[]replay.Frame, seen map[string]bool) {
	collectBMS(into, seen, bms.V104Table())
	collectBMS(into, seen, bms.V108Table())
	collectBMS(into, seen, bms.CommonTable(bms.ScopeMember))
	collectBMS(into, seen, bms.CommonTable(bms.ScopeAggregate))
	for hi := uint32(0x04); hi <= 0x14; hi += 0x04 {
		for mid := uint32(0); mid < 256; mid++ {
			base := hi<<24 | mid<<16
			if len(bms.MonitorTable(base|1).Native) == 0 {
				continue
			}
			addFamily(into, seen, "bms", base|1, true, zeros(8))
		}
	}
}

func collectBMS(into *[]replay.Frame, seen map[string]bool, table roweval.Table[bms.Selector]) {
	for _, row := range table.Native {
		addFamily(into, seen, "bms", row.At.Identifier, row.At.Extended, zeros(row.At.DLC))
	}
}

func projectFrames(into *[]replay.Frame, seen map[string]bool) {
	for _, table := range []roweval.Table[pcs.Selector]{
		pcs.Table(), pcs.FullTable(), pcs.SlotTable(), pcs.IdentityTable(),
		pcs.ParallelIdentityTable(), pcs.ExtendedTable(), pcs.ExtendedFaultTable(), pcs.ParallelReleaseTable(),
	} {
		for _, row := range table.Native {
			addFamily(into, seen, "pcs", row.At.Identifier, row.At.Extended, zeros(row.At.DLC))
		}
	}
	for _, table := range []roweval.Table[link.Selector]{
		link.Table(), link.AllTable(), link.FineTable(), link.AggregateTable(),
		link.IdentityTable(), link.DiagnosticsTable(), link.VectorTable(),
	} {
		for _, row := range table.Native {
			addFamily(into, seen, "link", uint32(row.At.Family)<<16, row.At.Extended, zeros(row.At.DLC))
		}
	}
}
