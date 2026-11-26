package pcs

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// gaugeVectorFile is the synthetic vector file of the gauge frames 0x315 to 0x318.
type gaugeVectorFile struct {
	Cases []gaugeVectorCase `json:"cases"`
}

type gaugeVectorCase struct {
	Name       string                  `json:"name"`
	Identifier string                  `json:"identifier"`
	Payload    string                  `json:"payload"`
	Selection  vectorSelection         `json:"selection"`
	Input      gaugeVectorInput        `json:"input"`
	Expect     map[string]vectorExpect `json:"expect"`
}

// gaugeVectorInput is the decoder input the gauge vectors supply: the measurement
// assertion, the count, the zero-integration and failed-precheck assertions and
// the per-field dispositions.
type gaugeVectorInput struct {
	MeasurementValid bool              `json:"measurement_valid"`
	CountValid       bool              `json:"count_valid"`
	Count            uint16            `json:"count"`
	ZeroIntegration  bool              `json:"zero_integration"`
	FailedPrecheck   bool              `json:"failed_precheck"`
	FieldStates      map[string]string `json:"field_states"`
	FieldAgesMS      map[string]int64  `json:"field_ages_ms"`
}

func loadGaugeVectors(t *testing.T) []gaugeVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/standard_315_318.json")
	if err != nil {
		t.Fatal(err)
	}
	var f gaugeVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func gaugeCaseNamed(t *testing.T, name string) gaugeVectorCase {
	t.Helper()
	for _, c := range loadGaugeVectors(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no gauge vector named %s", name)
	return gaugeVectorCase{}
}

// buildGauge turns one vector into the selection, the decoder input and the frame.
func buildGauge(t *testing.T, c gaugeVectorCase) (canbusreg.Selection, canbusreg.PCSIdentityPCSInput, canbus.Frame) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := c.Selection
	spec := canbusreg.SelectionSpec{Interface: iface, Flavor: sel.Flavor, ProjectCode: sel.Project, Release: sel.Release, Direction: "bms-to-pcs", Scope: sel.Scope, Bank: sel.Bank, Endpoint: sel.Endpoint, Group: sel.Group, Slot: sel.Slot, Epoch: 1}
	s, err := canbusreg.NewSelection(spec)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(c.Identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := canbus.NewStandardID(uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	frame, err := canbus.NewFrame(identifier, payload)
	if err != nil {
		t.Fatal(err)
	}
	return s, gaugeInput(iface, spec, frame, c.Input), frame
}

// gaugeInput is the decoder input of one frame under the selection spec.
func gaugeInput(iface canbus.InterfaceIdentity, spec canbusreg.SelectionSpec, frame canbus.Frame, in gaugeVectorInput) canbusreg.PCSIdentityPCSInput {
	ages := make(map[string]time.Duration, len(in.FieldAgesMS))
	for field, ms := range in.FieldAgesMS {
		ages[field] = time.Duration(ms) * time.Millisecond
	}
	base := canbusreg.PCSStandardPCSInput{
		LINKInput: canbusreg.LINKInput{
			Evidence: canbusreg.Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second},
			Epoch:    1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second,
		},
		PublisherResolved: true,
		MeasurementValid:  in.MeasurementValid,
		CountValid:        in.CountValid,
		Count:             in.Count,
		FieldStates:       in.FieldStates,
		FieldAges:         ages,
	}
	return canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: base, ZeroIntegration: in.ZeroIntegration, FailedPrecheck: in.FailedPrecheck}
}

// gaugeGated reports, for each native row identifier, whether the row names a gate.
func gaugeGated() map[string]bool {
	gated := make(map[string]bool)
	for _, s := range gaugeSpecs() {
		gated[gaugeNativeRow(s).ID] = s.gate != ""
	}
	return gated
}

// gaugeRowsByID returns the native rows of the gauge table keyed by identifier.
func gaugeRowsByID() map[string]roweval.NativeRow[Selector] {
	rows := make(map[string]roweval.NativeRow[Selector])
	for _, row := range gaugeTable().Native {
		rows[row.ID] = row
	}
	return rows
}

