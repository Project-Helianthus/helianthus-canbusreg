package driver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

const v104PayloadProfile = `{"profile":"growatt-bms-can-v104"}`

// recordingPublisher stores the batches a feed commits. beginErr, stageErr and
// commitErr refuse that step and record nothing.
type recordingPublisher struct {
	mu        sync.Mutex
	batches   []semreg.PublicationBatch
	beginErr  error
	stageErr  error
	commitErr error
}

func (p *recordingPublisher) Begin(context.Context) (semdriver.PublicationTransaction, error) {
	p.mu.Lock()
	err := p.beginErr
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &recordingTx{pub: p}, nil
}

func (p *recordingPublisher) snapshot() []semreg.PublicationBatch {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]semreg.PublicationBatch(nil), p.batches...)
}

type recordingTx struct {
	pub     *recordingPublisher
	batch   semreg.PublicationBatch
	samples []semdriver.Sample
	links   []semdriver.LinkClaim
}

func (t *recordingTx) OperatorLinks([]semreg.NativeBinding) []semreg.IdentityLink { return nil }
func (t *recordingTx) AdmitManufacturerSerial(semdriver.ManufacturerSerialClaim) error {
	return nil
}
func (t *recordingTx) DeclareSource(semdriver.Source) error             { return nil }
func (t *recordingTx) DeclareSettingRoute(semdriver.SettingRoute) error { return nil }
func (t *recordingTx) StageSample(s semdriver.Sample) error {
	t.samples = append(t.samples, s)
	return nil
}

func (t *recordingTx) StageLink(l semdriver.LinkClaim) error {
	t.links = append(t.links, l)
	return nil
}

func (t *recordingTx) Abort(context.Context) error { return nil }

func (t *recordingTx) Stage(batch semreg.PublicationBatch) error {
	t.pub.mu.Lock()
	err := t.pub.stageErr
	t.pub.mu.Unlock()
	if err != nil {
		return err
	}
	t.batch = batch
	return nil
}

func (t *recordingTx) Commit(context.Context) error {
	t.pub.mu.Lock()
	defer t.pub.mu.Unlock()
	if t.pub.commitErr != nil {
		return t.pub.commitErr
	}
	t.pub.batches = append(t.pub.batches, t.batch)
	return nil
}

type feedSession struct{ pub semdriver.Publisher }

func (s feedSession) Native() semdriver.NativeRegistry             { return nil }
func (s feedSession) SemanticInput() semdriver.SemanticInput       { return nil }
func (s feedSession) ScopedOperations() semdriver.ScopedOperations { return nil }
func (s feedSession) Calendar() semdriver.Calendar                 { return nil }
func (s feedSession) Publisher() semdriver.Publisher               { return s.pub }
func (s feedSession) Prepare(mutate func(*semreg.PublicationBatch)) (semreg.PublicationBatch, error) {
	var batch semreg.PublicationBatch
	mutate(&batch)
	return batch, nil
}
func (s feedSession) RegisterExecutor(semdriver.SettingRoute, semdriver.Executor) (func(), error) {
	return func() {}, nil
}

func startFeed(t *testing.T, key, iface string, index int, options string, pub *recordingPublisher) (*instance, *fakeReceiver) {
	t.Helper()
	r := newFakeReceiver(t, iface, index)
	f := factoryWith(r, nil)
	plans, err := f.Plan(config(key, `{"interface":"`+iface+`"}`, options))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan: %v %v", plans, err)
	}
	built, err := f.Construct(context.Background(), plans[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := built.Start(context.Background(), feedSession{pub: pub}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { built.Stop(context.Background()) })
	return built.(*instance), r
}

func waitBatches(t *testing.T, pub *recordingPublisher, n int) []semreg.PublicationBatch {
	t.Helper()
	waitFor(t, "batches", func() bool { return len(pub.snapshot()) >= n })
	got := pub.snapshot()
	if len(got) != n {
		t.Fatalf("batches = %d, want %d", len(got), n)
	}
	return got
}

func factByID(facts []semreg.FactCandidate, id string) *semreg.FactCandidate {
	for i := range facts {
		if string(facts[i].Key.FactID) == id {
			return &facts[i]
		}
	}
	return nil
}

func decimalRat(t *testing.T, number semreg.Decimal) *big.Rat {
	t.Helper()
	got, ok := new(big.Rat).SetString(number.Coefficient + "e" + strconv.FormatInt(int64(number.Exponent10), 10))
	if !ok {
		t.Fatalf("decimal %q exponent %d", number.Coefficient, number.Exponent10)
	}
	return got
}

func assertQuantity(t *testing.T, facts []semreg.FactCandidate, id, unit, value string) {
	t.Helper()
	fact := factByID(facts, id)
	if fact == nil || fact.Value == nil || fact.Value.Quantity == nil {
		t.Fatalf("%s missing: %+v", id, fact)
	}
	want, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatal(value)
	}
	if string(fact.Value.Quantity.Unit) != unit || decimalRat(t, fact.Value.Quantity.Number).Cmp(want) != 0 {
		t.Fatalf("%s = %s %s, want %s %s", id, fact.Value.Quantity.Number.Coefficient, fact.Value.Quantity.Unit, value, unit)
	}
	if fact.Value.Kind != semreg.ValueQuantity || fact.Value.Boolean != nil || fact.Value.Text != nil || fact.Value.Symbol != nil || fact.Value.Time != nil {
		t.Fatalf("%s value is not a bare quantity: %+v", id, fact.Value)
	}
}

