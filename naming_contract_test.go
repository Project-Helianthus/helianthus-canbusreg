package canbusreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func namingNoAliases(t *testing.T, r CXNativeResult) {
	t.Helper()
	for _, old := range []string{"maximum_temperature", "minimum_temperature"} {
		if _, ok := r.Fields[old]; ok {
			t.Fatalf("historical alias emitted: %s", old)
		}
	}
}

func TestNamingSupportedTemperatureKeys(t *testing.T) {
	c := selectionTestSpec(t, "QAAA", "0006")
	link := CXDecodeLINKTelemetry(selectionTestSelect(t, c), selectionTestInput(t, c, 0x1808, [8]byte{2, 81, 3, 39, 40, 0, 0, 0}))
	s, i := standardTestFixture(t, 0x322, [8]byte{0xff, 0xf6, 0x80, 0, 2, 3, 0, 0})
	standard := DecodePCSStandard(s, i).CXNativeResult
	pc := parallelReleaseTestSpec(t)
	parallel := parallelReleaseTestDecode(t, pc, 0x653, [8]byte{2, 0, 81, 3, 0, 39, 40, 0}, ParallelContext{})
	for _, tc := range []struct {
		name                          string
		result                        CXNativeResult
		maximum, minimum, denominator int64
	}{
		{"link", link, 41, -1, 1}, {"standard", standard, -10, -32768, 10}, {"parallel", parallel, 41, -1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			namingNoAliases(t, tc.result)
			for name, want := range map[string]int64{"temperature_maximum": tc.maximum, "temperature_minimum": tc.minimum} {
				f, ok := tc.result.Fields[name]
				if !ok || f.Numerator != want || f.Denominator != tc.denominator || f.Unit != "C" || f.Status != "accepted" || f.PhysicalQualified || tc.result.PhysicalQualified {
					t.Fatalf("%s: %+v", name, f)
				}
			}
		})
	}
	ec := extendedTestSpec(t)
	extended := DecodePCSExtended(extendedTestSelect(t, ec), extendedTestInput(t, ec, 0x1906, [8]byte{2, 0, 81, 3, 0, 39, 40, 0}))
	namingNoAliases(t, extended)
	for name, want := range map[string]int64{"maximum_temperature_code": 81, "minimum_temperature_code": 39} {
		f := extended.Fields[name]
		if f.Numerator != want || f.Status != "raw" || f.Grade != "Unknown" || f.Unit != "" {
			t.Fatal(name, f)
		}
	}
	for _, name := range []string{"temperature_maximum", "temperature_minimum"} {
		if _, ok := extended.Fields[name]; ok {
			t.Fatal("raw code promoted", name)
		}
	}
}

func TestNamingFieldKeysAndPartialRetention(t *testing.T) {
	// Each extremum independently catches an old-key fallback in FieldStates or
	// FieldAges, a swapped key, or loss of the accepted sibling during retention.
	for _, keys := range [][2]string{{"temperature_maximum", "maximum_temperature"}, {"temperature_minimum", "minimum_temperature"}} {
		t.Run(keys[0], func(t *testing.T) {
			s, i := standardTestFixture(t, 0x322, [8]byte{0, 10, 0, 20, 2, 3, 0, 0})
			previous := DecodePCSStandard(s, i).CXNativeResult
			sibling := "temperature_minimum"
			if keys[0] == sibling {
				sibling = "temperature_maximum"
			}
			for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned"} {
				update := i
				update.FieldStates = map[string]string{keys[0]: state}
				update.FieldAges = map[string]time.Duration{keys[0]: 5 * time.Second, sibling: 2 * time.Second}
				r := DecodePCSStandard(s, update).CXNativeResult
				if r.Fields[keys[0]].Status != state || r.Fields[keys[0]].Age != 5*time.Second || r.Fields[keys[0]].Unit != "" || r.Fields[sibling].Status != "accepted" {
					t.Fatal(state, r)
				}
				merged := RetainAccepted(previous, r)
				if merged.Fields[keys[0]] != previous.Fields[keys[0]] || merged.Fields[sibling].Age != 2*time.Second {
					t.Fatal("partial retention", merged)
				}
				namingNoAliases(t, merged)
			}
			i.FieldStates = map[string]string{keys[1]: "invalid"}
			i.FieldAges = map[string]time.Duration{keys[1]: -1}
			r := DecodePCSStandard(s, i).CXNativeResult
			if r.Fields[keys[0]] != previous.Fields[keys[0]] {
				t.Fatal("old key resolved", r)
			}
			namingNoAliases(t, r)
		})
	}
}

