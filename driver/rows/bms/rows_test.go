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
	"strings"
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
	Name       string           `json:"name"`
	Identifier uint32           `json:"identifier"`
	Extended   bool             `json:"extended"`
	DLC        uint8            `json:"dlc"`
	RawDLC     uint8            `json:"raw_dlc"`
	Data       string           `json:"data_hex"`
	Frame      string           `json:"frame"`
	Native     map[string]int64 `json:"native"`
	// Raw is the expected byte content, hex in frame order, of each raw native
	// row: a span the page leaves without a qualified byte order. The row's native
	// raw is the bit pattern of those bytes and it produces no candidate.
	Raw   map[string]string `json:"raw"`
	Facts map[string]fact   `json:"facts"`
	// Refused names the storage facts the pinned catalog refuses for this
	// frame. Their native values stay, and no candidate is produced.
	Refused       []string `json:"refused"`
	DecoderParity *bool    `json:"decoder_parity"`
}

// isRefused reports whether the vector expects the catalog to refuse fact.
func (v vector) isRefused(fact string) bool {
	for _, f := range v.Refused {
		if f == fact {
			return true
		}
	}
	return false
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
		ev, ok := evals[row.ID]
		if !ok || ev.Native == nil || ev.Native.RowID != row.ID {
			t.Fatalf("%s: no native evaluation", name)
		}
		if _, raw := v.Raw[name]; raw {
			checkRaw(t, v, name, row, frame, a, ev)
			continue
		}
		want, listed := v.Native[name]
		if !listed {
			t.Fatalf("%s: admitted frame has no expected native value", name)
		}
		n := ev.Native
		if n.Reason != "" || n.Value == nil || n.Value.Quantity == nil || !decimalEquals(n.Value.Quantity.Number, fmt.Sprint(want)) {
			t.Fatalf("%s: want native %d, got reason %q value %+v", name, want, n.Reason, n.Value)
		}
		if !reflect.DeepEqual(n.Evidence, a.Template.Evidence) {
			t.Fatalf("%s: native evaluation lost the raw source record", name)
		}
		if v.isRefused(factFor(name)) {
			seenFact[factFor(name)] = true
			if ev.Candidate != nil || ev.Reason != catalogRefused || ev.Detail == "" {
				t.Fatalf("%s: catalog-refused fact %s: candidate %+v reason %q, want withheld as %q", name, factFor(name), ev.Candidate, ev.Reason, catalogRefused)
			}
			continue
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
	if rows != len(v.Native)+len(v.Raw) {
		t.Fatalf("vector lists %d native and raw values, the frame has %d rows", len(v.Native)+len(v.Raw), rows)
	}
	for fact := range v.Facts {
		if !seenFact[fact] {
			t.Fatalf("expected fact %s was not emitted", fact)
		}
	}
	for _, fact := range v.Refused {
		if !seenFact[fact] {
			t.Fatalf("expected refused fact %s was not evaluated", fact)
		}
	}
}

// checkRaw compares a raw row with the vector's bytes. The evaluator's own
// native output must carry the bytes of the slice: Native.Raw is the extracted
// bit pattern, which for these four-byte bit slices is the little-endian integer
// of the bytes in frame order. The row makes no candidate, and its evidence is
// the raw source record.
func checkRaw(t *testing.T, v vector, name string, row roweval.NativeRow[Selector], frame canbus.Frame, a roweval.Acquired[Selector], ev roweval.Evaluation) {
	t.Helper()
	want, listed := v.Raw[name]
	if !listed {
		t.Fatalf("%s: admitted frame has no expected raw bytes", name)
	}
	n := ev.Native
	if n == nil || n.Reason != "" || n.Value == nil {
		t.Fatalf("%s: raw row gave native %+v, want the bit pattern of its bytes", name, n)
	}
	if ev.Candidate != nil {
		t.Fatalf("%s: raw row produced a candidate %+v", name, ev.Candidate)
	}
	if !reflect.DeepEqual(n.Evidence, a.Template.Evidence) {
		t.Fatalf("%s: native evaluation lost the raw source record", name)
	}
	from := int(row.Wire.BitOffset) / 8
	to := from + int(row.Wire.Width)/8
	wantBytes, err := hex.DecodeString(want)
	if err != nil || len(wantBytes) != to-from {
		t.Fatalf("%s: vector bytes %q do not fill the %d-byte slice", name, want, to-from)
	}
	if got := hex.EncodeToString(frame.Data()[from:to]); got != strings.ToLower(want) {
		t.Fatalf("%s: frame bytes %s, want %s", name, got, strings.ToLower(want))
	}
	var bitPattern uint64
	for i, b := range wantBytes {
		bitPattern |= uint64(b) << (8 * uint(i))
	}
	if n.Raw != bitPattern {
		t.Fatalf("%s: native raw %#x, want %#x from bytes %s", name, n.Raw, bitPattern, want)
	}
	if !decimalEquals(n.Value.Quantity.Number, fmt.Sprint(bitPattern)) {
		t.Fatalf("%s: native value %+v, want the bit pattern %d", name, n.Value.Quantity.Number, bitPattern)
	}
}

// TestNegativePackVoltageKeepsNativeAndIsWithheldByCatalog pins that a negative
// pack voltage keeps its signed native value and evidence, and that the pinned
// storage catalog, which bounds storage.pack.voltage at zero, refuses the
// candidate. The refusal is explicit and withholds only that candidate: the
// signed pattern itself gives the value the catalog refuses, and the pack
// current of the same frame stays publishable.
func TestNegativePackVoltageKeepsNativeAndIsWithheldByCatalog(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, roles)
	evals, reason, err := EvaluateFrame(table, frame, a)
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	ev := evals[Profile+".pack_voltage"]
	if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, "-100") {
		t.Fatalf("native pack voltage lost its signed value: %+v", ev.Native)
	}
	if ev.Candidate != nil || ev.Reason != catalogRefused || ev.Detail == "" {
		t.Fatalf("negative pack voltage candidate %+v reason %q, want withheld as %q", ev.Candidate, ev.Reason, catalogRefused)
	}
	assertRejectedAtIngestion(t, unwithheldCandidate(t, table, frame, a, Profile+".pack_voltage"), semreg.BoundsExceeded)
	if c := evals[Profile+".pack_current"].Candidate; c == nil || !decimalEquals(c.Value.Quantity.Number, "-10.0") {
		t.Fatalf("the refused voltage withheld the sibling pack current: %+v", c)
	}
}

