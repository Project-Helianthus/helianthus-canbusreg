package growattlowvoltage

import (
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Profile is the profile identifier of the V1.08 contract.
const Profile = "growatt.bms.low_voltage.can.v1_08"

type field struct {
	name     string
	index    int
	min, max uint8
	unit     string
}

type row struct {
	id         string
	identifier uint32
	fields     []field
}

func (r row) encode(values map[string]uint8) (canbus.Frame, error) {
	var data [8]byte
	for _, f := range r.fields {
		v, ok := values[f.name]
		if !ok {
			return canbus.Frame{}, fmt.Errorf("%s: field %s not supplied", r.id, f.name)
		}
		if v < f.min || v > f.max {
			return canbus.Frame{}, fmt.Errorf("%s: field %s value %d outside %d..%d", r.id, f.name, v, f.min, f.max)
		}
		data[f.index] = v
	}
	id, err := canbus.NewStandardID(r.identifier)
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(id, data[:])
}
