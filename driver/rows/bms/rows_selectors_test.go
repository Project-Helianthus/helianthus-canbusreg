package bms

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// selectorRecord is one entry of testdata/selector_vectors.json as written: a
// synthetic frame of one profile and its independent expectation. Profile is
// v104, common, v108 or monitor; Scope is the selected common publisher scope;
// Frame is admitted (Native lists the row values the frame must give, Absent
// the rows it must not evaluate), rejected (Reason names the gate reason) or
// no_match.
type selectorRecord struct {
	Name       string           `json:"name"`
	Profile    string           `json:"profile"`
	Scope      string           `json:"scope"`
	Identifier string           `json:"identifier"`
	Extended   bool             `json:"extended"`
	DLC        uint8            `json:"dlc"`
	RawDLC     uint8            `json:"raw_dlc"`
	DataHex    string           `json:"data_hex"`
	Frame      string           `json:"frame"`
	Reason     string           `json:"reason"`
	Native     map[string]int64 `json:"native"`
	Absent     []string         `json:"absent"`
	Rows       []string         `json:"rows"`
}

// selectorVector is one frame of the selector tests: the identifier, the
// extended flag, the DLC and raw DLC, and the eight data bytes of the record.
// Mutated copies of a vector vary one of these fields at a time. rows is the
// complete list of native row names an admitted frame must evaluate, without
// the profile prefix: a row missing from the live table, or a row evaluated on
// a frame of another identifier, changes that list and fails the vector.
type selectorVector struct {
	name, profile, scope string
	id                   uint32
	extended             bool
	dlc, rawDLC          uint8
	data                 []byte
	frame, reason        string
	native               map[string]int64
	absent               []string
	rows                 []string
}

// selectorProfilePrefixes maps each profile of the vector file to the prefix of
// its native row identifiers, the profile name and its dot.
var selectorProfilePrefixes = map[string]string{
	"v104":    Profile + ".",
	"common":  CommonProfile + ".",
	"v108":    V108Profile + ".",
	"monitor": MonitorProfile + ".",
}

func readSelectorVectors(t *testing.T) []selectorVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/selector_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var records []selectorRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("the selector vectors are empty")
	}
	names := map[string]bool{}
	out := make([]selectorVector, 0, len(records))
	for _, r := range records {
		if names[r.Name] {
			t.Fatalf("vector name %q is repeated", r.Name)
		}
		names[r.Name] = true
		if _, ok := selectorProfilePrefixes[r.Profile]; !ok {
			t.Fatalf("%s: unknown profile %q", r.Name, r.Profile)
		}
		id, err := strconv.ParseUint(r.Identifier, 0, 32)
		if err != nil {
			t.Fatalf("%s: identifier %q: %v", r.Name, r.Identifier, err)
		}
		data, err := hex.DecodeString(r.DataHex)
		if err != nil || len(data) != 8 {
			t.Fatalf("%s: data %q must be eight bytes", r.Name, r.DataHex)
		}
		switch r.Frame {
		case "admitted":
			if len(r.Rows) == 0 {
				t.Fatalf("%s: an admitted frame declares no rows", r.Name)
			}
		case "rejected":
			if r.Reason == "" {
				t.Fatalf("%s: a rejected frame names its reason", r.Name)
			}
		case "no_match":
		default:
			t.Fatalf("%s: unknown frame disposition %q", r.Name, r.Frame)
		}
		out = append(out, selectorVector{name: r.Name, profile: r.Profile, scope: r.Scope, id: uint32(id), extended: r.Extended,
			dlc: r.DLC, rawDLC: r.RawDLC, data: data, frame: r.Frame, reason: r.Reason, native: r.Native, absent: r.Absent, rows: r.Rows})
	}
	return out
}

// selectorByName returns the vector of the file with the given name.
func selectorByName(t *testing.T, name string) selectorVector {
	t.Helper()
	for _, v := range readSelectorVectors(t) {
		if v.name == name {
			return v
		}
	}
	t.Fatalf("no selector vector named %q", name)
	return selectorVector{}
}

// selectorFrame returns an admitted-shaped vector of DLC 8 and raw DLC 8 for
// the programmatic cases of the tests.
func selectorFrame(name, profile string, id uint32, data []byte) selectorVector {
	return selectorVector{name: name, profile: profile, id: id, dlc: 8, rawDLC: 8, data: data}
}

// record builds the 16-byte socketcan record: the identifier with the extended
// flag, the DLC at byte 4 and the data from byte 8. Byte 7 carries the wire DLC
// of an eight-byte frame: zero encodes raw DLC 8, and 9 to 15 are raw DLCs the
// canbus decoder admits for an eight-byte payload only. A payload shorter than
// eight bytes has byte 7 zero and raw DLC equal to its length.
func (v selectorVector) record() []byte {
	raw := make([]byte, 16)
	id := v.id
	if v.extended {
		id |= canEFFFlag
	}
	binary.NativeEndian.PutUint32(raw, id)
	raw[4] = v.dlc
	if v.rawDLC > 8 {
		raw[7] = v.rawDLC
	}
	copy(raw[8:], v.data)
	return raw
}