// TestSOHAboveStorageBoundIsWithheldAndSiblingsStayPublishable pins the batch
// case: one 0x313 frame carries a valid SOH of 101 with valid SOC and current.
// The SOH candidate is refused by the pinned catalog and withheld with its
// native value kept, and the SOC and current candidates of the same frame stay
// publishable, so a batch of the frame's candidates is not rejected whole.
func TestSOHAboveStorageBoundIsWithheldAndSiblingsStayPublishable(t *testing.T) {
	roles := storageRoles(t)
	table := V104Table()
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E5"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := acquired(raw, roles)
	evals, reason, err := EvaluateFrame(table, frame, a)
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
	if soh.Candidate != nil || soh.Reason != catalogRefused {
		t.Fatalf("SOH 101 candidate %+v reason %q, want withheld as %q", soh.Candidate, soh.Reason, catalogRefused)
	}
	assertRejectedAtIngestion(t, unwithheldCandidate(t, table, frame, a, Profile+".soh"), semreg.BoundsExceeded)
	soc := evals[Profile+".soc"].Candidate
	if soc == nil || !decimalEquals(soc.Value.Quantity.Number, "1.00") {
		t.Fatalf("the refused SOH withheld the sibling SOC candidate: %+v", soc)
	}
	current := evals[Profile+".pack_current"].Candidate
	if current == nil || !decimalEquals(current.Value.Quantity.Number, "-10.0") {
		t.Fatalf("the refused SOH withheld the sibling pack current candidate: %+v", current)
	}
}

// unwithheldCandidate returns the candidate the named row gives before the
// storage catalog check, so the refusal can be pinned against the storage
// validator with the value the row actually produced.
func unwithheldCandidate(t *testing.T, table roweval.Table[Selector], frame canbus.Frame, a roweval.Acquired[Selector], id string) *semreg.FactCandidate {
	t.Helper()
	for _, row := range table.Native {
		if row.ID != id {
			continue
		}
		ev, err := evaluateUnchecked(withPolicy(table, a.Template.FreshnessPolicy.PolicyID), frame, row, a)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Candidate == nil {
			t.Fatalf("%s gives no candidate before the catalog check: reason %q", id, ev.Reason)
		}
		return ev.Candidate
	}
	t.Fatalf("no native row %s", id)
	return nil
}

