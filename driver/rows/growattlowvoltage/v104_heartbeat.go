package growattlowvoltage

import (
	"errors"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	// V104Profile is the profile identifier of the V1.04 contract.
	V104Profile = "growatt.bms.low_voltage.can.v1_04"
	// V104InverterRole is the role the heartbeat row is selectable for.
	V104InverterRole = "inverter"

	// V104InverterHeartbeatRowID is the native write row of the V1.04 inverter heartbeat.
	V104InverterHeartbeatRowID = V104Profile + ".command.301"

	v104HeartbeatIdentifier uint32 = 0x301
)

var v104HeartbeatPayload = [8]byte{11, 22, 33, 44, 55, 66, 77, 88}

// V104Selection is the explicit choice of the V1.04 profile and of the
// inverter-side role.
type V104Selection struct {
	Profile string
	Role    string
}

// ErrNotV104Inverter reports a selection that cannot invoke the V1.04 heartbeat.
var ErrNotV104Inverter = errors.New("selection is not an explicit V1.04 inverter choice")

// EncodeV104InverterHeartbeat encodes the V1.04 configured inverter heartbeat
// as a standard 11-bit data frame 0x301 of DLC 8. It takes no application
// argument and refuses any selection other than the V1.04 inverter role.
func EncodeV104InverterHeartbeat(sel V104Selection) (canbus.Frame, error) {
	if sel.Profile != V104Profile || sel.Role != V104InverterRole {
		return canbus.Frame{}, ErrNotV104Inverter
	}
	id, err := canbus.NewStandardID(v104HeartbeatIdentifier)
	if err != nil {
		return canbus.Frame{}, err
	}
	return canbus.NewFrame(id, v104HeartbeatPayload[:])
}
