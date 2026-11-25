package bms

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/packs/storage"
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
		if !reflect.DeepEqual(c.FreshnessPolicy, a.Template.FreshnessPolicy) || !reflect.DeepEqual(c.Times, a.Template.Times) {
			t.Fatalf("%s: candidate lost its freshness policy or times", name)
		}
		if len(c.Key.Dimensions) != 1 || c.Key.Dimensions[0].Value.Text == nil || *c.Key.Dimensions[0].Value.Text != a.Coordinates[packSlot] {
			t.Fatalf("%s: candidate key %+v does not name the configured pack", name, c.Key)
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

// TestNegativePackVoltageKeepsNativeAndIsRejectedAtIngestion pins that a
// negative pack voltage keeps its signed native value and the signed pattern
// gives the candidate with no hook. The pinned storage catalog bounds
// storage.pack.voltage at zero, so the validator refuses that fact: the bound
// is a stop item of the package report, not a hook of the table.
func TestNegativePackVoltageKeepsNativeAndIsRejectedAtIngestion(t *testing.T) {
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
	if ev.Candidate == nil || ev.Reason != "" || !decimalEquals(ev.Candidate.Value.Quantity.Number, "-1.00") {
		t.Fatalf("negative pack voltage candidate %+v reason %q, want the signed -1.00 V", ev.Candidate, ev.Reason)
	}
	assertRejectedAtIngestion(t, ev.Candidate, semreg.BoundsExceeded)
}

// TestV104TableHooksAreOnlyTheSignedSOHGate pins that the table names one hook,
// the EX03 validity gate of the SOH binding, and no catalog-bound hook.
func TestV104TableHooksAreOnlyTheSignedSOHGate(t *testing.T) {
	table := V104Table()
	if len(table.Hooks) != 1 || table.Hooks[sohValidityHook] == nil {
		t.Fatalf("table hooks %v, want only %q", reflect.ValueOf(table.Hooks).MapKeys(), sohValidityHook)
	}
	for _, b := range table.Bindings {
		if b.Read != nil && b.Read.ToRegistry.Hook != "" && b.Read.ToRegistry.Hook != sohValidityHook {
			t.Fatalf("%s binds hook %q", b.NativeID, b.Read.ToRegistry.Hook)
		}
	}
}

// decoderUnnamed lists the native rows the frozen decoder names no field for.
// The page leaves bytes 4 to 7 of frame 0x321 unnamed: they stay raw rows and
// their expectation is the byte value of the vector alone.
var decoderUnnamed = map[string]bool{"reserved_bytes": true}

// checkDecoderParity runs the frozen canbusreg decoders on the same bytes and
// compares their output with the vector. The V1.04 profile gives frames 0x311
// to 0x313 (fed with the other two frames in valid fixed form); the V1.04 report
// decoder gives the named bits and the fields of frames 0x311 to 0x321. A frame
// a decoder rejects yields no projection or report.
func checkDecoderParity(t *testing.T, v vector, frame canbus.Frame) {
	t.Helper()
	if v.DecoderParity != nil && !*v.DecoderParity {
		return
	}
	projection, ok := decoderProjection(t, frame)
	report, reportOK := reportFields(t, frame)
	id := frame.ID().Value()
	switch v.Frame {
	case "admitted":
		if !reportOK {
			t.Fatalf("frozen report decoder gives no fields for an admitted frame")
		}
		fields := report
		for name, got := range projection {
			fields[name] = got
		}
		for name, want := range v.Native {
			got, named := fields[name]
			if !named {
				if decoderUnnamed[name] {
					continue
				}
				t.Fatalf("frozen decoders name no field %s", name)
			}
			if got != want {
				t.Fatalf("frozen decoder %s = %d, vector %d", name, got, want)
			}
		}
	case "rejected":
		if ok || reportOK {
			t.Fatalf("frozen decoder admits a frame the gate rejects")
		}
	case "no_match":
		if (id == 0x311 || id == 0x312 || id == 0x313) && ok {
			t.Fatalf("frozen decoder admits a frame the table does not match")
		}
	}
}

// reportFields returns the fields the frozen V1.04 report decoder gives for one
// frame, keyed by the row name the table uses for the same field. A raw field
// is its bytes read big-endian for a two-byte span and little-endian for a
// four-byte span, the integer the raw row extracts. The capacities are counts
// of 10 mAh in the decoder and raw counts in the table.
func reportFields(t *testing.T, target canbus.Frame) (map[string]int64, bool) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := canbusreg.NewGrowattBMSProfile(iface, canbusreg.BMSRevisionV104)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := profile.Classify(canbusreg.Evidence{Frame: target, Interface: iface, Sequence: 1}).Projection.(canbusreg.GrowattBMSReport)
	if !ok {
		return map[string]int64{}, false
	}
	out := make(map[string]int64, len(report.Fields))
	for name, f := range report.Fields {
		out[reportRowName(target.ID().Value(), name)] = reportValue(name, f)
	}
	return out, true
}

