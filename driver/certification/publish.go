// Package certification matches the identity a driver holds for an instance
// against the certification references data file and stages the accuracy
// values that a matching reference states, as declared candidates that cite a
// certification. A value the device publishes itself is never replaced, and a
// gap or an unavailable-source entry publishes nothing.
package certification

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"

	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"

	references "github.com/Project-Helianthus/helianthus-canbusreg/driverdata/certification"
)

const (
	packDimension    semreg.DefinitionID = "storage.dimension.pack"
	packVoltage      semreg.DefinitionID = "storage.pack.voltage"
	packCurrent      semreg.DefinitionID = "storage.pack.current"
	accuracyPrefix                       = "storage.accuracy."
	measuredFact     semreg.DefinitionID = "storage.accuracy.measured"
	ratioMaximumFact semreg.DefinitionID = "storage.accuracy.ratio_maximum"
	unitRatio        semreg.DefinitionID = "unit.ratio"
	unitAmpere       semreg.DefinitionID = "unit.ampere"
	unitWatt         semreg.DefinitionID = "unit.watt"
	activeCurrent                        = "active_current"
	activePower                          = "active_power"
	voltage                              = "voltage"
	measurementType  semreg.DefinitionID = "storage.dimension.measurement_type"

	// LowestPriority is the source priority of every certification source:
	// the lowest the priority type holds, so any other source wins.
	LowestPriority int32 = math.MinInt32
)

// Lookuper is the data the match reads; a loaded document and the embedded
// registry both implement it.
type Lookuper interface {
	Lookup(references.Query) []references.Entry
}

// Identity is the identity the driver holds for one instance. Model and
// Release are empty unless a decoded row carries them.
type Identity struct {
	Driver  string
	Vendor  string
	Flavor  string
	Model   string
	Release string
}

// Context is what the caller supplies for one staging.
type Context struct {
	// Template carries quality, times, freshness policy, candidate id base and
	// revision of the candidates; the origin, evidence, key and value are set
	// here.
	Template semreg.FactCandidate
	// Producer is the node that produces the staged sources.
	Producer semdriver.NodeRef
	// Subject resolves the canonical subject of one pack resource.
	Subject func(pack string) (semdriver.NodeRef, bool)
}

// Staged is one declared source with its sample.
type Staged struct {
	Source semdriver.Source
	Sample semdriver.Sample
}

// Result is the outcome of a match.
type Result struct {
	Staged []Staged
	// Unmapped names stated members that no element of the pinned catalog
	// carries; they are left as named gaps.
	Unmapped []string
	// Conflicts lists keys that matching entries state with different
	// values; none of them is published.
	Conflicts []semreg.FactKey
}

type packRef struct {
	id      semreg.DefinitionID
	version semreg.SemanticVersion
	text    string
}

// keyOf builds the key of an accuracy fact: the measurement-type qualifier
// the fact declares comes before the pack dimension (identifier order).
func keyOf(pack packRef, fact semreg.DefinitionID, kind string) semreg.FactKey {
	text := pack.text
	dims := []semreg.Dimension{}
	if kind != "" {
		dims = append(dims, semreg.Dimension{ID: measurementType, Value: semreg.Value{
			Kind: semreg.ValueSymbol, Symbol: &semreg.Symbol{Namespace: measurementType, Token: kind, Known: true},
		}})
	}
	dims = append(dims, semreg.Dimension{ID: packDimension, Value: semreg.Value{Kind: semreg.ValueText, Text: &text}})
	return semreg.FactKey{PackID: pack.id, PackVersion: pack.version, FactID: fact, Dimensions: dims}
}

func keyText(key semreg.FactKey) string {
	parts := []string{string(key.PackID), string(key.PackVersion), string(key.FactID)}
	for _, d := range key.Dimensions {
		text := ""
		if d.Value.Text != nil {
			text = *d.Value.Text
		}
		if d.Value.Symbol != nil {
			text = d.Value.Symbol.Token
		}
		parts = append(parts, string(d.ID)+"="+text)
	}
	return strings.Join(parts, "|")
}

