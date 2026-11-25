package bms

import (
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
)

// storagePack is the pinned storage pack that owns the relation definitions
// below. Its catalog entries are the element catalog of helianthus-semreg,
// read by the tests.
var storagePack = semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}

// ResourceRow declares one resource kind the BMS family always publishes. The
// anchor class is the catalog attribute of the kind.
type ResourceRow struct {
	Kind   semreg.DefinitionID
	Anchor semreg.AnchorClass
}

// RelationRow declares one storage relation definition the BMS family always
// publishes. CoreType, From, To, Cardinality, OverTime and Basis are the catalog
// attributes of the definition; From and To are the resource kinds at its ends.
type RelationRow struct {
	Definition  semreg.DefinitionRef
	CoreType    string
	From        semreg.DefinitionID
	To          semreg.DefinitionID
	Cardinality string
	OverTime    bool
	Basis       semreg.RelationBasis
}

// ResourceRows returns the resource kinds of the BMS family: the battery,
// its modules and its cells. The family publishes them unconditionally; the
// function takes no switch and returns a fresh slice on every call.
func ResourceRows() []ResourceRow {
	return []ResourceRow{
		{Kind: "resource.battery", Anchor: semreg.AnchorEquipment},
		{Kind: "resource.battery_module", Anchor: semreg.AnchorComponent},
		{Kind: "resource.battery_cell", Anchor: semreg.AnchorComponent},
	}
}

// RelationRows returns the three storage relations that connect the storage
// system, the battery, the pack and the pack interface. The family publishes
// them unconditionally; the function takes no switch and returns a fresh slice
// on every call.
func RelationRows() []RelationRow {
	return []RelationRow{
		{
			Definition:  storageRelation("storage.relation.system_battery"),
			CoreType:    "groups",
			From:        "resource.storage_system",
			To:          "resource.battery",
			Cardinality: "one_to_many",
			OverTime:    false,
			Basis:       semreg.RelationConfiguration,
		},
		{
			Definition:  storageRelation("storage.relation.battery_pack"),
			CoreType:    "contains",
			From:        "resource.battery",
			To:          "resource.battery_pack",
			Cardinality: "one_to_many",
			OverTime:    false,
			Basis:       semreg.RelationGateRule,
		},
		{
			Definition:  storageRelation("storage.relation.pack_interface"),
			CoreType:    "contains",
			From:        "resource.battery_pack",
			To:          "resource.storage_interface",
			Cardinality: "one_to_many",
			OverTime:    false,
			Basis:       semreg.RelationGateRule,
		},
	}
}

// storageRelation names one relation definition of the pinned storage pack at
// its catalog version.
func storageRelation(id semreg.DefinitionID) semreg.DefinitionRef {
	return semreg.DefinitionRef{Pack: storagePack, ID: id, Version: storagePack.Version}
}