// reportRowName maps a report field name to the table's row name. The 0x320
// manufacturer bytes carry the identity row, not the 0x312 manufacturer row.
func reportRowName(id uint32, name string) string {
	switch {
	case id == 0x320 && name == "manufacturer":
		return "manufacturer_identity"
	case name == "voltage":
		return "pack_voltage"
	case name == "current":
		return "pack_current"
	}
	return name
}

// reportValue is the integer the table's raw row extracts for one report field.
func reportValue(name string, f canbusreg.GrowattReportField) int64 {
	switch len(f.Raw) {
	case 2:
		return int64(binary.BigEndian.Uint16(f.Raw))
	case 4:
		return int64(binary.LittleEndian.Uint32(f.Raw))
	}
	if name == "remaining_capacity" || name == "full_charge_capacity" {
		return f.Numerator / 10
	}
	return f.Numerator
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
	return roweval.Acquired[Selector]{Template: template, Roles: roles, Receipt: roweval.Receipt{ReceivedAt: received, Monotonic: mono}, Payload: raw[8:],
		Coordinates: map[string]string{packSlot: "pack.site-a"}}
}

// TestSOHAboveStorageBoundKeepsNativeAndIsRejectedAtIngestion pins that a valid
// SOH of 101 keeps its native value and validity, and that the signed validity
// gate gives the candidate. The storage pack bounds storage.state.soh at one,
// so the validator refuses that fact; the bound is a stop item of the report.
func TestSOHAboveStorageBoundKeepsNativeAndIsRejectedAtIngestion(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E5"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	evals, reason, err := EvaluateFrame(table, frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	soh := evals[Profile+".soh"]
	if soh.Native == nil || soh.Native.Reason != "" || soh.Native.Value == nil || !decimalEquals(soh.Native.Value.Quantity.Number, "101") {
		t.Fatalf("native SOH lost its value: %+v", soh.Native)
	}
	valid := evals[Profile+".soh_valid"]
	if valid.Native == nil || valid.Native.Value == nil || !decimalEquals(valid.Native.Value.Quantity.Number, "1") {
		t.Fatalf("native SOH validity lost: %+v", valid.Native)
	}
	if soh.Candidate == nil || soh.Reason != "" || !decimalEquals(soh.Candidate.Value.Quantity.Number, "1.01") {
		t.Fatalf("SOH 101 candidate %+v reason %q, want the signed 1.01", soh.Candidate, soh.Reason)
	}
	assertRejectedAtIngestion(t, soh.Candidate, semreg.BoundsExceeded)
}

// TestPackFactKeyCarriesConfiguredPackResource pins that the fact key of a pack
// candidate names the configured pack resource, and that two configured packs
// give two distinct keys for the same fact.
func TestPackFactKeyCarriesConfiguredPackResource(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	keyText := func(resource string) string {
		a := acquired(raw, roles)
		a.Coordinates = map[string]string{packSlot: resource}
		evals, reason, err := EvaluateFrame(table, frame, a)
		if err != nil || reason != "" {
			t.Fatalf("admitted frame: reason %q err %v", reason, err)
		}
		c := evals[Profile+".soc"].Candidate
		if c == nil || len(c.Key.Dimensions) != 1 || c.Key.Dimensions[0].Value.Text == nil {
			t.Fatalf("SOC candidate has no pack dimension: %+v", c)
		}
		return *c.Key.Dimensions[0].Value.Text
	}
	siteA, siteB := keyText("pack.site-a"), keyText("pack.site-b")
	if siteA != "pack.site-a" || siteB != "pack.site-b" {
		t.Fatalf("pack keys name %q and %q, want the configured resources", siteA, siteB)
	}
}

