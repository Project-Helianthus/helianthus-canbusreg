package bms

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/packs/storage"
)

// semregModule is the module whose pinned element catalog the tests read. The
// catalog file is located through go list, so the test reads the revision that
// go.mod pins and fails when that revision is not available.
const semregModule = "github.com/Project-Helianthus/helianthus-semreg"

// catalogElement is one entry of the pinned element catalog that the relation
// and resource rows are checked against.
type catalogElement struct {
	Ref struct {
		Owner struct {
			Kind string       `json:"kind"`
			Pack *catalogPack `json:"pack"`
		} `json:"owner"`
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"ref"`
	ElementKind string            `json:"element_kind"`
	Disposition string            `json:"disposition"`
	Attributes  catalogAttributes `json:"attributes"`
}

type catalogPack struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type catalogAttributes struct {
	AnchorClass  string   `json:"anchor_class"`
	CoreType     string   `json:"core_type"`
	FromKinds    []string `json:"from_kinds"`
	ToKinds      []string `json:"to_kinds"`
	Cardinality  string   `json:"cardinality"`
	OverTime     bool     `json:"over_time"`
	BasisClasses []string `json:"basis_classes"`
}

// wantResourceKinds is the resource kinds the BMS family publishes, in row order.
var wantResourceKinds = []semreg.DefinitionID{"resource.battery", "resource.battery_module", "resource.battery_cell"}

// wantRelationIDs is the storage relations the BMS family publishes, in row order.
var wantRelationIDs = []semreg.DefinitionID{"storage.relation.system_battery", "storage.relation.battery_pack", "storage.relation.pack_interface"}

// pinnedCatalog reads the element catalog of the SemReg revision go.mod pins.
func pinnedCatalog(t *testing.T) []catalogElement {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", semregModule)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("locate the pinned SemReg module: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	raw, err := os.ReadFile(filepath.Join(dir, "semreg", "v1", "graph", "embedded", "element-catalog-v1.json"))
	if err != nil {
		t.Fatalf("read the pinned element catalog: %v", err)
	}
	var doc struct {
		Elements []catalogElement `json:"elements"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the pinned element catalog: %v", err)
	}
	return doc.Elements
}

// findCoreElement returns the core-owned catalog entry with the identifier.
func findCoreElement(t *testing.T, elements []catalogElement, id string) catalogElement {
	t.Helper()
	for _, e := range elements {
		if e.Ref.Owner.Kind == "core" && e.Ref.ID == id {
			return e
		}
	}
	t.Fatalf("pinned catalog has no core entry %s", id)
	return catalogElement{}
}

// findPackElement returns the entry of the pinned storage pack with the
// identifier and version of the definition.
func findPackElement(t *testing.T, elements []catalogElement, ref semreg.DefinitionRef) catalogElement {
	t.Helper()
	for _, e := range elements {
		p := e.Ref.Owner.Pack
		if e.Ref.Owner.Kind == "pack" && p != nil && p.ID == string(ref.Pack.ID) && p.Version == string(ref.Pack.Version) &&
			e.Ref.ID == string(ref.ID) && e.Ref.Version == string(ref.Version) {
			return e
		}
	}
	t.Fatalf("pinned catalog has no pack entry %+v", ref)
	return catalogElement{}
}

// graphRowGaps names what a set of resource and relation rows lacks against the
// published set, and what it has more than once. An empty result is complete.
func graphRowGaps(resources []ResourceRow, relations []RelationRow) []string {
	var gaps []string
	seen := map[semreg.DefinitionID]int{}
	for _, r := range resources {
		seen[r.Kind]++
	}
	for _, kind := range wantResourceKinds {
		if seen[kind] != 1 {
			gaps = append(gaps, "resource "+string(kind))
		}
	}
	if len(resources) != len(wantResourceKinds) {
		gaps = append(gaps, "resource row count")
	}
	seenRel := map[semreg.DefinitionID]int{}
	for _, r := range relations {
		seenRel[r.Definition.ID]++
	}
	for _, id := range wantRelationIDs {
		if seenRel[id] != 1 {
			gaps = append(gaps, "relation "+string(id))
		}
	}
	if len(relations) != len(wantRelationIDs) {
		gaps = append(gaps, "relation row count")
	}
	return gaps
}