// TestEveryDeclaredNativeRowIsExercised pins that every native row the table
// declares is listed by an admitted vector, so removing or mistranscribing any
// row fails the independent comparison, not only the rows the vectors happen to
// cover.
func TestEveryDeclaredNativeRowIsExercised(t *testing.T) {
	covered := map[string]bool{}
	for _, v := range readVectors(t) {
		for name := range v.Native {
			covered[fmt.Sprintf("%#x/%s", v.Identifier, name)] = true
		}
		for name := range v.Raw {
			covered[fmt.Sprintf("%#x/%s", v.Identifier, name)] = true
		}
	}
	for _, row := range V104Table().Native {
		key := fmt.Sprintf("%#x/%s", row.At.Identifier, rowName(row.ID))
		if !covered[key] {
			t.Errorf("native row %s is declared but no vector exercises it", key)
		}
	}
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
	report, raws, reportOK := reportFields(t, frame)
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
		for name, want := range v.Raw {
			got, named := raws[name]
			if !named {
				if decoderUnnamed[name] {
					continue
				}
				t.Fatalf("frozen report decoder keeps no raw bytes for %s", name)
			}
			if hex.EncodeToString(got) != strings.ToLower(want) {
				t.Fatalf("frozen report decoder %s bytes %x, vector %s", name, got, want)
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
func reportFields(t *testing.T, target canbus.Frame) (map[string]int64, map[string][]byte, bool) {
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
		return map[string]int64{}, map[string][]byte{}, false
	}
	out := make(map[string]int64, len(report.Fields))
	raws := make(map[string][]byte)
	for name, f := range report.Fields {
		row := reportRowName(target.ID().Value(), name)
		out[row] = reportValue(name, f)
		if len(f.Raw) > 0 {
			raws[row] = append([]byte(nil), f.Raw...)
		}
	}
	return out, raws, true
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
// A four-byte raw span has no integer reading: the report keeps its bytes and
// the table compares them as bytes.
func reportValue(name string, f canbusreg.GrowattReportField) int64 {
	if len(f.Raw) == 2 {
		return int64(binary.BigEndian.Uint16(f.Raw))
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

// commonFrame builds one frame of the common projection test from its bytes.
func commonFrame(t *testing.T, id uint32, data string) (canbus.Frame, []byte) {
	t.Helper()
	raw := vector{Identifier: id, DLC: 8, Data: data}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return frame, raw
}

// TestCommonProjectionPublishesOnlyTheSelectedScope pins that the common
// projection gives its rows for a selected member or aggregate scope, under the
// scope the selection carries, and nothing for an unknown scope.
func TestCommonProjectionPublishesOnlyTheSelectedScope(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x313, "FF9CFF9CFF3864D0")
	for _, scope := range []string{ScopeMember, ScopeAggregate} {
		evals, reason, err := EvaluateCommonFrame(scope, frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", scope, reason, err)
		}
		for _, id := range []string{"pack_voltage", "pack_current", "soc", "soh"} {
			if _, ok := evals[CommonProfile+"."+id]; !ok {
				t.Fatalf("%s: no evaluation of %s", scope, id)
			}
		}
		if _, ok := evals[Profile+".soc"]; ok {
			t.Fatalf("%s: the common projection evaluated a V1.04 row", scope)
		}
	}
	for _, scope := range []string{"", "unknown", "local"} {
		evals, reason, err := EvaluateCommonFrame(scope, frame, acquired(raw, roles))
		if err != nil || evals != nil || reason != roweval.ReasonNoMatch {
			t.Fatalf("scope %q: reason %q evaluations %d, want no match", scope, reason, len(evals))
		}
	}
}

// TestCommonProjectionBindsSharedSOCSOHVoltageAndCurrent pins the common
// bindings on the same bytes: the SOH is bits 0 to 6 of byte 7 with no validity
// gate, so an SOH byte of 0x50 gives the candidate 0.80 that V1.04 keeps native.
func TestCommonProjectionBindsSharedSOCSOHVoltageAndCurrent(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x313, "0000000000006450")
	evals, reason, err := EvaluateCommonFrame(ScopeMember, frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	soh := evals[CommonProfile+".soh"]
	if soh.Native == nil || soh.Native.Value == nil || !decimalEquals(soh.Native.Value.Quantity.Number, "80") {
		t.Fatalf("common SOH native %+v, want 80", soh.Native)
	}
	if soh.Candidate == nil || soh.Reason != "" || !decimalEquals(soh.Candidate.Value.Quantity.Number, "0.80") || string(soh.Candidate.Value.Quantity.Unit) != "unit.ratio" {
		t.Fatalf("common SOH candidate %+v reason %q, want 0.80 unit.ratio", soh.Candidate, soh.Reason)
	}
	if soh.Subject == nil || string(soh.Subject.Role) != packRole {
		t.Fatalf("common SOH subject %+v, want the pack", soh.Subject)
	}
	if c := evals[CommonProfile+".soc"].Candidate; c == nil || !decimalEquals(c.Value.Quantity.Number, "1.00") {
		t.Fatalf("common SOC candidate %+v, want 1.00", c)
	}
	if c := evals[CommonProfile+".pack_current"].Candidate; c == nil || !decimalEquals(c.Value.Quantity.Number, "0") || string(c.Value.Quantity.Unit) != "unit.ampere" {
		t.Fatalf("zero current candidate %+v, want 0 A", c)
	}
	if evals[CommonProfile+".pack_voltage"].Native.Value == nil {
		t.Fatalf("common pack voltage lost its native value")
	}
	if _, ok := evals[Profile+".soh"]; ok {
		t.Fatalf("common projection evaluated a V1.04 row")
	}
	v104Evals, v104Reason, err := EvaluateFrame(V104Table(), frame, acquired(raw, roles))
	if err != nil || v104Reason != "" {
		t.Fatalf("V1.04 on the same bytes: reason %q err %v", v104Reason, err)
	}
	if v104 := v104Evals[Profile+".soh"]; v104.Candidate != nil || v104.Native == nil || v104.Native.Value == nil || !decimalEquals(v104.Native.Value.Quantity.Number, "80") {
		t.Fatalf("V1.04 SOH without validity gave %+v, want native 80 and no candidate", v104)
	}
}

// TestCommonProjectionKeepsNativeSOCOutsideV104Range pins that the common
// projection applies no V1.04 frame range: SOC 101 is native 101 and the storage
// catalog refuses its candidate, while V1.04 rejects the whole frame.
func TestCommonProjectionKeepsNativeSOCOutsideV104Range(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x313, "0000000000006552")
	evals, reason, err := EvaluateCommonFrame(ScopeAggregate, frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("common frame: reason %q err %v", reason, err)
	}
	soc := evals[CommonProfile+".soc"]
	if soc.Native == nil || soc.Native.Value == nil || !decimalEquals(soc.Native.Value.Quantity.Number, "101") {
		t.Fatalf("common SOC native %+v, want 101", soc.Native)
	}
	if soc.Candidate != nil || soc.Reason != catalogRefused {
		t.Fatalf("common SOC 101 candidate %+v reason %q, want refused by the catalog", soc.Candidate, soc.Reason)
	}
	if _, reason, _ := EvaluateFrame(V104Table(), frame, acquired(raw, roles)); reason != "not valid" {
		t.Fatalf("V1.04 SOC 101 reason %q, want %q", reason, "not valid")
	}
}

// TestCommonProjectionLimitsAndStatusStayNative pins that the 0x311 limits and
// the 0x312 raw bytes and count are native rows only: the pinned read row
// carries no value limit, so no candidate is produced for them.
func TestCommonProjectionLimitsAndStatusStayNative(t *testing.T) {
	roles := storageRoles(t)
	cases := []struct {
		id     uint32
		data   string
		native map[string]string
	}{
		{0x311, "0FA000C800C80000", map[string]string{"charge_voltage_limit": "4000", "charge_current_limit": "200", "discharge_current_limit": "200"}},
		{0x312, "8144028001000000", map[string]string{"protection_flags": "33092", "warning_flags": "640", "pack_count": "1"}},
	}
	for _, c := range cases {
		frame, raw := commonFrame(t, c.id, c.data)
		evals, reason, err := EvaluateCommonFrame(ScopeMember, frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("frame %#x: reason %q err %v", c.id, reason, err)
		}
		for name, want := range c.native {
			ev := evals[CommonProfile+"."+name]
			if ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, want) {
				t.Fatalf("%s: native %+v, want %s", name, ev.Native, want)
			}
			if ev.Candidate != nil {
				t.Fatalf("%s: unexpected candidate %+v", name, ev.Candidate)
			}
		}
	}
}

// TestCommonProjectionRejectsMalformedFrames pins the common gate: a wrong DLC,
// a wrong raw DLC or an extended identifier gives no evaluation, and a
// neighbouring identifier is not a common frame.
func TestCommonProjectionRejectsMalformedFrames(t *testing.T) {
	roles := storageRoles(t)
	for _, c := range []vector{
		{Identifier: 0x313, DLC: 7, Data: "FF9CFF9CFF3864D0"},
		{Identifier: 0x313, DLC: 8, RawDLC: 12, Data: "FF9CFF9CFF3864D0"},
		{Identifier: 0x313, Extended: true, DLC: 8, Data: "FF9CFF9CFF3864D0"},
		{Identifier: 0x310, DLC: 8, Data: "FF9CFF9CFF3864D0"},
	} {
		raw := c.record(t)
		frame, err := canbus.DecodeSocketCANRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		evals, reason, err := EvaluateCommonFrame(ScopeMember, frame, acquired(raw, roles))
		if err != nil || evals != nil || reason != roweval.ReasonNoMatch {
			t.Fatalf("identifier %#x dlc %d: reason %q evaluations %d, want no match", c.Identifier, c.DLC, reason, len(evals))
		}
	}
}

// TestCommonProjectionRowsMatchTheFrozenProjector pins the native values of
// every common row against the frozen common projector on the same bytes.
func TestCommonProjectionRowsMatchTheFrozenProjector(t *testing.T) {
	roles := storageRoles(t)
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	projector, ok := canbusreg.NewGrowattLowVoltageBMSCommonProjector(iface)
	if !ok {
		t.Fatal("projector not selected")
	}
	seen := map[string]bool{}
	for _, c := range []struct {
		id   uint32
		data string
	}{
		{0x311, "0FA000C800C80000"},
		{0x312, "8144028001000000"},
		{0x313, "FF9CFF9CFF3864D0"},
	} {
		frame, raw := commonFrame(t, c.id, c.data)
		projection, ok := projector.Apply(canbusreg.Evidence{Frame: frame, Interface: iface, Sequence: 1})
		if !ok {
			t.Fatalf("frozen projector rejected frame %#x", c.id)
		}
		evals, reason, err := EvaluateCommonFrame(ScopeMember, frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("frame %#x: reason %q err %v", c.id, reason, err)
		}
		want := map[string]int64{}
		switch {
		case projection.Limits != nil:
			want["charge_voltage_limit"] = int64(projection.Limits.ChargeVoltageDecivolts)
			want["charge_current_limit"] = int64(projection.Limits.ChargeCurrentDeciamps)
			want["discharge_current_limit"] = int64(projection.Limits.DischargeCurrentDeciamps)
		case projection.Status != nil:
			want["protection_flags"] = int64(binary.BigEndian.Uint16(projection.Status.Protection[:]))
			want["warning_flags"] = int64(binary.BigEndian.Uint16(projection.Status.Warning[:]))
			want["pack_count"] = int64(projection.Status.PackCount)
		case projection.Measurements != nil:
			m := projection.Measurements
			want["pack_voltage"] = int64(m.VoltageCentivolts)
			want["pack_current"] = int64(m.CurrentDeciamps)
			want["soc"] = int64(m.SOCPercent)
			want["soh"] = int64(m.SOHValue)
		}
		for name, v := range want {
			ev := evals[CommonProfile+"."+name]
			if ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, fmt.Sprint(v)) {
				t.Fatalf("%s: native %+v, frozen projector gives %d", name, ev.Native, v)
			}
			seen[name] = true
		}
	}
	for _, row := range commonRows(ScopeMember) {
		if !seen[rowNameIn(CommonProfile, row.ID)] {
			t.Fatalf("common row %s is not compared with the frozen projector", row.ID)
		}
	}
}

// rowNameIn returns the field name of a native row of the named profile.
func rowNameIn(profile, id string) string { return strings.TrimPrefix(id, profile+".") }

// v108Report returns the fields the frozen V1.08 report decoder gives for one
// frame, keyed by the report's own field names, with the numerator of each.
func v108Report(t *testing.T, target canbus.Frame) map[string]int64 {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := canbusreg.NewGrowattBMSProfile(iface, canbusreg.BMSRevisionV108)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := profile.Classify(canbusreg.Evidence{Frame: target, Interface: iface, Sequence: 1}).Projection.(canbusreg.GrowattBMSReport)
	if !ok {
		t.Fatalf("frozen V1.08 report decoder gives no report for an admitted frame")
	}
	out := make(map[string]int64, len(report.Fields))
	for name, f := range report.Fields {
		out[name] = f.Numerator
	}
	return out
}

// TestV108TablesValidateAgainstTheStorageCatalog pins that the V1.08 table and
// the common projection table of each scope are valid tables: every reference
// resolves, every codec and order is named and every hook exists.
func TestV108TablesValidateAgainstTheStorageCatalog(t *testing.T) {
	roles := storageRoles(t)
	if err := roweval.ValidateTable(V108Table(), V104Descriptors(), roles); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{ScopeMember, ScopeAggregate} {
		if err := roweval.ValidateTable(CommonTable(scope), V104Descriptors(), roles); err != nil {
			t.Fatalf("common %s: %v", scope, err)
		}
	}
}

// TestV108SOHIsPublishedForEitherLifetimeAlarmState pins that the V1.08 SOH is
// published as 0.80 whether the lifetime-alarm bit is 0 or 1, and that the alarm
// is its own native row, unbound and independent of the SOH.
func TestV108SOHIsPublishedForEitherLifetimeAlarmState(t *testing.T) {
	roles := storageRoles(t)
	for _, c := range []struct {
		data  string
		alarm string
	}{{"FF9CFF9CFF386450", "0"}, {"FF9CFF9CFF3864D0", "1"}} {
		frame, raw := commonFrame(t, 0x313, c.data)
		evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", c.data, reason, err)
		}
		soh := evals[V108Profile+".soh"]
		if soh.Native == nil || soh.Native.Value == nil || !decimalEquals(soh.Native.Value.Quantity.Number, "80") {
			t.Fatalf("%s: native SOH %+v, want 80", c.data, soh.Native)
		}
		if soh.Candidate == nil || soh.Reason != "" || !decimalEquals(soh.Candidate.Value.Quantity.Number, "0.80") || string(soh.Candidate.Value.Quantity.Unit) != "unit.ratio" {
			t.Fatalf("%s: V1.08 SOH candidate %+v reason %q, want 0.80 unit.ratio", c.data, soh.Candidate, soh.Reason)
		}
		alarm := evals[V108Profile+".lifetime_alarm"]
		if alarm.Candidate != nil || alarm.Native == nil || alarm.Native.Value == nil || !decimalEquals(alarm.Native.Value.Quantity.Number, c.alarm) {
			t.Fatalf("%s: lifetime alarm %+v, want native %s and no candidate", c.data, alarm.Native, c.alarm)
		}
	}
}

