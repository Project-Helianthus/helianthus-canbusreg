package canbusreg

import (
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGrowattCommonProjectionExposesOnlyDocumentedSharedFields(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}

	limitsEvidence := growattCommonEvidence(t, source, 1, 0x311, []byte{0x02, 0x38, 0x00, 0xfa, 0x01, 0xf4, 0xab, 0xcd})
	limits, accepted := projector.Apply(limitsEvidence)
	if !accepted || limits.Limits == nil || limits.Status != nil || limits.Measurements != nil {
		t.Fatalf("limits projection = %#v, accepted = %t", limits, accepted)
	}
	if limits.Limits.ChargeVoltageDecivolts != 568 || limits.Limits.ChargeCurrentDeciamps != 250 || limits.Limits.DischargeCurrentDeciamps != 500 {
		t.Fatalf("limits = %#v", limits.Limits)
	}
	if limits.RawEvidence != limitsEvidence {
		t.Fatalf("limits raw evidence = %#v", limits.RawEvidence)
	}

	statusEvidence := growattCommonEvidence(t, source, 2, 0x312, []byte{0x12, 0x34, 0x56, 0x78, 2, 0xaa, 0xbb, 16})
	status, accepted := projector.Apply(statusEvidence)
	if !accepted || status.Limits != nil || status.Status == nil || status.Measurements != nil {
		t.Fatalf("status projection = %#v, accepted = %t", status, accepted)
	}
	if status.Status.Protection != [2]byte{0x12, 0x34} || status.Status.Warning != [2]byte{0x56, 0x78} || status.Status.PackCount != 2 {
		t.Fatalf("status = %#v", status.Status)
	}
	if status.RawEvidence != statusEvidence {
		t.Fatalf("status raw evidence = %#v", status.RawEvidence)
	}

	measurementEvidence := growattCommonEvidence(t, source, 3, 0x313, []byte{0x14, 0x00, 0xff, 0xf6, 0x7f, 0xff, 80, 0xff})
	measurements, accepted := projector.Apply(measurementEvidence)
	if !accepted || measurements.Limits != nil || measurements.Status != nil || measurements.Measurements == nil {
		t.Fatalf("measurement projection = %#v, accepted = %t", measurements, accepted)
	}
	if measurements.Measurements.VoltageCentivolts != 5120 || measurements.Measurements.CurrentDeciamps != -10 || measurements.Measurements.SOCPercent != 80 || measurements.Measurements.SOHValue != 127 {
		t.Fatalf("measurements = %#v", measurements.Measurements)
	}
}

func TestGrowattCommonProjectionWithholdsRevisionLocalSOHHighBit(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}

	for _, rawSOH := range []byte{0x7f, 0xff} {
		evidence := growattCommonEvidence(t, source, uint64(rawSOH), 0x313, []byte{0x14, 0x00, 0xff, 0xf6, 0x7f, 0xff, 80, rawSOH})
		got, accepted := projector.Apply(evidence)
		if !accepted || got.Measurements == nil || got.Measurements.SOHValue != 0x7f {
			t.Fatalf("raw SOH %#x projection = %#v, accepted = %t", rawSOH, got, accepted)
		}
		if got.RawEvidence != evidence {
			t.Fatalf("raw SOH %#x evidence = %#v", rawSOH, got.RawEvidence)
		}
	}
}

func TestGrowattCommonProjectionNeverUsesFirmwareAsRevision(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}

	for _, payload := range [][]byte{
		{0, 0, 0, 0x04, 0, 0, 0, 0},
		{0, 0, 0, 0x08, 0x01, 0, 0, 0},
	} {
		got, accepted := projector.Apply(growattCommonEvidence(t, source, 1, 0x320, payload))
		if !accepted || got.Limits != nil || got.Status != nil || got.Measurements != nil {
			t.Fatalf("firmware projection = %#v, accepted = %t", got, accepted)
		}
		if got.RawEvidence.Frame.ID().Value() != 0x320 || string(got.RawEvidence.Frame.Data()) != string(payload) {
			t.Fatalf("firmware raw evidence = %#v", got.RawEvidence)
		}
	}
}

func TestGrowattCommonProjectionKeepsRawAndIsolatesSource(t *testing.T) {
	source := growattInterface(t, "can7", 7)
	other := growattInterface(t, "can8", 8)
	projector, ok := NewGrowattLowVoltageBMSCommonProjector(source)
	if !ok {
		t.Fatal("selected source was rejected")
	}

	if _, accepted := projector.Apply(growattCommonEvidence(t, source, 1, 0x311, growattLimitsPayload())); !accepted {
		t.Fatal("source limits were rejected")
	}
	if got, accepted := projector.Apply(growattCommonEvidence(t, other, 1, 0x312, growattStatusPayload())); accepted || got != (GrowattLowVoltageBMSCommonProjection{}) {
		t.Fatalf("other source = %#v, accepted = %t", got, accepted)
	}
	if got, accepted := projector.Apply(growattCommonEvidence(t, source, 2, 0x313, growattMeasurementPayload())); !accepted || got.Limits != nil || got.Status != nil || got.Measurements == nil {
		t.Fatalf("cross-source non-completion = %#v, accepted = %t", got, accepted)
	}

	unknown := growattCommonEvidence(t, source, 2, 0x399, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	if got, accepted := projector.Apply(unknown); !accepted || got.RawEvidence != unknown || got.Limits != nil || got.Status != nil || got.Measurements != nil {
		t.Fatalf("unknown raw result = %#v, accepted = %t", got, accepted)
	}

	malformed := growattCommonEvidence(t, source, 3, 0x311, []byte{1, 2, 3})
	if got, accepted := projector.Apply(malformed); !accepted || got.RawEvidence != malformed || got.Limits != nil || got.Status != nil || got.Measurements != nil {
		t.Fatalf("malformed raw result = %#v, accepted = %t", got, accepted)
	}

	extended := growattExtendedEvidence(t, source, 4, 0x311, growattLimitsPayload())
	if got, accepted := projector.Apply(extended); !accepted || got.RawEvidence != extended || got.Limits != nil || got.Status != nil || got.Measurements != nil {
		t.Fatalf("extended raw result = %#v, accepted = %t", got, accepted)
	}
}

func TestGrowattCommonProjectionRequiresExplicitValidSource(t *testing.T) {
	if projector, ok := NewGrowattLowVoltageBMSCommonProjector(canbus.InterfaceIdentity{}); ok || projector != nil {
		t.Fatalf("invalid source projector = %#v, ok = %t", projector, ok)
	}
}

func growattCommonEvidence(t *testing.T, identity canbus.InterfaceIdentity, sequence uint64, identifier uint32, data []byte) Evidence {
	t.Helper()
	id, err := canbus.NewStandardID(identifier)
	if err != nil {
		t.Fatalf("NewStandardID(%#x): %v", identifier, err)
	}
	frame, err := canbus.NewFrame(id, data)
	if err != nil {
		t.Fatalf("NewFrame(%#x): %v", identifier, err)
	}
	evidence := Evidence{Frame: frame, Interface: identity, Sequence: sequence}
	evidence.RawRecord[0] = byte(sequence)
	return evidence
}
