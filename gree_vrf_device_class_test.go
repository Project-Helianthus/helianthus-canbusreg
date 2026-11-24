package canbusreg

import "testing"

func TestGreeVRFDeviceClassesAndVersions(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, row := range []struct {
		unit            byte
		product         uint16
		general, device uint16
		class           GreeVRFDeviceClass
		mismatch        bool
	}{
		{0x08, 0x6030, 1, 1, GreeVRFOutdoorUnit, false},
		{0x09, 0x6030, 10, 1, GreeVRFOutdoorUnit, true},
		{0x20, 0x6040, 1, 10, GreeVRFIndoorUnit, true},
		{0x21, 0x6036, 1, 0, GreeVRFHotWaterUnit, true},
		{0x22, 0x6035, 1, 1, GreeVRFWaterTank, false},
		{0x20, 0x6093, 1, 1, 0, false},
		{0x20, 0x6030, 1, 1, 0, false},
	} {
		id := uint32(0x1effc010) | uint32(row.unit)<<7
		image := NewGreeVRFRegisterImage(GreeVRFRegisterKey{iface, GreeVRFData, row.unit, 0x10})
		e := greeCandidateEvidence(t, iface, id, []byte{0, byte(row.general), byte(row.general >> 8), byte(row.device), byte(row.device >> 8), byte(row.product), byte(row.product >> 8)})
		if !image.Apply(e, false) {
			t.Fatal("image")
		}
		got := image.DeviceIdentity()
		if got.Class != row.class || got.Identifier != row.product || !got.HasIdentifier || got.VersionMismatch != row.mismatch || !got.HasGeneralVersion || got.GeneralVersion != row.general || !got.HasDeviceVersion || got.DeviceVersion != row.device {
			t.Fatalf("%+v: %+v", row, got)
		}
		if row.class != 0 && (got.ProfileVersion != 1 || got.DisplayNumber() != row.unit-greeVRFDisplayOffset(row.class)) {
			t.Fatal(got)
		}
	}
	if SelectGreeVRFProjection(0x60a4) != GreeVRFMapM115 || SelectGreeVRFProjection(0x6030) != GreeVRFMapM94 {
		t.Fatal("projection incorrectly coupled to class")
	}
}