// TestV108AverageTemperatureBindsToPackKelvin pins that a negative V1.08 average
// temperature keeps its signed native value and gives the pack temperature in
// kelvin: -20.0 degrees Celsius is 253.15 K.
func TestV108AverageTemperatureBindsToPackKelvin(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x313, "00000000FF386450")
	evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
	if err != nil || reason != "" {
		t.Fatalf("admitted frame: reason %q err %v", reason, err)
	}
	avg := evals[V108Profile+".average_temperature"]
	if avg.Native == nil || avg.Native.Value == nil || !decimalEquals(avg.Native.Value.Quantity.Number, "-200") {
		t.Fatalf("average temperature native %+v, want -200", avg.Native)
	}
	c := avg.Candidate
	if c == nil || string(c.Key.FactID) != "storage.temperature.pack" || !decimalEquals(c.Value.Quantity.Number, "253.15") || string(c.Value.Quantity.Unit) != "unit.kelvin" {
		t.Fatalf("average temperature candidate %+v, want storage.temperature.pack 253.15 unit.kelvin", c)
	}
	if avg.Subject == nil || string(avg.Subject.Role) != packRole {
		t.Fatalf("average temperature subject %+v, want the pack", avg.Subject)
	}
	if err := storage.New().ValidateFact(c.Key, c.Value); err != nil {
		t.Fatalf("the storage catalog refuses the pack temperature: %v", err)
	}
}

