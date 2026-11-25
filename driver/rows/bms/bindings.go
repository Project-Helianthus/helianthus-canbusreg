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
// SOC and SOH are ratios in [0, 1], and the pack voltage and the pack
// temperature in kelvin are not below zero. The pack current is unbounded.
var catalogBounds = map[semreg.DefinitionID]catalogBound{
	"storage.state.soc":        {min: decimalOf("0", 0), max: decimalOf("1", 0)},
	"storage.state.soh":        {min: decimalOf("0", 0), max: decimalOf("1", 0)},
	"storage.pack.voltage":     {min: decimalOf("0", 0)},
	"storage.temperature.pack": {min: decimalOf("0", 0)},
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