func assertNoAccuracy(t *testing.T, facts []semreg.FactCandidate) {
	t.Helper()
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("accuracy")) {
		t.Fatalf("staged facts carry accuracy: %s", raw)
	}
}

func subjectText(t *testing.T, fact semreg.FactCandidate) string {
	t.Helper()
	if len(fact.Key.Dimensions) != 1 || fact.Key.Dimensions[0].Value.Text == nil {
		t.Fatalf("dimension: %+v", fact.Key.Dimensions)
	}
	if string(fact.Key.Dimensions[0].ID) != "storage.dimension.pack" {
		t.Fatalf("dimension id %s", fact.Key.Dimensions[0].ID)
	}
	return *fact.Key.Dimensions[0].Value.Text
}

func TestReceivedFrameStagesBoundRowUnits(t *testing.T) {
	pub := &recordingPublisher{}
	_, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
	r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	batch := waitBatches(t, pub, 1)[0]
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	assertQuantity(t, batch.FactUpserts, "storage.state.soc", "unit.ratio", "0.5")
	assertQuantity(t, batch.FactUpserts, "storage.pack.voltage", "unit.volt", "0.01")
	assertQuantity(t, batch.FactUpserts, "storage.pack.current", "unit.ampere", "0.2")
	if factByID(batch.FactUpserts, "storage.state.soh") != nil {
		t.Fatal("SOH staged while its validity bit is clear")
	}
	if len(batch.FactUpserts) != 3 {
		t.Fatalf("facts = %d, want the three bound rows", len(batch.FactUpserts))
	}
	assertNoAccuracy(t, batch.FactUpserts)
	for _, fact := range batch.FactUpserts {
		if fact.Quality.Freshness != semreg.FreshnessUnknown {
			t.Fatalf("freshness %s", fact.Quality.Freshness)
		}
		if subjectText(t, fact) != "can/a/battery" {
			t.Fatalf("subject %s", subjectText(t, fact))
		}
	}
	record := evidenceOf(t, r, 1, 0x313, false, []byte{0, 1, 0, 2, 0, 0, 50, 90}).RawRecord
	sum := sha256.Sum256(record[:])
	got := batch.FactUpserts[0].Evidence[0].Digest
	if got != semreg.Digest("sha256:"+hexBytes(sum[:])) {
		t.Fatalf("evidence digest %s", got)
	}
}

func hexBytes(raw []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, b := range raw {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&0x0f]
	}
	return string(out)
}

func TestRefusedAndMalformedFramesStageNothing(t *testing.T) {
	t.Run("state of charge above the row gate", func(t *testing.T) {
		pub := &recordingPublisher{}
		inst, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
		r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 101, 90)
		r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
		batch := waitBatches(t, pub, 1)[0]
		assertQuantity(t, batch.FactUpserts, "storage.state.soc", "unit.ratio", "0.5")
		waitFor(t, "both frames", func() bool { n, _, _ := inst.counts(); return n == 2 })
	})
	t.Run("malformed record", func(t *testing.T) {
		pub := &recordingPublisher{}
		inst, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
		r.records <- receiver.Record{Interface: r.identity, Raw: []byte{1, 2, 3}, Sequence: 1}
		r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
		waitBatches(t, pub, 1)
		waitFor(t, "valid frame", func() bool {
			n, skipped, _ := inst.counts()
			return n == 1 && skipped == 1
		})
		if len(pub.snapshot()) != 1 {
			t.Fatalf("batches = %d", len(pub.snapshot()))
		}
	})
}

