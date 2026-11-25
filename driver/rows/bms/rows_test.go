package bms

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// canEFFFlag is the socketcan extended-frame flag of the record identifier.
const canEFFFlag = 0x80000000

// vector is one synthetic frame and its independent expectation. Frame is
// "admitted" (every native row of the frame is listed in Native, and the facts
// of the bound elements are listed in Facts), "rejected" (the frame is listed
// and fails admission: every native row of the frame carries the rejection
// and its evidence, and no value or candidate), or "no_match" (no native row
// of the table applies to the frame).
type vector struct {
	Name       string            `json:"name"`
	Identifier uint32            `json:"identifier"`
	Extended   bool              `json:"extended"`
	DLC        uint8             `json:"dlc"`
	Data       string            `json:"data_hex"`
	Frame      string            `json:"frame"`
	Native     map[string]int64  `json:"native"`
	Facts      map[string]string `json:"facts"`
}

// record builds the 16-byte socketcan record the canbus decoder reads.
func (v vector) record(t *testing.T) []byte {
	t.Helper()
	p, err := hex.DecodeString(v.Data)
	if err != nil || len(p) > 8 || len(p) < int(v.DLC) {
		t.Fatalf("%s: bad data: %v", v.Name, err)
	}
	raw := make([]byte, 16)
	id := v.Identifier
	if v.Extended {
		id |= canEFFFlag
	}
	binary.NativeEndian.PutUint32(raw, id)
	raw[4] = v.DLC
	copy(raw[8:], p)
	raw[7] = 0
	return raw
}

func readVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/v104_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []vector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestV104VectorsMatchTheirExpectations(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	if err := roweval.ValidateTable(table, V104Descriptors(), roles); err != nil {
		t.Fatal(err)
	}
	for _, v := range readVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			raw := v.record(t)
			frame, err := canbus.DecodeSocketCANRecord(raw)
			if err != nil {
				t.Fatal(err)
			}
			a := acquired(raw, roles)
			seenNative := map[string]bool{}
			seenFact := map[string]bool{}
			for _, row := range table.Native {
				name := strings.TrimPrefix(row.ID, Profile+".")
				ev, err := evaluateRow(table, frame, row, a)
				if err != nil {
					t.Fatal(err)
				}
				belongs := row.At.Identifier == frame.ID().Value() && row.At.Extended == frame.ID().Extended() &&
					row.At.DLC == frame.DLC() && row.At.RawDLC == frame.RawDLC()
				if v.Frame == "no_match" {
					if ev.Reason != roweval.ReasonNoMatch || ev.Native != nil || ev.Candidate != nil {
						t.Fatalf("%s: frame must not match any row: reason %q native %+v candidate %+v", name, ev.Reason, ev.Native, ev.Candidate)
					}
					continue
				}
				if !belongs {
					// Another frame's row: its slice may coincide with this one, so
					// the row is checked on its own frame only.
					continue
				}
				if ev.Native == nil || ev.Native.RowID != row.ID {
					t.Fatalf("%s: no native evaluation: %+v", name, ev)
				}
				if !reflect.DeepEqual(ev.Native.Evidence, a.Template.Evidence) {
					t.Fatalf("%s: native evaluation lost the raw source record", name)
				}
				if v.Frame == "rejected" {
					if ev.Native.Reason != "not valid" || ev.Native.Value != nil || ev.Reason != "not valid" || ev.Candidate != nil {
						t.Fatalf("%s: rejected frame gave value %+v reason %q candidate %+v", name, ev.Native.Value, ev.Reason, ev.Candidate)
					}
					continue
				}
				if v.Frame != "admitted" {
					t.Fatalf("unknown frame disposition %q", v.Frame)
				}
				want, listed := v.Native[name]
				if !listed {
					t.Fatalf("%s: admitted frame has no expected native value", name)
				}
				seenNative[name] = true
				n := ev.Native
				if n.Reason != "" || n.Value == nil || n.Value.Quantity == nil || !decimalEquals(n.Value.Quantity.Number, fmt.Sprint(want)) {
					t.Fatalf("%s: want native %d, got reason %q value %+v", name, want, n.Reason, n.Value)
				}
				fact, bound := v.Facts[factFor(name)]
				if !bound {
					if ev.Candidate != nil {
						t.Fatalf("%s: unexpected candidate %+v", name, ev.Candidate)
					}
					continue
				}
				seenFact[factFor(name)] = true
				c := ev.Candidate
				if c == nil || c.Key.FactID != semreg.DefinitionID(factFor(name)) || c.Value == nil || c.Value.Quantity == nil ||
					!decimalEquals(c.Value.Quantity.Number, fact) || c.Value.Quantity.Unit != bindingUnit(table, row.ID) {
					t.Fatalf("%s: want candidate %s = %s, got %+v reason %q", name, factFor(name), fact, c, ev.Reason)
				}
				if ev.Subject == nil || ev.Subject.Role != packRole {
					t.Fatalf("%s: candidate subject %+v", name, ev.Subject)
				}
				if !reflect.DeepEqual(c.Quality, a.Template.Quality) || !reflect.DeepEqual(c.Evidence, a.Template.Evidence) {
					t.Fatalf("%s: candidate lost its quality or evidence", name)
				}
			}
			if v.Frame == "admitted" {
				for name := range v.Native {
					if !seenNative[name] {
						t.Fatalf("expected native %s was not evaluated", name)
					}
				}
				for fact := range v.Facts {
					if !seenFact[fact] {
						t.Fatalf("expected fact %s was not emitted", fact)
					}
				}
			}
		})
	}
}

