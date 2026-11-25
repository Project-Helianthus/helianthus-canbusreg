//go:build linux

package can

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type witness struct{ Path, Case, Payload string }

type gatewayStep struct {
	Frame struct {
		wireVector
		PayloadLength uint8 `json:"payload_length"`
		DLCOverride   uint8 `json:"len8_dlc"`
	}
	Valid, Listed bool
	DLC           uint8
	RawDLC        uint8 `json:"raw_dlc"`
}

type nativeWitness struct {
	Steps []struct {
		Frame         wireVector
		Valid, Listed bool
	}
	Result struct {
		Profile    string
		Projection *canbusreg.GrowattLowVoltageBMSV104Projection
	}
}

type gatewayWitness struct {
	Steps  []gatewayStep
	Result struct {
		Snapshot *gatewaySnapshot
	}
}

type gatewaySnapshot struct {
	Admitted        bool
	OutboundAllowed bool `json:"outbound_allowed"`
	Profile         string
	Limits          struct {
		ChargeVoltageDecivolts   uint16 `json:"charge_voltage_decivolts"`
		ChargeCurrentDeciamps    uint16 `json:"charge_current_deciamps"`
		DischargeCurrentDeciamps uint16 `json:"discharge_current_deciamps"`
		RawStatus                uint16 `json:"raw_status"`
	}
	Status struct {
		Protection     [2]byte
		Warning        [2]byte
		PackCount      uint8 `json:"pack_count"`
		Manufacturer   [2]byte
		TotalCellCount uint8 `json:"total_cell_count"`
	}
	Measurements struct {
		VoltageCentivolts           int16 `json:"voltage_centivolts"`
		CurrentDeciamps             int16 `json:"current_deciamps"`
		MaximumCellTemperatureDeciC int16 `json:"maximum_cell_temperature_decic"`
		SOCPercent                  uint8 `json:"soc_percent"`
		SOHValue                    uint8 `json:"soh_value"`
		SOHValid                    bool  `json:"soh_valid"`
	}
}