// entry runs the entry point of the vector's profile on the decoded frame, the
// same call the receive path makes.
func (v selectorVector) entry(frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	switch v.profile {
	case "v104":
		return EvaluateFrame(V104Table(), frame, a)
	case "common":
		return EvaluateCommonFrame(v.scope, frame, a)
	case "v108":
		return EvaluateV108Frame(frame, a)
	case "monitor":
		return EvaluateMonitorFrame(frame, a)
	}
	return nil, "", fmt.Errorf("unknown profile %q", v.profile)
}

// evaluate decodes the record and runs the profile's entry point on it.
func (v selectorVector) evaluate(t *testing.T, roles roweval.Roles) (map[string]roweval.Evaluation, roweval.Reason) {
	t.Helper()
	raw := v.record()
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatalf("%s: record does not decode: %v", v.name, err)
	}
	evals, reason, err := v.entry(frame, acquired(raw, roles))
	if err != nil {
		t.Fatalf("%s: %v", v.name, err)
	}
	return evals, reason
}

// evaluateSelectorTable evaluates the vector's frame under the given table with
// the qualifiers the profile's entry point sets. It runs no gate, so the caller
// passes an admitted frame. The deletion test uses it to drop one row at a time.
func evaluateSelectorTable(t *testing.T, v selectorVector, table roweval.Table[Selector], roles roweval.Roles) map[string]roweval.Evaluation {
	t.Helper()
	raw := v.record()
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatalf("%s: record does not decode: %v", v.name, err)
	}
	a := acquired(raw, roles)
	values := map[string]string{}
	switch v.profile {
	case "common":
		values[scopeQualifier] = v.scope
	case "v108":
		if v.id == 0x324 && !v.extended {
			values[serialLayoutQualifier] = serialLayoutOf(frame.Bytes()[0])
			table.Native = rowsAppliedTo(table.Native, values)
		}
	}
	a.Qualifiers.Values = values
	evals, reason, err := evaluateAdmitted(withCellBindings(table), frame, a)
	if err != nil || reason != "" {
		t.Fatalf("%s: reason %q err %v", v.name, reason, err)
	}
	return evals
}

// fullSelectorTable is the whole table of the profile the vector's frame uses.
func fullSelectorTable(profile, scope string, id uint32) roweval.Table[Selector] {
	switch profile {
	case "v104":
		return V104Table()
	case "common":
		return CommonTable(scope)
	case "v108":
		return V108Table()
	case "monitor":
		return MonitorTable(id)
	}
	return roweval.Table[Selector]{}
}

// nativeEquals reports whether the evaluation gives the native decimal want.
func nativeEquals(ev roweval.Evaluation, want int64) bool {
	return ev.Native != nil && ev.Native.Reason == "" && ev.Native.Value != nil &&
		ev.Native.Value.Quantity != nil && decimalEquals(ev.Native.Value.Quantity.Number, strconv.FormatInt(want, 10))
}

// nativeKey is the comparable content of a native evaluation: its reason, or
// its decimal value.
func nativeKey(ev roweval.Evaluation) string {
	if ev.Native == nil {
		return "no native evaluation"
	}
	if ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
		return "reason " + string(ev.Native.Reason)
	}
	n := ev.Native.Value.Quantity.Number
	return fmt.Sprintf("%s e%d", n.Coefficient, n.Exponent10)
}

// rowNamesOf returns the sorted row names of the evaluations, without the
// profile prefix; a row of another profile keeps its full identifier.
func rowNamesOf(profile string, evals map[string]roweval.Evaluation) []string {
	prefix := selectorProfilePrefixes[profile]
	names := make([]string, 0, len(evals))
	for id := range evals {
		names = append(names, strings.TrimPrefix(id, prefix))
	}
	sort.Strings(names)
	return names
}

// rowMatches reports whether the row id is evaluated to a native value on the
// frame. An evaluation with a reason, or a row whose selector did not match, is
// not a match.
func rowMatches(evals map[string]roweval.Evaluation, id string) bool {
	ev, ok := evals[id]
	return ok && ev.Native != nil && ev.Native.Reason == "" && ev.Native.Value != nil
}

