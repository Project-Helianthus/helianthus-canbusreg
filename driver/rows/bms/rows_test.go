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
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// canEFFFlag is the socketcan extended-frame flag of the record identifier.
const canEFFFlag = 0x80000000

// vector is one synthetic frame and its independent expectation, written by
// hand from the V1.04 page and the frozen decoder's rules. Frame is "admitted"
// (every native row of the frame is listed in Native, and the candidates of the
// bound elements are listed in Facts with their own unit and subject),
// "rejected" (the frame fails the frame gate: no native row is evaluated and no
// candidate is produced), or "no_match" (the frame applies to no row of the
// table). DecoderParity false excludes a vector from the frozen decoder check
// only where the frozen decoder's own geometry rule does not test the field.
type vector struct {
	Name          string           `json:"name"`
	Identifier    uint32           `json:"identifier"`
	Extended      bool             `json:"extended"`
	DLC           uint8            `json:"dlc"`
	RawDLC        uint8            `json:"raw_dlc"`
	Data          string           `json:"data_hex"`
	Frame         string           `json:"frame"`
	Native        map[string]int64 `json:"native"`
	Facts         map[string]fact  `json:"facts"`
	DecoderParity *bool            `json:"decoder_parity"`
}

// fact is the expected candidate of one storage fact: its exact SI decimal,
// its unit and its subject role, written independently of the rows.
type fact struct {
	Value   string `json:"value"`
	Unit    string `json:"unit"`
	Subject string `json:"subject"`
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
	raw[7] = v.RawDLC
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
			evals, reason, err := EvaluateFrame(table, frame, a)
			if err != nil {
				t.Fatal(err)
			}
			switch v.Frame {
			case "no_match":
				if reason != roweval.ReasonNoMatch || evals != nil {
					t.Fatalf("frame must match no row: reason %q evaluations %d", reason, len(evals))
				}
			case "rejected":
				if reason != "not valid" || evals != nil {
					t.Fatalf("rejected frame: reason %q evaluations %d, want %q and none", reason, len(evals), "not valid")
				}
				if len(a.Template.Evidence) != 1 || a.Template.Evidence[0].Digest != digestOf(raw) {
					t.Fatalf("rejected frame lost its raw source record")
				}
			case "admitted":
				if reason != "" {
					t.Fatalf("admitted frame rejected: %q", reason)
				}
				checkAdmitted(t, v, table, frame, a, evals)
			default:
				t.Fatalf("unknown frame disposition %q", v.Frame)
			}
			checkDecoderParity(t, v, frame)
		})
	}
}

// checkAdmitted compares every native row of the frame and every candidate with
// the vector's own expectation.
func checkAdmitted(t *testing.T, v vector, table roweval.Table[Selector], frame canbus.Frame, a roweval.Acquired[Selector], evals map[string]roweval.Evaluation) {
	t.Helper()
	seenFact := map[string]bool{}
	seenID := map[semreg.CandidateID]bool{}
	rows := 0
	for _, row := range table.Native {
		if row.At.Identifier != frame.ID().Value() {
			continue
		}
		rows++
		name := rowName(row.ID)
		want, listed := v.Native[name]
		if !listed {
			t.Fatalf("%s: admitted frame has no expected native value", name)
		}
		ev, ok := evals[row.ID]
		if !ok || ev.Native == nil || ev.Native.RowID != row.ID {
			t.Fatalf("%s: no native evaluation", name)
		}
		n := ev.Native
		if n.Reason != "" || n.Value == nil || n.Value.Quantity == nil || !decimalEquals(n.Value.Quantity.Number, fmt.Sprint(want)) {
			t.Fatalf("%s: want native %d, got reason %q value %+v", name, want, n.Reason, n.Value)
		}
		if !reflect.DeepEqual(n.Evidence, a.Template.Evidence) {
			t.Fatalf("%s: native evaluation lost the raw source record", name)
		}
		f, bound := v.Facts[factFor(name)]
		if !bound {
			if ev.Candidate != nil {
				t.Fatalf("%s: unexpected candidate %+v", name, ev.Candidate)
			}
			continue
		}
		seenFact[factFor(name)] = true
		c := ev.Candidate
		if c == nil || c.Key.FactID != semreg.DefinitionID(factFor(name)) || c.Value == nil || c.Value.Quantity == nil ||
			!decimalEquals(c.Value.Quantity.Number, f.Value) || string(c.Value.Quantity.Unit) != f.Unit {
			t.Fatalf("%s: want candidate %s = %s %s, got %+v reason %q", name, factFor(name), f.Value, f.Unit, c, ev.Reason)
		}
		if seenID[c.CandidateID] {
			t.Fatalf("%s: candidate identity %q is shared within one frame", name, c.CandidateID)
		}
		seenID[c.CandidateID] = true
		if ev.Subject == nil || string(ev.Subject.Role) != f.Subject {
			t.Fatalf("%s: candidate subject %+v, want %s", name, ev.Subject, f.Subject)
		}
		if !reflect.DeepEqual(c.Quality, a.Template.Quality) || !reflect.DeepEqual(c.Evidence, a.Template.Evidence) {
			t.Fatalf("%s: candidate lost its quality or evidence", name)
		}
	}
	if rows != len(v.Native) {
		t.Fatalf("vector lists %d native values, the frame has %d rows", len(v.Native), rows)
	}
	for fact := range v.Facts {
		if !seenFact[fact] {
			t.Fatalf("expected fact %s was not emitted", fact)
		}
	}
}