// packs returns the pack resources that the facts publish voltage or current
// for, and the keys of the accuracy facts the device itself publishes.
func packs(facts []semreg.FactCandidate) ([]packRef, map[string]bool) {
	seen := map[string]bool{}
	device := map[string]bool{}
	var out []packRef
	for _, fact := range facts {
		if strings.HasPrefix(string(fact.Key.FactID), accuracyPrefix) {
			device[keyText(fact.Key)] = true
			continue
		}
		if fact.Key.FactID != packVoltage && fact.Key.FactID != packCurrent {
			continue
		}
		for _, d := range fact.Key.Dimensions {
			if d.ID != packDimension || d.Value.Text == nil {
				continue
			}
			ref := packRef{id: fact.Key.PackID, version: fact.Key.PackVersion, text: *d.Value.Text}
			if id := string(ref.id) + "|" + ref.text; !seen[id] {
				seen[id] = true
				out = append(out, ref)
			}
		}
	}
	return out, device
}

type stated struct {
	fact  semreg.DefinitionID
	kind  string
	value semreg.Value
	entry references.Entry
}

func (s stated) id() string { return string(s.fact) + "|" + s.kind }

func quantity(d *references.Decimal, unit semreg.DefinitionID) semreg.Value {
	return semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{
		Number: semreg.Decimal{Coefficient: d.Coefficient, Exponent10: int32(d.Exponent10)}, Unit: unit,
	}}
}

func members(entry references.Entry) ([]stated, []string) {
	value := entry.StatedValue
	var out []stated
	var unmapped []string
	kind := value.MeasurementType
	if kind != activeCurrent && kind != activePower && kind != voltage {
		return nil, []string{kind}
	}
	add := func(fact semreg.DefinitionID, v semreg.Value) { out = append(out, stated{fact, kind, v, entry}) }
	if value.Measured != nil {
		b := *value.Measured
		add(measuredFact, semreg.Value{Kind: semreg.ValueBoolean, Boolean: &b})
	}
	if value.RatioMaximum != nil {
		add(ratioMaximumFact, quantity(value.RatioMaximum, unitRatio))
	}
	unit := unitAmpere
	if kind == activePower {
		unit = unitWatt
	}
	for _, m := range []struct {
		name  string
		value *references.Decimal
	}{
		{"measured_minimum", value.MeasuredMinimum}, {"measured_maximum", value.MeasuredMaximum},
		{"range_minimum", value.RangeMinimum}, {"range_maximum", value.RangeMaximum},
	} {
		if m.value != nil && kind == voltage {
			// the catalog has no range or measured-bound fact for voltage
			unmapped = append(unmapped, kind+"."+m.name)
		} else if m.value != nil {
			add(semreg.DefinitionID(accuracyPrefix+m.name+"_"+kind), quantity(m.value, unit))
		}
	}
	if value.Statement != "" {
		unmapped = append(unmapped, kind+".statement")
	}
	return out, unmapped
}

func sameValue(a, b semreg.Value) bool {
	switch {
	case a.Boolean != nil && b.Boolean != nil:
		return *a.Boolean == *b.Boolean
	case a.Quantity != nil && b.Quantity != nil:
		return a.Quantity.Unit == b.Quantity.Unit && a.Quantity.Number == b.Quantity.Number
	}
	return false
}

func evidenceOf(entry references.Entry) semreg.EvidenceRef {
	sum := sha256.Sum256([]byte(strings.Join([]string{entry.Issuer, entry.CertificateNumber, entry.IssueDate, entry.URL, entry.RetrievedOn}, "\n")))
	return semreg.EvidenceRef{
		Owner: "driver.canbusreg", Kind: semreg.EvidenceKindCertification,
		Digest:   semreg.Digest(fmt.Sprintf("sha256:%x", sum)),
		Contract: semreg.ContractVersion(references.ContractID), Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone,
	}
}

