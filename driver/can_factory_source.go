package driver

import (
	"errors"

	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

// FrameSource opens the frame source of one configured interface. Hosts and
// tests pass a recorded source here; NewFactory keeps the transport opener.
type FrameSource func(name string) (receiver.Receiver, error)

// NewFactoryWithSource returns a factory whose instances read the frame source
// the caller supplies for each interface. A nil source is refused.
func NewFactoryWithSource(source FrameSource) (*Factory, error) {
	if source == nil {
		return nil, errors.New("frame source is required")
	}
	return &Factory{planned: map[semdriver.InstanceKey]InstanceConfig{}, open: openFunc(source)}, nil
}
