package driver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"sync"

	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/certification"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/bms"
	references "github.com/Project-Helianthus/helianthus-canbusreg/driverdata/certification"
)

// packCoordinate is the row slot whose text is the pack subject. The rows name
// this slot; a frame does not carry a separate pack anchor.
// certificationVendor is the vendor of every profile this driver configures.
const certificationVendor = "Growatt"

const packCoordinate = "pack-resource"

// The candidate contract requires a positive fresh window that retention
// exceeds. No received frame carries a lifetime, so the staged record uses the
// minimum the contract admits and leaves the age class unknown. The runtime
// classifies freshness; this window is not a protocol lifetime.
const (
	feedPolicyID         semreg.PolicyID        = "policy.can.growatt.bms"
	feedFreshForNS       semreg.Uint64          = "1"
	feedRetainForNS      semreg.Uint64          = "2"
	feedWallUncertainty  semreg.Uint64          = "0"
	feedPolicyVersion    semreg.SemanticVersion = "1.0.0"
	feedEvidenceContract semreg.ContractVersion = "native.can/v1"
)

// publishReceived evaluates the configured profile's rows for one admitted
// frame and stages the bound candidates. A nil session, a withdrawn instance,
// a frame with no candidate and a publisher that refuses all stage nothing.
// Malformed records never reach this path. No frame carries an accuracy; a certification
// reference is staged only when the held identity matches an entry.
func (i *instance) publishReceived(ctx context.Context, session semdriver.Session, evidence canbusreg.Evidence) {
	if session == nil || session.Publisher() == nil || i.withdrawn() {
		return
	}
	facts, err := i.boundFacts(evidence)
	if err != nil || len(facts) == 0 {
		return
	}
	i.stage(ctx, session, evidence, facts)
	i.certify(ctx, session, evidence, facts)
}

var (
	certificationOnce sync.Once
	certificationData *references.Registry
)

func certificationReferences() *references.Registry {
	certificationOnce.Do(func() {
		if registry, err := references.LoadEmbedded(); err == nil {
			certificationData = registry
		}
	})
	return certificationData
}

// certify stages the certification references that match the identity this
// instance holds: its driver, the vendor and the flavor of its profile. No
// profile decodes a model or a firmware release, so both are absent, no entry
// matches and nothing is published for a live instance.
func (i *instance) certify(ctx context.Context, session semdriver.Session, evidence canbusreg.Evidence, facts []semreg.FactCandidate) {
	registry := certificationReferences()
	native := session.Native()
	if registry == nil || native == nil {
		return
	}
	root := native.Root()
	identity := certification.Identity{Driver: string(Kind), Vendor: certificationVendor, Flavor: string(i.config.Options.Profile)}
	_ = certification.Publish(ctx, session.Publisher(), session.Prepare, registry, identity, certification.Context{
		Template: i.template(evidence), Producer: root,
		Subject: func(string) (semdriver.NodeRef, bool) { return root, true },
	}, facts)
}

func (i *instance) withdrawn() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fenced
}

func (i *instance) boundFacts(evidence canbusreg.Evidence) ([]semreg.FactCandidate, error) {
	frame := evidence.Frame
	var (
		evals map[string]roweval.Evaluation
		err   error
	)
	switch i.config.Options.Profile {
	case ProfileV104:
		table := bms.V104Table()
		evals, _, err = bms.EvaluateFrame(table, frame, i.acquired(evidence, table.Bindings))
	case ProfileV108:
		table := bms.V108Table()
		evals, _, err = bms.EvaluateV108Frame(frame, i.acquired(evidence, table.Bindings))
	case ProfileCommon:
		table := bms.CommonTable(i.commonScope())
		evals, _, err = bms.EvaluateCommonFrame(i.commonScope(), frame, i.acquired(evidence, table.Bindings))
	case ProfileMonitor:
		table := bms.MonitorTable(frame.ID().Value())
		evals, _, err = bms.EvaluateMonitorFrame(frame, i.acquired(evidence, table.Bindings))
	default:
		evals, err = i.projectEvaluations(evidence)
	}
	if err != nil {
		return nil, err
	}
	facts := make([]semreg.FactCandidate, 0, len(evals))
	for _, ev := range evals {
		if ev.Candidate != nil {
			facts = append(facts, *ev.Candidate)
		}
	}
	sort.Slice(facts, func(a, b int) bool { return facts[a].CandidateID < facts[b].CandidateID })
	return facts, nil
}

// commonScope is the publisher scope of a separately selected common
// projection: local is member and aggregate is aggregate. An absent selection
// has no row scope, matching the projection's unknown scope, and publishes no
// semantic candidate.
func (i *instance) commonScope() string {
	if i.config.Options.Selection == nil {
		return ""
	}
	switch i.config.Options.Selection.Scope {
	case "local":
		return bms.ScopeMember
	case "aggregate":
		return bms.ScopeAggregate
	default:
		return ""
	}
}