// Match returns the declared sources and samples for every pack resource that
// the facts publish pack voltage or current for. Entries that are a gap or an
// unavailable-source record contribute nothing, and a key the device publishes
// itself gets no certification candidate.
func Match(data Lookuper, identity Identity, ctx Context, facts []semreg.FactCandidate) (Result, error) {
	var result Result
	resources, device := packs(facts)
	if len(resources) == 0 || data == nil {
		return result, nil
	}
	entries := data.Lookup(references.Query{
		Driver: identity.Driver, Vendor: identity.Vendor, Flavor: identity.Flavor,
		Model: identity.Model, Release: identity.Release,
	})
	byFact := map[string][]stated{}
	var order []string
	unmapped := map[string]bool{}
	for _, entry := range entries {
		if entry.Category != references.CategoryReference || entry.StatedValue == nil {
			continue
		}
		got, gaps := members(entry)
		for _, name := range gaps {
			unmapped[name] = true
		}
		for _, s := range got {
			if byFact[s.id()] == nil {
				order = append(order, s.id())
			}
			byFact[s.id()] = append(byFact[s.id()], s)
		}
	}
	for name := range unmapped {
		result.Unmapped = append(result.Unmapped, name)
	}
	sort.Strings(result.Unmapped)
	for _, pack := range resources {
		subject, ok := ctx.Subject(pack.text)
		if !ok {
			continue
		}
		for _, id := range order {
			all := byFact[id]
			key := keyOf(pack, all[0].fact, all[0].kind)
			if device[keyText(key)] {
				continue
			}
			conflict := false
			for _, other := range all[1:] {
				conflict = conflict || !sameValue(all[0].value, other.value)
			}
			if conflict {
				result.Conflicts = append(result.Conflicts, key)
				continue
			}
			staged, err := build(ctx, subject, key, all[0])
			if err != nil {
				return Result{}, err
			}
			result.Staged = append(result.Staged, staged)
		}
	}
	return result, nil
}

func build(ctx Context, subject semdriver.NodeRef, key semreg.FactKey, s stated) (Staged, error) {
	element := semdriver.Element{Subject: subject, Key: key}
	path := "certification:" + s.entry.CertificateNumber
	source := semdriver.Source{For: element, Producer: ctx.Producer, PathKey: path, Priority: LowestPriority}
	evidence := evidenceOf(s.entry)
	value := s.value
	candidate := ctx.Template
	digest := sha256.Sum256([]byte(keyText(key)))
	candidate.CandidateID = semreg.CandidateID(fmt.Sprintf("%s.certification.%x", ctx.Template.CandidateID, digest[:]))
	candidate.Key = key
	candidate.Value = &value
	candidate.Quality.Assertion = semreg.AssertionInferred
	candidate.BindingID, candidate.SourceEpochID, candidate.DriverGeneration, candidate.Derivation = nil, nil, nil, nil
	candidate.Evidence = []semreg.EvidenceRef{evidence}
	candidate.Origin = semreg.OriginRef{
		OriginID: semreg.OriginID("origin.certification." + s.entry.CertificateNumber),
		Kind:     semreg.OriginDeclared, Evidence: []semreg.EvidenceRef{evidence},
	}
	sample := semdriver.Sample{From: source, Candidate: candidate, Receipt: semdriver.ReceiptStamp{At: ctx.Template.Times.ReceiptMonotonic}}
	if err := sample.Validate(); err != nil {
		return Staged{}, err
	}
	return Staged{Source: source, Sample: sample}, nil
}

// Prepare is the session's batch preparation: it fills the ownership stamp
// after the mutation has set the batch contents.
type Prepare func(mutate func(*semreg.PublicationBatch)) (semreg.PublicationBatch, error)

// Stage declares every source, stages the batch that carries the candidates of
// the samples, and stages the samples, so the publication the transaction
// applies holds the candidates the samples are admitted against.
func Stage(tx semdriver.PublicationTransaction, prepare Prepare, result Result) error {
	facts := make([]semreg.FactCandidate, 0, len(result.Staged))
	for _, staged := range result.Staged {
		facts = append(facts, staged.Sample.Candidate)
	}
	batch, err := prepare(func(b *semreg.PublicationBatch) { b.FactUpserts = facts })
	if err != nil {
		return err
	}
	if err := tx.Stage(batch); err != nil {
		return err
	}
	for _, staged := range result.Staged {
		if err := tx.DeclareSource(staged.Source); err != nil {
			return err
		}
		if err := tx.StageSample(staged.Sample); err != nil {
			return err
		}
	}
	return nil
}

// Publish matches and stages in one transaction of the publisher. A match
// with nothing to stage opens no transaction.
func Publish(ctx context.Context, publisher semdriver.Publisher, prepare Prepare, data Lookuper, identity Identity, c Context, facts []semreg.FactCandidate) error {
	result, err := Match(data, identity, c, facts)
	if err != nil || len(result.Staged) == 0 {
		return err
	}
	tx, err := publisher.Begin(ctx)
	if err != nil {
		return err
	}
	if err := Stage(tx, prepare, result); err != nil {
		_ = tx.Abort(ctx)
		return err
	}
	return tx.Commit(ctx)
}
