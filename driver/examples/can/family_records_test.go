package can

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/catalog"
)

func TestDriverDeclaredCANFamilyRecords(t *testing.T) {
	for _, tc := range []struct{ id, pack string }{
		{"gree-vrf-can", "helianthus.pack.thermal@1.0.0"},
		{"growatt-bms-can-v104", "helianthus.pack.storage@1.1.0"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			path := filepath.Join("driverdata", "families", tc.id+".json")
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", path))
			if err != nil {
				t.Fatal(err)
			}
			f, err := decodeFamily(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Validate(); err != nil {
				t.Fatal(err)
			}
			if f.FamilyID != tc.id || f.State != "qualified" || f.SemanticPack != tc.pack || f.Reason != "declared_by_specification" || f.Composition != "not_composed" || f.CompositionVerified || f.Module != "" || f.Version != "" || f.Verification != "not_tested_on_hardware" || len(f.Evidence) == 0 {
				t.Fatalf("unexpected declaration from %s: %+v", path, f)
			}
			t.Logf("%s: %s, %s, %s", path, f.State, f.Composition, f.Verification)
			for _, mutation := range []struct{ old, replacement, message string }{
				{`"state":`, `"state":"unknown","state":`, `duplicate member`},
				{`"state":`, `"STATE":"unknown","state":`, `duplicate member`},
				{`"statement":`, `"ſtatement":"shadowed","statement":`, `duplicate member`},
				{`"state":`, `"unknown_member":0,"state":`, `unknown field`},
			} {
				changed := bytes.Replace(raw, []byte(mutation.old), []byte(mutation.replacement), 1)
				if _, err := decodeFamily(changed); err == nil || !strings.Contains(err.Error(), mutation.message) {
					t.Fatalf("%s: %v", mutation.replacement, err)
				}
				t.Logf("%s: %s", mutation.replacement, mutation.message)
			}
			if _, err := decodeFamily(append(bytes.Clone(raw), []byte(" {}")...)); err == nil {
				t.Error("trailing object accepted")
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(raw, &members); err != nil {
				t.Fatal(err)
			}
			delete(members, "verification")
			changed, err := json.Marshal(members)
			if err != nil {
				t.Fatal(err)
			}
			without, err := decodeFamily(changed)
			if err != nil {
				t.Fatalf("mutation must pass the raw and typed decode: %v", err)
			}
			if err := without.Validate(); err == nil || !strings.Contains(err.Error(), "verification") {
				t.Fatalf("missing verification: %v", err)
			}
			t.Log("missing verification rejected by Family.Validate")
		})
	}
}

func decodeFamily(raw []byte) (catalog.Family, error) {
	var f catalog.Family
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return f, fmt.Errorf("family must be an object")
	}
	if err := checkFamilyMembers(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return f, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, err
	}
	if _, err := d.Token(); err != io.EOF {
		return f, fmt.Errorf("trailing family tokens")
	}
	return f, nil
}

// Like the catalog's raw pass, walk objects (including those in arrays) before
// Go can overwrite a field. Fold the same Unicode equivalences as encoding/json.
func checkFamilyMembers(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for d.More() {
			name, err := d.Token()
			if err != nil {
				return err
			}
			folded := strings.Map(familyFold, name.(string))
			if seen[folded] {
				return fmt.Errorf("duplicate member %q", name)
			}
			seen[folded] = true
			if err := checkFamilyMembers(d); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for d.More() {
			if err := checkFamilyMembers(d); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = d.Token()
	return err
}

func familyFold(r rune) rune {
	for next := unicode.SimpleFold(r); next > r; next = unicode.SimpleFold(r) {
		r = next
	}
	return unicode.SimpleFold(r)
}
