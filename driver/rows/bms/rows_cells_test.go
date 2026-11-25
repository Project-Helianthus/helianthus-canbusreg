package bms

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/packs/storage"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// cellVector is one synthetic frame of the cell bindings and its independent
// expectation, written by hand from the page byte layout and the conversions of
// the binding (cell_vectors.json). Rows name the native row without its profile
// prefix. A candidate of null names the reason the row gives no candidate.
type cellVector struct {
	Name         string                `json:"name"`
	Profile      string                `json:"profile"`
	Identifier   uint32                `json:"identifier"`
	Extended     bool                  `json:"extended"`
	DataHex      string                `json:"data_hex"`
	CellRole     bool                  `json:"cell_role"`
	CellResource *string               `json:"cell_resource"`
	Rows         map[string]cellExpect `json:"rows"`
}

// cellExpect is the expected native value of a row and its candidate or reason.
type cellExpect struct {
	Native    string         `json:"native"`
	Candidate *cellCandidate `json:"candidate"`
	Reason    string         `json:"reason"`
}

// cellCandidate is the expected storage fact, unit, SI value and cell resource
// of a candidate.
type cellCandidate struct {
	Fact  string `json:"fact"`
	Unit  string `json:"unit"`
	Value string `json:"value"`
	Cell  string `json:"cell"`
}

// cellBindingInventory is the set of cell bindings the package carries: the
// native row, the storage fact and the profile whose table holds it.
var cellBindingInventory = []struct{ profile, row, fact string }{
	{"v104", "maximum_cell_temperature", "storage.temperature.cell"},
	{"v104", "maximum_cell_voltage", "storage.cell.voltage_maximum"},
	{"v104", "minimum_cell_voltage", "storage.cell.voltage_minimum"},
	{"v108", "maximum_cell_voltage", "storage.cell.voltage_maximum"},
	{"v108", "minimum_cell_voltage", "storage.cell.voltage_minimum"},
	{"monitor", "maximum_cell_voltage", "storage.cell.voltage_maximum"},
	{"monitor", "minimum_cell_voltage", "storage.cell.voltage_minimum"},
	{"monitor", "maximum_cell_temperature", "storage.temperature.cell"},
}

func readCellVectors(t *testing.T) []cellVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/cell_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []cellVector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("the cell vectors are empty")
	}
	return out
}

// cellStorageRoles returns the storage roles with a cell subject bound, as a
// host that resolves a cell supplies them.
func cellStorageRoles(t *testing.T) roweval.Roles {
	t.Helper()
	r := storageRoles(t)
	capability := semreg.CapabilityInstanceID("capability.synthetic.cell")
	subjects := make(map[string]roweval.RoleBinding, len(r.Subjects)+1)
	for k, v := range r.Subjects {
		subjects[k] = v
	}
	subjects[cellRole] = roweval.RoleBinding{ServiceInstance: "service.synthetic.cell", CapabilityInstance: &capability}
	r.Subjects = subjects
	return r
}

// cellVectorFrame builds the frame of one vector and its acquisition: the pack
// resource is always supplied, the cell role and the cell resource as the vector
// states them.
func cellVectorFrame(t *testing.T, v cellVector) (canbus.Frame, roweval.Acquired[Selector]) {
	t.Helper()
	raw := vector{Name: v.Name, Identifier: v.Identifier, Extended: v.Extended, DLC: 8, Data: v.DataHex}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	roles := storageRoles(t)
	if v.CellRole {
		roles = cellStorageRoles(t)
	}
	a := acquired(raw, roles)
	a.Coordinates = map[string]string{packSlot: "pack.site-a"}
	if v.CellResource != nil {
		a.Coordinates[cellSlot] = *v.CellResource
	}
	return frame, a
}

// cellVectorTable is the table of the vector's profile for its identifier.
func cellVectorTable(v cellVector) roweval.Table[Selector] {
	switch v.Profile {
	case "v108":
		return withCellBindings(V108Table())
	case "monitor":
		return withCellBindings(MonitorTable(v.Identifier))
	}
	return withCellBindings(V104Table())
}

// profileOfCellVector is the profile identifier of the vector's profile.
func profileOfCellVector(v cellVector) string {
	switch v.Profile {
	case "v108":
		return V108Profile
	case "monitor":
		return MonitorProfile
	}
	return Profile
}