// bindingUnit is the unit the binding of a native row declares for its fact.
func bindingUnit(table roweval.Table[Selector], nativeID string) semreg.DefinitionID {
	for _, b := range table.Bindings {
		if b.NativeID == nativeID && b.Read != nil {
			return b.Read.Unit
		}
	}
	return ""
}

// factFor maps a native row name to the storage fact it feeds. Rows without a
// binding map to an empty fact name.
func factFor(name string) string {
	switch name {
	case "pack_current":
		return "storage.pack.current"
	case "pack_voltage":
		return "storage.pack.voltage"
	case "soc":
		return "storage.state.soc"
	case "soh":
		return "storage.state.soh"
	}
	return ""
}

func evaluateRow(table roweval.Table[Selector], frame canbus.Frame, row roweval.NativeRow[Selector], a roweval.Acquired[Selector]) (roweval.Evaluation, error) {
	a.Selector = Selector{Identifier: frame.ID().Value(), Extended: frame.ID().Extended(), DLC: frame.DLC(), RawDLC: frame.RawDLC(), Slice: row.At.Slice}
	a.Payload = frame.Data()
	a.Descriptors = V104Descriptors()
	return roweval.Evaluate(table, a)
}

func decimalEquals(number semreg.Decimal, text string) bool {
	got, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", number.Coefficient, number.Exponent10))
	want, valid := new(big.Rat).SetString(text)
	return ok && valid && got.Cmp(want) == 0
}

func acquired(raw []byte, roles roweval.Roles) roweval.Acquired[Selector] {
	source, epoch, binding := semreg.SourceID("source.synthetic"), semreg.SourceEpochID("epoch.synthetic"), semreg.NativeBindingID("binding.synthetic")
	generation := semreg.Uint64("2")
	evidence := []semreg.EvidenceRef{{Owner: "driver.synthetic", Kind: "evidence.can.frame", Digest: semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(raw))), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
	received := semreg.TimePoint{UnixNanoseconds: "100", ClockID: "clock.synthetic", UncertaintyNS: "0"}
	mono := semreg.MonotonicPoint{ClockEpochID: "clock:synthetic", Nanoseconds: semreg.Uint64("1")}
	template := semreg.FactCandidate{
		CandidateID: "candidate.synthetic", Revision: "41",
		Quality: semreg.Quality{Assertion: semreg.AssertionObserved, Qualification: semreg.QualificationCandidate, Promotion: semreg.PromotionUnpromoted, Validity: semreg.ValidityGood, Availability: semreg.AvailabilityAvailable, Freshness: semreg.FreshnessFresh, Reasons: []semreg.DefinitionID{}},
		Times: semreg.Times{ReceivedAt: received, ReceiptMonotonic: mono, EvaluatedAt: semreg.TimePoint{UnixNanoseconds: "200", ClockID: "clock.synthetic", UncertaintyNS: "0"}, EvaluateMonotonic: mono,
			SourceAt: &semreg.TimePoint{UnixNanoseconds: "90", ClockID: "clock.synthetic", UncertaintyNS: "5"}, PhenomenonAt: &semreg.TimePoint{UnixNanoseconds: "80", ClockID: "clock.synthetic", UncertaintyNS: "7"}},
		FreshnessPolicy: semreg.FreshnessPolicy{PolicyID: "policy.can.example", Version: "1.0.0", FreshForNS: "1000", RetainForNS: "2000", MaxWallUncertaintyNS: "0"},
		BindingID:       &binding, SourceEpochID: &epoch, DriverGeneration: &generation, Evidence: evidence,
		Origin: semreg.OriginRef{OriginID: "origin.synthetic", Kind: semreg.OriginNativeObservation, SourceID: &source, SourceEpochID: &epoch, BindingID: &binding, Evidence: evidence},
	}
	return roweval.Acquired[Selector]{Template: template, Roles: roles, Receipt: roweval.Receipt{ReceivedAt: received, Monotonic: mono}, Payload: raw[8:]}
}

// storageRoles reads the storage pack's service, capability and fact
// definitions from the element catalog of this repository.
func storageRoles(t *testing.T) roweval.Roles {
	t.Helper()
	type catalogRef struct {
		ID    semreg.DefinitionID
		Owner struct{ Pack semreg.PackRef }
	}
	raw, err := os.ReadFile("../../../driverdata/families/mapping-gates/element-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Elements []struct {
			Ref        catalogRef
			Kind       string `json:"element_kind"`
			Attributes struct {
				FactKeyDimension catalogRef `json:"fact_key_dimension"`
			}
		}
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	index := semreg.DefinitionIndex{Pack: semreg.PackRef{ID: "helianthus.pack.storage", Version: "1.1.0"}}
	for _, e := range catalog.Elements {
		if e.Ref.Owner.Pack != index.Pack {
			continue
		}
		ref := semreg.DefinitionRef{Pack: index.Pack, ID: e.Ref.ID}
		switch e.Kind {
		case "service":
			if e.Ref.ID == "storage.service.pack" {
				index.Services = append(index.Services, ref)
			}
		case "capability":
			index.Capabilities = append(index.Capabilities, ref)
		case "fact":
			index.Fields = append(index.Fields, ref)
		}
	}
	capability := semreg.CapabilityInstanceID("capability.synthetic.pack")
	return roweval.Roles{Subjects: map[string]roweval.RoleBinding{"pack": {ServiceInstance: "service.synthetic.pack", CapabilityInstance: &capability}}, Catalog: []semreg.DefinitionIndex{index}}
}
