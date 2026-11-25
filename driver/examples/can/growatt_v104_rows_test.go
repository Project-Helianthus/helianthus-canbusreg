//go:build linux

package can

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"reflect"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
	"golang.org/x/sys/unix"
)

type wireVector struct {
	Name          string
	Identifier    uint32
	Extended, RTR bool
	DLC           uint8
	RawDLC        uint8  `json:"raw_dlc"`
	Data          string `json:"data_hex"`
}

func (v wireVector) record(t *testing.T) []byte {
	t.Helper()
	p, err := hex.DecodeString(v.Data)
	if err != nil || len(p) < int(v.DLC) || len(p) > 8 {
		t.Fatalf("bad vector length: %+v: %v", v, err)
	}
	raw := make([]byte, 16)
	id := v.Identifier
	if v.Extended {
		id |= unix.CAN_EFF_FLAG
	}
	if v.RTR {
		id |= unix.CAN_RTR_FLAG
	}
	binary.NativeEndian.PutUint32(raw, id)
	raw[4] = v.DLC
	if v.RawDLC > 8 {
		raw[7] = v.RawDLC
	}
	copy(raw[8:], p)
	return raw
}

func receiveVectors(t *testing.T, frames ...wireVector) []receiver.Record {
	t.Helper()
	var n int
	r, err := receiver.OpenWith("synthetic", receiver.Syscalls{
		LookupInterface: func(name string) (*net.Interface, error) { return &net.Interface{Name: name, Index: 1}, nil },
		OpenRaw:         func(int, int, int) (int, error) { return 17, nil },
		Bind:            func(int, unix.Sockaddr) error { return nil },
		Poll:            func(fds []unix.PollFd, _ int) (int, error) { fds[0].Revents = unix.POLLIN; return 1, nil },
		Read:            func(_ int, dst []byte) (int, error) { raw := frames[n].record(t); n++; return copy(dst, raw), nil },
		Close:           func(int) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	var got []receiver.Record
	for i, frame := range frames {
		record, err := r.Receive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(record.Raw, frame.record(t)) || record.Interface != r.Identity() || record.Sequence != uint64(i+1) {
			t.Fatalf("receive evidence differs for %s: %+v", frame.Name, record)
		}
		got = append(got, record)
	}
	return got
}

func readFixture[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

type canVector struct {
	wireVector
	Group          string
	Native         map[string]int64
	Facts          map[string]string
	ExpectedReason roweval.Reason `json:"reason"`
	GatewayAccepts *bool          `json:"gateway_accepts"`
	Unknown        map[string]struct {
		Reason roweval.Reason
		Raw    uint64
	}
}

func TestGrowattV104Signed(t *testing.T)   { checkVectors(t, "signed") }
func TestGrowattV104Validity(t *testing.T) { checkVectors(t, "validity") }
func TestGrowattV104CountBound(t *testing.T) {
	checkVectors(t, "count")
	compareIndexedWitnesses(t)
}
func TestGrowattV104NoWayBack(t *testing.T) {
	checkVectors(t, "limits")
	table := v104Rows(canDescriptors())
	for _, row := range table.Native {
		if row.At.Identifier == 0x311 {
			assertNoWrite(t, table, row.ID, roweval.ReasonNoMatch)
		}
	}
}

func checkVectors(t *testing.T, group string) {
	t.Helper()
	vectors := readFixture[[]canVector](t, "testdata/growatt_v104_vectors.json")
	roles := exampleRoles(t)
	if err := roweval.ValidateTable(v104Rows(canDescriptors()), canDescriptors(), roles); err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, v := range vectors {
		if v.Group != group {
			continue
		}
		matches++
		t.Run(v.Name, func(t *testing.T) {
			r := receiveVectors(t, v.wireVector)[0]
			a := acquired(r, roles)
			got, reason, err := evaluateV104(r, a)
			if err != nil || reason != v.ExpectedReason || len(got) != len(v.Native)+len(v.Unknown) {
				t.Fatalf("reason=%q err=%v results=%+v", reason, err, got)
			}
			for name, n := range v.Native {
				ev := got[name]
				assertNative(t, ev, name, fmt.Sprint(n), a.Template)
				if text, ok := v.Facts[name]; ok {
					assertFact(t, ev, name, text, a)
				} else if ev.Candidate != nil || ev.Subject != nil || ev.Reason != roweval.ReasonNoBinding {
					t.Fatalf("native-only %s: %+v", name, ev)
				}
			}
			for name, want := range v.Unknown {
				ev := got[name]
				if ev.Native == nil || ev.Native.RowID != name || ev.Native.Value != nil || ev.Native.Raw != want.Raw || ev.Native.Reason != want.Reason || ev.Reason != want.Reason || ev.Subject != nil || ev.Candidate != nil || !reflect.DeepEqual(ev.Native.Evidence, a.Template.Evidence) {
					t.Fatalf("unknown %s: %+v", name, ev)
				}
			}
			if v.GatewayAccepts != nil {
				assertNativeCount(t, r, reason == "")
				t.Logf("frame %s: V1.04=%t frozen classifier=%t historical gateway=%t", v.Data, reason == "", reason == "", *v.GatewayAccepts)
			}
		})
	}
	if matches == 0 {
		t.Fatalf("missing %s vectors", group)
	}
}

func assertNoWrite(t *testing.T, table roweval.Table[Selector], row string, want roweval.Reason) {
	t.Helper()
	got, err := roweval.Invert(table, roweval.Writing[Selector]{NativeID: row, SubjectRole: "pack", Descriptors: canDescriptors()})
	if err != nil || got.Reason != want || got.Payload != nil || got.Target != "" {
		t.Fatalf("%s: write %+v: %v", row, got, err)
	}
}

func assertNative(t *testing.T, ev roweval.Evaluation, name, number string, template semreg.FactCandidate) {
	t.Helper()
	n := ev.Native
	if n == nil || n.RowID != name || n.Reason != "" || !reflect.DeepEqual(n.Evidence, template.Evidence) || n.Value == nil || n.Value.Quantity == nil || !decimalEquals(n.Value.Quantity.Number, number) {
		t.Fatalf("native %s expected %s with wire evidence: %+v", name, number, n)
	}
}

func decimalEquals(number semreg.Decimal, text string) bool {
	got, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", number.Coefficient, number.Exponent10))
	want, valid := new(big.Rat).SetString(text)
	return ok && valid && got.Cmp(want) == 0
}

func assertFact(t *testing.T, ev roweval.Evaluation, name, text string, a roweval.Acquired[Selector]) {
	t.Helper()
	fields := map[string]struct{ id, unit semreg.DefinitionID }{
		"current": {"storage.pack.current", "unit.ampere"}, "state_of_charge": {"storage.state.soc", "unit.ratio"},
		"state_of_health": {"storage.state.soh", "unit.ratio"}, "pack_voltage": {"storage.pack.voltage", "unit.volt"},
	}
	if ev.Reason != "" || ev.Candidate == nil || ev.Candidate.Value == nil || ev.Candidate.Value.Quantity == nil {
		t.Fatalf("missing %s candidate: %+v", name, ev)
	}
	got := *ev.Candidate
	want := fields[name]
	roleText := "pack"
	key := semreg.FactKey{PackID: "helianthus.pack.storage", PackVersion: "1.1.0", FactID: want.id, Dimensions: []semreg.Dimension{{ID: "storage.dimension.pack", Value: semreg.Value{Kind: semreg.ValueText, Text: &roleText}}}}
	pack := semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}
	capability := semreg.CapabilityInstanceID("capability.synthetic.pack")
	subject := &roweval.Subject{Role: "pack", Service: semreg.DefinitionRef{Pack: pack, ID: "storage.service.pack"}, Capability: semreg.DefinitionRef{Pack: pack, ID: "storage.capability.read.pack"}, ServiceInstance: "service.synthetic.pack", CapabilityInstance: &capability}
	if !reflect.DeepEqual(got.Key, key) || !reflect.DeepEqual(ev.Subject, subject) || got.Value.Kind != semreg.ValueQuantity || got.Value.Quantity.Unit != want.unit || !decimalEquals(got.Value.Quantity.Number, text) {
		t.Fatalf("%s: candidate %+v, subject %+v", name, got, ev.Subject)
	}
	got.Key, got.Value = a.Template.Key, a.Template.Value
	if !reflect.DeepEqual(got, a.Template) {
		t.Fatalf("%s: changed caller-owned metadata: %+v vs %+v", name, got, a.Template)
	}
}

func acquired(r receiver.Record, roles roweval.Roles) roweval.Acquired[Selector] {
	source, epoch, binding := semreg.SourceID("source.synthetic"), semreg.SourceEpochID("epoch.synthetic"), semreg.NativeBindingID("binding.synthetic")
	generation := semreg.Uint64("2")
	evidence := []semreg.EvidenceRef{{Owner: "driver.synthetic", Kind: "evidence.can.frame", Digest: semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(r.Raw))), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
	received := semreg.TimePoint{UnixNanoseconds: "100", ClockID: "clock.synthetic", UncertaintyNS: "0"}
	mono := semreg.MonotonicPoint{ClockEpochID: "clock:synthetic", Nanoseconds: semreg.Uint64(fmt.Sprint(r.Monotonic.Nanoseconds()))}
	template := semreg.FactCandidate{
		CandidateID: "candidate.synthetic", Revision: "41",
		Quality: semreg.Quality{Assertion: semreg.AssertionObserved, Qualification: semreg.QualificationCandidate, Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood, Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessFresh, Reasons: []semreg.DefinitionID{}},
		Times: semreg.Times{ReceivedAt: received, ReceiptMonotonic: mono, EvaluatedAt: semreg.TimePoint{UnixNanoseconds: "200", ClockID: "clock.synthetic", UncertaintyNS: "0"}, EvaluateMonotonic: mono,
			SourceAt: &semreg.TimePoint{UnixNanoseconds: "90", ClockID: "clock.synthetic", UncertaintyNS: "5"}, PhenomenonAt: &semreg.TimePoint{UnixNanoseconds: "80", ClockID: "clock.synthetic", UncertaintyNS: "7"}},
		FreshnessPolicy: semreg.FreshnessPolicy{PolicyID: "policy.can.example", Version: "1.0.0", FreshForNS: "1000", RetainForNS: "2000", MaxWallUncertaintyNS: "0"},
		BindingID:       &binding, SourceEpochID: &epoch, DriverGeneration: &generation, Evidence: evidence,
		Origin: semreg.OriginRef{OriginID: "origin.synthetic", Kind: semreg.OriginNativeObservation, SourceID: &source, SourceEpochID: &epoch, BindingID: &binding, Evidence: evidence},
	}
	return roweval.Acquired[Selector]{Template: template, Roles: roles, Receipt: roweval.Receipt{ReceivedAt: received, Monotonic: mono}}
}

func exampleRoles(t *testing.T) roweval.Roles {
	t.Helper()
	type catalogRef struct {
		ID    semreg.DefinitionID
		Owner struct{ Pack semreg.PackRef }
	}
	catalog := readFixture[struct {
		Elements []struct {
			Ref        catalogRef
			Kind       string `json:"element_kind"`
			Attributes struct {
				FactKeyDimension catalogRef `json:"fact_key_dimension"`
			}
		}
	}](t, "../../../driverdata/families/mapping-gates/element-catalog.json")
	index := semreg.DefinitionIndex{Pack: semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}}
	for _, e := range catalog.Elements {
		if e.Ref.Owner.Pack != index.Pack {
			continue
		}
		ref := semreg.DefinitionRef{Pack: index.Pack, ID: e.Ref.ID}
		switch e.Kind {
		case "service":
			if e.Ref.ID != "storage.service.pack" {
				continue
			}
			if e.Attributes.FactKeyDimension.ID != "storage.dimension.pack" || e.Attributes.FactKeyDimension.Owner.Pack != index.Pack {
				t.Fatal("catalog pack dimension differs")
			}
			index.Services = append(index.Services, ref)
		case "capability":
			index.Capabilities = append(index.Capabilities, ref)
		case "fact":
			index.Fields = append(index.Fields, ref)
		}
	}
	capability := semreg.CapabilityInstanceID("capability.synthetic.pack")
	return roweval.Roles{Subjects: map[string]roweval.RoleBinding{"pack": {ServiceInstance: "service.synthetic.pack", CapabilityInstance: &capability}}, Catalog: []semreg.DefinitionIndex{index}}
}
