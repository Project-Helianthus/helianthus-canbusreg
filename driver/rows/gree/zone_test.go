package gree

import (
	"fmt"
	"reflect"
	"testing"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/runtime/sources"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

func TestZoneRankingUsesDeclaredScoresAndRuntimeReceipts(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	var pair [2]ReadResult
	for i := range pair {
		_, p := imagePoint(t, iface, 0x20+uint8(i), 0x20, 0, false, 121+i)
		in := readInput(p, indoor, zone)
		in.Equipment.Anchor.Slot = fmt.Sprintf("equipment-%d", i)
		in.Equipment.Producer.ID = semreg.ResourceID(fmt.Sprintf("resource.equipment.%d", i))
		in.Template.CandidateID = semreg.CandidateID(fmt.Sprintf("candidate.synthetic.%d", i))
		var err error
		pair[i], err = Evaluate(p, in)
		if err != nil || pair[i].Source == nil || pair[i].Evaluation.Candidate == nil {
			t.Fatalf("read %d: %+v %v", i, pair[i], err)
		}
		in.Priority = nil
		out, err := Evaluate(p, in)
		if err != nil || out.Source != nil || out.Missing != "declared source priority" {
			t.Fatalf("unscored read %d: %+v %v", i, out, err)
		}
	}
	for _, ranking := range []struct {
		name      string
		scores    [2]int32
		order     []int
		leader    int
		freshness semreg.Freshness
	}{
		{"highest-first", [2]int32{11, 9}, []int{0, 1}, 0, semreg.FreshnessFresh},
		{"highest-last", [2]int32{11, 9}, []int{1, 0}, 0, semreg.FreshnessFresh},
		{"equal-left-latest", [2]int32{11, 11}, []int{1, 0}, 0, semreg.FreshnessFresh},
		{"equal-right-latest", [2]int32{11, 11}, []int{0, 1}, 1, semreg.FreshnessFresh},
		{"highest-unknown", [2]int32{11, 9}, []int{1}, 0, semreg.FreshnessUnknown},
		{"highest-stale", [2]int32{11, 9}, []int{0, 1}, 0, semreg.FreshnessStale},
	} {
		t.Run(ranking.name, func(t *testing.T) {
			tick := 0
			book, err := sources.NewTable(func() semreg.MonotonicPoint {
				tick++
				return semreg.MonotonicPoint{ClockEpochID: "clock:runtime", Nanoseconds: semreg.Uint64(fmt.Sprint(tick))}
			})
			if err != nil {
				t.Fatal(err)
			}
			var values [2]driver.Source
			var refs [2]driver.SourceRef
			for i, r := range pair {
				values[i] = *r.Source
				values[i].Priority = ranking.scores[i]
				refs[i] = driver.SourceRef{For: values[i].For, Producer: values[i].Producer, PathKey: values[i].PathKey}
				if err := book.Declare(values[i]); err != nil {
					t.Fatal(err)
				}
			}
			evals := make(map[sources.SourceKey]sources.MemberEvaluation)
			for _, i := range ranking.order {
				c := *pair[i].Evaluation.Candidate
				asset := semreg.AssetID(fmt.Sprintf("asset:unit%d", i))
				member := semreg.Snapshot{AssetID: asset, Facts: []semreg.FactEnvelope{{AssetID: asset, Key: c.Key, Candidates: []semreg.FactCandidate{c}}}}
				if err := book.Admit(driver.Sample{From: values[i], Candidate: c}, member); err != nil {
					t.Fatal(err)
				}
				fresh := semreg.FreshnessFresh
				available := semreg.AvailabilityAvailable
				if i == ranking.leader && ranking.freshness == semreg.FreshnessStale {
					fresh = semreg.FreshnessStale
					available = semreg.AvailabilityDegraded
				}
				evals[sources.KeyOf(refs[i])] = sources.MemberEvaluation{Member: member, View: semreg.EvaluationView{Facts: []semreg.EvaluatedFact{{CandidateID: c.CandidateID, CandidateRevision: c.Revision, Freshness: fresh, EffectiveAvailability: available}}}}
			}
			got := book.Element(values[0].For, evals)
			if got.Resolution.Winner == nil || !reflect.DeepEqual(*got.Resolution.Winner, refs[ranking.leader]) || len(got.Resolution.Sources) != 2 || got.Freshness != ranking.freshness {
				t.Fatalf("resolution: %+v", got)
			}
			var expected *semreg.Value
			if ranking.freshness != semreg.FreshnessUnknown {
				expected = pair[ranking.leader].Evaluation.Candidate.Value
			}
			if !reflect.DeepEqual(got.Value, expected) {
				t.Fatalf("value: %+v, want %+v", got.Value, expected)
			}
			if ranking.scores[0] == ranking.scores[1] {
				found := false
				for _, f := range got.Resolution.Findings {
					if f.PriorityTie != nil && *f.PriorityTie == 11 && reflect.DeepEqual(f.First, refs[0]) && reflect.DeepEqual(f.Second, refs[1]) {
						found = true
					}
				}
				if !found {
					t.Fatalf("tie finding: %+v", got.Resolution.Findings)
				}
			}
		})
	}
}

func TestConfiguredSubjectAndSettingTargetStayDistinctAcrossLifecycles(t *testing.T) {
	iface := bus(t, "synthetic", 1)
	zoneRef := driver.NodeRef{Bridge: "bridge.synthetic", ID: "resource.synthetic.zone"}
	type unit struct {
		Anchor driver.AnchorKey
		Link   driver.LinkClaim
		Frame  reg.GreeVRFCommand
	}
	var previous []unit
	for range 2 {
		var graph []unit
		for i := range 2 {
			address := 0x20 + uint8(i)
			identity, p := imagePoint(t, iface, address, 0x10, 2, false, 1)
			if !identity.Apply(fragment(t, p.Key, []byte{4, 0x40, 0x60}, 2), false) {
				t.Fatal("identity fragment rejected")
			}
			profile := identity.DeviceIdentity()
			if profile.Class != reg.GreeVRFIndoorUnit || profile.Identifier != 0x6040 || profile.DeviceVersion != 1 {
				t.Fatal(profile)
			}
			anchor := driver.AnchorKey{Slot: fmt.Sprintf("equipment-%d", i)}
			producer := driver.NodeRef{Bridge: zoneRef.Bridge, ID: semreg.ResourceID("resource." + anchor.Slot)}
			basis := evidence([]byte{7})
			link, ok := Relation("air_supply", producer, zoneRef, semreg.RelationConfiguration, basis)
			if !ok {
				t.Fatal("expected configured relation")
			}
			target := SettingTarget{Equipment: Equipment{Anchor: anchor, Class: profile.Class, Key: p.Key, Producer: producer}, Zone: zoneRef, ZoneBasis: basis}
			f, err := EncodeTemperature(zoneRef, anchor, []SettingTarget{target}, semreg.Quantity{Number: semreg.Decimal{Coefficient: "29615", Exponent10: -2}, Unit: "unit.kelvin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			graph = append(graph, unit{anchor, link, f})
		}
		if graph[0].Link.Key.From == graph[1].Link.Key.From || graph[0].Frame.Identifier != 0x1cbfd073 || graph[1].Frame.Identifier != 0x1cbfd0f3 {
			t.Fatalf("indistinct subjects or targets: %+v", graph)
		}
		if previous != nil && !reflect.DeepEqual(previous, graph) {
			t.Fatalf("unstable graph: %+v", graph)
		}
		previous = graph
	}
}
