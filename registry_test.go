package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
)

type matchProfile struct{ name string }

func (p matchProfile) Classify(Evidence) Classification { return Classification{p.name, p.name} }

func TestRegistryFailsClosed(t *testing.T) {
	id, _ := canbus.NewExtendedID(0x10000001)
	frame, _ := canbus.NewFrame(id, []byte{0, 1})
	e := Evidence{Frame: frame}
	if got := NewRegistry(GreeVRFCandidate()).Classify(e); got.Profile != "" {
		t.Fatalf("Gree=%#v", got)
	}
	if got := NewRegistry(matchProfile{"a"}, matchProfile{"b"}).Classify(e); got.Profile != "" {
		t.Fatalf("overlap=%#v", got)
	}
	if got := NewRegistry(matchProfile{"a"}).Classify(e); got.Profile != "a" {
		t.Fatalf("single=%#v", got)
	}
}
