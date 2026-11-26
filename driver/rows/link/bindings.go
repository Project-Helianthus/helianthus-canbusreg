package link

import (
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const (
	storagePackID      semreg.DefinitionID    = "helianthus.pack.storage"
	storagePackVersion semreg.SemanticVersion = "1.1.0"

	packRole                          = "pack"
	packDimension semreg.DefinitionID = "storage.dimension.pack"

	receivePolicy semreg.PolicyID = "policy.can.growatt.link"

	qCountAware = "count-aware"
	qCountValid = "count-valid"
)

// bindingSpec is one read binding of a native row to a storage pack element,
// with the priority score the driver publishes for that source. The bank is
// asserted by configuration and the frame carries no selector for it, so every
// spec yields one binding for each asserted bank. The subject is the
// storage.dimension.pack of the module that sent the frame, resolved by the
// caller through the pack role.
//
// No binding has a way back: the page documents no frame that sets these
// values, so the write row of every binding is nil.
type bindingSpec struct {
	native   string
	ref      string
	fact     semreg.DefinitionID
	unit     semreg.DefinitionID
	convert  roweval.Convert
	when     map[string]string
	priority int32
	fine     bool
}

func scaleConvert(divisor int64) roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: divisor}}
}

func offsetConvert(add semreg.Decimal) roweval.Convert {
	return roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: 1, Add: add}}
}

var (
	kelvinFromCelsius = semreg.Decimal{Coefficient: "27315", Exponent10: -2}
	kelvinFromCode    = semreg.Decimal{Coefficient: "23315", Exponent10: -2}
)

// bindingSpecs lists the bound sources. Priorities are distinct for every
// element that has more than one source: the finer decoded representation of
// one quantity outranks the coarser one, which is the driver's resolution rule
// and not a ranking the protocol states.
func bindingSpecs() []bindingSpec {
	local := map[string]string{qLocalUnits: "true"}
	release11 := map[string]string{qGroupEight: "true"}
	return []bindingSpec{
		{native: rowPrefix + "pack_voltage", ref: "LINK 1805 bits 0-13", fact: "storage.pack.voltage", unit: "unit.volt", convert: scaleConvert(1), when: local, priority: 100},
		{native: rowPrefix + "1805.current", ref: "LINK 1805 bits 14-26", fact: "storage.pack.current", unit: "unit.ampere", convert: scaleConvert(1), when: local, priority: 100},
		{native: rowPrefix + "1807.b4", ref: "LINK 1807 B4-B5, fine SOC", fact: "storage.state.soc", unit: "unit.ratio", convert: scaleConvert(100), when: release11, priority: 200},
		{native: rowPrefix + "whole_estimate", ref: "LINK 1807 B6, whole-percent estimate", fact: "storage.state.soc", unit: "unit.ratio", convert: scaleConvert(100), when: release11, priority: 100},
		{native: rowPrefix + "capacity", ref: "LINK 1803 B4-B5", fact: "storage.capacity.charge", unit: "unit.ampere_hour", convert: scaleConvert(1), when: map[string]string{qGroupTwo: "true"}, priority: 200},
		{native: rowPrefix + "full_capacity", ref: "LINK 180B layout W B0-B1", fact: "storage.capacity.charge", unit: "unit.ampere_hour", convert: scaleConvert(1), when: map[string]string{qGroupTwo: "true"}, priority: 100, fine: true},
		{native: rowPrefix + "1862.mean", ref: "LINK 1862 B4-B5, fine mean", fact: "storage.temperature.pack", unit: "unit.kelvin", convert: offsetConvert(kelvinFromCelsius), when: map[string]string{qFineGroup: "true", qGateMean: "true"}, priority: 200, fine: true},
		{native: rowPrefix + "extra_average", ref: "LINK 1808 B4, one-byte average", fact: "storage.temperature.pack", unit: "unit.kelvin", convert: offsetConvert(kelvinFromCode), when: map[string]string{qGroupEight: "true", qCountAware: "true", qCountValid: "true"}, priority: 100},
	}
}

func storageRef(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: semreg.PackRef{ID: storagePackID, Version: storagePackVersion}, ID: id}
}

func (b bindingSpec) rows() []roweval.BindingRow {
	var out []roweval.BindingRow
	for _, bank := range fineBanks {
		when := map[string]string{qBank: bank}
		for k, v := range b.when {
			when[k] = v
		}
		out = append(out, roweval.BindingRow{
			NativeID:    b.native,
			SubjectRole: packRole,
			SpecRef:     b.ref,
			When:        roweval.When{Equal: when},
			Service:     storageRef("storage.service.pack"),
			Capability:  storageRef("storage.capability.read.pack"),
			Read: &roweval.ReadRow{
				Fact:       storageRef(b.fact),
				Dimensions: map[semreg.DefinitionID]string{packDimension: packRole},
				Unit:       b.unit,
				Policy:     receivePolicy,
				ToRegistry: b.convert,
			},
		})
	}
	return out
}

// Bindings returns the read bindings of the LINK native rows.
func Bindings() []roweval.BindingRow {
	var out []roweval.BindingRow
	for _, b := range bindingSpecs() {
		out = append(out, b.rows()...)
	}
	return out
}

// SourcePriority is the priority score the driver publishes for one source of
// an element. A higher score outranks a lower one.
type SourcePriority struct {
	NativeID string
	Fact     semreg.DefinitionID
	Priority int32
}

// SourcePriorities returns the priority score of every bound source.
func SourcePriorities() []SourcePriority {
	var out []SourcePriority
	for _, b := range bindingSpecs() {
		out = append(out, SourcePriority{NativeID: b.native, Fact: b.fact, Priority: b.priority})
	}
	return out
}

func boundTable(t roweval.Table[Selector], fine bool) roweval.Table[Selector] {
	for _, b := range bindingSpecs() {
		if b.fine == fine {
			t.Bindings = append(t.Bindings, b.rows()...)
		}
	}
	return t
}

// BoundTable returns the native rows of the frames 1800 to 1808 with the
// read bindings of those frames.
func BoundTable() roweval.Table[Selector] { return boundTable(Table(), false) }

// BoundFineTable returns the native rows of 1862 and 180B with the read
// bindings of those frames.
func BoundFineTable() roweval.Table[Selector] { return boundTable(FineTable(), true) }

// BindingQualifiers adds the conditions of the 1808 average binding to a
// selection vocabulary: whether the count-aware path applies and whether the
// reported count of the reporting module is valid.
func BindingQualifiers(q roweval.Qualifiers, countAware, countValid bool) roweval.Qualifiers {
	values := make(map[string]string, len(q.Values)+2)
	for k, v := range q.Values {
		values[k] = v
	}
	values[qCountAware], values[qCountValid] = boolText(countAware), boolText(countValid)
	return roweval.Qualifiers{Values: values}
}