// checkGaugeEvidence compares the evidence the evaluator retains with the
// observation and with the evidence of every decoder field.
func checkGaugeEvidence(t *testing.T, name string, res GaugeResult, in canbusreg.PCSIdentityPCSInput, r canbusreg.NativeResult) {
	t.Helper()
	if !reflect.DeepEqual(res.Evidence, in.Evidence) {
		t.Errorf("%s: evaluator evidence differs from the observation", name)
	}
	for field, df := range r.Fields {
		if !reflect.DeepEqual(df.Evidence, res.Evidence) {
			t.Errorf("%s: %s decoder evidence differs from the evaluator's", name, field)
		}
	}
}

// checkGaugeField compares one decoder field with its row: the raw word, the
// status and, for an accepted field, the exact native value. The retained fault
// bits are published raw: their named condition label is the decoder's, so for a
// bit the row must give the raw disposition and the bit value, whatever the
// decoder's named or raw status.
func checkGaugeField(t *testing.T, name, field string, df canbusreg.NativeField, row roweval.NativeRow[Selector], gated bool, nat *roweval.Native, frame canbus.Frame) {
	t.Helper()
	if df.Raw != frame.Bytes() || df.Evidence.Frame != frame {
		t.Errorf("%s: %s raw evidence differs from the frame", name, field)
	}
	if nat == nil {
		t.Fatalf("%s: %s has no native evaluation", name, field)
	}
	if int64(nat.Raw) != df.Numerator {
		t.Errorf("%s: %s raw %d, decoder %d", name, field, nat.Raw, df.Numerator)
	}
	if strings.HasPrefix(field, "retained_fault_bit_") {
		if nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Fatalf("%s: %s row reason %q, no bit value", name, field, nat.Reason)
		}
		if want := gaugeBitDisposition(df.Status); nat.Disposition != want {
			t.Errorf("%s: %s row disposition %q, decoder status %q wants %q", name, field, nat.Disposition, df.Status, want)
		}
		if decimalRat(t, nat.Value.Quantity.Number).Cmp(new(big.Rat).SetFrac64(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: %s row value differs from the decoder bit %d", name, field, df.Numerator)
		}
		if df.Status != "accepted" && df.Status != "raw" {
			t.Errorf("%s: %s decoder status %q for a bit", name, field, df.Status)
		}
		return
	}
	switch df.Status {
	case "accepted":
		if nat.Reason != "" || nat.Value == nil || nat.Value.Quantity == nil {
			t.Fatalf("%s: %s accepted by the decoder, row reason %q", name, field, nat.Reason)
		}
		if nat.Disposition != dispositionAccepted {
			t.Errorf("%s: %s accepted by the decoder, row disposition %q", name, field, nat.Disposition)
		}
		if df.Unit != row.Unit {
			t.Errorf("%s: %s unit %q, row %q", name, field, df.Unit, row.Unit)
		}
		if decimalRat(t, nat.Value.Quantity.Number).Cmp(new(big.Rat).SetFrac64(df.Numerator, df.Denominator)) != 0 {
			t.Errorf("%s: %s row value differs from the decoder %d/%d", name, field, df.Numerator, df.Denominator)
		}
	case "raw":
		if nat.Disposition != dispositionRaw {
			t.Errorf("%s: %s raw in the decoder, row disposition %q", name, field, nat.Disposition)
		}
		if gated && nat.Reason != gateWithheld {
			t.Errorf("%s: %s raw in the decoder, row reason %q", name, field, nat.Reason)
		}
		if !gated && (nat.Reason != "" || nat.Value == nil) {
			t.Errorf("%s: %s ungated raw field, row reason %q", name, field, nat.Reason)
		}
	case "invalid":
		if nat.Reason != roweval.ReasonOutsideDomain {
			t.Errorf("%s: %s invalid in the decoder, row reason %q", name, field, nat.Reason)
		}
		if nat.Value != nil || nat.Unit != "" {
			t.Errorf("%s: %s invalid in the decoder, row still carries a typed value", name, field)
		}
	case "absent", "stale", "unsupported", "conflict", "unassigned":
		if nat.Reason != roweval.Reason(df.Status) {
			t.Errorf("%s: %s %s in the decoder, row reason %q", name, field, df.Status, nat.Reason)
		}
	default:
		t.Errorf("%s: %s has unexpected status %q", name, field, df.Status)
	}
}