// TestNegativePackVoltageKeepsNativeAndWithholdsCandidate pins that a negative
// pack voltage keeps its signed native value while no storage candidate is
// produced: the pinned storage catalog bounds storage.pack.voltage at zero.
func TestNegativePackVoltageKeepsNativeAndWithholdsCandidate(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	evals, reason, err := EvaluateFrame(table, frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	ev := evals[Profile+".pack_voltage"]
	if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, "-100") {
		t.Fatalf("native pack voltage lost its signed value: %+v", ev.Native)
	}
	if ev.Candidate != nil || ev.Reason != roweval.ReasonOutsideDomain {
		t.Fatalf("negative pack voltage gave candidate %+v reason %q", ev.Candidate, ev.Reason)
	}
}

// checkDecoderParity runs the frozen canbusreg V1.04 decoder on the same bytes,
// with the other two frames of the profile in valid fixed form, and compares
// its output with the vector. A frame the decoder rejects yields no projection.
func checkDecoderParity(t *testing.T, v vector, frame canbus.Frame) {
	t.Helper()
	if v.DecoderParity != nil && !*v.DecoderParity {
		return
	}
	projection, ok := decoderProjection(t, frame)
	id := frame.ID().Value()
	switch v.Frame {
	case "admitted":
		if !ok {
			t.Fatalf("frozen decoder gives no projection for an admitted frame")
		}
		for name, want := range v.Native {
			if got := projection[name]; got != want {
				t.Fatalf("frozen decoder %s = %d, vector %d", name, got, want)
			}
		}
	case "rejected":
		if ok {
			t.Fatalf("frozen decoder admits a frame the gate rejects")
		}
	case "no_match":
		if (id == 0x311 || id == 0x312 || id == 0x313) && ok {
			t.Fatalf("frozen decoder admits a frame the table does not match")
		}
	}
}

// siblingData are fixed valid bytes for the two frames of the profile that the
// target frame does not replace.
var siblingData = map[uint32]string{
	0x311: "0000000000000000",
	0x312: "0000000001000001",
	0x313: "0000000000000000",
}

// decoderProjection feeds the frozen decoder the two sibling frames and then the
// target frame, and returns the target frame's fields as the decoder names them.
func decoderProjection(t *testing.T, target canbus.Frame) (map[string]int64, bool) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile := canbusreg.GrowattLowVoltageBMSV104()
	var last canbusreg.Classification
	seq := uint64(0)
	feed := func(frame canbus.Frame) {
		seq++
		e := canbusreg.Evidence{Frame: frame, Interface: iface, Sequence: seq}
		last = profile.Classify(e)
	}
	for _, id := range []uint32{0x311, 0x312, 0x313} {
		if id == target.ID().Value() {
			continue
		}
		s := vector{Identifier: id, DLC: 8, Data: siblingData[id]}
		f, err := canbus.DecodeSocketCANRecord(s.record(t))
		if err != nil {
			t.Fatal(err)
		}
		feed(f)
	}
	feed(target)
	if last.Projection == nil {
		return nil, false
	}
	p, ok := last.Projection.(canbusreg.GrowattLowVoltageBMSV104Projection)
	if !ok {
		t.Fatalf("frozen decoder projection has type %T", last.Projection)
	}
	word := func(b [2]byte) int64 { return int64(b[0])<<8 | int64(b[1]) }
	fields := map[string]int64{}
	switch target.ID().Value() {
	case 0x311:
		fields["charge_voltage_limit"] = int64(p.Limits.ChargeVoltageDecivolts)
		fields["charge_current_limit"] = int64(p.Limits.ChargeCurrentDeciamps)
		fields["discharge_current_limit"] = int64(p.Limits.DischargeCurrentDeciamps)
		fields["status_word"] = int64(p.Limits.RawStatus)
		fields["operating_state"] = int64(p.Limits.RawStatus & 3)
	case 0x312:
		fields["protection_flags"] = word(p.Status.Protection)
		fields["warning_flags"] = word(p.Status.Warning)
		fields["pack_count"] = int64(p.Status.PackCount)
		fields["manufacturer"] = word(p.Status.Manufacturer)
		fields["total_cell_count"] = int64(p.Status.TotalCellCount)
	case 0x313:
		m := p.Measurements
		fields["pack_voltage"] = int64(m.VoltageCentivolts)
		fields["pack_current"] = int64(m.CurrentDeciamps)
		fields["maximum_cell_temperature"] = int64(m.MaximumCellTemperatureDeciC)
		fields["soc"] = int64(m.SOCPercent)
		fields["soh"] = int64(m.SOHValue)
		if m.SOHValid {
			fields["soh_valid"] = 1
		}
	}
	return fields, true
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

func digestOf(raw []byte) semreg.Digest {
	return semreg.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(raw)))
}

func decimalEquals(number semreg.Decimal, text string) bool {
	got, ok := new(big.Rat).SetString(fmt.Sprintf("%se%d", number.Coefficient, number.Exponent10))
	want, valid := new(big.Rat).SetString(text)
	return ok && valid && got.Cmp(want) == 0
}

func acquired(raw []byte, roles roweval.Roles) roweval.Acquired[Selector] {
	source, epoch, binding := semreg.SourceID("source.synthetic"), semreg.SourceEpochID("epoch.synthetic"), semreg.NativeBindingID("binding.synthetic")
	generation := semreg.Uint64("2")
	evidence := []semreg.EvidenceRef{{Owner: "driver.synthetic", Kind: "evidence.can.frame", Digest: digestOf(raw), Contract: "native.can/v1", Access: semreg.EvidenceAccessPublic, Redaction: semreg.RedactionNone}}
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