func (i *instance) acquired(evidence canbusreg.Evidence, bindings []roweval.BindingRow) roweval.Acquired[bms.Selector] {
	template := i.template(evidence)
	capability := semreg.CapabilityInstanceID("capability." + string(i.key) + ".pack")
	return roweval.Acquired[bms.Selector]{
		Template:    template,
		Roles:       packRoles(i.key, capability, bindings),
		Receipt:     roweval.Receipt{ReceivedAt: template.Times.ReceivedAt, Monotonic: template.Times.ReceiptMonotonic},
		Coordinates: map[string]string{packCoordinate: packSubject(i.key)},
	}
}

func packRoles(instance semdriver.InstanceKey, capability semreg.CapabilityInstanceID, bindings []roweval.BindingRow) roweval.Roles {
	return roweval.Roles{
		Subjects: map[string]roweval.RoleBinding{
			"pack": {ServiceInstance: semreg.ServiceInstanceID("service." + string(instance) + ".pack"), CapabilityInstance: &capability},
		},
		Catalog: roleCatalog(bindings),
	}
}

func roleCatalog(bindings []roweval.BindingRow) []semreg.DefinitionIndex {
	order := make([]semreg.PackRef, 0, 1)
	buckets := map[semreg.PackRef]*semreg.DefinitionIndex{}
	ensure := func(pack semreg.PackRef) *semreg.DefinitionIndex {
		if bucket, ok := buckets[pack]; ok {
			return bucket
		}
		bucket := &semreg.DefinitionIndex{Pack: pack, Fields: []semreg.DefinitionRef{}, Services: []semreg.DefinitionRef{}, Capabilities: []semreg.DefinitionRef{}}
		buckets[pack] = bucket
		order = append(order, pack)
		return bucket
	}
	for _, binding := range bindings {
		if binding.Read == nil {
			continue
		}
		ensure(binding.Service.Pack).Services = append(buckets[binding.Service.Pack].Services, binding.Service)
		if binding.Capability != (semreg.DefinitionRef{}) {
			ensure(binding.Capability.Pack).Capabilities = append(buckets[binding.Capability.Pack].Capabilities, binding.Capability)
		}
		ensure(binding.Read.Fact.Pack).Fields = append(buckets[binding.Read.Fact.Pack].Fields, binding.Read.Fact)
	}
	out := make([]semreg.DefinitionIndex, 0, len(order))
	for _, pack := range order {
		out = append(out, *buckets[pack])
	}
	return out
}

func (i *instance) template(evidence canbusreg.Evidence) semreg.FactCandidate {
	key := string(i.key)
	binding := semreg.NativeBindingID("binding." + key)
	source := semreg.SourceID("source." + key)
	epoch := semreg.SourceEpochID("epoch." + key)
	generation := semreg.Uint64("1")
	revision := semreg.Uint64(strconv.FormatUint(evidence.Sequence, 10))
	evidenceRef := frameEvidence(evidence.RawRecord[:])
	point := transportPoint(evidence.Monotonic, key)
	return semreg.FactCandidate{
		CandidateID: semreg.CandidateID("candidate." + key),
		Revision:    revision,
		Quality: semreg.Quality{
			Assertion: semreg.AssertionObserved, Qualification: semreg.QualificationCandidate,
			Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood,
			Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessUnknown,
			Reasons: []semreg.DefinitionID{},
		},
		Times: semreg.Times{
			ReceivedAt: point.wall, ReceiptMonotonic: point.mono,
			EvaluatedAt: point.wall, EvaluateMonotonic: point.mono,
		},
		FreshnessPolicy: semreg.FreshnessPolicy{
			PolicyID: feedPolicyID, Version: feedPolicyVersion,
			FreshForNS: feedFreshForNS, RetainForNS: feedRetainForNS, MaxWallUncertaintyNS: feedWallUncertainty,
		},
		BindingID: &binding, SourceEpochID: &epoch, DriverGeneration: &generation,
		Evidence: []semreg.EvidenceRef{evidenceRef},
		Origin: semreg.OriginRef{
			OriginID: semreg.OriginID("origin." + key), Kind: semreg.OriginNativeObservation,
			SourceID: &source, SourceEpochID: &epoch, BindingID: &binding,
			Evidence: []semreg.EvidenceRef{evidenceRef},
		},
	}
}

type transportTimes struct {
	wall semreg.TimePoint
	mono semreg.MonotonicPoint
}

