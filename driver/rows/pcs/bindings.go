package pcs

import (
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// This file holds the read bindings of the PCS native rows to the storage pack.
// Each binding names a native row of the standard (standard.go), extended
// (extended.go) or parallel (parallel_release11.go) table and gives the SemReg
// fact, the unit and the exact conversion of the native value. The subject of
// every binding is the storage.dimension.pack of the selected bank and scope,
// which the caller resolves through the pack role; the bank, group and slot
// stay attribution context in the native row selection and are not the
// dimension of the subject. A pack role the caller cannot resolve gives the
// roweval unresolved-role error and no candidate.
//
// Bound: state of charge and state of health (percent over 100 to a ratio),
// current (the decoder scale in amperes), the pack voltage of the legacy 0x313
// and of the 0x1902 B0-B1 word (volts) and the release-11 count-aware average
// temperature of 0x313 (degrees Celsius plus 273.15 to kelvin, only while the
// selection states a valid temperature and a count above zero).
//
// Not bound, and kept as native rows only: the release-11 0x313 voltage, which
// is the output-port voltage; the 0x1902 B4-B5 port voltage; the legacy 0x313
// temperature, which is a coded minimum; the 0x311 charge voltage and limit
// words, whose scale the page leaves to the measurement contract; and the
// parallel 0x650 state of health, which the decoder keeps as a raw byte with no
// unit.
//
// No binding has a way back. The roweval write row of a binding is nil: the
// page documents no command whose effect is read back as these values. The 191A
// and 191C command rows are native setters and are not the inverse of any
// binding here.

const (
	storagePackID      semreg.DefinitionID    = "helianthus.pack.storage"
	storagePackVersion semreg.SemanticVersion = "1.1.0"

	// packRole is the subject role the caller resolves to the pack of the bank.
	packRole = "pack"
	// packDimension is the fact key dimension of the storage pack service.
	packDimension semreg.DefinitionID = "storage.dimension.pack"

	// receivePolicy is the freshness policy named by every read binding.
	receivePolicy semreg.PolicyID = "policy.can.growatt.pcs"
)

func storageRef(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: semreg.PackRef{ID: storagePackID, Version: storagePackVersion}, ID: id}
}

func packBinding(native, ref string, fact, unit semreg.DefinitionID, convert roweval.Convert, when map[string]string) roweval.BindingRow {
	return roweval.BindingRow{
		NativeID:    native,
		SubjectRole: packRole,
		SpecRef:     ref,
		When:        roweval.When{Equal: when},
		Service:     storageRef("storage.service.pack"),
		Capability:  storageRef("storage.capability.read.pack"),
		Read: &roweval.ReadRow{
			Fact:       storageRef(fact),
			Dimensions: map[semreg.DefinitionID]string{packDimension: packRole},
			Unit:       unit,
			Policy:     receivePolicy,
			ToRegistry: convert,
		},
	}
}

func identityConvert() roweval.Convert { return roweval.Convert{} }

func scaleConvert(divisor int64) roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: divisor}}
}

func kelvinConvert() roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: 1, Add: semreg.Decimal{Coefficient: "27315", Exponent10: -2}}}
}

func standardBindings() []roweval.BindingRow {
	const prefix = rowPrefix + "313."
	pair := func(extra ...string) map[string]string {
		m := map[string]string{qFlavor: "pcs-standard", qBank: "ordinary"}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	return []roweval.BindingRow{
		packBinding(prefix+"soc", "PCS 0x313 B6", "storage.state.soc", "unit.ratio", scaleConvert(100), pair()),
		packBinding(prefix+"soh", "PCS 0x313 B7 bits 0-6", "storage.state.soh", "unit.ratio", scaleConvert(100), pair()),
		packBinding(prefix+"current", "PCS 0x313 B2-B3", "storage.pack.current", "unit.ampere", identityConvert(), pair()),
		packBinding(prefix+"legacy.voltage", "PCS 0x313 B0-B1, legacy pack voltage", "storage.pack.voltage", "unit.volt", identityConvert(), pair(qRelease11, "false")),
		packBinding(prefix+"release11.temperature", "PCS 0x313 B4-B5, release-11 average", "storage.temperature.pack", "unit.kelvin", kelvinConvert(), pair(qRelease11, "true", qTemperatureGate, "true")),
	}
}

func extendedBindings() []roweval.BindingRow {
	pair := map[string]string{qFlavor: "pcs-extended19"}
	local := map[string]string{qFlavor: "pcs-extended19", gateExtendedLocalUnits: "true"}
	return []roweval.BindingRow{
		packBinding(extendedID(0x1902, "pack_voltage"), "PCS 0x1902 B0-B1", "storage.pack.voltage", "unit.volt", identityConvert(), local),
		packBinding(extendedID(0x1902, "current"), "PCS 0x1902 B2-B3", "storage.pack.current", "unit.ampere", identityConvert(), pair),
		packBinding(extendedID(0x1902, "soc"), "PCS 0x1902 B6-B7", "storage.state.soc", "unit.ratio", scaleConvert(100), pair),
	}
}

func parallelBindings() []roweval.BindingRow {
	pair := map[string]string{qFlavor: "pcs-parallel", qBank: "parallel", qContractGroup: groupRelease11}
	return []roweval.BindingRow{
		packBinding(parallelRowPrefix+"610.current", "PCS 0x610 B2-B3", "storage.pack.current", "unit.ampere", identityConvert(), pair),
		packBinding(parallelRowPrefix+"610.soc", "PCS 0x610 B6-B7", "storage.state.soc", "unit.ratio", scaleConvert(100), pair),
	}
}

// Bindings returns the read bindings of every PCS table.
func Bindings() []roweval.BindingRow {
	all := standardBindings()
	all = append(all, extendedBindings()...)
	return append(all, parallelBindings()...)
}

// BoundTable returns the native rows of the standard PCS frames with their
// read bindings.
func BoundTable() roweval.Table[Selector] {
	t := Table()
	t.Bindings = standardBindings()
	return t
}

// BoundExtendedTable returns the native rows of the extended PCS frames with
// their read bindings.
func BoundExtendedTable() roweval.Table[Selector] {
	t := ExtendedTable()
	t.Bindings = extendedBindings()
	return t
}

// BoundParallelTable returns the native rows of the release-11 parallel
// families with their read bindings.
func BoundParallelTable() roweval.Table[Selector] {
	t := ParallelReleaseTable()
	t.Bindings = parallelBindings()
	return t
}