func TestNamingRetentionIdentityCollisionAxes(t *testing.T) {
	// Each axis catches weakening CX05RetainAccepted's exact selection comparisons
	// to ones that ignore that axis while retaining the same decoded field key.
	axes := []string{"interface-name", "interface-index", "flavor", "pcs-flavor", "project", "release", "choice", "direction", "scope", "bank", "endpoint", "group", "slot", "route-set", "destination", "source", "epoch"}
	for _, surface := range []string{"selection", "member"} {
		for _, axis := range axes {
			t.Run(surface+"/"+axis, func(t *testing.T) {
				s, i := standardTestFixture(t, 0x322, [8]byte{0, 10, 0, 20, 2, 3, 0, 0})
				previous := DecodePCSStandard(s, i).CXNativeResult
				previous.Member = s
				previous.HasMember = true
				update := previous
				update.Fields = map[string]CXNativeField{}
				c := s.SelectionSpecValue()
				switch axis {
				case "interface-name":
					c.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
				case "interface-index":
					c.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
				case "flavor":
					c.Flavor = "pcs-extended19"
				case "pcs-flavor":
					c.PCSFlavor = "pcs-parallel"
				case "project":
					c.ProjectCode = "QAAB"
				case "release":
					c.Release = "0007"
				case "choice":
					c.Choice = "legacy19"
				case "direction":
					c.Direction = "other"
				case "scope":
					c.Scope = "aggregate"
				case "bank":
					c.Bank = "other"
				case "endpoint":
					c.Endpoint = "other"
				case "group":
					c.Group = "other"
				case "slot":
					c.Slot = "other"
				case "route-set":
					c.RouteSet = true
				case "destination":
					c.Destination++
				case "source":
					c.Source++
				case "epoch":
					c.Epoch++
				}
				if surface == "selection" {
					update.Selection = selectionTestSelect(t, c)
				} else {
					update.Member = selectionTestSelect(t, c)
				}
				if len(RetainAccepted(previous, update).Fields) != 0 {
					t.Fatal("cross-identity retained", axis)
				}
			})
		}
	}
	t.Run("has-member", func(t *testing.T) {
		s, i := standardTestFixture(t, 0x322, [8]byte{0, 10, 0, 20, 2, 3, 0, 0})
		previous := DecodePCSStandard(s, i).CXNativeResult
		previous.Member = s
		previous.HasMember = true
		update := previous
		update.Fields = map[string]CXNativeField{}
		update.HasMember = false
		if len(RetainAccepted(previous, update).Fields) != 0 {
			t.Fatal("member presence ignored")
		}
	})
}

func TestNamingInventoryEmittedShape(t *testing.T) {
	var inventory struct {
		SourceFiles []string `json:"source_files"`
		Rows        []struct {
			File       string `json:"owning_file"`
			Name       string `json:"current_name"`
			Historical string `json:"historical_name"`
			Target     string `json:"target"`
			Status     string `json:"status"`
		} `json:"rows"`
	}
	b, err := os.ReadFile("naming-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &inventory); err != nil {
		t.Fatal(err)
	}
	adopted := 0
	for _, r := range inventory.Rows {
		if r.Status == "adopted" {
			adopted++
			b, err := os.ReadFile(r.File)
			if err != nil {
				t.Fatal(err)
			}
			if r.Name != r.Target || !strings.Contains(string(b), `"`+r.Name+`"`) || strings.Contains(string(b), `"`+r.Historical+`"`) {
				t.Fatal("inventory does not match emitted shape", r.File, r.Name)
			}
		}
	}
	if adopted != 6 {
		t.Fatal("supported map row count", adopted)
	}
	for _, p := range inventory.SourceFiles {
		if _, err := os.Stat(filepath.Clean(p)); err != nil {
			t.Fatal("missing inventory input", p)
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, old := range []string{"maximum_temperature", "minimum_temperature"} {
			if strings.Contains(string(b), `"`+old+`"`) {
				t.Fatal("historical runtime key", p, old)
			}
		}
	}
}
