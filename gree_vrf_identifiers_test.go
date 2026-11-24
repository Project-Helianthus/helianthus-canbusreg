package canbusreg

import (
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGreeVRFIdentifierFamilies(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, row := range []struct {
		id   uint32
		want GreeVRFIdentifier
		ok   bool
	}{
		{0x1effd020, GreeVRFIdentifier{GreeVRFData, 0xf7, 0x7f, 0x20, 0x20}, true},
		{0x1effc420, GreeVRFIdentifier{GreeVRFData, 0xf7, 0x7f, 0x08, 0x20}, true},
		{0x1effd021, GreeVRFIdentifier{GreeVRFData, 0xf7, 0x7f, 0x20, 0x21}, true},
		{0x1cffd020, GreeVRFIdentifier{GreeVRFAddressedData, 0xe7, 0x7f, 0x20, 0x20}, true},
		{0x1cbfd020, GreeVRFIdentifier{GreeVRFAddressedWrite, 0xe5, 0x7f, 0x20, 0x20}, true},
		{0x04820084, GreeVRFIdentifier{GreeVRFClock, 0x24, 8, 1, 4}, true},
		{0x04820000, GreeVRFIdentifier{GreeVRFClock, 0x24, 8, 0, 0}, true},
		{0x04820085, GreeVRFIdentifier{}, false},
		{0x1edfd020, GreeVRFIdentifier{}, false},
		{0x1eff9020, GreeVRFIdentifier{}, false},
	} {
		frame := greeCandidateEvidence(t, iface, row.id, nil).Frame
		if got, ok := ClassifyGreeVRFIdentifier(frame.ID()); got != row.want || ok != row.ok {
			t.Errorf("%08x: got %+v, %v; want %+v, %v", row.id, got, ok, row.want, row.ok)
		}
	}
	standard, err := canbus.NewStandardID(0x420)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ClassifyGreeVRFIdentifier(standard); ok {
		t.Fatal("accepted standard identifier")
	}
}
