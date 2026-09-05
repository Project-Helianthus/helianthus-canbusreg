package canbusreg

import (
	"encoding/binary"
	"reflect"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGreeVRFQualificationReplayRunsSelectedCandidateAndMapPaths(t *testing.T) {
	identity := greeReplayInterface(t, "can7", 7)
	initial := GreeVRFMapState{Cells: map[uint8]uint8{0x80: 0xdd}}
	evidence := greeReplayEvidence(t, identity, 0x1ee00410, []byte{0x02, 0x31, 0x32, 0x33, 0xa1, 0xa2}, 42)

	for _, testCase := range []struct {
		name       string
		mapProfile GreeVRFMapProfile
		productID  uint16
		wantCell   uint8
	}{
		{name: "M94", mapProfile: GreeVRFMapM94, wantCell: 0x03},
		{name: "M115", mapProfile: GreeVRFMapM115, productID: 0x6098, wantCell: 0x03},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			replay, ok := NewGreeVRFQualificationReplay(identity, testCase.mapProfile, testCase.productID)
			if !ok {
				t.Fatal("selected replay was rejected")
			}
			got, ok := replay.Apply(initial, evidence)
			if !ok {
				t.Fatal("positive replay was rejected")
			}
			if got.MapProfile != testCase.mapProfile || got.ProductID != testCase.productID {
				t.Fatalf("selection = %#v", got)
			}
			if got.Candidate.Evidence != evidence || got.Candidate.Evidence.RawRecord != evidence.RawRecord {
				t.Fatalf("raw provenance was not retained: %#v", got.Candidate.Evidence)
			}
			if got.CandidateProfile != GreeVRFCandidateProfile || got.Candidate.Unit7 != 8 || got.Candidate.Opcode7 != 0x10 {
				t.Fatalf("candidate = %#v", got.Candidate)
			}
			if !reflect.DeepEqual(got.Candidate.Updates, []GreeVRFCandidateStateCell{{Cell: 0x0f, Value: 0xa1}, {Cell: 0x10, Value: 0xa2}}) {
				t.Fatalf("candidate updates = %#v", got.Candidate.Updates)
			}
			if got.State.Cells[testCase.wantCell] != 0x31 || got.State.Cells[0x80] != 0xdd {
				t.Fatalf("state = %#v", got.State)
			}
			if _, exists := got.State.Cells[0x7f]; exists {
				t.Fatalf("unknown cell was fabricated: %#v", got.State.Cells)
			}
			if len(got.MatchedRows) == 0 {
				t.Fatal("selected map did not report its matched rows")
			}
		})
	}
}

func TestGreeVRFQualificationReplayFailsClosedAndRetainsPartialState(t *testing.T) {
	identity := greeReplayInterface(t, "can7", 7)
	replay, ok := NewGreeVRFQualificationReplay(identity, GreeVRFMapM94, 0)
	if !ok {
		t.Fatal("selected replay was rejected")
	}
	initial := GreeVRFMapState{Cells: map[uint8]uint8{0x03: 0x44, 0x7f: 0x55}}
	wrongInterface := greeReplayInterface(t, "can8", 8)

	for _, testCase := range []struct {
		name     string
		evidence Evidence
	}{
		{name: "wrong identifier", evidence: greeReplayEvidence(t, identity, 0x1ec00410, []byte{0x02, 0x31, 0x32, 0x33, 0xa1, 0xa2}, 1)},
		{name: "wrong unit", evidence: greeReplayEvidence(t, identity, 0x1ee00010, []byte{0x02, 0x31, 0x32, 0x33, 0xa1, 0xa2}, 2)},
		{name: "wrong interface", evidence: greeReplayEvidence(t, wrongInterface, 0x1ee00410, []byte{0x02, 0x31, 0x32, 0x33, 0xa1, 0xa2}, 3)},
		{name: "wrong DLC", evidence: greeReplayEvidence(t, identity, 0x1ee00410, nil, 4)},
		{name: "partial candidate span", evidence: greeReplayEvidence(t, identity, 0x1ee00410, []byte{0x05, 0xa1}, 5)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, accepted := replay.Apply(initial, testCase.evidence)
			if accepted || !reflect.DeepEqual(got.State, initial) {
				t.Fatalf("replay = %#v, accepted = %t", got, accepted)
			}
		})
	}

	if _, ok := NewGreeVRFQualificationReplay(identity, GreeVRFMapProfile(99), 0); ok {
		t.Fatal("unknown map profile was admitted")
	}
}

func greeReplayInterface(t *testing.T, name string, index int) canbus.InterfaceIdentity {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func greeReplayEvidence(t *testing.T, identity canbus.InterfaceIdentity, identifier uint32, payload []byte, sequence uint64) Evidence {
	t.Helper()
	id, err := canbus.NewExtendedID(identifier)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	var raw [canbus.SocketCANRecordSize]byte
	binary.LittleEndian.PutUint32(raw[:4], identifier|0x80000000)
	raw[4] = byte(len(payload))
	copy(raw[8:], payload)
	return Evidence{Frame: frame, Interface: identity, Sequence: sequence, Monotonic: time.Duration(sequence) * time.Millisecond, RawRecord: raw}
}
