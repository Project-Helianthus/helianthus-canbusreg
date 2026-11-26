package link

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const qSerialGroup = "serial-group"

// admissions reports which of the decoder's three admissions an observation
// passes: the module route to F4, the date report route to F5 and the
// coordinator route from F4. They mirror the admission tests of the frozen
// telemetry decoder.
func admissions(s canbusreg.Selection, in canbusreg.LINKInput) (module, date, coordinator bool) {
	c := s.SelectionSpecValue()
	f := in.Evidence.Frame
	id := f.ID().Value()
	frame := c.Flavor == "link" && c.RouteSet && f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 &&
		id>>24 == 0x18 && uint8(id>>8) == c.Destination && uint8(id) == c.Source &&
		c.Endpoint == fmt.Sprintf("%02X", uint8(id)) && in.Evidence.Interface == c.Interface &&
		in.Epoch == c.Epoch && in.Direction == c.Direction && in.Scope == c.Scope && in.Bank == c.Bank &&
		in.Endpoint == c.Endpoint && in.Group == c.Group && in.Slot == c.Slot && in.FieldAge >= 0
	if !frame {
		return false, false, false
	}
	fromModule := c.Direction == "module-to-coordinator" && c.Source != 0xff && c.Source != 0xf4 && c.Scope == "local"
	module = fromModule && c.Destination == 0xf4
	date = fromModule && c.Destination == 0xf5 && uint16(id>>16) == 0x1809
	coordinator = c.Direction == "coordinator-to-module" && c.Scope == "aggregate" && c.Source == 0xf4 &&
		(c.Destination == 0xf5 || c.Destination == 0xdf)
	return module, date, coordinator
}

func withConditions(r rowSpec, kv ...string) rowSpec {
	r.extra = make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		r.extra[kv[i]] = kv[i+1]
	}
	return r
}

// aggregateSpecs returns the rows of the coordinator to monitor frames 180D to
// 180F in frame order. The page gives no unit for any of their fields, so each
// keeps its raw unitless value.
func aggregateSpecs() []rowSpec {
	conditions := []string{qDirection, "coordinator-to-module", qScope, "aggregate", qSource, "F4", qDestination, "F5"}
	var s []rowSpec
	add := func(r rowSpec) { s = append(s, withConditions(r, conditions...)) }
	for _, r := range bytesOf(0x180d, "soc_cache", "state_1", "state_2", "state_3", "maximum_index", "minimum_index", "temperature_6", "temperature_7") {
		add(r)
	}
	add(raw(0x180e, "index_0", byteAt(0)))
	add(raw(0x180e, "index_1", byteAt(1)))
	add(raw(0x180e, "extremum_2", littleWord(2)))
	add(raw(0x180e, "extremum_4", littleWord(4)))
	add(raw(0x180e, "additional_limit", littleWord(6)))
	add(raw(0x180f, "limit", littleWord(0)))
	add(raw(0x180f, "current", littleWord(2)))
	add(raw(0x180f, "count", byteAt(4)))
	add(raw(0x180f, "soh_cache", byteAt(5)))
	add(raw(0x180f, "quotient", littleWord(6)))
	return s
}

// tableOf builds a table from specs. A row whose id has an entry in special
// uses that hook instead of the default one.
func tableOf(specs []rowSpec, special map[string]roweval.RowHook) roweval.Table[Selector] {
	rows := make([]roweval.NativeRow[Selector], 0, len(specs))
	hooks := make(map[string]roweval.RowHook, len(specs))
	for _, s := range specs {
		row := nativeRow(s)
		if hook, ok := special[row.ID]; ok {
			hooks[row.Decode.Hook] = hook
		} else {
			hooks[row.Decode.Hook] = rowHook(s)
		}
		rows = append(rows, row)
	}
	return roweval.Table[Selector]{Native: rows, Hooks: hooks}
}

// AggregateTable returns the native rows of the coordinator frames 180D to 180F
// and the hooks those rows name.
func AggregateTable() roweval.Table[Selector] {
	return tableOf(aggregateSpecs(), nil)
}

// EvaluateAggregate evaluates the native rows of one coordinator observation.
// It admits only what the decoder admits: an observation outside the route,
// the frame geometry or the selection context gives the reason invalid and no
// rows. The aggregate rows apply to the F5 destination only; the group
// destination DF and every other family give no rows, as in the decoder.
func EvaluateAggregate(s canbusreg.Selection, in canbusreg.LINKInput) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	module, date, coordinator := admissions(s, in)
	if !module && !date && !coordinator {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	res.Status = "unsupported"
	if !coordinator || s.SelectionSpecValue().Destination != 0xf5 || family < 0x180d || family > 0x180f {
		return res, roweval.ReasonNoMatch, nil
	}
	res.Status = "raw"
	rows, err := evaluateRows(AggregateTable(), frame, Qualifiers(s, in))
	if err != nil {
		return res, "", err
	}
	res.Rows = rows
	return res, "", nil
}
