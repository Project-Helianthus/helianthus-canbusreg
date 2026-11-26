package pcs

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type parallelIdentityVectorFile struct {
	Cases []parallelIdentityVectorCase `json:"cases"`
}

type parallelIdentityVectorCase struct {
	Name       string                    `json:"name"`
	Identifier string                    `json:"identifier"`
	Extended   bool                      `json:"extended"`
	Payload    string                    `json:"payload"`
	Selection  diagVectorSelection       `json:"selection"`
	Expect     map[string]diagVectorWant `json:"expect"`
	Refused    string                    `json:"refused"`
}

func parallelLoadVectors(t *testing.T) []parallelIdentityVectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/parallel_identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var f parallelIdentityVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no parallel identity vectors")
	}
	return f.Cases
}

func parallelRowID(identifier, key string) string {
	id, err := strconv.ParseUint(strings.TrimPrefix(identifier, "0x"), 16, 32)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s%X.%s", parallelPrefix, uint32(id)&^15, key)
}

func parallelEvaluate(t *testing.T, c parallelIdentityVectorCase) (canbusreg.NativeResult, ParallelResult, roweval.Reason) {
	t.Helper()
	s := diagSelection(t, c.Selection)
	in := diagLinkInput(s, diagFrame(t, c.Identifier, c.Extended, c.Payload))
	dec := canbusreg.ParallelIdentityDecodeParallel(s, in)
	res, reason, err := EvaluateParallelIdentity(s, in)
	if err != nil {
		t.Fatal(err)
	}
	return dec, res, reason
}

// TestParallelIdentityRowsMatchDecoder checks every field of every vector
// against the decoder: the native value, the raw word and the disposition, and
// against the vector's independent expectation. Every field the decoder emits
// has a row of the same value, and every row that applies has a decoder field.
func TestParallelIdentityRowsMatchDecoder(t *testing.T) {
	for _, c := range parallelLoadVectors(t) {
		if c.Refused != "" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			dec, res, reason := parallelEvaluate(t, c)
			if reason != "" {
				t.Fatalf("evaluate refused: %v", reason)
			}
			if len(dec.Fields) == 0 {
				t.Fatal("decoder emits no fields")
			}
			byKey := map[string]roweval.Evaluation{}
			for id, ev := range res.Rows {
				if ev.Native == nil {
					continue
				}
				key := id[strings.LastIndex(id, ".")+1:]
				if _, dup := byKey[key]; dup {
					t.Fatalf("two rows apply to key %s", key)
				}
				byKey[key] = ev
			}
			for key, f := range dec.Fields {
				if strings.HasPrefix(key, "current_fault_") || strings.HasPrefix(key, "fault_") {
					continue
				}
				ev, ok := byKey[key]
				if !ok {
					t.Fatalf("%s: no row", key)
				}
				if !strings.HasPrefix(ev.Native.RowID, parallelRowID(c.Identifier, "")) {
					t.Errorf("%s: row id %q does not carry the family", key, ev.Native.RowID)
				}
				kept := f.Status == "accepted" || f.Status == "raw"
				if (ev.Native.Reason == "") != kept || kept != (ev.Native.Value != nil) {
					t.Errorf("%s: row reason %q value %v, decoder status %q", key, ev.Native.Reason, ev.Native.Value, f.Status)
				}
				if ev.Native.Raw != uint64(f.Numerator) {
					t.Errorf("%s: raw %d, decoder %d", key, ev.Native.Raw, f.Numerator)
				}
				if kept && ev.Native.Disposition != f.Status {
					t.Errorf("%s: disposition %q, decoder %q", key, ev.Native.Disposition, f.Status)
				}
			}
			for key := range byKey {
				if _, ok := dec.Fields[key]; !ok {
					t.Errorf("%s: row applies but the decoder emits no field", key)
				}
			}
			for key, want := range c.Expect {
				f, ok := dec.Fields[key]
				ev := byKey[key]
				if want.Status == "absent" {
					if ok || ev.Native != nil {
						t.Errorf("%s: field or row present, vector says absent", key)
					}
					continue
				}
				if !ok || ev.Native == nil {
					t.Fatalf("%s: decoder field %v row %v", key, ok, ev.Native != nil)
				}
				diagCheckWant(t, key, want, f.Status, ok, ev)
			}
			if got := res.Attribution; got.Bank != "parallel" || !got.HasGroup || got.Group != c.Selection.Group || got.HasSlot {
				t.Errorf("attribution %+v", got)
			}
		})
	}
}

