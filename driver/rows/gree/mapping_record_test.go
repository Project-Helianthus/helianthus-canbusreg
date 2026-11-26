package gree

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

type mappingRecord struct {
	ID            string `json:"id"`
	NativeOwner   string `json:"native_owner"`
	State         string `json:"state"`
	Qualification string `json:"qualification"`
	Loss          string `json:"loss"`
}

func TestMappingRecordIsCurrentAndDeclaredFromTheSpecification(t *testing.T) {
	var family catalog.Family
	if err := decodeMappingDeclaration(readDeclaration(t, "gree-vrf-can.json"), &family); err != nil {
		t.Fatal(err)
	}
	if err := family.Validate(); err != nil {
		t.Fatal(err)
	}
	if family.FamilyID != "gree-vrf-can" || family.State != "qualified" || family.Reason != "declared_by_specification" || family.Verification != "not_tested_on_hardware" {
		t.Fatalf("unexpected family declaration: id=%q state=%q reason=%q verification=%q", family.FamilyID, family.State, family.Reason, family.Verification)
	}

	var got mappingRecord
	if err := decodeMappingDeclaration(readDeclaration(t, "candidate-mappings", "thermal.mapping.gree.vrf.json"), &got); err != nil {
		t.Fatal(err)
	}
	want := mappingRecord{
		ID: "thermal.mapping.gree.vrf", NativeOwner: "helianthus-docs-canbus",
		State: string(family.State), Qualification: string(family.Reason), Loss: "unknown",
	}
	if got != want {
		t.Fatalf("mapping declaration = %+v; want %+v", got, want)
	}
}

func TestMappingRecordRejectsDuplicateAndUnknownMembers(t *testing.T) {
	raw := readDeclaration(t, "candidate-mappings", "thermal.mapping.gree.vrf.json")
	for _, tc := range []struct {
		name, member, message string
	}{
		{"duplicate", `"state":"candidate"`, "duplicate member"},
		{"case alias", `"STATE":"candidate"`, "duplicate member"},
		{"Unicode alias", `"ſtate":"candidate"`, "duplicate member"},
		{"unknown member", `"unknown_member":0`, "unknown field"},
		{"trailing object", "", "trailing tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed []byte
			if tc.member == "" {
				changed = append(bytes.Clone(raw), []byte(" {}")...)
			} else {
				changed = bytes.Replace(raw, []byte(`"state":`), []byte(tc.member+`,"state":`), 1)
			}
			var record mappingRecord
			if err := decodeMappingDeclaration(changed, &record); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("decode error = %v; want %q", err, tc.message)
			}
		})
	}
}

func readDeclaration(t *testing.T, path ...string) []byte {
	t.Helper()
	name := filepath.Join(append([]string{"..", "..", "..", "driverdata", "families"}, path...)...)
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Match the raw and typed passes in the driver family record test.
func decodeMappingDeclaration(raw []byte, dst any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return fmt.Errorf("declaration must be an object")
	}
	if err := checkDeclarationMembers(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing tokens in declaration")
	}
	return nil
}

func checkDeclarationMembers(d *json.Decoder) error {
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
			folded := strings.Map(declarationFold, name.(string))
			if seen[folded] {
				return fmt.Errorf("duplicate member %q", name)
			}
			seen[folded] = true
			if err := checkDeclarationMembers(d); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for d.More() {
			if err := checkDeclarationMembers(d); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = d.Token()
	return err
}

func declarationFold(r rune) rune {
	for next := unicode.SimpleFold(r); next > r; next = unicode.SimpleFold(r) {
		r = next
	}
	return unicode.SimpleFold(r)
}