func TestCommonProjectionStagesOnlyASelectedScope(t *testing.T) {
	payload := []byte{0, 1, 0, 2, 0, 0, 50, 90}
	selection := func(scope string) string {
		return `{"profile":"growatt-bms-can-common","selection":{"scope":"` + scope + `","flavor":"pcs-standard","project_code":"QAAA","release":"0006","direction":"bms-to-pcs","bank":"ordinary","endpoint":"coordinator","epoch":1}}`
	}
	t.Run("unknown scope", func(t *testing.T) {
		pub := &recordingPublisher{}
		inst, r := startFeed(t, "a", "can0", 3, `{"profile":"growatt-bms-can-common"}`, pub)
		r.push(1, 0x313, false, payload...)
		waitFor(t, "frame", func() bool { n, _, _ := inst.counts(); return n == 1 })
		time.Sleep(20 * time.Millisecond)
		if len(pub.snapshot()) != 0 {
			t.Fatal("unknown scope staged a candidate")
		}
	})
	for _, scope := range []string{"local", "aggregate"} {
		t.Run(scope, func(t *testing.T) {
			pub := &recordingPublisher{}
			_, r := startFeed(t, "a", "can0", 3, selection(scope), pub)
			r.push(1, 0x313, false, payload...)
			batch := waitBatches(t, pub, 1)[0]
			assertQuantity(t, batch.FactUpserts, "storage.state.soc", "unit.ratio", "0.5")
			assertQuantity(t, batch.FactUpserts, "storage.pack.voltage", "unit.volt", "0.01")
			assertQuantity(t, batch.FactUpserts, "storage.pack.current", "unit.ampere", "0.2")
			assertNoAccuracy(t, batch.FactUpserts)
		})
	}
	t.Run("short frame", func(t *testing.T) {
		pub := &recordingPublisher{}
		inst, r := startFeed(t, "a", "can0", 3, selection("local"), pub)
		r.push(1, 0x311, false, 1, 2, 3)
		waitFor(t, "frame", func() bool { _, _, latest := inst.counts(); return latest != nil })
		time.Sleep(20 * time.Millisecond)
		if len(pub.snapshot()) != 0 {
			t.Fatal("short frame staged a candidate")
		}
	})
}

func TestV108PublishesSOHTheV104GateWithholds(t *testing.T) {
	pub := &recordingPublisher{}
	_, r := startFeed(t, "a", "can0", 3, `{"profile":"growatt-bms-can-v108"}`, pub)
	r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	batch := waitBatches(t, pub, 1)[0]
	assertQuantity(t, batch.FactUpserts, "storage.state.soh", "unit.ratio", "0.9")
	assertQuantity(t, batch.FactUpserts, "storage.state.soc", "unit.ratio", "0.5")
	assertNoAccuracy(t, batch.FactUpserts)
}

func TestStagedFreshnessStaysUnknown(t *testing.T) {
	pub := &recordingPublisher{}
	_, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
	r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	r.push(5000, 0x313, false, 0, 1, 0, 2, 0, 0, 40, 90)
	batches := waitBatches(t, pub, 2)
	for i, batch := range batches {
		fact := factByID(batch.FactUpserts, "storage.state.soc")
		if fact == nil {
			t.Fatalf("batch %d has no state of charge", i)
		}
		if fact.Quality.Freshness != semreg.FreshnessUnknown || fact.FreshnessPolicy.FreshForNS != "1" || fact.FreshnessPolicy.RetainForNS != "2" {
			t.Fatalf("batch %d freshness %+v policy %+v", i, fact.Quality.Freshness, fact.FreshnessPolicy)
		}
	}
	if batches[0].FactUpserts[0].Times.ReceiptMonotonic == batches[1].FactUpserts[0].Times.ReceiptMonotonic {
		t.Fatal("monotonic receipt did not follow the transport record")
	}
	assertQuantity(t, batches[1].FactUpserts, "storage.state.soc", "unit.ratio", "0.4")
}

