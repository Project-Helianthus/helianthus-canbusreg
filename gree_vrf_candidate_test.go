package canbusreg

import (
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGreeVRFCandidateProjectsOnlyBoundedOpaqueStateCells(t *testing.T) {
	identity := greeCandidateInterface(t)
	for _, testCase := range []struct {
		name    string
		id      uint32
		payload []byte
		opaque7 uint8
		cells   []GreeVRFCandidateStateCell
	}{
		{name: "opcode 10 pair", id: 0x1ee00410, payload: []byte{0x05, 0xa1, 0xa2}, cells: []GreeVRFCandidateStateCell{{Cell: 0x0f, Value: 0xa1}, {Cell: 0x10, Value: 0xa2}}},
		{name: "opcode 10 singleton", id: 0x1ee00410, payload: []byte{0x20, 0xa3}, cells: []GreeVRFCandidateStateCell{{Cell: 0x11, Value: 0xa3}}},
		{name: "opcode 52 packed bit", id: 0x1ee00452, payload: []byte{0x59, 0x01}, cells: []GreeVRFCandidateStateCell{{Cell: 0x12, Value: 1}}},
		{name: "opcode 58 first pair", id: 0x1ee00458, payload: []byte{0x5d, 0xa4, 0xa5}, cells: []GreeVRFCandidateStateCell{{Cell: 0x13, Value: 0xa4}, {Cell: 0x14, Value: 0xa5}}},
		{name: "opcode 58 second pair", id: 0x1ee00458, payload: []byte{0x5f, 0xa6, 0xa7}, cells: []GreeVRFCandidateStateCell{{Cell: 0x15, Value: 0xa6}, {Cell: 0x16, Value: 0xa7}}},
		{name: "opcode 58 third pair", id: 0x1ee00458, payload: []byte{0x59, 0xa8, 0xa9}, cells: []GreeVRFCandidateStateCell{{Cell: 0x17, Value: 0xa8}, {Cell: 0x18, Value: 0xa9}}},
		{name: "opcode 58 fourth pair", id: 0x1ee00458, payload: []byte{0x5b, 0xaa, 0xab}, cells: []GreeVRFCandidateStateCell{{Cell: 0x19, Value: 0xaa}, {Cell: 0x1a, Value: 0xab}}},
		{name: "opcode 11 singleton", id: 0x1ee00411, payload: []byte{0x09, 0xac}, cells: []GreeVRFCandidateStateCell{{Cell: 0x1b, Value: 0xac}}},
		{name: "opaque source retained", id: 0x1ee04410, payload: []byte{0x20, 0xad}, opaque7: 1, cells: []GreeVRFCandidateStateCell{{Cell: 0x11, Value: 0xad}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			profile := GreeVRFCandidate()
			got := profile.Classify(greeCandidateEvidence(t, identity, testCase.id, testCase.payload))
			if got.Profile != GreeVRFCandidateProfile {
				t.Fatalf("profile = %#v", got)
			}
			projection, ok := got.Projection.(GreeVRFCandidateProjection)
			if !ok {
				t.Fatalf("projection type = %T", got.Projection)
			}
			if projection.Evidence.Frame.ID().Value() != testCase.id || projection.Class8 != 0xf7 || projection.Opaque7 != testCase.opaque7 || projection.Unit7 != 8 {
				t.Fatalf("identifier projection = %#v", projection)
			}
			if len(projection.Updates) != len(testCase.cells) {
				t.Fatalf("updates = %#v", projection.Updates)
			}
			for index, want := range testCase.cells {
				if projection.Updates[index] != want {
					t.Fatalf("update[%d] = %#v, want %#v", index, projection.Updates[index], want)
				}
			}
		})
	}
}

func TestGreeVRFCandidateFailsClosedForNonCandidateGeometry(t *testing.T) {
	identity := greeCandidateInterface(t)
	standard, err := canbus.NewStandardID(0x610)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := canbus.NewFrame(mustGreeCandidateID(t, 0x1ee00410), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name     string
		evidence Evidence
	}{
		{name: "standard identifier", evidence: greeCandidateFrameEvidence(t, identity, standard, []byte{0x05, 0xa1, 0xa2})},
		{name: "wrong unit", evidence: greeCandidateEvidence(t, identity, 0x1ee00010, []byte{0x05, 0xa1, 0xa2})},
		{name: "wrong opcode", evidence: greeCandidateEvidence(t, identity, 0x1ee00412, []byte{0x05, 0xa1, 0xa2})},
		{name: "zero dlc", evidence: Evidence{Frame: empty, Interface: identity, Sequence: 1, Monotonic: time.Second}},
		{name: "uncovered coordinate", evidence: greeCandidateEvidence(t, identity, 0x1ee00410, []byte{0x06, 0xa1})},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := GreeVRFCandidate().Classify(testCase.evidence); got != (Classification{}) {
				t.Fatalf("classification = %#v", got)
			}
		})
	}
}

func greeCandidateInterface(t *testing.T) canbus.InterfaceIdentity {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func greeCandidateEvidence(t *testing.T, identity canbus.InterfaceIdentity, identifier uint32, payload []byte) Evidence {
	t.Helper()
	return greeCandidateFrameEvidence(t, identity, mustGreeCandidateID(t, identifier), payload)
}

func greeCandidateFrameEvidence(t *testing.T, identity canbus.InterfaceIdentity, identifier canbus.Identifier, payload []byte) Evidence {
	t.Helper()
	frame, err := canbus.NewFrame(identifier, payload)
	if err != nil {
		t.Fatal(err)
	}
	return Evidence{Frame: frame, Interface: identity, Sequence: 1, Monotonic: time.Second}
}

func mustGreeCandidateID(t *testing.T, value uint32) canbus.Identifier {
	t.Helper()
	identifier, err := canbus.NewExtendedID(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}
