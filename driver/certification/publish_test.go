package certification

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/packs/storage"

	references "github.com/Project-Helianthus/helianthus-canbusreg/driverdata/certification"
)

const packText = "can/test/battery"

func loadTestData(t *testing.T) *references.Document {
	t.Helper()
	data, err := os.ReadFile("testdata/test_references.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := references.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func identity(model, release string) Identity {
	return Identity{Driver: "test-data-driver", Vendor: "Test Data Vendor", Flavor: "test-data-profile", Model: model, Release: release}
}

func fact(id semreg.DefinitionID) semreg.FactCandidate {
	kind := ""
	if strings.HasPrefix(string(id), accuracyPrefix) {
		kind = activeCurrent
	}
	return semreg.FactCandidate{Key: keyOf(packRef{id: "helianthus.pack.storage", version: "1.1.0", text: packText}, id, kind)}
}

func byKey(result Result) map[string]Staged {
	out := map[string]Staged{}
	for _, s := range result.Staged {
		out[keyText(s.Sample.Candidate.Key)] = s
	}
	return out
}

func context0() Context {
	node := semdriver.NodeRef{Bridge: "bridge", ID: "node/test"}
	template := semreg.FactCandidate{
		CandidateID: "candidate.test", Revision: "1",
		Quality: semreg.Quality{
			Assertion: semreg.AssertionInferred, Qualification: semreg.QualificationCandidate,
			Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood,
			Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessUnknown, Reasons: []semreg.DefinitionID{},
		},
		Times: semreg.Times{
			ReceivedAt:        semreg.TimePoint{UnixNanoseconds: "0", ClockID: "clock.test", UncertaintyNS: "0"},
			ReceiptMonotonic:  semreg.MonotonicPoint{ClockEpochID: "epoch.test", Nanoseconds: "0"},
			EvaluatedAt:       semreg.TimePoint{UnixNanoseconds: "0", ClockID: "clock.test", UncertaintyNS: "0"},
			EvaluateMonotonic: semreg.MonotonicPoint{ClockEpochID: "epoch.test", Nanoseconds: "0"},
		},
		FreshnessPolicy: semreg.FreshnessPolicy{PolicyID: "policy.test", Version: "1.0.0", FreshForNS: "1", RetainForNS: "2", MaxWallUncertaintyNS: "0"},
	}
	return Context{Template: template, Producer: node, Subject: func(string) (semdriver.NodeRef, bool) { return node, true }}
}

func match(t *testing.T, id Identity, facts ...semreg.FactCandidate) Result {
	t.Helper()
	result, err := Match(loadTestData(t), id, context0(), facts)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func factIDs(result Result) map[semreg.DefinitionID]Staged {
	out := map[semreg.DefinitionID]Staged{}
	for _, s := range result.Staged {
		out[s.Sample.Candidate.Key.FactID] = s
	}
	return out
}

func TestReferenceEntryYieldsDeclaredCertificationCandidatesAtLowestPriority(t *testing.T) {
	result := match(t, identity("TEST-DATA-REFERENCE", "2.1"), fact(packVoltage), fact(packCurrent))
	got := factIDs(result)
	for _, id := range []semreg.DefinitionID{measuredFact, ratioMaximumFact, "storage.accuracy.range_maximum_active_current"} {
		staged, ok := got[id]
		if !ok {
			t.Fatalf("%s not staged: %v", id, got)
		}
		c := staged.Sample.Candidate
		if c.Origin.Kind != semreg.OriginDeclared || c.Origin.Evidence[0].Kind != semreg.EvidenceKindCertification || c.Evidence[0].Kind != semreg.EvidenceKindCertification {
			t.Fatalf("%s origin %+v", id, c.Origin)
		}
		if staged.Source.Priority != LowestPriority || c.BindingID != nil || c.SourceEpochID != nil || c.DriverGeneration != nil {
			t.Fatalf("%s source %+v", id, staged.Source)
		}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(result.Staged) != 3 {
		t.Fatalf("staged %d", len(result.Staged))
	}
	if q := got[ratioMaximumFact].Sample.Candidate.Value.Quantity; q.Unit != unitRatio || q.Number.Coefficient != "3" || q.Number.Exponent10 != -3 {
		t.Fatalf("ratio %+v", q)
	}
	if q := got["storage.accuracy.range_maximum_active_current"].Sample.Candidate.Value.Quantity; q.Unit != unitAmpere || q.Number.Coefficient != "7" {
		t.Fatalf("range %+v", q)
	}
	if len(result.Unmapped) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("%+v", result)
	}
}

func TestGapAndUnavailableEntriesPublishNothing(t *testing.T) {
	for _, model := range []string{"TEST-DATA-GAP", "TEST-DATA-UNAVAILABLE"} {
		if result := match(t, identity(model, ""), fact(packVoltage)); len(result.Staged) != 0 {
			t.Fatalf("%s staged %d", model, len(result.Staged))
		}
	}
}

func TestDeviceAccuracyValueMeansNoCertificationCandidateForItsKey(t *testing.T) {
	published := fact(measuredFact)
	result := match(t, identity("TEST-DATA-REFERENCE", "2.1"), fact(packCurrent), published)
	got := factIDs(result)
	if _, ok := got[measuredFact]; ok {
		t.Fatal("certification candidate for a key the device publishes")
	}
	if len(got) != 2 {
		t.Fatalf("other keys must stay: %v", got)
	}
}

func TestIdentityWithoutModelYieldsNoCandidate(t *testing.T) {
	if result := match(t, identity("", "2.1"), fact(packVoltage)); len(result.Staged) != 0 {
		t.Fatalf("staged %d", len(result.Staged))
	}
}

func TestFirmwareBelowFloorYieldsNoCandidate(t *testing.T) {
	if result := match(t, identity("TEST-DATA-REFERENCE", "1.9"), fact(packVoltage)); len(result.Staged) != 0 {
		t.Fatalf("staged %d", len(result.Staged))
	}
	if result := match(t, identity("TEST-DATA-REFERENCE", ""), fact(packVoltage)); len(result.Staged) != 0 {
		t.Fatalf("unknown release staged %d", len(result.Staged))
	}
}

func TestOtherIdentityMembersYieldNoCandidate(t *testing.T) {
	for name, mutate := range map[string]func(*Identity){
		"driver": func(i *Identity) { i.Driver = "other" }, "vendor": func(i *Identity) { i.Vendor = "other" },
		"flavor": func(i *Identity) { i.Flavor = "other" }, "model": func(i *Identity) { i.Model = "other" },
	} {
		id := identity("TEST-DATA-REFERENCE", "2.1")
		mutate(&id)
		if result := match(t, id, fact(packVoltage)); len(result.Staged) != 0 {
			t.Fatalf("%s staged %d", name, len(result.Staged))
		}
	}
}

func TestStatementOnlyValueIsNamedAndNotPublished(t *testing.T) {
	result := match(t, identity("TEST-DATA-STATEMENT", ""), fact(packVoltage))
	if len(result.Staged) != 0 || len(result.Unmapped) != 1 || result.Unmapped[0] != "voltage.statement" {
		t.Fatalf("%+v", result)
	}
}

func TestNoPackPublicationMeansNothingIsStaged(t *testing.T) {
	if result := match(t, identity("TEST-DATA-REFERENCE", "2.1"), fact(measuredFact)); len(result.Staged) != 0 {
		t.Fatalf("staged %d", len(result.Staged))
	}
}

type tx struct {
	semdriver.PublicationTransaction
	sources []semdriver.Source
	samples []semdriver.Sample
	commit  int
}

func (x *tx) DeclareSource(s semdriver.Source) error { x.sources = append(x.sources, s); return nil }
func (x *tx) StageSample(s semdriver.Sample) error   { x.samples = append(x.samples, s); return nil }
func (x *tx) Commit(context.Context) error           { x.commit++; return nil }

type publisher struct {
	semdriver.Publisher
	tx    *tx
	begun int
}

func (p *publisher) Begin(context.Context) (semdriver.PublicationTransaction, error) {
	p.begun++
	return p.tx, nil
}

func TestPublishStagesSourcesAndSamplesInOneTransaction(t *testing.T) {
	p := &publisher{tx: &tx{}}
	err := Publish(context.Background(), p, loadTestData(t), identity("TEST-DATA-REFERENCE", "2.1"), context0(), []semreg.FactCandidate{fact(packCurrent)})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.tx.sources) != 3 || len(p.tx.samples) != 3 || p.tx.commit != 1 {
		t.Fatalf("%d %d %d", len(p.tx.sources), len(p.tx.samples), p.tx.commit)
	}
	q := &publisher{tx: &tx{}}
	if err := Publish(context.Background(), q, loadTestData(t), identity("", ""), context0(), []semreg.FactCandidate{fact(packCurrent)}); err != nil || q.begun != 0 {
		t.Fatalf("%v %d", err, q.begun)
	}
}

func TestPublishSourceHoldsNoNumericAccuracyLiteral(t *testing.T) {
	raw, err := os.ReadFile("publish.go")
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`"[^"]*[0-9][^"]*"|[0-9]+\.[0-9]+`)
	for number, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "github.com") || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if literal.MatchString(strings.ReplaceAll(line, "sha256", "")) {
			t.Fatalf("line %d holds a numeric literal: %s", number+1, line)
		}
	}
}