// TestParallelIdentityRowsRefuseNeighbouringFamilyWrongLengthAndExtendedFormat
// checks that a frame the decoder refuses, or admits without a field, has no
// rows and carries the decoder's status; this covers the neighbouring families,
// a group nibble other than the selected one, a coordinator frame outside the
// aggregate scope, a short frame, the extended format and 0x37F and 0x3F7.
func TestParallelIdentityRowsRefuseNeighbouringFamilyWrongLengthAndExtendedFormat(t *testing.T) {
	refused := 0
	for _, c := range parallelLoadVectors(t) {
		if c.Refused == "" {
			continue
		}
		refused++
		t.Run(c.Name, func(t *testing.T) {
			dec, res, reason := parallelEvaluate(t, c)
			if len(dec.Fields) != 0 || dec.Status != c.Refused {
				t.Fatalf("decoder status %q with %d fields, vector %q", dec.Status, len(dec.Fields), c.Refused)
			}
			if string(reason) != c.Refused || len(res.Rows) != 0 {
				t.Errorf("evaluator reason %q with %d rows, vector %q", reason, len(res.Rows), c.Refused)
			}
		})
	}
	if refused == 0 {
		t.Fatal("no refusal vectors")
	}
	for _, row := range ParallelIdentityTable().Native {
		switch row.At.Identifier {
		case 0x37f, 0x3f7, 0x510, 0x520, 0x590, 0x680, 0x6c0, 0x6d0:
			t.Errorf("%s: a family without decoder fields has a row", row.ID)
		}
		if row.At.Extended || row.At.DLC != 8 || row.At.RawDLC != 8 {
			t.Errorf("%s: declared geometry %+v", row.ID, row.At)
		}
	}
}

type parallelFaultFamily struct {
	identifier string
	family     uint32
	offset     int
	project    [2]string
}

// TestParallelIdentityFaultBitRowsMatchDecoder checks all 64 current_fault bit
// rows and 8 fault byte rows of each of 0x500, 0x660 and 0x670 against the
// decoder and against the bit position computed from the payload. The bit key is
// 8 x (byte offset of the family + byte) + bit; every bit is set in one payload
// and clear in another.
func TestParallelIdentityFaultBitRowsMatchDecoder(t *testing.T) {
	families := []parallelFaultFamily{
		{"0x503", 0x500, 16, [2]string{"QAAB", "0004"}},
		{"0x663", 0x660, 0, [2]string{"QAAB", "0002"}},
		{"0x673", 0x670, 8, [2]string{"QAAB", "0002"}},
	}
	for _, f := range families {
		for _, payload := range []string{"0000000000000000", "ffffffffffffffff", "0180040820408010"} {
			t.Run(fmt.Sprintf("%X-%s", f.family, payload), func(t *testing.T) {
				c := parallelIdentityVectorCase{Identifier: f.identifier, Payload: payload, Selection: sel(f.project)}
				dec, res, reason := parallelEvaluate(t, c)
				if reason != "" {
					t.Fatalf("evaluate refused: %v", reason)
				}
				for at := 0; at < 8; at++ {
					n, err := strconv.ParseUint(payload[2*at:2*at+2], 16, 8)
					if err != nil {
						t.Fatal(err)
					}
					b := byte(n)
					byteKey := fmt.Sprintf("fault_%d", f.offset+at)
					ev := res.Rows[parallelRowID(f.identifier, byteKey)]
					if ev.Native == nil || ev.Native.Value == nil || ev.Native.Raw != uint64(b) {
						t.Fatalf("%s: row %+v, payload byte %d", byteKey, ev.Native, b)
					}
					for bit := 0; bit < 8; bit++ {
						key := fmt.Sprintf("current_fault_%d", 8*(f.offset+at)+bit)
						want := uint64(b >> bit & 1)
						d, ok := dec.Fields[key]
						ev := res.Rows[parallelRowID(f.identifier, key)]
						if !ok || ev.Native == nil {
							t.Fatalf("%s: decoder %v row %v", key, ok, ev.Native != nil)
						}
						if uint64(d.Numerator) != want || ev.Native.Raw != want {
							t.Errorf("%s: decoder %d row %d, payload bit %d", key, d.Numerator, ev.Native.Raw, want)
						}
						if ev.Native.Reason != "" || ev.Native.Disposition != d.Status || (d.Status != "accepted" && d.Status != "raw") {
							t.Errorf("%s: reason %q disposition %q decoder %q", key, ev.Native.Reason, ev.Native.Disposition, d.Status)
						}
					}
				}
				if len(res.Rows) != 72 {
					t.Errorf("%d rows, want 72", len(res.Rows))
				}
			})
		}
	}
}

func sel(project [2]string) diagVectorSelection {
	return diagVectorSelection{Flavor: "pcs-parallel", Bank: "parallel", Project: project[0], Release: project[1], Scope: "local", Endpoint: "coordinator", Group: "3"}
}

// TestParallelIdentityEveryRowHasAVector evaluates the vectors and the fault
// bit payloads and checks that each row of the table applied at least once.
func TestParallelIdentityEveryRowHasAVector(t *testing.T) {
	seen := map[string]bool{}
	mark := func(c parallelIdentityVectorCase) {
		_, res, _ := parallelEvaluate(t, c)
		for id, ev := range res.Rows {
			if ev.Native != nil {
				seen[id] = true
			}
		}
	}
	for _, c := range parallelLoadVectors(t) {
		mark(c)
	}
	for _, id := range []string{"0x503", "0x663", "0x673"} {
		mark(parallelIdentityVectorCase{Identifier: id, Payload: "0000000000000000", Selection: sel([2]string{"QAAB", "0004"})})
	}
	for _, row := range ParallelIdentityTable().Native {
		if !seen[row.ID] {
			t.Errorf("%s: no vector evaluates the row", row.ID)
		}
	}
}
