package bms

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// serialVector is one synthetic 0x324 frame and its independent expectation,
// written from the serial response layout: byte 0 is the frame number, frame 0
// carries the battery identifier at byte 1 and six serial characters at bytes 2
// to 7, and a later frame carries seven serial characters at bytes 1 to 7.
type serialVector struct {
	Name        string `json:"name"`
	FrameNumber uint8  `json:"frame_number"`
	DataHex     string `json:"data_hex"`
	BatteryID   *uint8 `json:"battery_id"`
	FragmentHex string `json:"serial_fragment_hex"`
}

func readSerialVectors(t *testing.T) []serialVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/v108_serial_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []serialVector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// serialEvaluations evaluates one 0x324 vector under the V1.08 profile and checks
// that the frame is admitted.
func serialEvaluations(t *testing.T, v serialVector) map[string]roweval.Evaluation {
	t.Helper()
	frame, raw := commonFrame(t, 0x324, v.DataHex)
	evals, reason, err := EvaluateV108Frame(frame, acquired(raw, storageRoles(t)))
	if err != nil {
		t.Fatalf("%s: %v", v.Name, err)
	}
	if reason != "" {
		t.Fatalf("%s: the frame gives reason %q, want admitted", v.Name, reason)
	}
	return evals
}

// fragmentPattern is the bit pattern the serial fragment row extracts from the
// fragment bytes of the vector: the bitSlice order of the V1.08 rows.
func fragmentPattern(t *testing.T, fragmentHex string) uint64 {
	t.Helper()
	bytes, err := hex.DecodeString(fragmentHex)
	if err != nil {
		t.Fatal(err)
	}
	return rawBitPattern(bytes, false)
}

// checkSerialFragment pins that the evaluated serial_fragment row holds exactly
// the fragment bytes of the vector, in its own layout.
func checkSerialFragment(t *testing.T, v serialVector, ev roweval.Evaluation) {
	t.Helper()
	if ev.Native == nil || ev.Native.Reason != "" || ev.Native.Value == nil {
		t.Fatalf("%s: serial_fragment gave no native value: %+v", v.Name, ev.Native)
	}
	if want := fragmentPattern(t, v.FragmentHex); ev.Native.Raw != want {
		t.Fatalf("%s: serial_fragment raw %#x, want the fragment bytes %s give %#x", v.Name, ev.Native.Raw, v.FragmentHex, want)
	}
}

// TestV108SerialFrameZeroGivesTheBatteryIdAndSixSerialBytes pins that frame 0 of
// the serial response gives the battery identifier at byte 1 and the six serial
// characters of bytes 2 to 7, and that the frame number is zero.
func TestV108SerialFrameZeroGivesTheBatteryIdAndSixSerialBytes(t *testing.T) {
	checked := 0
	for _, v := range readSerialVectors(t) {
		if v.FrameNumber != 0 {
			continue
		}
		checked++
		evals := serialEvaluations(t, v)
		number := evals[V108Profile+".serial_frame_number"]
		if number.Native == nil || number.Native.Raw != 0 {
			t.Fatalf("%s: serial_frame_number is not zero: %+v", v.Name, number.Native)
		}
		battery := evals[V108Profile+".serial_battery_id"]
		if battery.Native == nil || battery.Native.Reason != "" || battery.Native.Value == nil || v.BatteryID == nil {
			t.Fatalf("%s: serial_battery_id gave no value: %+v", v.Name, battery.Native)
		}
		if !decimalEquals(battery.Native.Value.Quantity.Number, fmt.Sprint(*v.BatteryID)) {
			t.Fatalf("%s: serial_battery_id is %s, want %d", v.Name, battery.Native.Value.Quantity.Number.Coefficient, *v.BatteryID)
		}
		checkSerialFragment(t, v, evals[V108Profile+".serial_fragment"])
		if n := len(v.FragmentHex) / 2; n != 6 {
			t.Fatalf("%s: vector gives %d fragment bytes, want six", v.Name, n)
		}
	}
	if checked == 0 {
		t.Fatal("no frame zero vector")
	}
}

// TestV108SerialLaterFrameGivesSevenSerialBytesAndNoBatteryId pins that a later
// frame gives seven serial characters at bytes 1 to 7 and no battery identifier.
func TestV108SerialLaterFrameGivesSevenSerialBytesAndNoBatteryId(t *testing.T) {
	checked := 0
	for _, v := range readSerialVectors(t) {
		if v.FrameNumber == 0 {
			continue
		}
		checked++
		evals := serialEvaluations(t, v)
		if _, applies := evals[V108Profile+".serial_battery_id"]; applies {
			t.Fatalf("%s: a later frame gives a serial_battery_id row", v.Name)
		}
		number := evals[V108Profile+".serial_frame_number"]
		if number.Native == nil || number.Native.Raw != uint64(v.FrameNumber) {
			t.Fatalf("%s: serial_frame_number is not %d: %+v", v.Name, v.FrameNumber, number.Native)
		}
		checkSerialFragment(t, v, evals[V108Profile+".serial_fragment"])
		if n := len(v.FragmentHex) / 2; n != 7 {
			t.Fatalf("%s: vector gives %d fragment bytes, want seven", v.Name, n)
		}
	}
	if checked == 0 {
		t.Fatal("no later frame vector")
	}
}

// TestV108SerialRowsFailWhenDeleted pins the two layouts of the serial response
// as rows of the V1.08 table: one serial_battery_id row, and two serial_fragment
// rows whose conditions are disjoint. Deleting either the battery identifier row
// or a fragment layout gives a count the test refuses, and the evaluation of the
// matching frame would give no value for it.
func TestV108SerialRowsFailWhenDeleted(t *testing.T) {
	count := map[string]int{}
	for _, r := range V108Table().Native {
		if r.At.Identifier == 0x324 {
			count[rowNameIn(V108Profile, r.ID)]++
		}
	}
	if count["serial_battery_id"] != 1 {
		t.Fatalf("the table has %d serial_battery_id rows, want one", count["serial_battery_id"])
	}
	if count["serial_fragment"] != 2 {
		t.Fatalf("the table has %d serial_fragment rows, want one per layout", count["serial_fragment"])
	}
	for _, v := range readSerialVectors(t) {
		evals := serialEvaluations(t, v)
		if ev, ok := evals[V108Profile+".serial_fragment"]; !ok || ev.Native == nil {
			t.Fatalf("%s: no serial_fragment evaluation", v.Name)
		}
		_, battery := evals[V108Profile+".serial_battery_id"]
		if battery != (v.FrameNumber == 0) {
			t.Fatalf("%s: serial_battery_id present %v, want %v", v.Name, battery, v.FrameNumber == 0)
		}
	}
}