func TestBMSResourceRowsDeclareBatteryModuleAndCell(t *testing.T) {
	rows := BMSResourceRows()
	elements := pinnedCatalog(t)
	if len(rows) != len(wantResourceKinds) {
		t.Fatalf("resource rows = %d, want %d", len(rows), len(wantResourceKinds))
	}
	for i, row := range rows {
		if row.Kind != wantResourceKinds[i] {
			t.Fatalf("resource row %d kind = %s, want %s", i, row.Kind, wantResourceKinds[i])
		}
		if err := row.Kind.Validate(); err != nil {
			t.Fatalf("%s: %v", row.Kind, err)
		}
		if err := row.Anchor.Validate(); err != nil {
			t.Fatalf("%s anchor: %v", row.Kind, err)
		}
		e := findCoreElement(t, elements, string(row.Kind))
		if e.ElementKind != "resource_kind" || e.Disposition != "member" {
			t.Fatalf("%s: catalog kind %q disposition %q, want resource_kind member", row.Kind, e.ElementKind, e.Disposition)
		}
		if e.Attributes.AnchorClass != string(row.Anchor) {
			t.Fatalf("%s: catalog anchor class %q, row %q", row.Kind, e.Attributes.AnchorClass, row.Anchor)
		}
	}
}

func TestBMSRelationRowsUseTheCatalogDefinitionsAndBasis(t *testing.T) {
	if storage.Metadata().Pack != storagePack {
		t.Fatalf("storage pack = %+v, want %+v", storage.Metadata().Pack, storagePack)
	}
	elements := pinnedCatalog(t)
	rows := BMSRelationRows()
	if len(rows) != len(wantRelationIDs) {
		t.Fatalf("relation rows = %d, want %d", len(rows), len(wantRelationIDs))
	}
	for i, row := range rows {
		if row.Definition.ID != wantRelationIDs[i] {
			t.Fatalf("relation row %d id = %s, want %s", i, row.Definition.ID, wantRelationIDs[i])
		}
		if err := row.Definition.Validate(); err != nil {
			t.Fatalf("%s: %v", row.Definition.ID, err)
		}
		if err := row.Basis.Validate(); err != nil {
			t.Fatalf("%s basis: %v", row.Definition.ID, err)
		}
		e := findPackElement(t, elements, row.Definition)
		if e.ElementKind != "relation" || e.Disposition != "member" {
			t.Fatalf("%s: catalog kind %q disposition %q, want relation member", row.Definition.ID, e.ElementKind, e.Disposition)
		}
		a := e.Attributes
		want := catalogAttributes{
			CoreType:     row.CoreType,
			FromKinds:    []string{string(row.From)},
			ToKinds:      []string{string(row.To)},
			Cardinality:  row.Cardinality,
			OverTime:     row.OverTime,
			BasisClasses: []string{string(row.Basis)},
		}
		got := catalogAttributes{CoreType: a.CoreType, FromKinds: a.FromKinds, ToKinds: a.ToKinds, Cardinality: a.Cardinality, OverTime: a.OverTime, BasisClasses: a.BasisClasses}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: catalog attributes %+v, row %+v", row.Definition.ID, got, want)
		}
	}
}

func TestBMSRelationRowsAreAlwaysPublishedWithNoSwitch(t *testing.T) {
	var _ func() []ResourceRow = BMSResourceRows
	var _ func() []RelationRow = BMSRelationRows
	first := BMSRelationRows()
	if gaps := graphRowGaps(BMSResourceRows(), first); len(gaps) != 0 {
		t.Fatalf("published graph is incomplete: %v", gaps)
	}
	// Each call returns a fresh slice, so an edit of one result never changes the
	// rows a later call publishes.
	first[0].From = "resource.other"
	first[0].Basis = semreg.RelationGateRule
	again := BMSRelationRows()
	if again[0].From != "resource.storage_system" || again[0].Basis != semreg.RelationConfiguration {
		t.Fatalf("second call returned an edited row: %+v", again[0])
	}
	resources := BMSResourceRows()
	resources[0].Kind = "resource.other"
	if got := BMSResourceRows()[0].Kind; got != "resource.battery" {
		t.Fatalf("second resource call returned an edited row: %s", got)
	}
}

func TestBMSResourceAndRelationRowsFailWhenDeleted(t *testing.T) {
	resources, relations := BMSResourceRows(), BMSRelationRows()
	if gaps := graphRowGaps(resources, relations); len(gaps) != 0 {
		t.Fatalf("complete graph reports gaps: %v", gaps)
	}
	for i := range resources {
		cut := append(append([]ResourceRow{}, resources[:i]...), resources[i+1:]...)
		if gaps := graphRowGaps(cut, relations); len(gaps) == 0 {
			t.Fatalf("deleting resource row %s is not reported", resources[i].Kind)
		}
	}
	for i := range relations {
		cut := append(append([]RelationRow{}, relations[:i]...), relations[i+1:]...)
		if gaps := graphRowGaps(resources, cut); len(gaps) == 0 {
			t.Fatalf("deleting relation row %s is not reported", relations[i].Definition.ID)
		}
	}
}