// checkGaugeCase compares every decoder field of a vector with the one row that
// applies to it, and the expectations of the vector with both. A vector the
// decoder refuses must be refused by the evaluator with the decoder's status and
// no rows. It returns the identifiers of the rows that applied.
func checkGaugeCase(t *testing.T, c gaugeVectorCase) map[string]bool {
	t.Helper()
	s, in, frame := buildGauge(t, c)
	r := canbusreg.DecodePCSGauge(s, in)
	res, reason, err := EvaluateGauge(s, in)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	if r.Status != "decoded" {
		if reason != roweval.Reason(r.Status) || res.Rows != nil || len(r.Fields) != 0 {
			t.Errorf("%s: decoder status %q with %d fields, evaluator reason %q", c.Name, r.Status, len(r.Fields), reason)
		}
		return map[string]bool{}
	}
	if reason != "" {
		t.Fatalf("%s: decoder admits the observation, evaluator refuses it (%q)", c.Name, reason)
	}
	checkGaugeEvidence(t, c.Name, res, in, r)
	rows := gaugeRowsByID()
	gated := gaugeGated()
	applied := make(map[string]bool)
	byField := make(map[string][]string)
	for id, ev := range res.Rows {
		if ev.Reason == roweval.ReasonNoMatch {
			continue
		}
		applied[id] = true
		byField[fieldOf(id)] = append(byField[fieldOf(id)], id)
	}
	total := 0
	for _, ids := range byField {
		total += len(ids)
	}
	if total != len(r.Fields) {
		t.Fatalf("%s: %d decoder fields, %d rows applied", c.Name, len(r.Fields), total)
	}
	for name, df := range r.Fields {
		ids := byField[name]
		if len(ids) != 1 {
			t.Errorf("%s: field %s has %d applied rows", c.Name, name, len(ids))
			continue
		}
		checkGaugeField(t, c.Name, name, df, rows[ids[0]], gated[ids[0]], res.Rows[ids[0]].Native, frame)
	}
	for name, want := range c.Expect {
		df, ok := r.Fields[name]
		if !ok || df.Status != want.Status {
			t.Errorf("%s: %s status %q, want %q", c.Name, name, df.Status, want.Status)
		}
		if got := new(big.Rat).SetFrac64(df.Numerator, df.Denominator); got.Cmp(ratText(t, want.Value)) != 0 {
			t.Errorf("%s: %s decoder value %s, want %s", c.Name, name, got.RatString(), want.Value)
		}
		for _, id := range byField[name] {
			if v := res.Rows[id].Native.Value; v != nil && v.Quantity != nil {
				if got := decimalRat(t, v.Quantity.Number); got.Cmp(ratText(t, want.Value)) != 0 {
					t.Errorf("%s: %s row value %s, want %s", c.Name, name, got.RatString(), want.Value)
				}
			}
		}
	}
	return applied
}

func TestGaugeRowsMatchDecoder(t *testing.T) {
	for _, c := range loadGaugeVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			checkGaugeCase(t, c)
		})
	}
}

// TestGaugeSingleLayoutCellRowsCarryNoLayoutToken requires the 0x316 cell rows of
// contract groups L0 and L1 to take the plain field identifier: their field name
// is unique within the frame, so no layout token belongs between frame and field.
func TestGaugeSingleLayoutCellRowsCarryNoLayoutToken(t *testing.T) {
	rows := gaugeRowsByID()
	for _, k := range []string{"5", "6", "7", "8"} {
		want := "growatt.bms.pcs.standard.316.cell_" + k
		if _, ok := rows[want]; !ok {
			t.Errorf("row %s is not declared", want)
		}
	}
	for id := range rows {
		if strings.Contains(id, ".316.early.") {
			t.Errorf("row %s carries a layout token its field does not need", id)
		}
	}
}

func TestGaugeRowIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range gaugeSpecs() {
		id := gaugeNativeRow(s).ID
		if seen[id] {
			t.Errorf("row identifier %s is declared twice", id)
		}
		seen[id] = true
	}
}

// TestGaugeVectorsCoverEveryRow requires every gauge row to apply to at least one vector.
func TestGaugeVectorsCoverEveryRow(t *testing.T) {
	covered := make(map[string]bool)
	for _, c := range loadGaugeVectors(t) {
		for id := range checkGaugeCase(t, c) {
			covered[id] = true
		}
	}
	for _, s := range gaugeSpecs() {
		if id := gaugeNativeRow(s).ID; !covered[id] {
			t.Errorf("row %s applies to no vector", id)
		}
	}
}