func transportPoint(monotonic interface{ Nanoseconds() int64 }, instance string) transportTimes {
	return transportTimes{
		wall: semreg.TimePoint{UnixNanoseconds: "0", ClockID: "clock.transport", UncertaintyNS: "0"},
		mono: semreg.MonotonicPoint{
			ClockEpochID: semreg.ClockEpochID("clock-epoch.transport." + instance),
			Nanoseconds:  semreg.Uint64(strconv.FormatInt(monotonic.Nanoseconds(), 10)),
		},
	}
}

func frameEvidence(raw []byte) semreg.EvidenceRef {
	sum := sha256.Sum256(raw)
	return semreg.EvidenceRef{
		Owner: "driver.canbusreg", Kind: "evidence.can.frame",
		Digest:   semreg.Digest(fmt.Sprintf("sha256:%x", sum)),
		Contract: feedEvidenceContract, Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone,
	}
}

// packSubject is the stable pack text of one instance: the battery identity
// path of that instance, without a bridge. It does not use arrival order, a
// counter or the interface name.
func packSubject(instance semdriver.InstanceKey) string {
	return "can/" + string(instance) + "/battery"
}

func (i *instance) stage(ctx context.Context, session semdriver.Session, evidence canbusreg.Evidence, facts []semreg.FactCandidate) {
	batch, err := publicationBatch(i.key, i.config.Options.Profile, evidence, facts)
	if err != nil {
		return
	}
	tx, err := session.Publisher().Begin(ctx)
	if err != nil {
		return
	}
	if err := tx.Stage(batch); err != nil {
		_ = tx.Abort(ctx)
		return
	}
	_ = tx.Commit(ctx)
}

func publicationBatch(instance semdriver.InstanceKey, profile Profile, evidence canbusreg.Evidence, facts []semreg.FactCandidate) (semreg.PublicationBatch, error) {
	key := string(instance)
	sequence := semreg.Uint64(strconv.FormatUint(evidence.Sequence, 10))
	point := transportPoint(evidence.Monotonic, key)
	source := semreg.SourceID("source." + key)
	epoch := semreg.SourceEpochID("epoch." + key)
	asset := semreg.AssetID("asset." + key)
	bindings := publicationBindings(key, asset, source, epoch, facts)
	links, resources := publicationGraph(key, asset, bindings, profile, facts)
	batch := semreg.PublicationBatch{
		Contract: semreg.ContractKernelV1,
		BatchID:  semreg.BatchID("batch." + key + "." + string(sequence)),
		AssetID:  asset, SourceID: source,
		SourceEpochID: epoch, DriverGeneration: "1",
		Sequence: sequence, ExpectedSemanticRevision: "0", ObservedAt: point.wall,
		SourceUpserts:       []semreg.SourceDescriptor{publicationSource(key, profile, source, epoch, point.wall)},
		SourceRetirements:   []semreg.SourceEpochID{},
		BindingUpserts:      bindings,
		IdentityLinkUpserts: links,
		FactUpserts:         facts, FactWithdrawals: []semreg.CandidateID{},
		ServiceUpserts: []semreg.ServiceInstance{}, ServiceWithdrawals: []semreg.ServiceInstanceID{},
		CapabilityUpserts: []semreg.CapabilityInstance{}, CapabilityWithdrawals: []semreg.CapabilityInstanceID{},
		ResourceUpserts:  resources,
		GenerationFences: []semreg.GenerationFence{},
	}
	digest, err := batch.ComputedDigest()
	if err != nil {
		return semreg.PublicationBatch{}, err
	}
	batch.BatchDigest = digest
	return batch, nil
}

// publicationSource is the instance's native source. The descriptor is stable
// across frames: a later batch repeats it, and an identical upsert is not a
// metadata change. The host rejects a batch whose header source is neither
// current nor in this list.
func publicationSource(key string, profile Profile, source semreg.SourceID, epoch semreg.SourceEpochID, started semreg.TimePoint) semreg.SourceDescriptor {
	return semreg.SourceDescriptor{
		SourceID: source, SourceEpochID: epoch,
		ProtocolID: "protocol.can", ProfileID: semreg.DefinitionID(profile), ProfileVersion: semreg.VersionLabel(feedPolicyVersion),
		RegistryEvidence: frameEvidence([]byte("source." + key)),
		StartedAt:        started, State: semreg.SourceCurrent, Revision: "1",
	}
}