// runCellVector gates the frame with its profile's frame gate and evaluates the
// rows of the given table on it, as EvaluateFrame, EvaluateV108Frame and
// EvaluateMonitorFrame do for the real tables.
func runCellVector(table roweval.Table[Selector], v cellVector, frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	switch v.Profile {
	case "v108":
		if reason := V108FrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	case "monitor":
		if reason := MonitorFrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	default:
		if reason := FrameDisposition(frame); reason != "" {
			return nil, reason, nil
		}
	}
	return evaluateAdmitted(table, frame, a)
}

// cellVectorMismatches lists every way the evaluations differ from the vector's
// expectation: a native value or evidence, a candidate's fact, unit, SI value,
// cell dimension or subject, or a reason where no candidate is expected.
func cellVectorMismatches(v cellVector, evals map[string]roweval.Evaluation, reason roweval.Reason, a roweval.Acquired[Selector]) []string {
	if reason != "" {
		return []string{fmt.Sprintf("%s: frame gate %q", v.Name, reason)}
	}
	var out []string
	for row, want := range v.Rows {
		id := profileOfCellVector(v) + "." + row
		ev, ok := evals[id]
		if !ok {
			out = append(out, fmt.Sprintf("%s: row %s not evaluated", v.Name, id))
			continue
		}
		if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, want.Native) {
			out = append(out, fmt.Sprintf("%s: %s native %+v, want %s", v.Name, id, ev.Native, want.Native))
			continue
		}
		if !reflect.DeepEqual(ev.Native.Evidence, a.Template.Evidence) {
			out = append(out, fmt.Sprintf("%s: %s lost its evidence", v.Name, id))
		}
		if want.Candidate == nil {
			if ev.Candidate != nil || string(ev.Reason) != want.Reason {
				out = append(out, fmt.Sprintf("%s: %s candidate %+v reason %q, want none and %q", v.Name, id, ev.Candidate, ev.Reason, want.Reason))
			}
			continue
		}
		c := ev.Candidate
		if c == nil || ev.Reason != "" {
			out = append(out, fmt.Sprintf("%s: %s has no candidate (reason %q)", v.Name, id, ev.Reason))
			continue
		}
		if string(c.Key.FactID) != want.Candidate.Fact {
			out = append(out, fmt.Sprintf("%s: %s fact %s, want %s", v.Name, id, c.Key.FactID, want.Candidate.Fact))
		}
		if c.Value == nil || c.Value.Quantity == nil || string(c.Value.Quantity.Unit) != want.Candidate.Unit || !decimalEquals(c.Value.Quantity.Number, want.Candidate.Value) {
			out = append(out, fmt.Sprintf("%s: %s value %+v, want %s %s", v.Name, id, c.Value, want.Candidate.Value, want.Candidate.Unit))
		}
		if len(c.Key.Dimensions) != 1 || string(c.Key.Dimensions[0].ID) != cellDimension || c.Key.Dimensions[0].Value.Text == nil || *c.Key.Dimensions[0].Value.Text != want.Candidate.Cell {
			out = append(out, fmt.Sprintf("%s: %s key %+v, want the cell %s", v.Name, id, c.Key.Dimensions, want.Candidate.Cell))
		}
		if ev.Subject == nil || ev.Subject.Role != cellRole {
			out = append(out, fmt.Sprintf("%s: %s subject %+v, want role %s", v.Name, id, ev.Subject, cellRole))
		}
	}
	return out
}

// checkCellVector runs one vector on its table and fails on any mismatch.
func checkCellVector(t *testing.T, v cellVector) {
	t.Helper()
	frame, a := cellVectorFrame(t, v)
	evals, reason, err := runCellVector(cellVectorTable(v), v, frame, a)
	if err != nil {
		t.Fatalf("%s: %v", v.Name, err)
	}
	for _, m := range cellVectorMismatches(v, evals, reason, a) {
		t.Error(m)
	}
}

