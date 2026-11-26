package link

import (
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file binds the PCS-carried module and cell facts that the PCS package
// does not bind, under the PCS native row ids (growatt.bms.pcs.<flavor>.<frame>.<field>).
//
// Already bound by the PCS package and not bound again here: 0x313 state of
// charge, state of health, current, legacy pack voltage and the release-11
// average temperature; the 0x1902 pack voltage, current and state of charge;
// the 0x610 current and state of charge.
//
// Bound here:
//   - 0x326 voltage (0.01 V per count) and current (the row applies the bank
//     offset) on the storage.dimension.pack of the slot, for the ordinary and the
//     parallel bank.
//   - 0x327, 0x319 and 0x330 maximum and minimum cell words (millivolt to volt)
//     as storage.cell.voltage_maximum and voltage_minimum on a
//     storage.dimension.cell. The page gives no index base, no cell count rule and
//     no association of a position with a cell, so the driver does not resolve the
//     cell: the subject is the cell role the caller resolves from the native index
//     rows (0x327 maximum_index and minimum_index, 0x319 and 0x330 indices and
//     locations): the role cell.maximum by the maximum index and cell.minimum by
//     the minimum index, so the two resolve independently. A caller that
//     cannot resolve a role gets the roweval unresolved-role error and no
//     candidate, and the native value and its index evidence stay native. A 0x330
//     location byte of 1 is never a module identifier.
//
// Not bound, native rows only: 0x326 soc_low (the low byte of the SOC-tenths
// word, not a percentage); the 0x322 temperature extrema (the page gives
// positions of a sensor, not of a cell); 0x329 (units not universal); 0x333
// (the decoder states no condition class); 0x334 (opaque state codes). The
// module charge and discharge permission flags are native rows with native
// values and are bound to no status symbol.
//
// No binding has a way back: the page documents no frame that sets these
// values, so the write row of every binding is nil.
//
// Priorities. The driver publishes a score per source. A PCS slot value is a
// cached copy of the module's own value, so it ranks below the LINK module's
// accepted field of the same element. Among the PCS sources of one cell
// element the driver prefers the frame scoped to one slot over the module
// frame over the frame whose location can be an aggregate. This is the
// driver's rule, not a ranking the protocol states.

const (
	pcsReceivePolicy semreg.PolicyID = "policy.can.growatt.pcs"

	cellMaximumRole                     = "cell.maximum"
	cellMinimumRole                     = "cell.minimum"
	cellDimension   semreg.DefinitionID = "storage.dimension.cell"

	pcsStandardPrefix = "growatt.bms.pcs.standard."
	pcsParallelPrefix = "growatt.bms.pcs.parallel."
)

const (
	pcsPriorityPackCopy  int32 = 50
	pcsPrioritySlot      int32 = 30
	pcsPriorityModule    int32 = 20
	pcsPriorityAggregate int32 = 10
)

type pcsBindingSpec struct {
	native   string
	ref      string
	fact     semreg.DefinitionID
	unit     semreg.DefinitionID
	convert  roweval.Convert
	flavor   string
	bank     string
	cell     bool
	priority int32
}

func pcsBindingSpecs() []pcsBindingSpec {
	type bank struct{ prefix, flavor, bank string }
	banks := []bank{
		{pcsStandardPrefix, "pcs-standard", "ordinary"},
		{pcsParallelPrefix, "pcs-parallel", "parallel"},
	}
	var out []pcsBindingSpec
	for _, b := range banks {
		out = append(out,
			pcsBindingSpec{native: b.prefix + "326.voltage", ref: "PCS 0x326 B1-B2", fact: "storage.pack.voltage", unit: "unit.volt", convert: scaleConvert(1), flavor: b.flavor, bank: b.bank, priority: pcsPriorityPackCopy},
			pcsBindingSpec{native: b.prefix + "326.current", ref: "PCS 0x326 B3-B4", fact: "storage.pack.current", unit: "unit.ampere", convert: scaleConvert(1), flavor: b.flavor, bank: b.bank, priority: pcsPriorityPackCopy},
			pcsBindingSpec{native: b.prefix + "327.maximum_cell", ref: "PCS 0x327 B1-B2", fact: "storage.cell.voltage_maximum", unit: "unit.volt", convert: scaleConvert(1000), flavor: b.flavor, bank: b.bank, cell: true, priority: pcsPrioritySlot},
			pcsBindingSpec{native: b.prefix + "327.minimum_cell", ref: "PCS 0x327 B3-B4", fact: "storage.cell.voltage_minimum", unit: "unit.volt", convert: scaleConvert(1000), flavor: b.flavor, bank: b.bank, cell: true, priority: pcsPrioritySlot},
		)
	}
	for _, c := range []struct {
		frame    string
		maxRef   string
		minRef   string
		priority int32
	}{
		{"319", "PCS 0x319 B1-B2", "PCS 0x319 B3-B4", pcsPriorityModule},
		{"330", "PCS 0x330 B4-B5", "PCS 0x330 B6-B7", pcsPriorityAggregate},
	} {
		out = append(out,
			pcsBindingSpec{native: pcsStandardPrefix + c.frame + ".maximum_cell", ref: c.maxRef, fact: "storage.cell.voltage_maximum", unit: "unit.volt", convert: scaleConvert(1000), flavor: "pcs-standard", bank: "ordinary", cell: true, priority: c.priority},
			pcsBindingSpec{native: pcsStandardPrefix + c.frame + ".minimum_cell", ref: c.minRef, fact: "storage.cell.voltage_minimum", unit: "unit.volt", convert: scaleConvert(1000), flavor: "pcs-standard", bank: "ordinary", cell: true, priority: c.priority},
		)
	}
	return out
}

func (b pcsBindingSpec) row() roweval.BindingRow {
	role, service, capability, dimension := packRole, "storage.service.pack", "storage.capability.read.pack", packDimension
	if b.cell {
		role, service, capability, dimension = cellMaximumRole, "storage.service.cell", "storage.capability.read.cell", cellDimension
		if b.fact == "storage.cell.voltage_minimum" {
			role = cellMinimumRole
		}
	}
	return roweval.BindingRow{
		NativeID:    b.native,
		SubjectRole: role,
		SpecRef:     b.ref,
		When:        roweval.When{Equal: map[string]string{qFlavor: b.flavor, qBank: b.bank}},
		Service:     storageRef(semreg.DefinitionID(service)),
		Capability:  storageRef(semreg.DefinitionID(capability)),
		Read: &roweval.ReadRow{
			Fact:       storageRef(b.fact),
			Dimensions: map[semreg.DefinitionID]string{dimension: role},
			Unit:       b.unit,
			Policy:     pcsReceivePolicy,
			ToRegistry: b.convert,
		},
	}
}

// PCSBindings returns the read bindings of the PCS slot, cell extremum rows.
func PCSBindings() []roweval.BindingRow {
	var out []roweval.BindingRow
	for _, b := range pcsBindingSpecs() {
		out = append(out, b.row())
	}
	return out
}

// PCSSourcePriorities returns the priority score of every PCS source bound
// here. The scores are distinct for every element on one subject.
func PCSSourcePriorities() []SourcePriority {
	var out []SourcePriority
	for _, b := range pcsBindingSpecs() {
		out = append(out, SourcePriority{NativeID: b.native, Fact: b.fact, Priority: b.priority})
	}
	return out
}

// PCSBoundTable returns the PCS standard, extrema and slot native rows with
// the bindings of this file.
func PCSBoundTable() roweval.Table[pcs.Selector] {
	all := roweval.Table[pcs.Selector]{Hooks: map[string]roweval.RowHook{}, Bindings: PCSBindings()}
	for _, p := range []roweval.Table[pcs.Selector]{pcs.FullTable(), pcs.SlotTable()} {
		all.Native = append(all.Native, p.Native...)
		for name, hook := range p.Hooks {
			all.Hooks[name] = hook
		}
	}
	return all
}
