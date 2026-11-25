//go:build linux

package can

import (
	"crypto/sha256"
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

type nativeWitness struct {
	Steps []struct {
		Frame         wireVector
		Valid, Listed bool
		DLC           uint8
		RawDLC        uint8 `json:"raw_dlc"`
	}
	Result struct {
		Profile    string
		Projection *canbusreg.GrowattLowVoltageBMSV104Projection
		Snapshot   *struct {
			Status struct {
				Pack  uint8 `json:"pack_count"`
				Cells uint8 `json:"total_cell_count"`
			}
		}
	}
}

func compareIndexedWitnesses(t *testing.T) {
	t.Helper()
	witnesses := readFixture[[]witness](t, "testdata/qq01-count-witnesses.json")
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
			gateway := unpackWitness[nativeWitness](t, parts["gateway"])
			if len(native.Steps) == 0 || len(gateway.Steps) != len(native.Steps) {
				t.Fatal("missing paired observations")
			}
			var frames []wireVector
			divergentCount := false
			for i, step := range native.Steps {
				v := step.Frame
				v.DLC = uint8(len(v.Data) / 2)
				v.RawDLC = v.DLC
				if gw := gateway.Steps[i]; gw.Frame != step.Frame || gw.Listed != step.Listed || gw.DLC != v.DLC || gw.RawDLC != v.RawDLC {
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
					if gateway.Result.Snapshot == nil || gateway.Result.Snapshot.Status.Pack != p[4] || gateway.Result.Snapshot.Status.Cells != p[7] {
						t.Fatal("missing historical native count values")
					}
					t.Logf("%s spec/selector rejects, frozen gateway accepts: pack=%d cells=%d", frames[i].Data, p[4], p[7])
				}
			}
			if last.Profile != native.Result.Profile {
				t.Fatalf("native classification = %q; want %q", last.Profile, native.Result.Profile)
			}
			if native.Result.Profile != "" {
				compareNativeProjection(t, last, native.Result.Projection, results, records, roles)
			}
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

func compareNativeProjection(t *testing.T, last canbusreg.Classification, want *canbusreg.GrowattLowVoltageBMSV104Projection, rows map[uint32]map[string]roweval.Evaluation, records []receiver.Record, roles roweval.Roles) {
	t.Helper()
	p, ok := last.Projection.(canbusreg.GrowattLowVoltageBMSV104Projection)
	if !ok || want == nil || p.Limits != want.Limits || p.Status != want.Status || p.Measurements != want.Measurements {
		t.Fatalf("donor native values differ: %+v, want %+v", p, want)
	}
	for _, c := range []struct {
		got, want canbusreg.Evidence
		native    map[string]int64
	}{
		{p.LimitsEvidence, want.LimitsEvidence, map[string]int64{"charge_voltage_limit": int64(p.Limits.ChargeVoltageDecivolts), "charge_current_limit": int64(p.Limits.ChargeCurrentDeciamps), "discharge_current_limit": int64(p.Limits.DischargeCurrentDeciamps), "status_word": int64(p.Limits.RawStatus)}},
		{p.StatusEvidence, want.StatusEvidence, map[string]int64{"pack_count": int64(p.Status.PackCount), "total_cell_count": int64(p.Status.TotalCellCount)}},
		{p.MeasurementsEvidence, want.MeasurementsEvidence, map[string]int64{"voltage": int64(p.Measurements.VoltageCentivolts), "current": int64(p.Measurements.CurrentDeciamps), "maximum_cell_temperature": int64(p.Measurements.MaximumCellTemperatureDeciC), "state_of_charge": int64(p.Measurements.SOCPercent), "state_of_health": int64(p.Measurements.SOHValue)}},
	} {
		if c.got.Sequence != c.want.Sequence || c.got.Sequence == 0 || int(c.got.Sequence) > len(records) {
			t.Fatal("donor evidence ordinal differs")
		}
		r := records[c.got.Sequence-1]
		e, err := decodeRecord(r)
		if err != nil || e != c.got {
			t.Fatal("native decoder lost received evidence", err)
		}
		a := acquired(r, roles)
		for name, n := range c.native {
			assertNative(t, rows[e.Frame.ID().Value()][name], name, fmt.Sprint(n), a.Template)
		}
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