// TestV108StatusWordReservedBitsKeepTheDefinedFields pins that bits 10 and 11 of
// the V1.08 status word stay in the raw word and do not change the decoded
// states, and that a named state in bits 8 and 9 gives its own row.
func TestV108StatusWordReservedBitsKeepTheDefinedFields(t *testing.T) {
	roles := storageRoles(t)
	word := func(data string) map[string]string {
		frame, raw := commonFrame(t, 0x311, data)
		evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("%s: reason %q err %v", data, reason, err)
		}
		out := map[string]string{}
		for id, ev := range evals {
			if ev.Native == nil || ev.Native.Value == nil {
				out[id] = "no value:" + string(ev.Native.Reason)
				continue
			}
			out[id] = ev.Native.Value.Quantity.Number.Coefficient + "e" + fmt.Sprint(ev.Native.Value.Quantity.Number.Exponent10)
		}
		return out
	}
	plain := word("0FA000C800C80003")
	reserved := word("0FA000C800C80C03")
	if plain[V108Profile+".status_word"] != "3e0" || reserved[V108Profile+".status_word"] != "3075e0" {
		t.Fatalf("status word raw %q and %q, want 3 and 3075", plain[V108Profile+".status_word"], reserved[V108Profile+".status_word"])
	}
	for _, id := range []string{"operating_state", "master_operation_mode", "charge_voltage_limit", "charge_current_limit", "discharge_current_limit", "cell_balance", "sleep"} {
		if plain[V108Profile+"."+id] != reserved[V108Profile+"."+id] {
			t.Fatalf("%s differs with reserved bits set: %q and %q", id, plain[V108Profile+"."+id], reserved[V108Profile+"."+id])
		}
	}
	if plain[V108Profile+".operating_state"] != "3e0" {
		t.Fatalf("operating state %q, want 3 (discharging)", plain[V108Profile+".operating_state"])
	}
	named := word("0FA000C800C80103")
	if named[V108Profile+".master_operation_mode"] != "1e0" || named[V108Profile+".status_word"] != "259e0" {
		t.Fatalf("named bits 8 and 9 give mode %q and word %q", named[V108Profile+".master_operation_mode"], named[V108Profile+".status_word"])
	}
}