// frame316Payload is the replayed frame 0x316: B0-B1 13 88, B2-B3 00 2A, B4-B5 00 35, B6-B7 00 43.
const frame316Payload = "1388002A00350043"

// replay316 evaluates the replayed frame under one identity and local measurement.
func replay316(t *testing.T, identity string, measurementValid bool) (canbusreg.Selection, canbusreg.PCSIdentityPCSInput, canbus.Frame, GaugeResult, canbusreg.NativeResult) {
	t.Helper()
	c := gaugeVectorCase{
		Name:       "replay-316-" + identity,
		Identifier: "0x316",
		Payload:    frame316Payload,
		Selection:  vectorSelection{Project: identity[:4], Release: identity[4:], Flavor: "pcs-standard", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1"},
		Input:      gaugeVectorInput{MeasurementValid: measurementValid},
	}
	s, in, frame := buildGauge(t, c)
	r := canbusreg.DecodePCSGauge(s, in)
	res, reason, err := EvaluateGauge(s, in)
	if err != nil || reason != "" {
		t.Fatalf("%s: evaluate refused: %v %v", c.Name, reason, err)
	}
	return s, in, frame, res, r
}

// TestFrame316ReplayGivesCapacityAndCyclesForL2AndL8 replays the frame 0x316 under
// the L2 selection of QAAA0006 and the L8 selection of QAAA0011 and checks the
// values under their separate row identifiers, with the raw evidence unchanged.
func TestFrame316ReplayGivesCapacityAndCyclesForEachLayout(t *testing.T) {
	for _, tc := range []struct {
		identity                       string
		capacityID, cyclesID           string
		wantCapacity, wantCycles       string
		wantCapacityRaw, wantCyclesRaw uint64
	}{
		{"QAAA0006", "growatt.bms.pcs.standard.316.l2.capacity", "growatt.bms.pcs.standard.316.l2.cycles", "4.2", "5.3", 42, 53},
		{"QAAA0011", "growatt.bms.pcs.standard.316.l8.capacity", "growatt.bms.pcs.standard.316.l8.cycles", "5.3", "6.7", 53, 67},
	} {
		t.Run(tc.identity, func(t *testing.T) {
			_, in, frame, res, r := replay316(t, tc.identity, true)
			if r.Status != "decoded" {
				t.Fatalf("decoder status %q", r.Status)
			}
			if r.Raw != frame.Bytes() || res.Evidence != in.Evidence {
				t.Fatal("raw evidence changed")
			}
			check := func(id, unit, want string, raw uint64) {
				ev, ok := res.Rows[id]
				if !ok || ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil || ev.Native.Value.Quantity == nil {
					t.Fatalf("%s: no accepted value: %+v", id, ev)
				}
				if ev.Native.Unit != unit {
					t.Errorf("%s: unit %q, want %q", id, ev.Native.Unit, unit)
				}
				if ev.Native.Raw != raw {
					t.Errorf("%s: raw %d, want %d", id, ev.Native.Raw, raw)
				}
				if got := decimalRat(t, ev.Native.Value.Quantity.Number); got.Cmp(ratText(t, want)) != 0 {
					t.Errorf("%s: value %s, want %s", id, got.RatString(), want)
				}
				if ev.Native.Disposition != dispositionAccepted {
					t.Errorf("%s: disposition %q", id, ev.Native.Disposition)
				}
			}
			check(tc.capacityID, "Ah", tc.wantCapacity, tc.wantCapacityRaw)
			check(tc.cyclesID, "cycles", tc.wantCycles, tc.wantCyclesRaw)
		})
	}
}

// TestFrame316L3AndL4KeepRawWordsWithoutCapacityOrCycles checks that the L3 and
// L4 selections keep the words of frame 0x316 as raw evidence, without capacity
// or cycles names, and that the decoder gives no capacity or cycles field.
func TestFrame316OtherLayoutsKeepRawWordsWithoutCapacityOrCycles(t *testing.T) {
	for _, identity := range []string{"QAAA0007", "QAAA0009"} {
		t.Run(identity, func(t *testing.T) {
			_, _, _, res, r := replay316(t, identity, true)
			if _, ok := r.Fields["capacity"]; ok {
				t.Error("decoder emits capacity on this selection")
			}
			if _, ok := r.Fields["cycles"]; ok {
				t.Error("decoder emits cycles on this selection")
			}
			for id, ev := range res.Rows {
				if ev.Reason == roweval.ReasonNoMatch {
					continue
				}
				if strings.HasSuffix(id, ".capacity") || strings.HasSuffix(id, ".cycles") {
					t.Errorf("%s applies on %s", id, identity)
				}
			}
			for id, raw := range map[string]uint64{
				"growatt.bms.pcs.standard.316.l3.word_2": 42, "growatt.bms.pcs.standard.316.l3.word_4": 53, "growatt.bms.pcs.standard.316.l3.word_6": 67,
			} {
				if identity == "QAAA0009" {
					id = strings.Replace(id, ".l3.", ".l4.", 1)
				}
				ev := res.Rows[id]
				if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil {
					t.Fatalf("%s: %+v", id, ev)
				}
				if ev.Native.Raw != raw || ev.Native.Disposition != dispositionRaw {
					t.Errorf("%s: raw %d disposition %q", id, ev.Native.Raw, ev.Native.Disposition)
				}
			}
		})
	}
}

// TestFrame316WithheldFieldsKeepNativeNamesAndRawEvidence checks that a field the
// measurement assertion withholds keeps its native row name and raw word, with no value.
func TestFrame316WithheldFieldsKeepNativeNamesAndRawEvidence(t *testing.T) {
	for _, tc := range []struct {
		identity, id string
		raw          uint64
	}{
		{"QAAA0006", "growatt.bms.pcs.standard.316.l2.capacity", 42},
		{"QAAA0006", "growatt.bms.pcs.standard.316.l2.cycles", 53},
		{"QAAA0011", "growatt.bms.pcs.standard.316.l8.capacity", 53},
		{"QAAA0011", "growatt.bms.pcs.standard.316.l8.cycles", 67},
	} {
		t.Run(tc.id, func(t *testing.T) {
			_, in, _, res, r := replay316(t, tc.identity, false)
			ev, ok := res.Rows[tc.id]
			if !ok || ev.Native == nil {
				t.Fatalf("%s does not apply when withheld", tc.id)
			}
			if ev.Native.Reason != gateWithheld || ev.Native.Value != nil || ev.Native.Unit != "" {
				t.Errorf("%s: reason %q value %v unit %q", tc.id, ev.Native.Reason, ev.Native.Value, ev.Native.Unit)
			}
			if ev.Native.Raw != tc.raw || ev.Native.Disposition != dispositionRaw {
				t.Errorf("%s: raw %d disposition %q", tc.id, ev.Native.Raw, ev.Native.Disposition)
			}
			if !reflect.DeepEqual(res.Evidence, in.Evidence) {
				t.Errorf("%s: evidence changed", tc.id)
			}
			if df := r.Fields[fieldOf(tc.id)]; df.Status != "raw" {
				t.Errorf("%s: decoder status %q, want raw", tc.id, df.Status)
			}
		})
	}
}

// TestGaugeRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat checks
// that the evaluator refuses, with the decoder's status and no rows, a neighbouring
// identifier, a wrong length and the extended format.
func TestGaugeRowsRefuseNeighbouringIdentifierWrongLengthAndExtendedFormat(t *testing.T) {
	base := gaugeCaseNamed(t, "l2-316-valid")
	s, in, _ := buildGauge(t, base)
	payload, err := hex.DecodeString(base.Payload)
	if err != nil {
		t.Fatal(err)
	}
	neighbour, err := canbus.NewStandardID(0x314)
	if err != nil {
		t.Fatal(err)
	}
	after, err := canbus.NewStandardID(0x319)
	if err != nil {
		t.Fatal(err)
	}
	extended, err := canbus.NewExtendedID(0x316)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := canbus.NewStandardID(0x316)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		id      canbus.Identifier
		payload []byte
	}{
		{"neighbour-314", neighbour, payload},
		{"neighbour-319", after, payload},
		{"extended-316", extended, payload},
		{"wrong-length-316", standard, payload[:7]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := canbus.NewFrame(tc.id, tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			in.Evidence.Frame = frame
			r := canbusreg.DecodePCSGauge(s, in)
			res, reason, err := EvaluateGauge(s, in)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status == "decoded" {
				t.Fatalf("decoder admits %s", tc.name)
			}
			if reason != roweval.Reason(r.Status) || len(res.Rows) != 0 {
				t.Errorf("%s: reason %q with decoder %q and %d rows", tc.name, reason, r.Status, len(res.Rows))
			}
		})
	}
}

