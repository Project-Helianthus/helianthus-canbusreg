package canbusreg

// GreeVRFDeviceClass identifies a register-set family, not a model or write permission.
type GreeVRFDeviceClass uint8

const (
	// GreeVRFOutdoorUnit is the outdoor module class.
	GreeVRFOutdoorUnit GreeVRFDeviceClass = iota + 1
	// GreeVRFIndoorUnit is the indoor class.
	GreeVRFIndoorUnit
	// GreeVRFHotWaterUnit is the hot-water class.
	GreeVRFHotWaterUnit
	// GreeVRFWaterTank is the water tank class.
	GreeVRFWaterTank
)

// GreeVRFDeviceIdentity retains the reported versions apart from the class profile version.
type GreeVRFDeviceIdentity struct {
	Unit                                                                uint8
	Class                                                               GreeVRFDeviceClass
	Identifier, GeneralVersion, DeviceVersion, ProfileVersion           uint16
	HasIdentifier, HasGeneralVersion, HasDeviceVersion, VersionMismatch bool
}

// DeviceIdentity selects the class profile from complete values in a type 0x10 image.
func (i *GreeVRFRegisterImage) DeviceIdentity() GreeVRFDeviceIdentity {
	identity := GreeVRFDeviceIdentity{Unit: i.key.Unit}
	if i.key.Type != 0x10 || i.bits {
		return identity
	}
	identity.GeneralVersion, identity.HasGeneralVersion = i.identityWord(0)
	identity.DeviceVersion, identity.HasDeviceVersion = i.identityWord(2)
	identity.Identifier, identity.HasIdentifier = i.identityWord(4)
	if identity.HasIdentifier {
		class := greeVRFClass(identity.Identifier)
		if class != 0 && greeVRFClassUnit(class, identity.Unit) {
			identity.Class = class
			// The exact/higher/lower lookup has only one defined version per class.
			identity.ProfileVersion = 1
			identity.VersionMismatch = identity.HasGeneralVersion && identity.GeneralVersion != 1 || identity.HasDeviceVersion && identity.DeviceVersion != 1
		}
	}
	return identity
}

func (i *GreeVRFRegisterImage) identityWord(coordinate uint16) (uint16, bool) {
	raw, _, ok := i.Read(coordinate, 2)
	if !ok {
		return 0, false
	}
	return uint16(greeVRFUnsigned(raw, 2)), true
}

func greeVRFClass(id uint16) GreeVRFDeviceClass {
	if id >= 0x6040 && id <= 0x605e {
		return GreeVRFIndoorUnit
	}
	switch id {
	case 0x0603, 0x6030, 0x6031, 0x6032, 0x6033, 0x6034, 0x6037, 0x6038, 0x6039,
		0x6062, 0x6063, 0x6064, 0x6066, 0x6067, 0x6068, 0x6069, 0x606a, 0x606e, 0x6070,
		0x607a, 0x607b, 0x607d, 0x6082, 0x6083, 0x6089, 0x608a, 0x608c, 0x6090,
		0x6094, 0x6095, 0x6096, 0x6097, 0x609a, 0x609b, 0x609c, 0x609f:
		return GreeVRFOutdoorUnit
	case 0x4021, 0x6071, 0x6072, 0x6073, 0x6074, 0x6075, 0x6076, 0x6079, 0x607f, 0x6080,
		0x6084, 0x6085, 0x6086, 0x6087, 0x6088, 0x608b, 0x608d, 0x608e, 0x608f, 0x6091,
		0x6092, 0x6098, 0x6099, 0x609d, 0x60a1, 0x60ff:
		return GreeVRFIndoorUnit
	case 0x6036, 0x6065:
		return GreeVRFHotWaterUnit
	case 0x6035:
		return GreeVRFWaterTank
	default:
		return 0
	}
}

func greeVRFClassUnit(class GreeVRFDeviceClass, unit uint8) bool {
	switch class {
	case GreeVRFOutdoorUnit:
		return unit >= 0x08 && unit <= 0x0b
	case GreeVRFIndoorUnit, GreeVRFHotWaterUnit, GreeVRFWaterTank:
		return unit >= 0x20 && unit <= 0x6f
	default:
		return false
	}
}

func greeVRFDisplayOffset(class GreeVRFDeviceClass) uint8 {
	if class == GreeVRFOutdoorUnit {
		return 7
	}
	return 31
}

// DisplayNumber is presentation only. An unresolved class has no display number.
func (identity GreeVRFDeviceIdentity) DisplayNumber() uint8 {
	if !greeVRFClassUnit(identity.Class, identity.Unit) {
		return 0
	}
	return identity.Unit - greeVRFDisplayOffset(identity.Class)
}

// OutdoorRegisterUnit selects the documented source of an outdoor module or system reference value.
func (identity GreeVRFDeviceIdentity) OutdoorRegisterUnit(system bool) (uint8, bool) {
	if identity.Class != GreeVRFOutdoorUnit || !greeVRFClassUnit(identity.Class, identity.Unit) {
		return 0, false
	}
	if system {
		return 0x08, true
	}
	return identity.Unit, true
}

// SelectGreeVRFProjection selects the map independently of the device class.
func SelectGreeVRFProjection(identifier uint16) GreeVRFMapProfile {
	switch identifier {
	case 0x605d, 0x6079, 0x6084, 0x608d, 0x608e, 0x6091, 0x6098, 0x6099, 0x60a0, 0x60a4, 0x60a9:
		return GreeVRFMapM115
	default:
		return GreeVRFMapM94
	}
}