func compareIndexedWitnesses(t *testing.T) {
	t.Helper()
	witnesses := readFixture[[]witness](t, "testdata/count-witnesses.json")
	roles := exampleRoles(t)
	byCase := make(map[string]map[string]witness)
	for _, w := range witnesses {
		kind := strings.Split(w.Path, "/")[0]
		if kind == "current" {
			kind = strings.Split(w.Path, "/")[1]
		}
		if byCase[w.Case] == nil {
			byCase[w.Case] = make(map[string]witness)
		}
		if byCase[w.Case][kind].Path != "" || w.Payload == "" {
			t.Fatalf("duplicate or empty witness %s", w.Path)
		}
		byCase[w.Case][kind] = w
	}
	cases := []string{"can-311-short-7-bytes", "can-312-cells-0", "can-312-count-0", "can-312-count-254-cells-254", "can-312-count-255-cells-16", "can-312-count4-eq-1-byte7-eq-255", "can-312-extended-identifier", "can-313-soc-100", "can-313-soc-101", "can-313-soc-sentinel-ff", "can-neighbour-310", "can-neighbour-314", "can-311-312-313"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			parts := byCase[name]
			native := unpackWitness[nativeWitness](t, parts["canbusreg"])
			gateway := unpackWitness[gatewayWitness](t, parts["gateway"])
			if len(native.Steps) == 0 || len(gateway.Steps) != len(native.Steps) {
				t.Fatal("missing paired observations")
			}
			var frames []wireVector
			divergentCount := false
			for i, step := range native.Steps {
				v := step.Frame
				v.DLC = uint8(len(v.Data) / 2)
				v.RawDLC = v.DLC
				if gw := gateway.Steps[i]; gw.Frame.wireVector != step.Frame || gw.Listed != step.Listed || gw.DLC != v.DLC || gw.RawDLC != v.RawDLC {
					t.Fatal("donor operands differ", step, gw)
				}
				if step.Valid != gateway.Steps[i].Valid {
					if v.Identifier != 0x312 || step.Valid || !step.Listed {
						t.Fatal("unexpected donor disposition difference")
					}
					divergentCount = true
				}
				frames = append(frames, v)
			}
			if divergentCount != (name == "can-312-count4-eq-1-byte7-eq-255" || name == "can-312-count-255-cells-16") {
				t.Fatal("missing or unexpected gateway 255 difference")
			}
			records := receiveVectors(t, frames...)
			classifier := canbusreg.GrowattLowVoltageBMSV104()
			var last canbusreg.Classification
			results := make(map[uint32]map[string]roweval.Evaluation)
			for i, record := range records {
				e, err := decodeRecord(record)
				if err != nil {
					t.Fatal(err)
				}
				assertWitnessEvidence(t, e, record, gateway.Steps[i])
				last = classifier.Classify(e)
				a := acquired(record, roles)
				rows, reason, err := evaluateV104(record, a)
				step := native.Steps[i]
				signedHere := e.Frame.ID().Value() >= 0x311 && e.Frame.ID().Value() <= 0x313
				if err != nil || (step.Valid && step.Listed && signedHere) != (reason == "" && len(rows) > 0) {
					t.Fatalf("step %d (%+v): %s / %v", i, step, reason, err)
				}
				if !step.Valid && (len(rows) != 0 || reason == "") {
					t.Fatalf("step %d admitted an invalid frame: %v", i, rows)
				}
				if len(rows) > 0 {
					results[e.Frame.ID().Value()] = rows
				}
				if !step.Valid && gateway.Steps[i].Valid {
					p := e.Frame.Bytes()
					if gateway.Result.Snapshot == nil || gateway.Result.Snapshot.Status.PackCount != p[4] || gateway.Result.Snapshot.Status.TotalCellCount != p[7] {
						t.Fatal("missing historical native count values")
					}
					t.Logf("%s spec/selector rejects, frozen gateway accepts: pack=%d cells=%d", frames[i].Data, p[4], p[7])
				}
			}
			if last.Profile != native.Result.Profile {
				t.Fatalf("native classification = %q; want %q", last.Profile, native.Result.Profile)
			}
			if native.Result.Profile != "" {
				compareNativeProjection(t, last, native.Result.Projection, records, gateway.Steps)
			}
			compareGatewaySnapshot(t, last, gateway.Result.Snapshot, divergentCount, results, records, gateway.Steps, roles)
			switch name {
			case "can-312-count4-eq-1-byte7-eq-255":
				spec := unpackWitness[struct {
					Input struct {
						Data string `json:"frame_312_hex"`
					}
					Expected struct{ Verdict string }
				}](t, parts["specification"])
				if spec.Input.Data != frames[1].Data || spec.Expected.Verdict != "reject" || results[0x312] != nil {
					t.Fatal("missing specification differential on the same byte-7 operand")
				}
			case "can-311-312-313":
				spec := unpackWitness[struct {
					Input struct {
						Data string `json:"frame_313_hex"`
					}
					Expected struct {
						Fact, Unit string
						Value      struct{ Number string }
					}
				}](t, parts["specification"])
				if spec.Input.Data != frames[2].Data || spec.Expected.Fact != "storage.pack.current" || spec.Expected.Unit != "A" {
					t.Fatal("binding operand differs")
				}
				assertFact(t, results[0x313]["current"], "current", spec.Expected.Value.Number, acquired(records[2], roles))
			}
		})
	}
	w := byCase["pack-255"]["cmd"]
	var id uint32
	var ide, rtr, accepted bool
	var dlc, rawDLC uint8
	var data string
	if _, err := fmt.Sscanf(w.Payload, "id=0x%x extended=%t rtr=%t dlc=%d raw_dlc=%d data=%s accepted=%t", &id, &ide, &rtr, &dlc, &rawDLC, &data, &accepted); err != nil || !accepted {
		t.Fatalf("missing byte-4 classifier result: %v", err)
	}
	for _, v := range readFixture[[]canVector](t, "testdata/growatt_v104_vectors.json") {
		if v.Name != "pack-255" {
			continue
		}
		want := wireVector{Identifier: id, Extended: ide, RTR: rtr, DLC: dlc, RawDLC: rawDLC, Data: data}
		if v.Identifier != id || !reflect.DeepEqual(v.record(t), want.record(t)) || v.GatewayAccepts == nil || *v.GatewayAccepts != accepted {
			t.Fatal("scratch donor and byte-4 vector operands differ")
		}
		r := receiveVectors(t, v.wireVector)[0]
		got, reason, err := evaluateV104(r, acquired(r, roles))
		if len(got) != 0 || reason != "not valid" || err != nil {
			t.Fatalf("byte-4 comparison: %s / %v / %v", reason, err, got)
		}
		t.Logf("%s payload SHA-256 %x: %s; spec and selector reject", w.Path, sha256.Sum256([]byte(w.Payload)), strings.TrimSpace(w.Payload))
		return
	}
	t.Fatal("missing required byte-4 comparison")
}