// TestHostFreshnessPolicyYieldsCandidates pins that the candidates follow the
// freshness policy of the caller's template, not the exemplar's synthetic
// identifier: a host with its own policy gets the same native rows and
// candidates as the exemplar policy gives.
func TestHostFreshnessPolicyYieldsCandidates(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "1388006400C864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, roles)
	a.Template.FreshnessPolicy.PolicyID = "policy.host.configured"
	evals, reason, err := EvaluateFrame(table, frame, a)
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	for _, id := range []string{"pack_voltage", "pack_current", "soc", "soh"} {
		ev := evals[Profile+"."+id]
		if ev.Native == nil || ev.Native.Reason != "" {
			t.Fatalf("%s: native evaluation lost: %+v", id, ev.Native)
		}
		if ev.Candidate == nil || ev.Reason != "" {
			t.Fatalf("%s: host policy withheld the candidate: reason %q detail %q", id, ev.Reason, ev.Detail)
		}
		if ev.Candidate.FreshnessPolicy.PolicyID != "policy.host.configured" {
			t.Fatalf("%s: candidate policy %q, want the host policy", id, ev.Candidate.FreshnessPolicy.PolicyID)
		}
	}
}

// TestEmptyPackCoordinateIsUnresolved pins that a pack coordinate present with
// an empty value is treated as unresolved: no candidate carries an empty pack
// dimension, and the native rows and raw evidence stay available.
func TestEmptyPackCoordinateIsUnresolved(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, roles)
	a.Coordinates = map[string]string{packSlot: ""}
	evals, reason, err := EvaluateFrame(table, frame, a)
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	for id, ev := range evals {
		if ev.Candidate != nil {
			t.Fatalf("%s: empty pack coordinate produced candidate %+v", id, ev.Candidate)
		}
		if ev.Native == nil || ev.Native.Reason != "" || !reflect.DeepEqual(ev.Native.Evidence, a.Template.Evidence) {
			t.Fatalf("%s: native evaluation or evidence lost: %+v", id, ev.Native)
		}
	}
	if evals[Profile+".soc"].Reason != unresolvedSlot {
		t.Fatalf("soc reason %q, want %q", evals[Profile+".soc"].Reason, unresolvedSlot)
	}
}

// TestUnresolvedPackKeepsNativeRowsAndEvidence pins that a well-formed frame
// whose pack resource is not supplied keeps every native row, its raw evidence
// and its native signed values, and produces no storage candidate.
func TestUnresolvedPackKeepsNativeRowsAndEvidence(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, roles)
	a.Coordinates = nil
	evals, reason, err := EvaluateFrame(table, frame, a)
	if err != nil {
		t.Fatalf("unresolved pack returned an error and discarded the frame: %v", err)
	}
	if reason != "" || len(evals) != 6 {
		t.Fatalf("admitted frame: reason %q, %d native rows, want 6", reason, len(evals))
	}
	for id, ev := range evals {
		if ev.Native == nil || ev.Native.Reason != "" || !reflect.DeepEqual(ev.Native.Evidence, a.Template.Evidence) {
			t.Fatalf("%s: native evaluation or evidence lost: %+v", id, ev.Native)
		}
		if ev.Candidate != nil {
			t.Fatalf("%s: unresolved pack produced candidate %+v", id, ev.Candidate)
		}
	}
	if !decimalEquals(evals[Profile+".pack_current"].Native.Value.Quantity.Number, "-100") ||
		!decimalEquals(evals[Profile+".maximum_cell_temperature"].Native.Value.Quantity.Number, "-200") {
		t.Fatalf("signed native values changed when the pack is unresolved")
	}
	if evals[Profile+".pack_current"].Reason != unresolvedSlot {
		t.Fatalf("pack_current reason %q, want %q", evals[Profile+".pack_current"].Reason, unresolvedSlot)
	}
}

// storageRoles returns the role bindings of the synthetic pack and the catalog
// the host supplies: the definitions of the pinned storage pack, as its own
// validator lists them. The references the table names must resolve there.
func storageRoles(t *testing.T) roweval.Roles {
	t.Helper()
	capability := semreg.CapabilityInstanceID("capability.synthetic.pack")
	return roweval.Roles{
		Subjects: map[string]roweval.RoleBinding{"pack": {ServiceInstance: "service.synthetic.pack", CapabilityInstance: &capability}},
		Catalog:  []semreg.DefinitionIndex{storage.New().Definitions()},
	}
}

// assertRejectedAtIngestion pins that the pinned storage validator refuses the
// candidate's fact: a value outside the catalog bound is not admitted as a
// fact, whatever the row that produced it.
func assertRejectedAtIngestion(t *testing.T, c *semreg.FactCandidate, want semreg.ErrorID) {
	t.Helper()
	if c == nil || c.Value == nil {
		t.Fatalf("no candidate value to validate")
	}
	err := storage.New().ValidateFact(c.Key, c.Value)
	var e *semreg.Error
	if !errors.As(err, &e) || e.ID != want {
		t.Fatalf("storage validator error %v, want %s", err, want)
	}
}
