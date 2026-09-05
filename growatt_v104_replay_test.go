package canbusreg

import (
	"sync"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGrowattV104QualificationReplayUsesProviderProfile(t *testing.T) {
	identity := growattInterface(t, "can7", 7)
	replay, ok := NewGrowattLowVoltageBMSV104QualificationReplay(identity)
	if !ok {
		t.Fatal("selected replay was rejected")
	}

	for _, evidence := range []Evidence{
		growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()),
		growattEvidence(t, identity, 2, 0x312, growattStatusPayload()),
		growattEvidence(t, identity, 3, 0x313, growattMeasurementPayload()),
	} {
		got, accepted := replay.Apply(evidence)
		if evidence.Sequence < 3 {
			if accepted {
				t.Fatalf("partial sequence %d = %#v", evidence.Sequence, got)
			}
			continue
		}
		if !accepted {
			t.Fatalf("complete replay was rejected: %#v", got)
		}
		if got.Interface != identity || got.LimitsEvidence.Sequence != 1 || got.StatusEvidence.Sequence != 2 || got.MeasurementsEvidence.Sequence != 3 {
			t.Fatalf("source evidence = %#v", got)
		}
		if got.Limits.ChargeVoltageDecivolts != 568 || got.Limits.ChargeCurrentDeciamps != 250 || got.Limits.DischargeCurrentDeciamps != 500 {
			t.Fatalf("limits = %#v", got.Limits)
		}
		if got.Measurements.VoltageCentivolts != 5120 || got.Measurements.CurrentDeciamps != -10 || got.Measurements.MaximumCellTemperatureDeciC != 300 || got.Measurements.SOCPercent != 80 || got.Measurements.SOHValue != 127 || !got.Measurements.SOHValid {
			t.Fatalf("measurements = %#v", got.Measurements)
		}
		if got.Status.Protection != [2]byte{0, 0x80} || got.Status.Warning != [2]byte{} || got.Status.PackCount != 2 || got.Status.TotalCellCount != 16 {
			t.Fatalf("status = %#v", got.Status)
		}
	}
}

func TestGrowattV104QualificationReplayFailsClosed(t *testing.T) {
	identity := growattInterface(t, "can7", 7)
	other := growattInterface(t, "can8", 8)

	for _, testCase := range []struct {
		name     string
		evidence []Evidence
	}{
		{
			name: "incomplete tuple",
			evidence: []Evidence{
				growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()),
				growattEvidence(t, identity, 2, 0x312, growattStatusPayload()),
			},
		},
		{
			name: "malformed listed frame resets sequence",
			evidence: []Evidence{
				growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()),
				growattEvidence(t, identity, 2, 0x312, []byte{0, 0, 0, 0, 2, 0xaa, 0xbb}),
				growattEvidence(t, identity, 3, 0x312, growattStatusPayload()),
				growattEvidence(t, identity, 4, 0x313, growattMeasurementPayload()),
			},
		},
		{
			name: "wrong interface cannot complete selected replay",
			evidence: []Evidence{
				growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()),
				growattEvidence(t, other, 2, 0x312, growattStatusPayload()),
				growattEvidence(t, identity, 3, 0x313, growattMeasurementPayload()),
			},
		},
		{
			name: "extended listed identifier resets sequence",
			evidence: []Evidence{
				growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()),
				growattExtendedEvidence(t, identity, 2, 0x311, growattLimitsPayload()),
				growattEvidence(t, identity, 3, 0x312, growattStatusPayload()),
				growattEvidence(t, identity, 4, 0x313, growattMeasurementPayload()),
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			replay, ok := NewGrowattLowVoltageBMSV104QualificationReplay(identity)
			if !ok {
				t.Fatal("selected replay was rejected")
			}
			for _, evidence := range testCase.evidence {
				if got, accepted := replay.Apply(evidence); accepted {
					t.Fatalf("replay unexpectedly admitted: %#v", got)
				}
			}
		})
	}
}

func TestGrowattV104QualificationReplayExpiresAndUsesNewestTuple(t *testing.T) {
	identity := growattInterface(t, "can7", 7)
	replay, ok := NewGrowattLowVoltageBMSV104QualificationReplay(identity)
	if !ok {
		t.Fatal("selected replay was rejected")
	}

	if _, accepted := replay.Apply(growattEvidence(t, identity, 1, 0x311, growattLimitsPayload())); accepted {
		t.Fatal("limits-only replay admitted")
	}
	for sequence := uint64(2); sequence <= 17; sequence++ {
		if _, accepted := replay.Apply(growattEvidence(t, identity, sequence, 0x100, make([]byte, 8))); accepted {
			t.Fatalf("expired replay admitted at %d", sequence)
		}
	}
	if _, accepted := replay.Apply(growattEvidence(t, identity, 18, 0x312, growattStatusPayload())); accepted {
		t.Fatal("expired partial replay admitted")
	}
	if _, accepted := replay.Apply(growattEvidence(t, identity, 19, 0x313, growattMeasurementPayload())); accepted {
		t.Fatal("expired tuple replay admitted")
	}

	old, accepted := replay.Apply(growattEvidence(t, identity, 20, 0x311, []byte{0x01, 0x00, 0, 1, 0, 2, 0, 0}))
	if !accepted || old.Limits.ChargeVoltageDecivolts != 256 || old.LimitsEvidence.Sequence != 20 {
		t.Fatalf("replacement tuple = %#v, accepted = %t", old, accepted)
	}
	got, accepted := replay.Apply(growattEvidence(t, identity, 21, 0x311, growattLimitsPayload()))
	if !accepted || got.Limits.ChargeVoltageDecivolts != 568 || got.LimitsEvidence.Sequence != 21 {
		t.Fatalf("newest tuple = %#v, accepted = %t", got, accepted)
	}
}

func TestGrowattV104QualificationReplayConcurrentObservations(t *testing.T) {
	identity := growattInterface(t, "can7", 7)
	replay, ok := NewGrowattLowVoltageBMSV104QualificationReplay(identity)
	if !ok {
		t.Fatal("selected replay was rejected")
	}

	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for offset := 0; offset < 32; offset++ {
				_, _ = replay.Apply(growattEvidence(t, identity, uint64(worker*32+offset+1), 0x100, make([]byte, canbus.MaxClassicDataLength)))
			}
		}(worker)
	}
	workers.Wait()
}