func unpackWitness[T any](t *testing.T, w witness) T {
	t.Helper()
	var d T
	if w.Path == "" {
		t.Fatal("missing indexed witness")
	}
	if err := json.Unmarshal([]byte(w.Payload), &d); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s (%s), payload SHA-256 %x", w.Path, w.Case, sha256.Sum256([]byte(w.Payload)))
	return d
}

func assertWitnessEvidence(t *testing.T, got canbusreg.Evidence, record receiver.Record, donor gatewayStep) {
	t.Helper()
	wire := donor.Frame.wireVector
	wire.DLC, wire.RawDLC = donor.DLC, donor.RawDLC
	payload, err := hex.DecodeString(wire.Data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Frame.ID().Value() != wire.Identifier || got.Frame.ID().Extended() != wire.Extended ||
		got.Frame.DLC() != donor.DLC || got.Frame.RawDLC() != donor.RawDLC || got.RawRecord[4] != donor.Frame.PayloadLength ||
		got.RawRecord[7] != donor.Frame.DLCOverride || !bytes.Equal(got.Frame.Data(), payload) ||
		!bytes.Equal(got.RawRecord[:], wire.record(t)) {
		t.Fatalf("destination wire evidence %+v differs from emitted donor frame %+v", got, donor)
	}
	if got.Interface != record.Interface || got.Sequence != record.Sequence || got.Monotonic != record.Monotonic {
		t.Fatalf("destination lost receiver metadata: %+v", got)
	}
}

func compareNativeProjection(t *testing.T, last canbusreg.Classification, want *canbusreg.GrowattLowVoltageBMSV104Projection, records []receiver.Record, wire []gatewayStep) {
	t.Helper()
	p, ok := last.Projection.(canbusreg.GrowattLowVoltageBMSV104Projection)
	if !ok || want == nil || p.Limits != want.Limits || p.Status != want.Status || p.Measurements != want.Measurements {
		t.Fatalf("donor native values differ: %+v, want %+v", p, want)
	}
	for i, c := range []struct {
		got, want canbusreg.Evidence
	}{
		{p.LimitsEvidence, want.LimitsEvidence}, {p.StatusEvidence, want.StatusEvidence}, {p.MeasurementsEvidence, want.MeasurementsEvidence},
	} {
		seq := c.want.Sequence
		if seq == 0 || seq > uint64(len(records)) || wire[seq-1].Frame.Identifier != uint32(0x311+i) || c.got.Sequence != seq || p.Interface != c.got.Interface {
			t.Fatalf("donor evidence ordinal or destination interface differs: got %d want %d", c.got.Sequence, seq)
		}
		// The canbusreg donor emits the ordinal, but not a usable Frame or RawRecord.
		// The paired gateway witness emits the wire fields for that same ordinal.
		assertWitnessEvidence(t, c.got, records[seq-1], wire[seq-1])
	}
}

func compareGatewaySnapshot(t *testing.T, last canbusreg.Classification, want *gatewaySnapshot, divergent bool, rows map[uint32]map[string]roweval.Evaluation, records []receiver.Record, wire []gatewayStep, roles roweval.Roles) {
	t.Helper()
	p, projected := last.Projection.(canbusreg.GrowattLowVoltageBMSV104Projection)
	if want == nil {
		if projected {
			t.Fatal("destination projected a tuple not admitted by the gateway witness")
		}
		return
	}
	if !want.Admitted || want.OutboundAllowed || want.Profile != "growatt.bms.low_voltage.can.v1_04" || projected == divergent {
		t.Fatalf("destination and donor dispositions differ beyond the known count divergence: %+v", want)
	}
	limits := canbusreg.GrowattLowVoltageBMSV104Limits(want.Limits)
	status := canbusreg.GrowattLowVoltageBMSV104Status(want.Status)
	measurements := canbusreg.GrowattLowVoltageBMSV104Measurements(want.Measurements)
	if projected && (last.Profile != want.Profile || p.Limits != limits || p.Status != status || p.Measurements != measurements) {
		t.Fatalf("gateway native output differs: got %+v, want %+v", p, want)
	}
	for i, r := range records {
		// Every expected value below comes from the emitted snapshot, not a second
		// decode of the destination. The receive metadata comes from the local receiver.
		at := wire[i].Frame.Identifier
		a := acquired(r, roles)
		switch at {
		case 0x311:
			assertGatewayRows(t, rows[at], a, map[string]int64{"charge_voltage_limit": int64(limits.ChargeVoltageDecivolts), "charge_current_limit": int64(limits.ChargeCurrentDeciamps), "discharge_current_limit": int64(limits.DischargeCurrentDeciamps), "status_word": int64(limits.RawStatus)})
		case 0x312:
			if divergent {
				if len(rows[at]) != 0 {
					t.Fatal("255 admitted a destination status row")
				}
				continue
			}
			assertGatewayRows(t, rows[at], a, map[string]int64{"pack_count": int64(status.PackCount), "total_cell_count": int64(status.TotalCellCount)})
		case 0x313:
			assertGatewayRows(t, rows[at], a, map[string]int64{"voltage": int64(measurements.VoltageCentivolts), "current": int64(measurements.CurrentDeciamps), "maximum_cell_temperature": int64(measurements.MaximumCellTemperatureDeciC), "state_of_charge": int64(measurements.SOCPercent)})
			soh := rows[at]["state_of_health"]
			if want.Measurements.SOHValid {
				assertNative(t, soh, "state_of_health", fmt.Sprint(measurements.SOHValue), a.Template)
			} else if soh.Reason != "not valid" || soh.Native == nil || soh.Native.Value != nil || soh.Native.Raw != uint64(measurements.SOHValue) || soh.Candidate != nil {
				t.Fatalf("invalid gateway SOH became a destination value: %+v", soh)
			}
		}
	}
}

func assertGatewayRows(t *testing.T, got map[string]roweval.Evaluation, a roweval.Acquired[Selector], want map[string]int64) {
	t.Helper()
	for name, value := range want {
		assertNative(t, got[name], name, fmt.Sprint(value), a.Template)
	}
}

func assertNativeCount(t *testing.T, received receiver.Record, accepted bool) {
	t.Helper()
	// Supply the other two native reports before the count frame, to make the count disposition observable.
	r := receiveVectors(t, wireVector{Identifier: 0x311, DLC: 8, RawDLC: 8, Data: "023800FA01F40043"}, wireVector{Identifier: 0x313, DLC: 8, RawDLC: 8, Data: "1400FFF6012C50FF"})
	classifier := canbusreg.GrowattLowVoltageBMSV104()
	for _, rec := range r {
		e, err := decodeRecord(rec)
		if err != nil {
			t.Fatal(err)
		}
		classifier.Classify(e)
	}
	e, err := decodeRecord(received)
	if err != nil {
		t.Fatal(err)
	}
	p, admitted := classifier.Classify(e).Projection.(canbusreg.GrowattLowVoltageBMSV104Projection)
	if admitted != accepted {
		t.Fatalf("native count classifier admitted=%t want %t", admitted, accepted)
	}
	if admitted {
		raw := e.Frame.Bytes()
		if p.Status.PackCount != raw[4] || p.Status.TotalCellCount != raw[7] || p.StatusEvidence != e {
			t.Fatal("native counts or evidence differ", p)
		}
	}
}
