package link

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the status, extremum, aggregate and limit bindings of the
// LINK rows, which are none, and the listing of the native rows that no
// binding feeds.
//
// Cell extrema. storage.cell.voltage_maximum and voltage_minimum have the
// subject storage.dimension.cell. The page gives the 1806 minimum-cell index
// (B3) with no index base, no maximum-cell index in QAAA0005 and QAAA0006 (B0
// is the low byte of the spread there), and no rule that ties a position to a
// cell of the module; 180D and 180E carry the stack's extremum indices with no
// unit. No cell resolves, so every extremum stays a native row with its index
// and location evidence.
//
// Interlock, alarm, warning and protection. The decoder states condition names
// for fault bits and diagnostic indices and no condition class, and it states
// no charge or discharge permission flag, so there is no class to bind.
//
// Aggregate values. The page defines no unit for any 180D to 180F field, so
// no aggregate binding on the stack subject is written, and aggregate
// telemetry never reaches a member's pack.
//
// Limits. The 1805 charge and discharge limit codes and the 180F limit are raw
// until the page qualifies their units, so no value limit is written.
func statusBindings() []roweval.BindingRow { return nil }

// NativeOnlyRow is a native row that no binding feeds: the reason, and the
// package E element the field would take once the catalog has it. An empty
// element means the page assigns the field no quantity of its own.
type NativeOnlyRow struct {
	ID      string
	Reason  string
	Element string
}

type nativeOnlyRule struct {
	match   *regexp.Regexp
	reason  string
	element string
}

const (
	reasonNoCellElement = "the pinned catalog has no element for this value"
	reasonUnresolved    = "no physical cell resolves: the page gives no index base or cell association"
	reasonSensor        = "the page documents no physical cell association for a sensor"
	reasonNoClass       = "the decoder states condition names and no condition class"
	reasonNoUnit        = "the page defines no unit for this coordinator field"
	reasonLimitRaw      = "the page leaves the limit code raw until its unit is qualified"
)

func rule(pattern, reason, element string) nativeOnlyRule {
	return nativeOnlyRule{match: regexp.MustCompile("^(?:" + pattern + ")$"), reason: reason, element: element}
}

var nativeOnlyRules = []nativeOnlyRule{
	rule(`cell_voltage_\d+`, reasonNoCellElement+" (the catalog has the extrema, not one element per cell)", "storage.cell.voltage"),
	rule(`1806\.mean`, reasonNoCellElement, "storage.cell.voltage_mean"),
	rule(`1806\.(maximum|minimum|minimum_index)`, reasonUnresolved, "storage.cell.voltage_maximum / storage.cell.voltage_minimum"),
	rule(`180D\.(maximum|minimum)_index|index_[01]|extremum_[24]`, reasonNoUnit, ""),
	rule(`enable_\d+`, reasonNoCellElement, "storage.cell.balancing_enabled"),
	rule(`temperature_[1-4]|temperature_(maximum|minimum)|1862\.(maximum|minimum)|(maximum|minimum)_code|1808\.(maximum|minimum)_index`, reasonSensor, "storage.temperature.sensor"),
	rule(`remaining_capacity`, reasonNoCellElement+" (not a discharge capacity)", "storage.capacity.remaining"),
	rule(`cycles|fine_cycles`, reasonNoCellElement, "storage.cycle.count"),
	rule(`port_voltage`, "a different quantity from pack voltage", "storage.pack.port_voltage"),
	rule(`discharge_limit|charge_limit`, reasonLimitRaw, "value limit of storage.pack.current"),
	rule(`current_fault_\d+|selected_[ab]|selected_[ab]_index|1861\..*|1869\..*|coordinator_.*`, reasonNoClass, "storage.status.alarm / storage.status.warning / storage.status.protection"),
	rule(`soc_cache|state_[123]|temperature_[67]|additional_limit|limit|180F\.current|count|soh_cache|quotient`, reasonNoUnit, ""),
	rule(`project_code|release|year|month|day|hour|minute|second|1809\.status|serial_.*`, reasonNoCellElement, "storage.identity"),
	rule(`soc_status`, "B7 is not a state-of-charge percentage", ""),
}

// NativeOnly lists every native row of the package that no binding feeds, in
// row id order, with the reason and the package E element it would take. A row
// with no stated element is raw evidence of the frame that the page assigns no
// quantity.
func NativeOnly() []NativeOnlyRow {
	bound := map[string]bool{}
	for _, b := range Bindings() {
		bound[b.NativeID] = true
	}
	seen := map[string]bool{}
	var out []NativeOnlyRow
	for _, r := range AllTable().Native {
		if bound[r.ID] || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		row := NativeOnlyRow{ID: r.ID, Reason: "raw field of the frame; the page assigns it no quantity"}
		short := strings.TrimPrefix(r.ID, rowPrefix)
		for _, n := range nativeOnlyRules {
			if n.match.MatchString(short) {
				row.Reason, row.Element = n.reason, n.element
				break
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
