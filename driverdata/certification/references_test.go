package certification

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadValid(t *testing.T) *Document {
	t.Helper()
	document, err := Load(readFixture(t, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func mutated(t *testing.T, change func(root map[string]any, entries []any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(readFixture(t, "valid.json"), &root); err != nil {
		t.Fatal(err)
	}
	change(root, root["entries"].([]any))
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func entryAt(entries []any, index int) map[string]any { return entries[index].(map[string]any) }

func expectFinding(t *testing.T, data []byte, reason string, entry int, member string) {
	t.Helper()
	_, err := Load(data)
	var got *Finding
	if !errors.As(err, &got) {
		t.Fatalf("want finding %s, got %v", reason, err)
	}
	if got.Reason != reason || got.Entry != entry || got.Member != member {
		t.Fatalf("got %+v, want %s entry=%d member=%q", *got, reason, entry, member)
	}
}

func TestValidFileLoadsAllThreeCategories(t *testing.T) {
	document := loadValid(t)
	counts := map[string]int{}
	for _, entry := range document.Entries {
		counts[entry.Category]++
		if entry.Source != SourceCertification {
			t.Fatalf("source %q", entry.Source)
		}
	}
	want := map[string]int{CategoryReference: 3, CategoryGap: 1, CategoryUnavailable: 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts %v", counts)
	}
	first := document.Entries[0]
	if first.StatedValue == nil || first.StatedValue.RatioMaximum == nil ||
		first.StatedValue.RatioMaximum.Rat().FloatString(2) != "0.01" || first.StatedValue.Measured != nil {
		t.Fatalf("stated value %+v", first.StatedValue)
	}
	if document.Entries[3].StatedValue != nil || document.Entries[4].Refusal == "" {
		t.Fatal("gap carries a value or unavailable lost its refusal")
	}
}

func TestEmbeddedPackageHoldsNoDataEntries(t *testing.T) {
	registry, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Documents) != 0 {
		t.Fatalf("data files %d", len(registry.Documents))
	}
	if got := registry.Lookup(Query{Driver: "example-driver"}); len(got) != 0 {
		t.Fatalf("matches %v", got)
	}
}

func TestEveryMemberRemovalOfEveryEntryIsRejected(t *testing.T) {
	var base map[string]any
	if err := json.Unmarshal(readFixture(t, "valid.json"), &base); err != nil {
		t.Fatal(err)
	}
	for index, raw := range base["entries"].([]any) {
		for name := range raw.(map[string]any) {
			data := mutated(t, func(_ map[string]any, entries []any) { delete(entryAt(entries, index), name) })
			if _, err := Load(data); err == nil {
				t.Fatalf("entry %d without %s accepted", index, name)
			}
		}
		data := mutated(t, func(_ map[string]any, entries []any) { entryAt(entries, index)["source"] = "device" })
		expectFinding(t, data, ReasonSourceNotCertification, index, "source")
	}
}

func TestRejectionReasonsAreStable(t *testing.T) {
	cases := []struct {
		name   string
		change func(root map[string]any, entries []any)
		reason string
		entry  int
		member string
	}{
		{"device published member", func(_ map[string]any, e []any) { entryAt(e, 0)["measured_value"] = 1 }, ReasonUnknownMember, 0, "measured_value"},
		{"gap with stated value", func(_ map[string]any, e []any) {
			entryAt(e, 3)["stated_value"] = entryAt(e, 0)["stated_value"]
		}, ReasonStatedValueNotAllowed, 3, "stated_value"},
		{"unavailable with stated value", func(_ map[string]any, e []any) {
			entryAt(e, 4)["stated_value"] = entryAt(e, 0)["stated_value"]
		}, ReasonStatedValueNotAllowed, 4, "stated_value"},
		{"unavailable without refusal text", func(_ map[string]any, e []any) { entryAt(e, 4)["refusal"] = "" }, ReasonRefusalMissing, 4, "refusal"},
		{"reference with refusal", func(_ map[string]any, e []any) { entryAt(e, 0)["refusal"] = "x" }, ReasonUnknownMember, 0, "refusal"},
		{"unknown category", func(_ map[string]any, e []any) { entryAt(e, 0)["category"] = "default" }, ReasonInvalidValue, 0, "category"},
		{"empty text", func(_ map[string]any, e []any) { entryAt(e, 1)["model"] = "" }, ReasonInvalidValue, 1, "model"},
		{"impossible date", func(_ map[string]any, e []any) { entryAt(e, 0)["issue_date"] = "2001-02-30" }, ReasonInvalidValue, 0, "issue_date"},
		{"plain http address", func(_ map[string]any, e []any) { entryAt(e, 0)["url"] = "http://example.invalid/x" }, ReasonInvalidValue, 0, "url"},
		{"unknown firmware form", func(_ map[string]any, e []any) { entryAt(e, 0)["firmware"] = map[string]any{"form": "ceiling"} }, ReasonFirmwareInvalid, 0, "firmware"},
		{"release of another form", func(_ map[string]any, e []any) {
			entryAt(e, 0)["firmware"] = map[string]any{"form": "release", "release": "v1.4"}
		}, ReasonFirmwareInvalid, 0, "firmware"},
		{"range from above to", func(_ map[string]any, e []any) {
			entryAt(e, 1)["firmware"] = map[string]any{"form": "range", "from": "2.6", "to": "2.5", "upper_bound_stated_by": "certificate"}
		}, ReasonFirmwareInvalid, 1, "firmware"},
		{"range upper bound stated by nobody", func(_ map[string]any, e []any) {
			entryAt(e, 1)["firmware"] = map[string]any{"form": "range", "from": "2.0", "to": "2.5"}
		}, ReasonFirmwareUpperUnstated, 1, "firmware"},
		{"stated value only names the type", func(_ map[string]any, e []any) {
			entryAt(e, 0)["stated_value"] = map[string]any{"measurement_type": "active_power"}
		}, ReasonStatedValueInvalid, 0, "stated_value"},
		{"null stated member", func(_ map[string]any, e []any) {
			entryAt(e, 0)["stated_value"] = map[string]any{"measurement_type": "active_power", "measured": nil}
		}, ReasonStatedValueInvalid, 0, "stated_value"},
		{"non canonical decimal", func(_ map[string]any, e []any) {
			entryAt(e, 0)["stated_value"] = map[string]any{"measurement_type": "active_power",
				"ratio_maximum": map[string]any{"coefficient": "10", "exponent10": -3}}
		}, ReasonStatedValueInvalid, 0, "stated_value"},
		{"unknown contract identifier", func(r map[string]any, _ []any) { r["contract"] = "other" }, ReasonSchema, -1, ""},
		{"extra top level member", func(r map[string]any, _ []any) { r["note"] = "x" }, ReasonSchema, -1, ""},
		{"no entries", func(r map[string]any, _ []any) { r["entries"] = []any{} }, ReasonSchema, -1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectFinding(t, mutated(t, c.change), c.reason, c.entry, c.member)
		})
	}
}

func TestRepeatedMemberNameIsRejected(t *testing.T) {
	data := []byte(`{"contract":"helianthus.semreg.certification-references/v1","contract":"x","entries":[]}`)
	expectFinding(t, data, ReasonSchema, -1, "")
}

func TestTwoCategoriesForOneIdentityAreRejected(t *testing.T) {
	expectFinding(t, readFixture(t, "category_conflict.json"), ReasonCategoryConflict, 1, "")
}

func TestEveryIdentityFieldSeparatesCategories(t *testing.T) {
	for _, field := range []string{"driver", "vendor", "flavor", "model", "firmware"} {
		t.Run(field, func(t *testing.T) {
			data := mutated(t, func(_ map[string]any, e []any) {
				conflict := entryAt(e, 3)
				source := entryAt(e, 0)
				for _, name := range []string{"driver", "vendor", "flavor", "model", "firmware"} {
					conflict[name] = source[name]
				}
				if field == "firmware" {
					conflict["firmware"] = map[string]any{"form": "release", "release": "9.9"}
				} else {
					conflict[field] = "other"
				}
			})
			if _, err := Load(data); err != nil {
				t.Fatalf("differing %s should not conflict: %v", field, err)
			}
		})
	}
}

func TestTwoEntriesOfOneCategoryForOneIdentityAreAllowed(t *testing.T) {
	data := mutated(t, func(_ map[string]any, e []any) {
		second := map[string]any{}
		for k, v := range entryAt(e, 0) {
			second[k] = v
		}
		second["certificate_number"] = "EX-CERT-0009"
		e[1] = second
	})
	if _, err := Load(data); err != nil {
		t.Fatal(err)
	}
}

func TestCertifiedReleaseAppliesToLaterReleaseNotEarlier(t *testing.T) {
	document := loadValid(t)
	base := Query{Driver: "example-driver", Vendor: "Example Vendor", Flavor: "example-profile", Model: "EX-1000"}
	for release, want := range map[string]int{"1.3.9": 0, "1.4": 1, "1.4.0": 1, "1.4.1": 1, "1.10": 1, "2.0": 1, "": 0, "v1.5": 0} {
		query := base
		query.Release = release
		got := document.Lookup(query)
		if len(got) != want {
			t.Fatalf("release %q: %d entries, want %d", release, len(got), want)
		}
		if want == 1 && !reflect.DeepEqual(got[0], document.Entries[0]) {
			t.Fatalf("release %q: entry changed", release)
		}
	}
}

func TestRangeCoversItsBoundsAndNothingOutside(t *testing.T) {
	firmware := Firmware{Form: FormRange, From: "2.0", To: "2.5", UpperBoundStatedBy: "certificate"}
	for release, want := range map[string]string{"1.9": NotCovered, "2.0": Covered, "2.5": Covered, "2.5.0": Covered, "2.5.1": NotCovered, "3": NotCovered} {
		if got := Coverage(firmware, release); got != want {
			t.Fatalf("release %s: %s, want %s", release, got, want)
		}
	}
	if got := Coverage(Firmware{Form: FormNotStated}, "1.0"); got != NotStated {
		t.Fatal(got)
	}
}

func TestNotStatedFirmwareAppliesToTheModelForEveryRelease(t *testing.T) {
	document := loadValid(t)
	query := Query{Driver: "example-driver", Vendor: "Example Vendor", Flavor: "example-profile", Model: "EX-1200"}
	for _, release := range []string{"", "0.1", "9.9"} {
		query.Release = release
		got := document.Lookup(query)
		if len(got) != 1 || got[0].Firmware.Form != FormNotStated || got[0].StatedValue == nil {
			t.Fatalf("release %q: %+v", release, got)
		}
	}
}

func TestGapAndUnavailableEntriesAreReturnedUnchanged(t *testing.T) {
	document := loadValid(t)
	gap := document.Lookup(Query{Driver: "example-driver", Vendor: "Example Vendor", Flavor: "example-profile", Model: "EX-2000", Release: "5.0"})
	if len(gap) != 1 || !reflect.DeepEqual(gap[0], document.Entries[3]) || gap[0].Category != CategoryGap || gap[0].StatedValue != nil {
		t.Fatalf("gap %+v", gap)
	}
	query := Query{Driver: "example-driver", Vendor: "Example Vendor", Flavor: "example-profile", Model: "EX-3000"}
	query.Release = "3.2"
	unavailable := document.Lookup(query)
	if len(unavailable) != 1 || !reflect.DeepEqual(unavailable[0], document.Entries[4]) || unavailable[0].Refusal == "" {
		t.Fatalf("unavailable %+v", unavailable)
	}
	query.Release = "3.0"
	if got := document.Lookup(query); len(got) != 0 {
		t.Fatalf("release below the floor matched %v", got)
	}
}

func TestEveryQueryFieldSelectsTheEntry(t *testing.T) {
	document := loadValid(t)
	full := Query{Driver: "example-driver", Vendor: "Example Vendor", Flavor: "example-profile", Model: "EX-1000", Release: "1.4.0"}
	if len(document.Lookup(full)) != 1 {
		t.Fatal("full query must match")
	}
	for name, change := range map[string]func(q *Query){
		"driver": func(q *Query) { q.Driver = "other" },
		"vendor": func(q *Query) { q.Vendor = "other" },
		"flavor": func(q *Query) { q.Flavor = "other" },
		"model":  func(q *Query) { q.Model = "other" },
	} {
		query := full
		change(&query)
		if got := document.Lookup(query); len(got) != 0 {
			t.Fatalf("%s: %v", name, got)
		}
	}
}
