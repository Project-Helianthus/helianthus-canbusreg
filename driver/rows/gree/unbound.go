package gree

const (
	compressor          = "compressor"
	heatGeneratorModule = "heat generator module"
	shaftRateStatement  = "statement that operating frequency in Hz is shaft rotation rate"
	electricalPower     = "electrical power element in the pinned catalog"
	electricalEnergy    = "electrical energy element in the pinned catalog"
)

var unboundPoints = map[Coordinate]struct {
	subject string
	missing string
}{
	{0x28, 0x89, bytePoint}: {compressor, shaftRateStatement},
	{0x28, 0xb3, bytePoint}: {compressor, shaftRateStatement},
	{0x28, 0xc1, bytePoint}: {compressor, shaftRateStatement},
	{0x14, 0x04, bytePoint}: {heatGeneratorModule, electricalPower},
	{0x14, 0x06, bytePoint}: {heatGeneratorModule, electricalEnergy},
	{0x14, 0x08, bytePoint}: {heatGeneratorModule, electricalEnergy},
	{0x14, 0x0a, bytePoint}: {heatGeneratorModule, electricalEnergy},
	{0x14, 0x0c, bytePoint}: {heatGeneratorModule, electricalEnergy},
	{0x14, 0x10, bytePoint}: {heatGeneratorModule, electricalEnergy},
	{0x14, 0x14, bytePoint}: {heatGeneratorModule, electricalPower},
}
