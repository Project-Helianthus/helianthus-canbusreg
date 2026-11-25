package bms

import (
	"fmt"
	"math/big"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// catalogBound is the range the pinned storage pack declares for one fact: a
// nil side is unbounded. The values mirror the pinned storage pack's own
// declarations, and a test pins every entry against that pack's validator, so
// a candidate outside a bound is refused here, before it can reach a batch.
type catalogBound struct {
	min, max *semreg.Decimal
}

func decimalOf(coefficient string, exponent int32) *semreg.Decimal {
	return &semreg.Decimal{Coefficient: coefficient, Exponent10: exponent}
}

// catalogBounds are the bounds of the storage facts this package binds. The
// SOC and SOH are ratios in [0, 1], and the pack and cell voltages, the pack
// temperature and the cell temperature in kelvin are not below zero. The pack
// current is unbounded.
var catalogBounds = map[semreg.DefinitionID]catalogBound{
	"storage.state.soc":            {min: decimalOf("0", 0), max: decimalOf("1", 0)},
	"storage.state.soh":            {min: decimalOf("0", 0), max: decimalOf("1", 0)},
	"storage.pack.voltage":         {min: decimalOf("0", 0)},
	"storage.temperature.pack":     {min: decimalOf("0", 0)},
	"storage.temperature.cell":     {min: decimalOf("0", 0)},
	"storage.cell.voltage_maximum": {min: decimalOf("0", 0)},
	"storage.cell.voltage_minimum": {min: decimalOf("0", 0)},
}

const (
	// cellRole is the subject role of a cell binding. The host binds it to the
	// cell resource of the configured source; without a binding the cell is
	// unresolved and the cell's native values stay published.
	cellRole = "cell"
	// cellSlot names the coordinate slot that carries the configured cell
	// resource. The fact key takes its cell dimension from this slot, so two
	// cells of one pack give two keys.
	cellSlot = "cell-resource"
	// cellDimension is the dimension a cell fact is keyed by in the pinned pack.
	cellDimension = "storage.dimension.cell"
)

// cellBindings are the bindings of the cell subject: the cell extrema of the
// V1.04, V1.08 and addressed monitor profiles and the maximum cell temperature of
// the V1.04 and addressed monitor profiles. They are kept out of the profile
// tables so that each table validates against the pack role alone; a host that
// supplies no cell role sees each of them refuse with unresolved_role, with the
// native value kept (withCellBindings).
func cellBindings() []roweval.BindingRow {
	volt := roweval.Affine{Num: 1, Den: 1000}
	return []roweval.BindingRow{
		cellReadIn(Profile, "maximum_cell_temperature", "CAN 0x313", "storage.temperature.cell", "unit.kelvin",
			roweval.Affine{Num: 1, Den: 10, Add: kelvinOffset}),
		cellReadIn(Profile, "maximum_cell_voltage", "CAN 0x319", "storage.cell.voltage_maximum", "unit.volt", volt),
		cellReadIn(Profile, "minimum_cell_voltage", "CAN 0x319", "storage.cell.voltage_minimum", "unit.volt", volt),
		cellReadIn(V108Profile, "maximum_cell_voltage", "CAN 0x319", "storage.cell.voltage_maximum", "unit.volt", volt),
		cellReadIn(V108Profile, "minimum_cell_voltage", "CAN 0x319", "storage.cell.voltage_minimum", "unit.volt", volt),
		// The monitor native rows already carry the page's 0.001 V scale in volts.
		cellReadIn(MonitorProfile, "maximum_cell_voltage", "addressed monitor 0x04020000", "storage.cell.voltage_maximum", "unit.volt", roweval.Affine{Num: 1, Den: 1}),
		cellReadIn(MonitorProfile, "minimum_cell_voltage", "addressed monitor 0x04020000", "storage.cell.voltage_minimum", "unit.volt", roweval.Affine{Num: 1, Den: 1}),
		// The monitor native row carries (raw - 400) x 0.1 degC; the binding adds 273.15 K.
		cellReadIn(MonitorProfile, "maximum_cell_temperature", "addressed monitor 0x04030000", "storage.temperature.cell", "unit.kelvin", roweval.Affine{Num: 1, Den: 1, Add: kelvinOffset}),
	}
}

// withCellBindings returns the table with each cell binding whose native row the
// table holds. A cell binding is attached to its own native row only: the
// monitor cell rows are named by the monitor profile and exist in the table of
// their own address alone.
func withCellBindings(t roweval.Table[Selector]) roweval.Table[Selector] {
	have := make(map[string]bool, len(t.Native))
	for _, r := range t.Native {
		have[r.ID] = true
	}
	bindings := append([]roweval.BindingRow(nil), t.Bindings...)
	for _, b := range cellBindings() {
		if have[b.NativeID] {
			bindings = append(bindings, b)
		}
	}
	t.Bindings = bindings
	return t
}

// catalogRefusal names the bound a candidate's value lies outside, or "" when
// the value is inside its catalog bounds or the fact has none.
func catalogRefusal(key semreg.FactKey, value *semreg.Value) string {
	b, bounded := catalogBounds[key.FactID]
	if !bounded || value == nil || value.Quantity == nil {
		return ""
	}
	x, ok := decimalRat(value.Quantity.Number)
	if !ok {
		return fmt.Sprintf("value of %s is not a decimal", key.FactID)
	}
	if b.min != nil {
		if lo, _ := decimalRat(*b.min); x.Cmp(lo) < 0 {
			return fmt.Sprintf("%s is below the catalog minimum %s", key.FactID, decimalText(*b.min))
		}
	}
	if b.max != nil {
		if hi, _ := decimalRat(*b.max); x.Cmp(hi) > 0 {
			return fmt.Sprintf("%s is above the catalog maximum %s", key.FactID, decimalText(*b.max))
		}
	}
	return ""
}

// decimalRat is the exact rational value of a decimal.
func decimalRat(d semreg.Decimal) (*big.Rat, bool) {
	return new(big.Rat).SetString(fmt.Sprintf("%se%d", d.Coefficient, d.Exponent10))
}

func decimalText(d semreg.Decimal) string {
	return fmt.Sprintf("%se%d", d.Coefficient, d.Exponent10)
}

// refuseUncatalogued withholds the candidate of a row when its value lies
// outside the catalog bounds of its fact. The evaluation keeps the native value,
// its evidence and the refusal, and the candidates of the other rows of the
// frame are not affected.
func refuseUncatalogued(ev *roweval.Evaluation) {
	if ev.Candidate == nil {
		return
	}
	if reason := catalogRefusal(ev.Candidate.Key, ev.Candidate.Value); reason != "" {
		ev.Reason, ev.Detail = catalogRefused, reason
		ev.Candidate, ev.Subject = nil, nil
	}
}
