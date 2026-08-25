package canbusreg

import (
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGrowattV104RequiresCompleteTupleFromOneInterface(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	first := growattInterface(t, "can0", 1)
	second := growattInterface(t, "can1", 2)

	if got := profile.Classify(growattEvidence(t, first, 1, 0x311, growattLimitsPayload())); got.Profile != "" {
		t.Fatalf("limits only = %#v", got)
	}
	if got := profile.Classify(growattEvidence(t, first, 2, 0x312, growattStatusPayload())); got.Profile != "" {
		t.Fatalf("limits and status = %#v", got)
	}
	if got := profile.Classify(growattEvidence(t, second, 1, 0x313, growattMeasurementPayload())); got.Profile != "" {
		t.Fatalf("other interface completed tuple = %#v", got)
	}

	got := profile.Classify(growattEvidence(t, first, 3, 0x313, growattMeasurementPayload()))
	if got.Profile != growattLowVoltageBMSV104Profile {
		t.Fatalf("complete tuple profile = %#v", got)
	}
	projection, ok := got.Projection.(GrowattLowVoltageBMSV104Projection)
	if !ok {
		t.Fatalf("projection type = %T", got.Projection)
	}
	if projection.Interface != first {
		t.Fatalf("projection interface = %#v, want %#v", projection.Interface, first)
	}
	if projection.LimitsEvidence.Frame.ID().Value() != 0x311 || projection.StatusEvidence.Frame.ID().Value() != 0x312 || projection.MeasurementsEvidence.Frame.ID().Value() != 0x313 {
		t.Fatalf("source frames = %#v", projection)
	}
	if projection.Limits.ChargeVoltageDecivolts != 0x238 || projection.Limits.ChargeCurrentDeciamps != 250 || projection.Limits.DischargeCurrentDeciamps != 500 {
		t.Fatalf("limits = %#v", projection.Limits)
	}
	if projection.Measurements.VoltageCentivolts != 0x1400 || projection.Measurements.CurrentDeciamps != -10 || projection.Measurements.MaximumCellTemperatureDeciC != 300 || projection.Measurements.SOCPercent != 80 || projection.Measurements.SOHValue != 127 || !projection.Measurements.SOHValid {
		t.Fatalf("measurements = %#v", projection.Measurements)
	}
	if projection.Status.PackCount != 2 || projection.Status.TotalCellCount != 16 || projection.Status.Protection != [2]byte{0, 0x80} || projection.Status.Warning != [2]byte{} {
		t.Fatalf("status = %#v", projection.Status)
	}
}

func TestGrowattV104MalformedListedFrameResetsAdmission(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	identity := growattInterface(t, "can0", 1)

	profile.Classify(growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()))
	profile.Classify(growattEvidence(t, identity, 2, 0x312, []byte{0, 0, 0, 0, 2, 0xaa, 0xbb}))
	profile.Classify(growattEvidence(t, identity, 3, 0x312, growattStatusPayload()))
	if got := profile.Classify(growattEvidence(t, identity, 4, 0x313, growattMeasurementPayload())); got.Profile != "" {
		t.Fatalf("post-reset tuple = %#v", got)
	}
	if got := profile.Classify(growattEvidence(t, identity, 5, 0x311, growattLimitsPayload())); got.Profile != growattLowVoltageBMSV104Profile {
		t.Fatalf("restarted tuple = %#v", got)
	}
}

func TestGrowattV104InvalidDeclaredRangeResetsAdmission(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	identity := growattInterface(t, "can0", 1)

	profile.Classify(growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()))
	profile.Classify(growattEvidence(t, identity, 2, 0x312, []byte{0, 0, 0, 0, 0, 0xaa, 0xbb, 16}))
	profile.Classify(growattEvidence(t, identity, 3, 0x312, growattStatusPayload()))
	if got := profile.Classify(growattEvidence(t, identity, 4, 0x313, growattMeasurementPayload())); got.Profile != "" {
		t.Fatalf("post-range-reset tuple = %#v", got)
	}
	if got := profile.Classify(growattEvidence(t, identity, 5, 0x311, growattLimitsPayload())); got.Profile != growattLowVoltageBMSV104Profile {
		t.Fatalf("restarted range tuple = %#v", got)
	}
}