// TestV108GateAndV104GateSplitTheSameIdentifiers pins that a V1.04 frame and a
// V1.08 frame of one identifier each feed only their own profile's rows, and
// that the count byte 7 of 0x312 gates V1.04 and not V1.08.
func TestV108GateAndV104GateSplitTheSameIdentifiers(t *testing.T) {
	roles := storageRoles(t)
	frame, raw := commonFrame(t, 0x312, "00000000FE000000")
	v108, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
	if err != nil || reason != "" || len(v108) == 0 {
		t.Fatalf("V1.08 count 254 with byte 7 zero: reason %q err %v rows %d", reason, err, len(v108))
	}
	for id := range v108 {
		if !strings.HasPrefix(id, V108Profile+".") {
			t.Fatalf("V1.08 evaluated %s", id)
		}
	}
	if _, v104Reason, _ := EvaluateFrame(V104Table(), frame, acquired(raw, roles)); v104Reason != "not valid" {
		t.Fatalf("V1.04 accepted byte 7 zero, reason %q", v104Reason)
	}
	for _, data := range []string{"00000000FF000000", "0000000000000000"} {
		f, fraw := commonFrame(t, 0x312, data)
		if _, reason, _ := EvaluateV108Frame(f, acquired(fraw, roles)); reason != "not valid" {
			t.Fatalf("V1.08 %s: reason %q, want not valid", data, reason)
		}
	}
	soc101, rawSOC := commonFrame(t, 0x313, "0000000000006552")
	if _, reason, _ := EvaluateV108Frame(soc101, acquired(rawSOC, roles)); reason != "not valid" {
		t.Fatalf("V1.08 SOC 101 reason %q, want not valid", reason)
	}
	soc100, rawSOCFull := commonFrame(t, 0x313, "FF9CFF9CFF3864D0")
	v104, reason, err := EvaluateFrame(V104Table(), soc100, acquired(rawSOCFull, roles))
	if err != nil || reason != "" {
		t.Fatalf("V1.04 frame: reason %q err %v", reason, err)
	}
	for id := range v104 {
		if !strings.HasPrefix(id, Profile+".") {
			t.Fatalf("V1.04 evaluated %s", id)
		}
	}
}

