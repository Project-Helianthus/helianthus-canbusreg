package canbusreg

import (
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func scopeTestSelection(t *testing.T, source canbus.InterfaceIdentity, scope string) Selection {
	t.Helper()
	s, err := NewSelection(SelectionSpec{Interface: source, Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs", Scope: scope, Bank: "ordinary", Endpoint: "coordinator", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A projector selected only by interface asserts no publisher role, so every
// projection, including a raw-only one, stays at the unresolved scope.
func TestCommonProjectionScopeDefaultsToUnknown(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}
	for n, id := range []uint32{0x311, 0x312, 0x313, 0x320} {
		got, accepted := projector.Apply(growattCommonEvidence(t, source, uint64(n+1), id, []byte{0, 1, 0, 2, 3, 0, 50, 90}))
		if !accepted || got.Scope != "unknown" {
			t.Fatalf("%#x scope = %q, accepted = %t", id, got.Scope, accepted)
		}
	}
}

// The scope key is (interface, asserted selection scope). A hub-aggregated
// pack count is marked aggregate and never reads as a single-BMS count:
//   - Scope: mapping aggregate to member, local to aggregate, or hardcoding
//     either value fails one of the two rows.
//   - Interface: ignoring the selection interface admits the other source.
func TestCommonProjectionScopeMarksAnAggregatedPackCount(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	for _, v := range []struct{ asserted, want string }{
		{"aggregate", "aggregate"},
		{"local", "member"},
	} {
		projector, ok := NewGrowattLowVoltageBMSCommonProjectorFromSelection(scopeTestSelection(t, source, v.asserted))
		if !ok {
			t.Fatalf("%s selection was rejected", v.asserted)
		}
		evidence := growattCommonEvidence(t, source, 1, 0x312, []byte{0, 0, 0, 0, 4, 0, 0, 16})
		got, accepted := projector.Apply(evidence)
		if !accepted || got.Status == nil || got.Status.PackCount != 4 || got.Scope != v.want || got.RawEvidence != evidence {
			t.Fatalf("%s pack count projection = %#v, accepted = %t", v.asserted, got, accepted)
		}
		other := growattInterface(t, "can8", 8)
		if got, accepted := projector.Apply(growattCommonEvidence(t, other, 2, 0x312, []byte{0, 0, 0, 0, 4, 0, 0, 16})); accepted || got.Status != nil || got.Scope != "" {
			t.Fatalf("%s projector admitted another source: %#v", v.asserted, got)
		}
	}
	if projector, ok := NewGrowattLowVoltageBMSCommonProjectorFromSelection(Selection{}); ok || projector != nil {
		t.Fatalf("unvalidated selection projector = %#v, ok = %t", projector, ok)
	}
}
