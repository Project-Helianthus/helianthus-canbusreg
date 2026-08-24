package canbusreg

import "testing"

func TestRegistryFailsClosedAndBoundsEvidence(t *testing.T) {
	registry := NewRegistry(GreeVRFCandidate())

	result := registry.Classify(Observation{Extended: false, ID: 0x123, Data: []byte{1, 2, 3}})
	if result.Profile != "" || result.Projection != nil {
		t.Fatalf("unknown observation = %#v, want opaque without projection", result)
	}

	result = registry.Classify(Observation{Extended: true, ID: 0x10000001, Data: make([]byte, 9)})
	if result.Profile != "" || result.Projection != nil {
		t.Fatalf("oversized observation = %#v, want opaque without projection", result)
	}

	result = registry.Classify(Observation{Extended: true, ID: 0x10000001, Data: []byte{0, 1}})
	if result.Profile != "" || result.Projection != nil {
		t.Fatalf("unqualified Gree candidate = %#v, want opaque without projection", result)
	}
}