// TestGaugeParallelPlaceholderRowsApplyUnderParallelBank replays the frame 0x315
// on the parallel bank of a QAAB0002 selection. The decoder emits nine fields
// there (eight raw bytes and the placeholder), and each must have one row under
// the parallel identifiers, so the evaluator is not silent on an admitted frame.
func TestGaugeParallelPlaceholderRowsApplyUnderParallelBank(t *testing.T) {
	c := gaugeVectorCase{
		Name:       "replay-315-parallel-qaab",
		Identifier: "0x315",
		Payload:    frame316Payload,
		Selection:  vectorSelection{Project: "QAAB", Release: "0002", Flavor: "pcs-parallel", Bank: "parallel", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1"},
	}
	s, in, _ := buildGauge(t, c)
	r := canbusreg.DecodePCSGauge(s, in)
	if r.Status != "decoded" || len(r.Fields) != 9 {
		t.Fatalf("decoder status %q with %d fields", r.Status, len(r.Fields))
	}
	res, reason, err := EvaluateGauge(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused: %v %v", reason, err)
	}
	for name := range r.Fields {
		id := "growatt.bms.pcs.parallel.315." + name
		ev, ok := res.Rows[id]
		if !ok || ev.Native == nil {
			t.Errorf("%s: no parallel row applies", id)
			continue
		}
		if ev.Native.Reason != "" && name != "placeholder" {
			t.Errorf("%s: reason %q", id, ev.Native.Reason)
		}
	}
	if raw := res.Rows["growatt.bms.pcs.parallel.315.byte_0"].Native.Raw; raw != 19 {
		t.Errorf("byte_0 raw %d, want 19", raw)
	}
	if ev := res.Rows["growatt.bms.pcs.parallel.315.placeholder"]; ev.Native == nil || ev.Native.Raw != 0 {
		t.Errorf("placeholder raw %v, want 0", ev.Native)
	}
	if _, ok := res.Rows["growatt.bms.pcs.standard.315.placeholder"]; ok {
		t.Error("a standard-bank placeholder row applies on the parallel bank")
	}
	checkGaugeEvidence(t, c.Name, res, in, r)
}

// TestGaugePlaceholderRawIsDecoderZeroNotByteZero replays a nonzero gateway
// frame 0x316: the placeholder keeps the decoder's raw zero, while byte_0 keeps
// its own raw word 19. The withheld placeholder has no value.
func TestGaugePlaceholderRawIsDecoderZeroNotByteZero(t *testing.T) {
	c := gaugeCaseNamed(t, "placeholder-316-nonzero-payload")
	s, in, _ := buildGauge(t, c)
	res, reason, err := EvaluateGauge(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused: %v %v", reason, err)
	}
	ph := res.Rows["growatt.bms.pcs.standard.316.placeholder"].Native
	if ph == nil || ph.Raw != 0 || ph.Value != nil || ph.Reason != gateWithheld {
		t.Errorf("placeholder %+v, want raw 0 withheld", ph)
	}
	if first := res.Rows["growatt.bms.pcs.standard.316.byte_0"].Native; first == nil || first.Raw != 19 {
		t.Errorf("byte_0 %+v, want raw 19", first)
	}
}

// TestGaugeRetainedFaultBitDispositionFollowsDecoder replays a 0x318 frame whose
// retained byte is all ones under the release-11 selection and checks that each
// retained fault bit row keeps the accepted or raw disposition the decoder gives
// its field, and that at least one named bit is accepted.
func TestGaugeRetainedFaultBitDispositionFollowsDecoder(t *testing.T) {
	c := gaugeVectorCase{
		Name:       "retained-bits-318-l8-named",
		Identifier: "0x318",
		Payload:    "FF00000000000000",
		Selection:  vectorSelection{Project: "QAAA", Release: "0011", Flavor: "pcs-standard", Bank: "ordinary", Scope: "local", Endpoint: "coordinator", Group: "0", Slot: "1"},
		Input:      gaugeVectorInput{MeasurementValid: true},
	}
	s, in, _ := buildGauge(t, c)
	r := canbusreg.DecodePCSGauge(s, in)
	if r.Status != "decoded" {
		t.Fatalf("decoder status %q", r.Status)
	}
	res, reason, err := EvaluateGauge(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused: %v %v", reason, err)
	}
	accepted := 0
	for id, field := range gaugeBitFields() {
		df, ok := r.Fields[field]
		if !ok {
			continue
		}
		ev, ok := res.Rows[id]
		if !ok || ev.Native == nil {
			continue
		}
		if ev.Native.Disposition != gaugeBitDisposition(df.Status) {
			t.Errorf("%s: row disposition %q, decoder status %q", field, ev.Native.Disposition, df.Status)
		}
		if df.Status == "accepted" {
			accepted++
		}
	}
	if accepted == 0 {
		t.Fatal("no retained fault bit is accepted by the decoder under the named selection")
	}
}

// TestGaugeParallelBankFramesKeepEveryDecoderField replays the admitted QAAB0002
// parallel-bank frames 0x316, 0x317 and 0x318. The decoder emits seven, eight and
// eight fields there, each raw, and every field must have one applying row under
// the parallel identifiers with the same raw word and the raw disposition.
func TestGaugeParallelBankFramesKeepEveryDecoderField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields int
	}{
		{"parallel-316-l6-raw-at-l2-positions", 7},
		{"parallel-317-other-soh-and-slot", 8},
		{"parallel-318-other-retained-bytes", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := gaugeCaseNamed(t, tc.name)
			if c.Selection.Flavor != "pcs-parallel" {
				t.Fatalf("%s is not a parallel-bank vector", tc.name)
			}
			s, in, frame := buildGauge(t, c)
			r := canbusreg.DecodePCSGauge(s, in)
			if r.Status != "decoded" || len(r.Fields) != tc.fields {
				t.Fatalf("decoder status %q with %d fields, want %d", r.Status, len(r.Fields), tc.fields)
			}
			res, reason, err := EvaluateGauge(s, in)
			if err != nil || reason != "" {
				t.Fatalf("evaluate refused: %v %v", reason, err)
			}
			applied := 0
			for id, ev := range res.Rows {
				if ev.Reason != roweval.ReasonNoMatch {
					if !strings.HasPrefix(id, "growatt.bms.pcs.parallel.") {
						t.Errorf("%s applies on the parallel bank", id)
					}
					applied++
				}
			}
			if applied != tc.fields {
				t.Errorf("%d rows apply, want %d", applied, tc.fields)
			}
			for name, df := range r.Fields {
				id := ""
				for candidate, ev := range res.Rows {
					if ev.Reason != roweval.ReasonNoMatch && fieldOf(candidate) == name {
						id = candidate
					}
				}
				if id == "" {
					t.Errorf("%s: no parallel row applies", name)
					continue
				}
				checkGaugeField(t, c.Name, name, df, gaugeRowsByID()[id], gaugeGated()[id], res.Rows[id].Native, frame)
			}
		})
	}
}

