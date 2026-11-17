package canbusreg

import (
	"fmt"
	"testing"
	"time"
)

// The core extrema records 319 and 330 share the field names maximum_index,
// minimum_index, maximum_cell and minimum_cell; 330 pairs each index with a
// location byte, so the two records carry different evidence. Both records are
// accepted in one generation and each keeps its own retained values: 319 under
// the core key and 330 under core/330.
//
// Key fields varied and the production mutation each catches:
//   - prefix: core/voltage (0x313) stays under core, catching a key that scopes
//     every core record by frame identifier.
//   - record kind: 319 and 330 extrema of one selection, sent in both orders,
//     catching a single core key (the later record overwrites the earlier one)
//     and a key that scopes 319 away from the core names consumers read.
//   - field name: each of the four shared extrema names is asserted under both
//     keys, and the location byte only under core/330.
var retentionKeyRecords = map[uint32]struct {
	payload [8]byte
	want    map[string]int64
}{
	0x319: {[8]byte{0, 0x0d, 0x10, 0x0c, 0x80, 3, 5, 0}, map[string]int64{"maximum_index": 3, "minimum_index": 5, "maximum_cell": 0x0d10, "minimum_cell": 0x0c80}},
	0x330: {[8]byte{1, 9, 1, 11, 0x0d, 0x20, 0x0c, 0x90}, map[string]int64{"maximum_index": 9, "minimum_index": 11, "maximum_cell": 0x0d20, "minimum_cell": 0x0c90}},
}

func retentionKeyFor(record uint32) string {
	if record == 0x330 {
		return "core/330"
	}
	return "core"
}

func retentionKeyInputs(t *testing.T, first, second uint32) (Selection, ReplayInput, ReplayInput, ReplayInput) {
	t.Helper()
	s, core := replayTestFixture(t)
	core.PCS.IndexValid, core.PCS.IndexScope = true, "local"
	core.PCS.IndexMinimum, core.PCS.IndexMaximum = 1, 16
	a, b := core, core
	a.PCS.Evidence.Sequence, a.PCS.Evidence.Monotonic = 2, 2*time.Second
	replayTestFrame(t, &a, first, false, retentionKeyRecords[first].payload)
	b.PCS.Evidence.Sequence, b.PCS.Evidence.Monotonic = 3, 3*time.Second
	replayTestFrame(t, &b, second, false, retentionKeyRecords[second].payload)
	return s, core, a, b
}

func retentionKeyCheck(t *testing.T, lastGood map[string]NativeField, record uint32, evidence Evidence) {
	t.Helper()
	key := retentionKeyFor(record)
	for name, want := range retentionKeyRecords[record].want {
		if f := lastGood[key+"/"+name]; f.Status != "accepted" || f.Numerator != want || f.Evidence != evidence {
			t.Fatalf("%s/%s: %#v", key, name, f)
		}
	}
}

func TestCoreExtremaRetentionKeyByRecordKind(t *testing.T) {
	for _, order := range [][2]uint32{{0x319, 0x330}, {0x330, 0x319}} {
		t.Run(fmt.Sprintf("%03x-then-%03x", order[0], order[1]), func(t *testing.T) {
			s, core, a, b := retentionKeyInputs(t, order[0], order[1])
			p := replayTestProfile(t, s, core, a, b)
			replayTestClassify(t, p, core.PCS.Evidence)
			replayTestClassify(t, p, a.PCS.Evidence)
			r := replayTestClassify(t, p, b.PCS.Evidence)
			if r.Current.Status != "descriptive" {
				t.Fatal(r.Current)
			}
			retentionKeyCheck(t, r.LastGood, order[0], a.PCS.Evidence)
			retentionKeyCheck(t, r.LastGood, order[1], b.PCS.Evidence)
			if _, ok := r.RetainedNative["core/330"].Fields["maximum_location"]; !ok {
				t.Fatal("location byte not retained with the 330 evidence")
			}
			for _, name := range []string{"maximum_location", "minimum_location"} {
				if _, ok := r.RetainedNative["core"].Fields[name]; ok {
					t.Fatalf("location byte %s retained under the 319 key", name)
				}
			}
			if _, ok := r.RetainedNative["core/319"]; ok {
				t.Fatal("319 moved away from the core key")
			}
			if f := r.LastGood["core/voltage"]; f.Status != "accepted" || f.Evidence != core.PCS.Evidence {
				t.Fatalf("core/voltage: %#v", f)
			}
		})
	}
}

// The provider names its current and last-known-good fields with the same
// retention key, so one generation keeps both extrema records apart as well.
func TestCoreExtremaRetentionKeyProvider(t *testing.T) {
	for _, order := range [][2]uint32{{0x319, 0x330}, {0x330, 0x319}} {
		t.Run(fmt.Sprintf("%03x-then-%03x", order[0], order[1]), func(t *testing.T) {
			s, core, a, b := retentionKeyInputs(t, order[0], order[1])
			p, err := NewPCSProvider(s)
			if err != nil {
				t.Fatal(err)
			}
			source := providerTestSource()
			if err := p.Start(source, 1); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			for _, in := range []ReplayInput{core, a, b} {
				providerTestSend(t, source, p, 1, in)
			}
			got := p.ProjectProviderSnapshot(b.PCS.Evidence.Monotonic, time.Hour)
			if got.State == "conflict" {
				t.Fatal(got.State)
			}
			retentionKeyCheck(t, got.LastGood, order[0], a.PCS.Evidence)
			retentionKeyCheck(t, got.LastGood, order[1], b.PCS.Evidence)
			for name, want := range retentionKeyRecords[order[1]].want {
				if f := got.Current[retentionKeyFor(order[1])+"/"+name]; f.Numerator != want {
					t.Fatalf("current %s: %#v", name, f)
				}
			}
		})
	}
}