func TestStagedAccuracyKeysCarryMeasurementTypeAndStayDistinct(t *testing.T) {
	result := match(t, identity("TEST-DATA-REFERENCE", "2.1"), fact(packCurrent))
	for _, s := range result.Staged {
		ds := s.Sample.Candidate.Key.Dimensions
		if len(ds) != 2 || ds[0].ID != measurementType || ds[0].Value.Symbol == nil || ds[0].Value.Symbol.Token != activeCurrent || ds[1].ID != packDimension {
			t.Fatalf("dimensions %+v", ds)
		}
	}
	// the same members for another measurement type are different keys
	pack := packRef{id: "helianthus.pack.storage", version: "1.1.0", text: packText}
	if keyText(keyOf(pack, measuredFact, activeCurrent)) == keyText(keyOf(pack, measuredFact, activePower)) {
		t.Fatal("measurement types share a key")
	}
}

func TestVoltageReferenceStagesMeasuredAndRatioAndNamesRange(t *testing.T) {
	result := match(t, identity("TEST-DATA-VOLTAGE", ""), fact(packVoltage))
	got := byKey(result)
	pack := packRef{id: "helianthus.pack.storage", version: "1.1.0", text: packText}
	for _, id := range []semreg.DefinitionID{measuredFact, ratioMaximumFact} {
		if _, ok := got[keyText(keyOf(pack, id, voltage))]; !ok {
			t.Fatalf("%s not staged for voltage: %v", id, got)
		}
	}
	if len(result.Staged) != 2 {
		t.Fatalf("staged %d", len(result.Staged))
	}
	if len(result.Unmapped) != 1 || result.Unmapped[0] != "voltage.range_maximum" {
		t.Fatalf("unmapped %v", result.Unmapped)
	}
}

func TestStagedAccuracyKeysAreAcceptedByTheStoragePackValidator(t *testing.T) {
	v := storage.New()
	for _, model := range []string{"TEST-DATA-REFERENCE", "TEST-DATA-VOLTAGE"} {
		result := match(t, identity(model, "2.1"), fact(packCurrent))
		if len(result.Staged) == 0 {
			t.Fatalf("%s staged nothing", model)
		}
		for _, s := range result.Staged {
			c := s.Sample.Candidate
			if err := v.ValidateFact(c.Key, c.Value); err != nil {
				t.Fatalf("%s: %v", c.Key.FactID, err)
			}
		}
	}
}