func TestGrowattV104RejectsExtendedAndExpiresWindow(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	identity := growattInterface(t, "can0", 1)

	profile.Classify(growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()))
	profile.Classify(growattExtendedEvidence(t, identity, 2, 0x311, growattLimitsPayload()))
	for sequence := uint64(3); sequence < 18; sequence++ {
		profile.Classify(growattEvidence(t, identity, sequence, 0x100, make([]byte, 8)))
	}
	profile.Classify(growattEvidence(t, identity, 18, 0x312, growattStatusPayload()))
	if got := profile.Classify(growattEvidence(t, identity, 19, 0x313, growattMeasurementPayload())); got.Profile != "" {
		t.Fatalf("expired tuple = %#v", got)
	}
	if got := profile.Classify(growattEvidence(t, identity, 20, 0x311, growattLimitsPayload())); got.Profile != growattLowVoltageBMSV104Profile {
		t.Fatalf("fresh tuple = %#v", got)
	}
}

func TestGrowattV104UsesNewestValueAfterWindowRollover(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	identity := growattInterface(t, "can0", 1)
	for sequence := uint64(1); sequence < 16; sequence++ {
		profile.Classify(growattEvidence(t, identity, sequence, 0x100, make([]byte, 8)))
	}
	profile.Classify(growattEvidence(t, identity, 16, 0x311, []byte{0x01, 0x00, 0, 1, 0, 2, 0, 0}))
	profile.Classify(growattEvidence(t, identity, 17, 0x311, growattLimitsPayload()))
	profile.Classify(growattEvidence(t, identity, 18, 0x312, growattStatusPayload()))
	got := profile.Classify(growattEvidence(t, identity, 19, 0x313, growattMeasurementPayload()))
	projection, ok := got.Projection.(GrowattLowVoltageBMSV104Projection)
	if !ok {
		t.Fatalf("projection type = %T", got.Projection)
	}
	if projection.Limits.ChargeVoltageDecivolts != 0x238 {
		t.Fatalf("newest charge voltage = %#x", projection.Limits.ChargeVoltageDecivolts)
	}
}

func TestGrowattV104BoundsInterfaceState(t *testing.T) {
	profile := GrowattLowVoltageBMSV104().(*growattLowVoltageBMSV104)
	for index := 1; index <= growattV104MaximumInterfaces+1; index++ {
		identity := growattInterface(t, "can"+string(rune('a'+index)), index)
		profile.Classify(growattEvidence(t, identity, 1, 0x311, growattLimitsPayload()))
	}
	if got := len(profile.windows); got != growattV104MaximumInterfaces {
		t.Fatalf("interface states = %d, want %d", got, growattV104MaximumInterfaces)
	}
}

func TestGrowattV104ConcurrentClassification(t *testing.T) {
	profile := GrowattLowVoltageBMSV104()
	identity := growattInterface(t, "can0", 1)

	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for offset := 0; offset < 32; offset++ {
				profile.Classify(growattEvidence(t, identity, uint64(worker*32+offset+1), 0x100, make([]byte, 8)))
			}
		}(worker)
	}
	workers.Wait()
}

func growattInterface(t *testing.T, name string, index int) canbus.InterfaceIdentity {
	t.Helper()
	identity, err := canbus.NewInterfaceIdentity(name, index)
	if err != nil {
		t.Fatalf("NewInterfaceIdentity(%q, %d): %v", name, index, err)
	}
	return identity
}

func growattEvidence(t *testing.T, identity canbus.InterfaceIdentity, sequence uint64, identifier uint32, data []byte) Evidence {
	t.Helper()
	id, err := canbus.NewStandardID(identifier)
	if err != nil {
		t.Fatalf("NewStandardID(%#x): %v", identifier, err)
	}
	frame, err := canbus.NewFrame(id, data)
	if err != nil {
		t.Fatalf("NewFrame(%#x): %v", identifier, err)
	}
	return Evidence{Frame: frame, Interface: identity, Sequence: sequence, Monotonic: time.Duration(sequence) * time.Second}
}

func growattExtendedEvidence(t *testing.T, identity canbus.InterfaceIdentity, sequence uint64, identifier uint32, data []byte) Evidence {
	t.Helper()
	id, err := canbus.NewExtendedID(identifier)
	if err != nil {
		t.Fatalf("NewExtendedID(%#x): %v", identifier, err)
	}
	frame, err := canbus.NewFrame(id, data)
	if err != nil {
		t.Fatalf("NewFrame(%#x): %v", identifier, err)
	}
	return Evidence{Frame: frame, Interface: identity, Sequence: sequence, Monotonic: time.Duration(sequence) * time.Second}
}

func growattLimitsPayload() []byte      { return []byte{0x02, 0x38, 0x00, 0xfa, 0x01, 0xf4, 0x00, 0x43} }
func growattStatusPayload() []byte      { return []byte{0x00, 0x80, 0x00, 0x00, 2, 0xaa, 0xbb, 16} }
func growattMeasurementPayload() []byte { return []byte{0x14, 0x00, 0xff, 0xf6, 0x01, 0x2c, 80, 0xff} }
