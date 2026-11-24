package canbusreg

import (
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// GreeVRFRegisterKey names a register on one interface. Opaque is not a unit identifier.
type GreeVRFRegisterKey struct {
	Interface  canbus.InterfaceIdentity
	Family     GreeVRFIdentifierFamily
	Unit, Type uint8
}

// GreeVRFRegisterImage retains the latest fragment for each wire span. This
// preserves prior complete values when a later fragment covers only part of one.
type GreeVRFRegisterImage struct {
	key       GreeVRFRegisterKey
	fragments map[greeVRFRegisterSpan]greeVRFRegisterFragment
	latest    Evidence
	order     uint64
	bits      bool
}

type greeVRFRegisterSpan struct {
	start uint16
	width uint8
}

type greeVRFRegisterFragment struct {
	evidence Evidence
	order    uint64
}

// NewGreeVRFRegisterImage creates an empty image for one exact key.
func NewGreeVRFRegisterImage(key GreeVRFRegisterKey) *GreeVRFRegisterImage {
	return &GreeVRFRegisterImage{key: key, fragments: make(map[greeVRFRegisterSpan]greeVRFRegisterFragment)}
}

// Key returns the identity used to fence updates.
func (i *GreeVRFRegisterImage) Key() GreeVRFRegisterKey { return i.key }

// Apply records a data or addressed-data fragment using the register's
// coordinate kind. An observation from another key cannot change the image.
func (i *GreeVRFRegisterImage) Apply(e Evidence, bits bool) bool {
	id, ok := ClassifyGreeVRFIdentifier(e.Frame.ID())
	if !ok || id.Family != GreeVRFData && id.Family != GreeVRFAddressedData ||
		(GreeVRFRegisterKey{e.Interface, id.Family, id.Unit, id.Type}) != i.key ||
		e.Frame.DLC() == 0 || e.Frame.RawDLC() != e.Frame.DLC() ||
		i.order != 0 && bits != i.bits {
		return false
	}
	i.order++
	i.latest, i.bits = e, bits
	data := e.Frame.Data()
	if len(data) > 1 {
		width := uint8(len(data) - 1)
		if bits {
			width *= 8
		}
		i.fragments[greeVRFRegisterSpan{uint16(data[0]), width}] = greeVRFRegisterFragment{e, i.order}
	}
	return true
}

// Read returns the latest complete observation spanning the coordinates, not
// a join of latest individual coordinates. A bit read returns one byte, 0 or 1.
func (i *GreeVRFRegisterImage) Read(coordinate uint16, width int) ([]byte, time.Duration, bool) {
	if i.bits && width != 1 {
		return nil, 0, false
	}
	fragment, offset, present := i.fragmentAt(coordinate, width)
	if !present {
		return nil, 0, false
	}
	data := fragment.evidence.Frame.Data()[1:]
	if i.bits {
		return []byte{data[offset/8] >> (offset % 8) & 1}, fragment.evidence.Monotonic, true
	}
	return append([]byte(nil), data[offset:offset+width]...), fragment.evidence.Monotonic, true
}

func (i *GreeVRFRegisterImage) fragmentAt(coordinate uint16, width int) (greeVRFRegisterFragment, int, bool) {
	var latest greeVRFRegisterFragment
	var offset int
	if width < 1 {
		return latest, 0, false
	}
	for span, fragment := range i.fragments {
		delta := int(coordinate) - int(span.start)
		if delta >= 0 && width <= int(span.width)-delta && fragment.order > latest.order {
			latest, offset = fragment, delta
		}
	}
	return latest, offset, latest.order != 0
}