// TestV108RowsMatchTheFrozenReportDecoder pins every numeric V1.08 row against
// the frozen V1.08 report decoder on the same bytes, and that every row of the
// frame's identifier is evaluated. Rows the report does not name are the raw
// words, the derating span and the reserved byte, compared by their evaluation
// alone.
func TestV108RowsMatchTheFrozenReportDecoder(t *testing.T) {
	roles := storageRoles(t)
	renamed := map[string]string{"pack_voltage": "voltage", "pack_current": "current", "average_temperature": "average_battery_temperature", "lifetime_alarm": "life_warning"}
	unnamed := map[string]bool{"status_word": true, "protection_flags": true, "warning_flags": true, "derating_reason": true, "reserved_byte_7": true}
	for _, c := range []struct {
		id   uint32
		data string
	}{
		{0x311, "0FA000C800C80C03"},
		{0x312, "81440280FE000C0A"},
		{0x313, "FF9CFF9CFF3864D0"},
	} {
		frame, raw := commonFrame(t, c.id, c.data)
		evals, reason, err := EvaluateV108Frame(frame, acquired(raw, roles))
		if err != nil || reason != "" {
			t.Fatalf("frame %#x: reason %q err %v", c.id, reason, err)
		}
		report := v108Report(t, frame)
		for _, row := range v108Rows() {
			if row.At.Identifier != c.id {
				continue
			}
			name := rowNameIn(V108Profile, row.ID)
			ev, ok := evals[row.ID]
			if !ok {
				t.Fatalf("%s: not evaluated", row.ID)
			}
			if unnamed[name] {
				continue
			}
			want, named := report[renamedOr(renamed, name)]
			if !named {
				t.Fatalf("%s: the frozen V1.08 report names no field %s", name, renamedOr(renamed, name))
			}
			if ev.Native == nil || ev.Native.Value == nil || !decimalEquals(ev.Native.Value.Quantity.Number, fmt.Sprint(want)) {
				t.Fatalf("%s: native %+v, frozen report gives %d", name, ev.Native, want)
			}
		}
	}
}