// publicationBindings names each binding the candidates already carry. The
// native resource is the binding, not the frame, so a later frame does not
// look like a different resource on the same binding.
func publicationBindings(key string, asset semreg.AssetID, source semreg.SourceID, epoch semreg.SourceEpochID, facts []semreg.FactCandidate) []semreg.NativeBinding {
	seen := map[semreg.NativeBindingID]struct{}{}
	ids := make([]semreg.NativeBindingID, 0, 1)
	for _, fact := range facts {
		if fact.BindingID == nil {
			continue
		}
		if _, ok := seen[*fact.BindingID]; ok {
			continue
		}
		seen[*fact.BindingID] = struct{}{}
		ids = append(ids, *fact.BindingID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]semreg.NativeBinding, 0, len(ids))
	for _, id := range ids {
		out = append(out, semreg.NativeBinding{
			BindingID: id, AssetID: asset, SourceID: source, SourceEpochID: epoch,
			DriverGeneration: "1",
			NativeResource:   frameEvidence([]byte("binding." + key + "." + string(id))),
			State:            semreg.BindingCurrent, Revision: "1",
		})
	}
	return out
}

// publicationGraph is the pack the candidates name. A PCS instance uses the
// pack role text. A LINK instance uses one pack per module source, so two
// modules are not two observations of one pack. The other profiles name the
// instance battery resource. Each binding observes the pack its facts name.
// The record of one pack stays the same on every frame, so a later batch
// repeats it.
func publicationGraph(key string, asset semreg.AssetID, bindings []semreg.NativeBinding, profile Profile, facts []semreg.FactCandidate) ([]semreg.IdentityLink, []semreg.Resource) {
	if profile == ProfileLINK {
		return linkGraph(key, asset, bindings, facts)
	}
	subject := packSubject(semdriver.InstanceKey(key))
	if profile == ProfilePCS {
		subject = "pack"
	}
	return onePackGraph(key, asset, bindings, subject)
}

func onePackGraph(key string, asset semreg.AssetID, bindings []semreg.NativeBinding, subject string) ([]semreg.IdentityLink, []semreg.Resource) {
	resource := packResource(key, subject)
	id := resource.ResourceID
	links := make([]semreg.IdentityLink, 0, len(bindings))
	for _, binding := range bindings {
		links = append(links, semreg.IdentityLink{
			AssetID: asset, BindingID: binding.BindingID, State: semreg.LinkQualified,
			Basis: resource.Anchor.Evidence, Revision: "1", Resource: &id,
		})
	}
	return links, []semreg.Resource{resource}
}

func linkGraph(key string, asset semreg.AssetID, bindings []semreg.NativeBinding, facts []semreg.FactCandidate) ([]semreg.IdentityLink, []semreg.Resource) {
	subjects := distinctPacks(facts)
	if len(subjects) == 0 {
		subjects = []string{"pack"}
	}
	resources := make([]semreg.Resource, 0, len(subjects))
	index := map[string]semreg.ResourceID{}
	for _, subject := range subjects {
		resource := packResource(key, subject)
		index[subject] = resource.ResourceID
		resources = append(resources, resource)
	}
	ids := make([]semreg.ResourceID, len(bindings))
	links := make([]semreg.IdentityLink, 0, len(bindings))
	evidence := resources[0].Anchor.Evidence
	for i, binding := range bindings {
		subject := packOfBinding(facts, binding.BindingID)
		if subject == "" {
			subject = subjects[0]
		}
		ids[i] = index[subject]
		links = append(links, semreg.IdentityLink{
			AssetID: asset, BindingID: binding.BindingID, State: semreg.LinkQualified,
			Basis: evidence, Revision: "1", Resource: &ids[i],
		})
	}
	return links, resources
}

func packResource(key, subject string) semreg.Resource {
	slot := subject
	evidence := frameEvidence([]byte("resource." + key))
	return semreg.Resource{
		ResourceID: semreg.ResourceID(subject), Kind: "resource.battery_pack",
		Anchor: semreg.ResourceAnchor{
			Class: semreg.AnchorEquipment, Basis: semreg.AnchorOperatorSlot,
			Slot: &slot, Evidence: []semreg.EvidenceRef{evidence},
		},
		State: semreg.ResourceCurrent,
		Origin: semreg.OriginRef{
			OriginID: semreg.OriginID("origin.operator." + key), Kind: semreg.OriginOperator,
			Evidence: []semreg.EvidenceRef{evidence},
		},
		Revision: "1",
	}
}

func distinctPacks(facts []semreg.FactCandidate) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1)
	for _, fact := range facts {
		text := packDimensionText(fact)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		out = append(out, text)
	}
	sort.Strings(out)
	return out
}

func packOfBinding(facts []semreg.FactCandidate, id semreg.NativeBindingID) string {
	for _, fact := range facts {
		if fact.BindingID != nil && *fact.BindingID == id {
			if text := packDimensionText(fact); text != "" {
				return text
			}
		}
	}
	return ""
}

func packDimensionText(fact semreg.FactCandidate) string {
	for _, dim := range fact.Key.Dimensions {
		if dim.ID == "storage.dimension.pack" && dim.Value.Text != nil {
			return *dim.Value.Text
		}
	}
	return ""
}
