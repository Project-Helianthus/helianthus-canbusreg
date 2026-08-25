package canbusreg

import (
	"encoding/binary"
	"sync"

	"github.com/Project-Helianthus/helianthus-canbus"
)

const (
	growattLowVoltageBMSV104Profile = "growatt.bms.low_voltage.can.v1_04"
	growattV104WindowSize           = 16
	growattV104MaximumInterfaces    = 4
)

// GrowattLowVoltageBMSV104Projection is a read-only snapshot of the three
// frames required for V1.04 admission.
type GrowattLowVoltageBMSV104Projection struct {
	Interface            canbus.InterfaceIdentity
	LimitsEvidence       Evidence
	StatusEvidence       Evidence
	MeasurementsEvidence Evidence
	Limits               GrowattLowVoltageBMSV104Limits
	Status               GrowattLowVoltageBMSV104Status
	Measurements         GrowattLowVoltageBMSV104Measurements
}

// GrowattLowVoltageBMSV104Limits carries the engineering values from frame 0x311.
type GrowattLowVoltageBMSV104Limits struct {
	ChargeVoltageDecivolts   uint16
	ChargeCurrentDeciamps    uint16
	DischargeCurrentDeciamps uint16
	RawStatus                uint16
}

// GrowattLowVoltageBMSV104Status preserves the raw protection and warning bytes
// plus the fixed metadata from frame 0x312.
type GrowattLowVoltageBMSV104Status struct {
	Protection     [2]byte
	Warning        [2]byte
	PackCount      uint8
	Manufacturer   [2]byte
	TotalCellCount uint8
}

// GrowattLowVoltageBMSV104Measurements carries the engineering values from frame 0x313.
type GrowattLowVoltageBMSV104Measurements struct {
	VoltageCentivolts           int16
	CurrentDeciamps             int16
	MaximumCellTemperatureDeciC int16
	SOCPercent                  uint8
	SOHValue                    uint8
	SOHValid                    bool
}

type growattLowVoltageBMSV104 struct {
	mu      sync.Mutex
	windows map[canbus.InterfaceIdentity]growattV104Window
	clock   uint64
}

type growattV104Window struct {
	observations [growattV104WindowSize]Evidence
	count        int
	next         int
	lastAccess   uint64
}

type growattV104Aggregate struct {
	limits         GrowattLowVoltageBMSV104Limits
	status         GrowattLowVoltageBMSV104Status
	measurements   GrowattLowVoltageBMSV104Measurements
	hasLimits      bool
	hasStatus      bool
	hasMeasures    bool
	limitsSeq      uint64
	statusSeq      uint64
	measuresSeq    uint64
	limitsSource   Evidence
	statusSource   Evidence
	measuresSource Evidence
}

// GrowattLowVoltageBMSV104 returns the receive-only V1.04 profile.
func GrowattLowVoltageBMSV104() Profile {
	return &growattLowVoltageBMSV104{windows: make(map[canbus.InterfaceIdentity]growattV104Window)}
}