func TestPackSubjectFollowsTheInstanceKey(t *testing.T) {
	// Subject text is can/<instance>/battery. Each comparison catches one
	// production mutation:
	//   - instance key replaced by the interface name, a counter or the other instance
	//   - kind replaced by pack, module or cell
	//   - an anchor, slot or sequence appended
	// CandidateID, BindingID, SourceID, SourceEpochID and OriginID each include
	// the instance and stay put when only the transport sequence changes. Folding
	// those identifiers together, or putting the sequence into the candidate
	// identity, fails the comparison of the two frames on the left instance.
	leftPub, rightPub := &recordingPublisher{}, &recordingPublisher{}
	leftRx, rightRx := newFakeReceiver(t, "can0", 3), newFakeReceiver(t, "can1", 4)
	f := NewFactory()
	f.open = func(name string) (receiver.Receiver, error) {
		if name == "can1" {
			return rightRx, nil
		}
		return leftRx, nil
	}
	start := func(key, iface string, pub *recordingPublisher) {
		t.Helper()
		plans, err := f.Plan(config(key, `{"interface":"`+iface+`"}`, v104PayloadProfile))
		if err != nil || len(plans) != 1 {
			t.Fatalf("plan %s: %v %v", key, plans, err)
		}
		built, err := f.Construct(context.Background(), plans[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := built.Start(context.Background(), feedSession{pub: pub}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { built.Stop(context.Background()) })
	}
	start("left", "can0", leftPub)
	start("right", "can1", rightPub)
	leftRx.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	rightRx.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	leftRx.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
	left := waitBatches(t, leftPub, 2)
	right := waitBatches(t, rightPub, 1)
	leftFact := factByID(left[0].FactUpserts, "storage.state.soc")
	again := factByID(left[1].FactUpserts, "storage.state.soc")
	rightFact := factByID(right[0].FactUpserts, "storage.state.soc")
	if leftFact == nil || again == nil || rightFact == nil {
		t.Fatal("missing state of charge")
	}
	if subjectText(t, *leftFact) != "can/left/battery" || subjectText(t, *rightFact) != "can/right/battery" {
		t.Fatalf("subjects %s %s", subjectText(t, *leftFact), subjectText(t, *rightFact))
	}
	if leftFact.CandidateID != again.CandidateID || *leftFact.BindingID != *again.BindingID || *leftFact.SourceEpochID != *again.SourceEpochID || leftFact.Origin.OriginID != again.Origin.OriginID || *leftFact.Origin.SourceID != *again.Origin.SourceID {
		t.Fatal("identity changed when only the sequence changed")
	}
	if leftFact.Revision == again.Revision {
		t.Fatal("revision did not follow the transport sequence")
	}
	if leftFact.CandidateID == rightFact.CandidateID || *leftFact.BindingID == *rightFact.BindingID {
		t.Fatal("two instances share an identity")
	}
	ids := []string{string(leftFact.CandidateID), string(*leftFact.BindingID), string(*leftFact.Origin.SourceID), string(*leftFact.SourceEpochID), string(leftFact.Origin.OriginID)}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || !bytes.Contains([]byte(id), []byte("left")) {
			t.Fatalf("identifier %q", id)
		}
		seen[id] = true
	}
}

func TestFencedNilAndRefusedPublisherStageNothing(t *testing.T) {
	t.Run("nil session", func(t *testing.T) {
		r := newFakeReceiver(t, "can0", 3)
		inst := startedInstance(t, factoryWith(r, nil), config("a", `{"interface":"can0"}`, v104PayloadProfile))
		r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
		waitFor(t, "frame", func() bool { n, _, _ := inst.counts(); return n == 1 })
	})
	t.Run("fenced", func(t *testing.T) {
		pub := &recordingPublisher{}
		inst, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
		r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
		waitBatches(t, pub, 1)
		inst.FenceWithdrawal()
		r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 40, 90)
		time.Sleep(20 * time.Millisecond)
		if n, _, _ := inst.counts(); n != 1 || len(pub.snapshot()) != 1 {
			t.Fatalf("received %d batches %d", n, len(pub.snapshot()))
		}
	})
	for _, name := range []string{"begin", "stage", "commit"} {
		t.Run(name, func(t *testing.T) {
			pub := &recordingPublisher{}
			switch name {
			case "begin":
				pub.beginErr = errors.New("begin refused")
			case "stage":
				pub.stageErr = errors.New("stage refused")
			default:
				pub.commitErr = errors.New("commit refused")
			}
			inst, r := startFeed(t, "a", "can0", 3, v104PayloadProfile, pub)
			r.push(1, 0x313, false, 0, 1, 0, 2, 0, 0, 50, 90)
			r.push(2, 0x313, false, 0, 1, 0, 2, 0, 0, 40, 90)
			waitFor(t, "frames", func() bool { n, _, _ := inst.counts(); return n == 2 })
			time.Sleep(20 * time.Millisecond)
			if len(pub.snapshot()) != 0 {
				t.Fatalf("%s recorded %d batches", name, len(pub.snapshot()))
			}
		})
	}
}

func TestMonitorStagesTheBoundVoltageAndCurrent(t *testing.T) {
	pub := &recordingPublisher{}
	_, r := startFeed(t, "a", "can0", 3, `{"profile":"growatt-bms-can-monitor"}`, pub)
	r.push(1, 0x04010001, true, 0x64, 0x00, 0x3a, 0x75, 0, 0, 0, 0)
	batch := waitBatches(t, pub, 1)[0]
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	assertQuantity(t, batch.FactUpserts, "storage.pack.voltage", "unit.volt", "10")
	assertQuantity(t, batch.FactUpserts, "storage.pack.current", "unit.ampere", "1")
	assertNoAccuracy(t, batch.FactUpserts)
	if subjectText(t, batch.FactUpserts[0]) != "can/a/battery" {
		t.Fatalf("subject %s", subjectText(t, batch.FactUpserts[0]))
	}
}