// renamedOr returns the report name of a row name, or the row name itself.
func renamedOr(renamed map[string]string, name string) string {
	if r, ok := renamed[name]; ok {
		return r
	}
	return name
}

// TestCatalogBoundsMirrorThePinnedStoragePack pins the bounds the driver checks
// before a candidate leaves the table against the pinned storage pack's own
// validator, on the edge values of each bounded fact.
func TestCatalogBoundsMirrorThePinnedStoragePack(t *testing.T) {
	roles := storageRoles(t)
	raw := vector{Identifier: 0x313, DLC: 8, Data: "FF9CFF9CFF3864E2"}.record(t)
	frame, err := canbus.DecodeSocketCANRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	evals, reason, err := EvaluateFrame(V104Table(), frame, acquired(raw, roles))
	if err != nil || reason != "" || evals[Profile+".soc"].Candidate == nil {
		t.Fatalf("base candidate: reason %q err %v", reason, err)
	}
	base := evals[Profile+".soc"].Candidate.Key
	type edge struct {
		coefficient string
		exponent    int32
		inside      bool
	}
	cases := []struct {
		fact, unit string
		edges      []edge
	}{
		{"storage.state.soc", "unit.ratio", []edge{{"-1", 2, false}, {"0", 0, true}, {"1", 0, true}, {"101", -2, false}}},
		{"storage.state.soh", "unit.ratio", []edge{{"-1", 2, false}, {"0", 0, true}, {"1", 0, true}, {"101", -2, false}}},
		{"storage.pack.voltage", "unit.volt", []edge{{"-1", 2, false}, {"0", 0, true}}},
		{"storage.temperature.pack", "unit.kelvin", []edge{{"-1", 2, false}, {"0", 0, true}}},
	}
	for _, c := range cases {
		if _, ok := catalogBounds[semreg.DefinitionID(c.fact)]; !ok {
			t.Fatalf("%s has no mirrored bound", c.fact)
		}
		key := base
		key.FactID = semreg.DefinitionID(c.fact)
		for _, e := range c.edges {
			value := semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: semreg.Decimal{Coefficient: e.coefficient, Exponent10: e.exponent}, Unit: semreg.DefinitionID(c.unit)}}
			pinned := storage.New().ValidateFact(key, &value) == nil
			mirrored := catalogRefusal(key, &value) == ""
			if pinned != e.inside || mirrored != e.inside {
				t.Fatalf("%s at %se%d: pinned accepts %v, mirror accepts %v, want %v", c.fact, e.coefficient, e.exponent, pinned, mirrored, e.inside)
			}
		}
	}
}