// unmatchedRows returns the sorted IDs of the evaluations that give no native
// value. A selector miss keeps its ID in the evaluation map, so the count and
// the IDs of an evaluation do not show that each of its rows matched the frame.
func unmatchedRows(evals map[string]roweval.Evaluation) []string {
	var ids []string
	for id := range evals {
		if !rowMatches(evals, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// sameNames reports whether two name lists hold the same names, in any order.
func sameNames(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\x00") == strings.Join(y, "\x00")
}

// checkSelectorOutcome compares the gate outcome and the row values of one
// vector with its expectation.
func checkSelectorOutcome(t *testing.T, v selectorVector, evals map[string]roweval.Evaluation, reason roweval.Reason) {
	t.Helper()
	prefix := selectorProfilePrefixes[v.profile]
	switch v.frame {
	case "admitted":
		if reason != "" || evals == nil {
			t.Fatalf("%s: frame not admitted: reason %q", v.name, reason)
		}
		for name, want := range v.native {
			ev, ok := evals[prefix+name]
			if !ok {
				t.Fatalf("%s: row %s not evaluated", v.name, name)
			}
			if !nativeEquals(ev, want) {
				t.Fatalf("%s: row %s gives reason %q value %+v, want %d", v.name, name, ev.Native.Reason, ev.Native.Value, want)
			}
		}
		for _, name := range v.absent {
			if _, ok := evals[prefix+name]; ok {
				t.Fatalf("%s: row %s evaluated on a frame it does not apply to", v.name, name)
			}
		}
		// The vector file declares the row list of every admitted frame it holds
		// (readSelectorVectors). A frame built in code carries no list and is
		// checked by its values only.
		if v.rows != nil {
			if got := rowNamesOf(v.profile, evals); !sameNames(got, v.rows) {
				t.Fatalf("%s: evaluated rows %v, the frame declares %v", v.name, got, v.rows)
			}
		}
	case "rejected":
		if reason != roweval.Reason(v.reason) || evals != nil {
			t.Fatalf("%s: reason %q with %d evaluations, want %q and none", v.name, reason, len(evals), v.reason)
		}
	case "no_match":
		if reason != roweval.ReasonNoMatch || evals != nil {
			t.Fatalf("%s: reason %q with %d evaluations, want no match", v.name, reason, len(evals))
		}
	}
}

// TestSelectorVectorsMatchTheirExpectations runs every vector of the file
// through its profile's entry point and compares the gate outcome and the row
// values with the file's expectation.
func TestSelectorVectorsMatchTheirExpectations(t *testing.T) {
	roles := storageRoles(t)
	for _, v := range readSelectorVectors(t) {
		t.Run(v.name, func(t *testing.T) {
			evals, reason := v.evaluate(t, roles)
			checkSelectorOutcome(t, v, evals, reason)
		})
	}
}

// TestEveryProfileRejectsNeighbouringIdentifierWrongDLCWrongRawDLCAndExtendedIdentifier
// mutates each admitted frame of the V1.04, common projection, V1.08 and addressed
// monitor profiles in four fields, one at a time: the identifier (a neighbouring
// standard identifier; for the extended monitor frames, the identifier with
// another bit of the base), the DLC, the raw DLC, and the identifier form (extended
// for the standard profiles; standard for the monitor frames). Each mutated frame
// matches no row and gives no evaluation.
func TestEveryProfileRejectsNeighbouringIdentifierWrongDLCWrongRawDLCAndExtendedIdentifier(t *testing.T) {
	roles := storageRoles(t)
	mutations := 0
	for _, base := range readSelectorVectors(t) {
		if base.frame != "admitted" {
			continue
		}
		neighbour := base
		form := base
		if base.profile == "monitor" {
			// A standard identifier cannot carry a 29-bit value: the standard
			// form is the low eleven bits of the same identifier.
			neighbour.id ^= 0x100
			form.id &= 0x7ff
			form.extended = false
		} else {
			neighbour.id ^= 0x10
			form.extended = true
		}
		wrongDLC := base
		wrongDLC.dlc = 7
		wrongRawDLC := base
		wrongRawDLC.rawDLC = 9 // the wire DLC of an eight-byte frame that is not eight
		type mutation struct {
			field string
			v     selectorVector
		}
		cases := []mutation{
			{"neighbouring identifier", neighbour},
			{"wrong DLC", wrongDLC},
			{"wrong raw DLC", wrongRawDLC},
			{"identifier form", form},
		}
		if base.profile != "monitor" {
			// The frames directly before and after the base identifier. An
			// adjacent identifier the profile itself defines is another frame
			// with its own vectors; any other adjacent identifier is a negative
			// case, which catches a gate that admits a frame of another revision
			// or of the common projection (for example 0x314 on common).
			defined := admittedStandardIdentifiers(t, base.profile)
			for _, adj := range []uint32{base.id - 1, base.id + 1} {
				if defined[adj] {
					continue
				}
				next := base
				next.id = adj
				cases = append(cases, mutation{"adjacent identifier", next})
			}
		}
		for _, m := range cases {
			mutated := m.v
			mutated.name = base.name + " with " + m.field
			mutated.frame = "no_match"
			evals, reason := mutated.evaluate(t, roles)
			checkSelectorOutcome(t, mutated, evals, reason)
			mutations++
		}
	}
	if mutations == 0 {
		t.Fatal("no admitted frame was mutated")
	}
}

// TestMonitorFamiliesMatchOnlyTheirOwnAddressedRows pins that an addressed
// monitor frame gives the rows of its own family only, at its own address: the
// same family at another BCU or BMU address gives rows of that address and none
// of the frame's own, and the system and module forms never share rows.
func TestMonitorFamiliesMatchOnlyTheirOwnAddressedRows(t *testing.T) {
	roles := storageRoles(t)
	checked := 0
	for _, v := range readSelectorVectors(t) {
		if v.profile != "monitor" || v.frame != "admitted" {
			continue
		}
		base, ok := monitorBase(v.id)
		if !ok {
			t.Fatalf("%s: identifier has no family", v.name)
		}
		own := map[string]bool{}
		for _, f := range monitorLayouts[base] {
			own[f.name] = true
		}
		evals, reason := v.evaluate(t, roles)
		if reason != "" || len(evals) != len(own) {
			t.Fatalf("%s: %d rows with reason %q, its family has %d", v.name, len(evals), reason, len(own))
		}
		for id := range evals {
			if !own[strings.TrimPrefix(id, MonitorProfile+".")] {
				t.Fatalf("%s: row %s belongs to another family", v.name, id)
			}
		}
		if got := unmatchedRows(evals); len(got) != 0 {
			t.Fatalf("%s: rows %v of the frame's own address give no native value", v.name, got)
		}
		// The same bytes at another address, each address changed on its own. The
		// BCU is the low byte of both forms and is varied for every frame; the BMU
		// (bits 20 to 23) is varied for the module form too. Each variant runs
		// through the production entry point, which runs the frame gate, so a gate
		// that rejects another address fails here. The variant must keep the family,
		// be admitted, and give exactly the family's rows, all at its own address.
		flips := []uint32{0x1} // the BCU, the low byte of both forms
		if v.id>>24 != 0x04 {
			flips = append(flips, 0x00100000) // the BMU, bits 20 to 23 of the module form
		}
		for _, flip := range flips {
			other := v
			other.id ^= flip
			otherBase, ok := monitorBase(other.id)
			if !ok || otherBase != base {
				t.Fatalf("%s: other address %#x leaves the family", v.name, other.id)
			}
			if got := evaluateSelectorTable(t, v, MonitorTable(other.id), roles); len(got) != 0 {
				t.Fatalf("%s: the table of address %#x evaluated a frame of %#x: %d rows", v.name, other.id, v.id, len(got))
			}
			otherEvals, otherReason := other.evaluate(t, roles)
			if otherReason != "" || len(otherEvals) != len(own) {
				t.Fatalf("%s: address %#x gives %d rows with reason %q, its family has %d", v.name, other.id, len(otherEvals), otherReason, len(own))
			}
			// A row whose selector misses keeps its ID in the map, so the count above
			// does not show that the row matched: each row must give a native value.
			if got := unmatchedRows(otherEvals); len(got) != 0 {
				t.Fatalf("%s: rows %v at address %#x give no native value", v.name, got, other.id)
			}
			atOther := map[string]bool{}
			for _, row := range MonitorTable(other.id).Native {
				if row.At.Identifier == other.id {
					atOther[row.ID] = true
				}
			}
			for id := range otherEvals {
				if !atOther[id] || !own[strings.TrimPrefix(id, MonitorProfile+".")] {
					t.Fatalf("%s: row %s is not a row of its family at address %#x", v.name, id, other.id)
				}
			}
		}
		for _, row := range MonitorTable(v.id).Native {
			if row.At.Identifier != v.id {
				t.Fatalf("%s: row %s of address %#x", v.name, row.ID, row.At.Identifier)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no monitor frame was checked")
	}
}

// TestMonitorRowWhoseSelectorMissesGivesNoValue pins the native-value check of the
// monitor family test. The rows of each admitted monitor frame are moved to raw
// DLC 9 in a copy of the live table, so no row selects the frame. The evaluation
// keeps every row ID, as the count check of the family test sees, and no row
// gives a native value, which the unmatched-row check must report.
func TestMonitorRowWhoseSelectorMissesGivesNoValue(t *testing.T) {
	roles := storageRoles(t)
	checked := 0
	for _, v := range readSelectorVectors(t) {
		if v.profile != "monitor" || v.frame != "admitted" {
			continue
		}
		table := MonitorTable(v.id)
		for i := range table.Native {
			table.Native[i].At.RawDLC = 9
		}
		evals := evaluateSelectorTable(t, v, table, roles)
		if len(evals) != len(table.Native) || len(evals) == 0 {
			t.Fatalf("%s: %d evaluations for %d rows", v.name, len(evals), len(table.Native))
		}
		if got := unmatchedRows(evals); len(got) != len(evals) {
			t.Fatalf("%s: %d of %d rows with a missed selector are reported as unmatched", v.name, len(got), len(evals))
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no monitor frame was checked")
	}
}

// TestV104AndV108FramesOfOneIdentifierFeedOnlyTheirOwnProfile feeds the same bytes
// at the same identifier to the V1.04 and the V1.08 profile. Each profile gives
// only its own rows; a frame the V1.04 gate rejects can still feed V1.08.
func TestV104AndV108FramesOfOneIdentifierFeedOnlyTheirOwnProfile(t *testing.T) {
	roles := storageRoles(t)
	cases := []struct {
		name     string
		id       uint32
		data     string
		v104Gate string
	}{
		{"shared 0x313 bytes", 0x313, "1388006400C864E2", "admitted"},
		{"shared 0x312 bytes with byte 7 zero", 0x312, "0000000001000000", "not valid"},
		{"shared 0x312 bytes with byte 7 one", 0x312, "0000000001000010", "admitted"},
	}
	for _, c := range cases {
		data, err := hex.DecodeString(c.data)
		if err != nil {
			t.Fatal(err)
		}
		frame104 := selectorFrame(c.name+" as V1.04", "v104", c.id, data)
		frame108 := selectorFrame(c.name+" as V1.08", "v108", c.id, data)
		evals4, reason4 := frame104.evaluate(t, roles)
		evals8, reason8 := frame108.evaluate(t, roles)
		if c.v104Gate == "admitted" {
			if reason4 != "" || len(evals4) == 0 {
				t.Fatalf("%s: V1.04 reason %q with %d rows", c.name, reason4, len(evals4))
			}
		} else if reason4 != roweval.Reason(c.v104Gate) || evals4 != nil {
			t.Fatalf("%s: V1.04 reason %q, want %q", c.name, reason4, c.v104Gate)
		}
		if reason8 != "" || len(evals8) == 0 {
			t.Fatalf("%s: V1.08 reason %q with %d rows", c.name, reason8, len(evals8))
		}
		for id := range evals4 {
			if !strings.HasPrefix(id, Profile+".") {
				t.Fatalf("%s: V1.04 gave row %s of another profile", c.name, id)
			}
		}
		for id := range evals8 {
			if !strings.HasPrefix(id, V108Profile+".") {
				t.Fatalf("%s: V1.08 gave row %s of another profile", c.name, id)
			}
		}
	}
}

// TestCommonSelectionFeedsOnlyCommonRows pins that a selected common projection
// gives exactly the common rows of its frame for its selected scope, and that
// the V1.04 rows of the same identifier are not evaluated under it.
func TestCommonSelectionFeedsOnlyCommonRows(t *testing.T) {
	roles := storageRoles(t)
	checked := 0
	for _, v := range readSelectorVectors(t) {
		if v.profile != "common" || v.frame != "admitted" {
			continue
		}
		evals, reason := v.evaluate(t, roles)
		if reason != "" {
			t.Fatalf("%s: reason %q", v.name, reason)
		}
		expected := map[string]bool{}
		for _, r := range CommonTable(v.scope).Native {
			if r.At.Identifier == v.id {
				expected[r.ID] = true
			}
		}
		if len(evals) != len(expected) {
			t.Fatalf("%s: %d rows, the common projection has %d at %#x", v.name, len(evals), len(expected), v.id)
		}
		for id := range evals {
			if !expected[id] {
				t.Fatalf("%s: row %s is not a common row", v.name, id)
			}
		}
		// The V1.04 profile at the same identifier and bytes gives its own rows only.
		frame104 := selectorFrame(v.name+" as V1.04", "v104", v.id, v.data)
		evals4, reason4 := frame104.evaluate(t, roles)
		if reason4 == "" {
			for id := range evals4 {
				if strings.HasPrefix(id, CommonProfile+".") {
					t.Fatalf("%s: V1.04 gave common row %s", v.name, id)
				}
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no common frame was checked")
	}
	if _, reason := selectorByName(t, "common-0x313-unknown-scope-has-no-rows").evaluate(t, roles); reason != roweval.ReasonNoMatch {
		t.Fatalf("unknown scope gave reason %q, want no match", reason)
	}
}

// TestPackAndCellCountGateAdmits254AndRejectsZeroAndTwoHundredFiftyFive pins the
// count admission: 1 through 254 is admitted and 0 and 255 are not. V1.04 gates
// both count bytes of 0x312 (pack count in byte 4, total cell count in byte 7);
// V1.08 gates the pack count in byte 4 of 0x312 and the total cell count in byte
// 0 of 0x323, and byte 7 of 0x312 is not gated.
func TestPackAndCellCountGateAdmits254AndRejectsZeroAndTwoHundredFiftyFive(t *testing.T) {
	roles := storageRoles(t)
	values := []uint8{0, 1, 128, 254, 255}
	admitted := func(n uint8) bool { return n >= 1 && n <= 254 }
	rejected := func(name string, v selectorVector) selectorVector {
		v.name, v.frame, v.reason = name, "rejected", "not valid"
		return v
	}
	for _, pack := range values {
		for _, total := range values {
			data := []byte{0, 0, 0, 0, pack, 0, 0, total}
			frame104 := selectorFrame(fmt.Sprintf("V1.04 0x312 pack %d total %d", pack, total), "v104", 0x312, data)
			if admitted(pack) && admitted(total) {
				frame104.frame, frame104.native = "admitted", map[string]int64{"pack_count": int64(pack), "total_cell_count": int64(total)}
			} else {
				frame104 = rejected(frame104.name, frame104)
			}
			evals, reason := frame104.evaluate(t, roles)
			checkSelectorOutcome(t, frame104, evals, reason)

			frame108 := selectorFrame(fmt.Sprintf("V1.08 0x312 pack %d byte 7 %d", pack, total), "v108", 0x312, data)
			if admitted(pack) {
				frame108.frame, frame108.native = "admitted", map[string]int64{"pack_count": int64(pack)}
			} else {
				frame108 = rejected(frame108.name, frame108)
			}
			evals, reason = frame108.evaluate(t, roles)
			checkSelectorOutcome(t, frame108, evals, reason)
		}
	}
	for _, n := range values {
		v := selectorFrame(fmt.Sprintf("V1.08 0x323 total cell count %d", n), "v108", 0x323, []byte{n, 0, 0, 0, 0, 0, 0, 0})
		if admitted(n) {
			v.frame, v.native = "admitted", map[string]int64{"total_cell_count": int64(n)}
		} else {
			v = rejected(v.name, v)
		}
		evals, reason := v.evaluate(t, roles)
		checkSelectorOutcome(t, v, evals, reason)
	}
}

// TestV108StatusWordBitsTenAndElevenStayRaw pins that bits 10 and 11 of the V1.08
// status word are reserved: a frame with only those bits set keeps the raw word,
// and every other row gives the value it gives with those bits cleared.
func TestV108StatusWordBitsTenAndElevenStayRaw(t *testing.T) {
	roles := storageRoles(t)
	cleared := selectorByName(t, "v108-0x311-limits-and-discharging-state")
	set := selectorByName(t, "v108-0x311-bits-10-and-11-set")
	clearedEvals, reasonC := cleared.evaluate(t, roles)
	setEvals, reasonS := set.evaluate(t, roles)
	if reasonC != "" || reasonS != "" {
		t.Fatalf("reasons %q and %q", reasonC, reasonS)
	}
	if len(clearedEvals) != len(setEvals) {
		t.Fatalf("%d rows with bits cleared and %d with bits 10 and 11 set", len(clearedEvals), len(setEvals))
	}
	word := V108Profile + ".status_word"
	for id, ev := range clearedEvals {
		other, ok := setEvals[id]
		if !ok {
			t.Fatalf("row %s is missing with bits 10 and 11 set", id)
		}
		if id == word {
			continue
		}
		if nativeKey(ev) != nativeKey(other) {
			t.Fatalf("row %s differs with bits 10 and 11 set: %s and %s", id, nativeKey(ev), nativeKey(other))
		}
	}
	if !nativeEquals(clearedEvals[word], 3) || !nativeEquals(setEvals[word], 3075) {
		t.Fatalf("status word raw %+v and %+v, want 3 and 3075 (0x0C03)", clearedEvals[word].Native, setEvals[word].Native)
	}
	// Only bits 10 and 11 set (0x0C00): the raw word stays 3072 and every row,
	// the two-bit operating state included, gives the value the all-clear word
	// 0x0000 gives.
	onlyTop := selectorByName(t, "v108-0x311-bits-10-and-11-only")
	allClear := selectorByName(t, "v108-0x311-status-all-clear")
	topEvals, reasonT := onlyTop.evaluate(t, roles)
	clearEvals, reasonA := allClear.evaluate(t, roles)
	if reasonT != "" || reasonA != "" {
		t.Fatalf("reasons %q and %q", reasonT, reasonA)
	}
	if len(topEvals) != len(clearEvals) {
		t.Fatalf("%d rows with only bits 10 and 11 set and %d with the word cleared", len(topEvals), len(clearEvals))
	}
	for id, ev := range clearEvals {
		other, ok := topEvals[id]
		if !ok {
			t.Fatalf("row %s is missing with only bits 10 and 11 set", id)
		}
		if id == word {
			continue
		}
		if nativeKey(ev) != nativeKey(other) {
			t.Fatalf("row %s differs with only bits 10 and 11 set: %s and %s", id, nativeKey(other), nativeKey(ev))
		}
	}
	if !nativeEquals(topEvals[word], 3072) || !nativeEquals(clearEvals[word], 0) {
		t.Fatalf("status word raw %+v and %+v, want 3072 (0x0C00) and 0", topEvals[word].Native, clearEvals[word].Native)
	}
	// Bits 10 and 11 of the big-endian status word are bits 2 and 3 of byte 6
	// (frame offsets 50 and 51). Only the raw word row may read them.
	for _, row := range V108Table().Native {
		if row.At.Identifier != 0x311 || row.ID == word {
			continue
		}
		start, end := row.At.Slice.offset, row.At.Slice.offset+row.At.Slice.width
		if start <= 50 && end > 50 || start <= 51 && end > 51 {
			t.Fatalf("row %s reads the reserved bits 10 and 11", row.ID)
		}
	}
}

// TestAbsentOptionalCellFrameGivesNoValue pins that an absent optional cell frame
// gives no value: with the frame of cells 1 to 4 (0x315) absent from the stream,
// no cell 1 to 4 value is evaluated for either profile, and the other cells keep
// their values.
func TestAbsentOptionalCellFrameGivesNoValue(t *testing.T) {
	roles := storageRoles(t)
	for _, profile := range []string{"v104", "v108"} {
		prefix := selectorProfilePrefixes[profile]
		seen := map[string]bool{}
		for _, v := range readSelectorVectors(t) {
			if v.profile != profile || v.frame != "admitted" || v.id == 0x315 {
				continue
			}
			evals, reason := v.evaluate(t, roles)
			if reason != "" {
				t.Fatalf("%s: reason %q", v.name, reason)
			}
			for id := range evals {
				seen[id] = true
			}
		}
		for cell := 1; cell <= 4; cell++ {
			if seen[fmt.Sprintf("%scell_voltage_%d", prefix, cell)] {
				t.Fatalf("%s: cell %d gives a value with its frame absent", profile, cell)
			}
		}
		for cell := 5; cell <= 16; cell++ {
			if !seen[fmt.Sprintf("%scell_voltage_%d", prefix, cell)] {
				t.Fatalf("%s: cell %d gives no value with its frame present", profile, cell)
			}
		}
	}
}

// TestV108ParallelFailureBitIsNamedOnlyInV108 pins that byte 1 bit 1 of 0x312
// alone gives the named parallel-failure row in V1.08 and no named row in V1.04:
// the V1.04 table reserves that bit, so the frame activates no named V1.04 row.
func TestV108ParallelFailureBitIsNamedOnlyInV108(t *testing.T) {
	roles := storageRoles(t)
	frame108 := selectorByName(t, "v108-0x312-byte-1-bit-1-parallel-failure")
	evals8, reason8 := frame108.evaluate(t, roles)
	checkSelectorOutcome(t, frame108, evals8, reason8)

	frame104 := selectorByName(t, "v104-0x312-byte-1-bit-1-is-reserved")
	evals4, reason4 := frame104.evaluate(t, roles)
	checkSelectorOutcome(t, frame104, evals4, reason4)
	for id, ev := range evals4 {
		name := strings.TrimPrefix(id, Profile+".")
		if strings.Contains(name, "parallel") {
			t.Fatalf("V1.04 names the parallel bit with row %s", id)
		}
		if strings.HasPrefix(name, "protection_") && name != "protection_flags" && ev.Native != nil && !nativeEquals(ev, 0) {
			t.Fatalf("V1.04 row %s is active on the frame of the parallel bit alone", id)
		}
	}
	for _, row := range V104Table().Native {
		if row.At.Identifier == 0x312 && row.At.Slice.offset == 9 {
			t.Fatalf("V1.04 has a named row at byte 1 bit 1: %s", row.ID)
		}
	}
	found := false
	for _, row := range V108Table().Native {
		if row.ID == V108Profile+".protection_parallel_failure" && row.At.Identifier == 0x312 && row.At.Slice.offset == 9 {
			found = true
		}
	}
	if !found {
		t.Fatal("V1.08 has no named row at byte 1 bit 1 of 0x312")
	}
}

// TestEveryNativeRowIsNeededByAnIndependentVector removes each native row that
// the vectors declare, one at a time, from the live table, and requires a vector
// to fail. The inventory is the row list each admitted vector declares, not the
// live table: a declared row the live table no longer has is an error, and a
// row removed from the table must change the rows an admitted vector evaluates.
// The live table must also give, for every admitted vector, exactly the rows the
// vector declares, so a row the table adds, or evaluates on another identifier,
// is caught as well.
func TestEveryNativeRowIsNeededByAnIndependentVector(t *testing.T) {
	roles := storageRoles(t)
	vectors := readSelectorVectors(t)
	for base := range monitorLayouts {
		covered := false
		for _, v := range vectors {
			if b, ok := monitorBase(v.id); v.profile == "monitor" && ok && b == base && v.frame == "admitted" {
				covered = true
			}
		}
		if !covered {
			t.Errorf("monitor family %#x has no admitted vector", base)
		}
	}
	removed := 0
	live := map[string]bool{}
	witnessed := map[string]bool{}
	for _, v := range vectors {
		if v.frame != "admitted" {
			continue
		}
		prefix := selectorProfilePrefixes[v.profile]
		full := fullSelectorTable(v.profile, v.scope, v.id)
		fullEvals := evaluateSelectorTable(t, v, full, roles)
		if got := rowNamesOf(v.profile, fullEvals); !sameNames(got, v.rows) {
			t.Fatalf("%s: the live table evaluates rows %v, the frame declares %v", v.name, got, v.rows)
		}
		for _, name := range v.rows {
			id := prefix + name
			found := false
			for _, row := range full.Native {
				found = found || row.ID == id
			}
			if !found {
				t.Errorf("%s: declared row %s is missing from the live table", v.name, id)
			}
			if !rowMatches(fullEvals, id) {
				t.Errorf("%s: declared row %s gives no value on its admitted frame", v.name, id)
			}
		}
		// Every live definition of the table is removed alone, by its position:
		// a row ID may stand for several definitions under different layout
		// conditions, and removing one of them must not take the others with it.
		for i, row := range full.Native {
			key := definitionKey(v.profile, row)
			live[key] = true
			reduced := full
			reduced.Native = make([]roweval.NativeRow[Selector], 0, len(full.Native)-1)
			reduced.Native = append(reduced.Native, full.Native[:i]...)
			reduced.Native = append(reduced.Native, full.Native[i+1:]...)
			// A witness must be a row that matches this admitted frame. A selector
			// miss keeps its key in the evaluation map, so the key alone is not
			// evidence that the removed row covers the frame.
			if rowMatches(fullEvals, row.ID) && !sameNames(rowNamesOf(v.profile, evaluateSelectorTable(t, v, reduced, roles)), v.rows) {
				witnessed[key] = true
			}
			removed++
		}
	}
	for key := range live {
		if !witnessed[key] {
			t.Errorf("no admitted vector fails when the live native row definition %s is removed", key)
		}
	}
	if removed == 0 {
		t.Fatal("no native row was removed")
	}
	t.Logf("%d declared native rows removed in turn; %d admitted vectors", removed, admittedVectors(vectors))
}

// definitionKey identifies one native row definition of a table: the profile,
// the row ID, the identifier, slice and layout condition the definition carries.
func definitionKey(profile string, row roweval.NativeRow[Selector]) string {
	return fmt.Sprintf("%s|%s|%+v|%+v", profile, row.ID, row.At, row.When)
}

// admittedVectors counts the admitted frames of the file.
func admittedVectors(vectors []selectorVector) int {
	n := 0
	for _, v := range vectors {
		if v.frame == "admitted" {
			n++
		}
	}
	return n
}

// admittedStandardIdentifiers returns the standard identifiers that the admitted
// vectors of one profile carry. An adjacent identifier in this set is another
// frame of the profile and is not a negative case.
func admittedStandardIdentifiers(t *testing.T, profile string) map[uint32]bool {
	t.Helper()
	ids := map[uint32]bool{}
	for _, v := range readSelectorVectors(t) {
		if v.frame == "admitted" && v.profile == profile && !v.extended {
			ids[v.id] = true
		}
	}
	return ids
}

// TestDeletionWitnessNeedsAFrameThatMatchesTheRow pins the witness rule of the
// deletion test: a row whose raw-DLC selector no longer matches an admitted
// frame keeps its key in the evaluation, but it gives no value there and cannot
// witness its own removal. The V1.08 reserved byte 7 row is moved to raw DLC 9
// in a copy of the live table; the admitted vector that declares it must then
// stop giving it a value.
func TestDeletionWitnessNeedsAFrameThatMatchesTheRow(t *testing.T) {
	roles := storageRoles(t)
	const name = "v108-0x312-pack-count-1-and-byte-7-not-gated"
	var vector selectorVector
	found := false
	for _, v := range readSelectorVectors(t) {
		if v.name == name {
			vector, found = v, true
		}
	}
	if !found {
		t.Fatalf("vector %s is not in the vector file", name)
	}
	id := V108Profile + ".reserved_byte_7"
	live := V108Table()
	if !rowMatches(evaluateSelectorTable(t, vector, live, roles), id) {
		t.Fatalf("%s: the live row %s gives no value on its admitted frame", name, id)
	}
	mutated := V108Table()
	mutated.Native = append([]roweval.NativeRow[Selector](nil), mutated.Native...)
	moved := 0
	for i := range mutated.Native {
		if mutated.Native[i].ID == id {
			mutated.Native[i].At.RawDLC = 9
			moved++
		}
	}
	if moved != 1 {
		t.Fatalf("expected one live definition of %s, found %d", id, moved)
	}
	if rowMatches(evaluateSelectorTable(t, vector, mutated, roles), id) {
		t.Fatalf("%s: the row with raw DLC 9 still gives a value on a raw DLC 8 frame", name)
	}
}