// vectorsWithPrefix returns the vectors whose name has one of the prefixes.
func vectorsWithPrefix(t *testing.T, prefixes ...string) []cellVector {
	t.Helper()
	var out []cellVector
	for _, v := range readCellVectors(t) {
		for _, p := range prefixes {
			if strings.HasPrefix(v.Name, p) {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// TestCellVoltageExtremaBindToTheResolvedCellInEveryProfile pins that the
// maximum and minimum cell voltage of the V1.04 profile, the V1.08 profile and
// the addressed monitor profile give a storage candidate in volts on the resolved
// cell, with the cell's own key and subject.
func TestCellVoltageExtremaBindToTheResolvedCellInEveryProfile(t *testing.T) {
	vs := vectorsWithPrefix(t, "resolved-voltage-")
	profiles := map[string]bool{}
	for _, v := range vs {
		profiles[v.Profile] = true
		checkCellVector(t, v)
	}
	for _, p := range []string{"v104", "v108", "monitor"} {
		if !profiles[p] {
			t.Fatalf("no resolved cell voltage vector for profile %s", p)
		}
	}
}

// TestCellVoltageExtremaStayNativeWhenTheCellIsUnresolved pins that a cell
// voltage whose cell role or cell resource is not supplied keeps its native value
// and evidence, gives no candidate and names the reason.
func TestCellVoltageExtremaStayNativeWhenTheCellIsUnresolved(t *testing.T) {
	vs := vectorsWithPrefix(t, "unresolved-voltage-")
	if len(vs) == 0 {
		t.Fatal("no unresolved cell voltage vector")
	}
	for _, v := range vs {
		checkCellVector(t, v)
	}
}

// TestMaximumCellTemperatureBindsToKelvinOnlyForAResolvedCell pins that the V1.04
// maximum cell temperature gives kelvin on a resolved cell for a positive and a
// negative temperature, and keeps its native value and no candidate when the cell
// is not resolved.
func TestMaximumCellTemperatureBindsToKelvinOnlyForAResolvedCell(t *testing.T) {
	vs := vectorsWithPrefix(t, "temperature-v104-", "unresolved-temperature-v104-")
	resolved, unresolved, positive, negative := 0, 0, 0, 0
	for _, v := range vs {
		checkCellVector(t, v)
		if v.Rows["maximum_cell_temperature"].Candidate != nil {
			resolved++
			if strings.HasPrefix(v.Name, "temperature-v104-0x313-positive") {
				positive++
			} else {
				negative++
			}
		} else {
			unresolved++
		}
	}
	if resolved == 0 || unresolved == 0 || positive == 0 || negative == 0 {
		t.Fatalf("vectors: %d resolved, %d unresolved, %d positive, %d negative", resolved, unresolved, positive, negative)
	}
}

// TestMonitorMaximumTemperatureUsesItsOwnRecordAndResolvedCell pins that the
// monitor maximum cell temperature takes its cell from the maximum temperature
// sensor position of its own 04030000 record, and gives no candidate without a
// resolved cell. The voltage record's position is not used for it.
func TestMonitorMaximumTemperatureUsesItsOwnRecordAndResolvedCell(t *testing.T) {
	for _, v := range vectorsWithPrefix(t, "temperature-monitor-", "unresolved-temperature-monitor-") {
		checkCellVector(t, v)
	}

	roles := cellStorageRoles(t)
	tempFrame, tempRaw := monitorFrame(t, 0x04030000, "8A02900105000200")
	voltFrame, voltRaw := monitorFrame(t, 0x04020000, "E50CDA0C03000900")

	tempEvals, reason, err := EvaluateMonitorFrame(tempFrame, acquired(tempRaw, roles))
	if err != nil || reason != "" {
		t.Fatalf("temperature record: reason %q err %v", reason, err)
	}
	position := tempEvals[MonitorProfile+".position_of_maximum_temperature_sensor"]
	if position.Native == nil || position.Native.Value == nil || !decimalEquals(position.Native.Value.Quantity.Number, "5") {
		t.Fatalf("own record position: %+v, want 5", position.Native)
	}
	voltEvals, reason, err := EvaluateMonitorFrame(voltFrame, acquired(voltRaw, roles))
	if err != nil || reason != "" {
		t.Fatalf("voltage record: reason %q err %v", reason, err)
	}
	voltPosition := voltEvals[MonitorProfile+".position_of_maximum_voltage_cell"]
	if voltPosition.Native == nil || voltPosition.Native.Value == nil || !decimalEquals(voltPosition.Native.Value.Quantity.Number, "3") {
		t.Fatalf("voltage record position: %+v, want 3", voltPosition.Native)
	}

	a := acquired(tempRaw, roles)
	a.Coordinates[cellSlot] = "cell.site-a.5"
	evals, reason, err := EvaluateMonitorFrame(tempFrame, a)
	if err != nil || reason != "" {
		t.Fatalf("resolved temperature record: reason %q err %v", reason, err)
	}
	c := evals[MonitorProfile+".maximum_cell_temperature"].Candidate
	if c == nil || len(c.Key.Dimensions) != 1 || c.Key.Dimensions[0].Value.Text == nil || *c.Key.Dimensions[0].Value.Text != "cell.site-a.5" {
		t.Fatalf("temperature candidate key %+v, want the cell of its own position 5", c)
	}
	if c.Key.FactID != "storage.temperature.cell" || !decimalEquals(c.Value.Quantity.Number, "298.15") {
		t.Fatalf("temperature candidate %+v, want 298.15 K", c)
	}
}

// TestCellBindingsFailWhenAnyOneIsDeleted pins that each of the eight cell
// bindings is exercised by a vector, and that removing any one of them from its
// table makes at least one vector fail.
func TestCellBindingsFailWhenAnyOneIsDeleted(t *testing.T) {
	vectors := readCellVectors(t)
	exercised := map[string]bool{}
	for _, v := range vectors {
		if v.CellRole == false || v.CellResource == nil {
			continue
		}
		table := cellVectorTable(v)
		for i, b := range table.Bindings {
			if b.SubjectRole != cellRole || b.Read == nil {
				continue
			}
			if _, expected := v.Rows[strings.TrimPrefix(b.NativeID, profileOfCellVector(v)+".")]; !expected {
				continue
			}
			key := v.Profile + "." + strings.TrimPrefix(b.NativeID, profileOfCellVector(v)+".") + "." + string(b.Read.Fact.ID)
			exercised[key] = true
			reduced := table
			reduced.Bindings = append(append([]roweval.BindingRow{}, table.Bindings[:i]...), table.Bindings[i+1:]...)
			frame, a := cellVectorFrame(t, v)
			evals, reason, err := runCellVector(reduced, v, frame, a)
			if err != nil {
				t.Fatalf("%s without %s: %v", v.Name, b.NativeID, err)
			}
			if len(cellVectorMismatches(v, evals, reason, a)) == 0 {
				t.Errorf("deleting the cell binding %s of %s left every vector passing", b.NativeID, v.Name)
			}
		}
	}
	for _, want := range cellBindingInventory {
		key := want.profile + "." + want.row + "." + want.fact
		if !exercised[key] {
			t.Errorf("cell binding %s is not exercised by any vector", key)
		}
	}
	if len(exercised) != len(cellBindingInventory) {
		t.Errorf("%d cell bindings exercised, want %d", len(exercised), len(cellBindingInventory))
	}
}

// TestCellCatalogBoundsMirrorThePinnedStoragePack pins the lower bound of the cell
// temperature and the cell voltages: the mirror of the pinned storage pack accepts
// exactly the values the pinned validator accepts.
func TestCellCatalogBoundsMirrorThePinnedStoragePack(t *testing.T) {
	vs := vectorsWithPrefix(t, "temperature-v104-0x313-positive")
	if len(vs) != 1 {
		t.Fatalf("want one positive cell temperature vector, got %d", len(vs))
	}
	frame, a := cellVectorFrame(t, vs[0])
	evals, reason, err := runCellVector(cellVectorTable(vs[0]), vs[0], frame, a)
	if err != nil || reason != "" || evals[Profile+".maximum_cell_temperature"].Candidate == nil {
		t.Fatalf("base cell candidate: reason %q err %v", reason, err)
	}
	base := evals[Profile+".maximum_cell_temperature"].Candidate.Key
	type edge struct {
		coefficient string
		exponent    int32
		inside      bool
	}
	cases := []struct {
		fact, unit string
		edges      []edge
	}{
		{"storage.temperature.cell", "unit.kelvin", []edge{{"-1", 2, false}, {"0", 0, true}, {"29815", -2, true}}},
		{"storage.cell.voltage_maximum", "unit.volt", []edge{{"-1", 3, false}, {"0", 0, true}, {"3301", -3, true}}},
		{"storage.cell.voltage_minimum", "unit.volt", []edge{{"-1", 3, false}, {"0", 0, true}, {"329", -2, true}}},
	}
	for _, c := range cases {
		if _, ok := catalogBounds[semreg.DefinitionID(c.fact)]; !ok {
			t.Fatalf("%s has no mirrored bound", c.fact)
		}
		key := base
		key.FactID = semreg.DefinitionID(c.fact)
		for _, e := range c.edges {
			value := semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: semreg.Decimal{Coefficient: e.coefficient, Exponent10: e.exponent}, Unit: semreg.DefinitionID(c.unit)}}
			pinned := storage.New().ValidateFact(key, &value) == nil
			mirrored := catalogRefusal(key, &value) == ""
			if pinned != e.inside || mirrored != e.inside {
				t.Fatalf("%s at %se%d: pinned accepts %v, mirror accepts %v, want %v", c.fact, e.coefficient, e.exponent, pinned, mirrored, e.inside)
			}
		}
	}
}