func (p *growattLowVoltageBMSV104) Classify(e Evidence) Classification {
	if e.Interface.Name() == "" || e.Interface.Index() <= 0 {
		return Classification{}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.clock++
	window := p.windowFor(e.Interface)
	if growattV104ListedFrame(e.Frame) && !growattV104Valid(e.Frame) {
		window = growattV104Window{}
	}
	window.append(e)
	window.lastAccess = p.clock
	p.windows[e.Interface] = window

	aggregate := window.aggregate()
	if !aggregate.hasLimits || !aggregate.hasStatus || !aggregate.hasMeasures {
		return Classification{}
	}
	return Classification{
		Profile: growattLowVoltageBMSV104Profile,
		Projection: GrowattLowVoltageBMSV104Projection{
			Interface:            e.Interface,
			LimitsEvidence:       aggregate.limitsSource,
			StatusEvidence:       aggregate.statusSource,
			MeasurementsEvidence: aggregate.measuresSource,
			Limits:               aggregate.limits,
			Status:               aggregate.status,
			Measurements:         aggregate.measurements,
		},
	}
}

func (p *growattLowVoltageBMSV104) windowFor(identity canbus.InterfaceIdentity) growattV104Window {
	if window, ok := p.windows[identity]; ok {
		return window
	}
	if len(p.windows) == growattV104MaximumInterfaces {
		var oldest canbus.InterfaceIdentity
		var oldestAccess uint64
		for candidate, window := range p.windows {
			if oldestAccess == 0 || window.lastAccess < oldestAccess {
				oldest = candidate
				oldestAccess = window.lastAccess
			}
		}
		delete(p.windows, oldest)
	}
	return growattV104Window{}
}

func (w *growattV104Window) append(e Evidence) {
	w.observations[w.next] = e
	w.next = (w.next + 1) % growattV104WindowSize
	if w.count < growattV104WindowSize {
		w.count++
	}
}

func (w growattV104Window) aggregate() growattV104Aggregate {
	var aggregate growattV104Aggregate
	for index := 0; index < w.count; index++ {
		observation := w.observations[index]
		frame := observation.Frame
		if !growattV104Valid(frame) {
			continue
		}
		data := frame.Bytes()
		switch frame.ID().Value() {
		case 0x311:
			if aggregate.hasLimits && observation.Sequence < aggregate.limitsSeq {
				continue
			}
			aggregate.limits = GrowattLowVoltageBMSV104Limits{
				ChargeVoltageDecivolts:   binary.BigEndian.Uint16(data[0:2]),
				ChargeCurrentDeciamps:    binary.BigEndian.Uint16(data[2:4]),
				DischargeCurrentDeciamps: binary.BigEndian.Uint16(data[4:6]),
				RawStatus:                binary.BigEndian.Uint16(data[6:8]),
			}
			aggregate.hasLimits = true
			aggregate.limitsSeq = observation.Sequence
			aggregate.limitsSource = observation
		case 0x312:
			if aggregate.hasStatus && observation.Sequence < aggregate.statusSeq {
				continue
			}
			aggregate.status = GrowattLowVoltageBMSV104Status{
				Protection:     [2]byte{data[0], data[1]},
				Warning:        [2]byte{data[2], data[3]},
				PackCount:      data[4],
				Manufacturer:   [2]byte{data[5], data[6]},
				TotalCellCount: data[7],
			}
			aggregate.hasStatus = true
			aggregate.statusSeq = observation.Sequence
			aggregate.statusSource = observation
		case 0x313:
			if aggregate.hasMeasures && observation.Sequence < aggregate.measuresSeq {
				continue
			}
			aggregate.measurements = GrowattLowVoltageBMSV104Measurements{
				VoltageCentivolts:           int16(binary.BigEndian.Uint16(data[0:2])),
				CurrentDeciamps:             int16(binary.BigEndian.Uint16(data[2:4])),
				MaximumCellTemperatureDeciC: int16(binary.BigEndian.Uint16(data[4:6])),
				SOCPercent:                  data[6],
				SOHValue:                    data[7] & 0x7f,
				SOHValid:                    data[7]&0x80 != 0,
			}
			aggregate.hasMeasures = true
			aggregate.measuresSeq = observation.Sequence
			aggregate.measuresSource = observation
		}
	}
	return aggregate
}

func growattV104ListedFrame(frame canbus.Frame) bool {
	identifier := frame.ID().Value()
	return identifier >= 0x311 && identifier <= 0x321
}

func growattV104Geometry(frame canbus.Frame) bool {
	return !frame.ID().Extended() && frame.DLC() == 8 && frame.RawDLC() == 8
}

func growattV104Valid(frame canbus.Frame) bool {
	if !growattV104Geometry(frame) {
		return false
	}
	data := frame.Bytes()
	switch frame.ID().Value() {
	case 0x312:
		return data[4] >= 1 && data[4] <= 254 && data[7] >= 1 && data[7] <= 254
	case 0x313:
		return data[6] <= 100
	default:
		return true
	}
}