// TestGaugeParallelL6CapacityAndCyclesStayRawUnderParallelBank checks that the
// L6 words at the L2 byte positions keep their raw evidence on the parallel bank,
// under the parallel L6 identifiers, as raw words with no unit and no L2 capacity
// or cycles name.
func TestGaugeParallelBankSixthLayoutWordsStayRaw(t *testing.T) {
	c := gaugeCaseNamed(t, "parallel-316-l6-raw-at-l2-positions")
	s, in, _ := buildGauge(t, c)
	res, reason, err := EvaluateGauge(s, in)
	if err != nil || reason != "" {
		t.Fatalf("evaluate refused: %v %v", reason, err)
	}
	for id, raw := range map[string]uint64{
		"growatt.bms.pcs.parallel.316.l6.capacity": 42,
		"growatt.bms.pcs.parallel.316.l6.cycles":   53,
	} {
		ev := res.Rows[id]
		if ev.Native == nil || ev.Native.Raw != raw || ev.Native.Disposition != dispositionRaw || ev.Native.Reason != "" {
			t.Errorf("%s: %+v, want raw %d with the raw disposition", id, ev.Native, raw)
		}
		if ev.Native != nil && ev.Native.Unit != "" {
			t.Errorf("%s: unit %q, want none (raw word, not Ah or cycles)", id, ev.Native.Unit)
		}
	}
	for id := range res.Rows {
		if strings.HasPrefix(id, "growatt.bms.pcs.standard.") {
			t.Errorf("standard row %s applies on the parallel bank", id)
		}
	}
}
