package canbusreg

import "github.com/Project-Helianthus/helianthus-canbus"

// GrowattLowVoltageBMSV104QualificationReplay consumes already accepted
// receive-only evidence from one selected interface through the existing V1.04
// registry profile. It has no transport, controller, or command handle.
type GrowattLowVoltageBMSV104QualificationReplay struct {
	source   canbus.InterfaceIdentity
	registry Registry
}

// NewGrowattLowVoltageBMSV104QualificationReplay constructs a provider-local
// offline replay with an explicit source interface. Selecting it neither
// identifies equipment nor qualifies another Growatt version or family.
func NewGrowattLowVoltageBMSV104QualificationReplay(source canbus.InterfaceIdentity) (*GrowattLowVoltageBMSV104QualificationReplay, bool) {
	if source.Name() == "" || source.Index() <= 0 {
		return nil, false
	}
	return &GrowattLowVoltageBMSV104QualificationReplay{
		source:   source,
		registry: NewRegistry(GrowattLowVoltageBMSV104()),
	}, true
}

// Apply feeds one observation through the existing V1.04 profile. Evidence
// from another interface and all incomplete or invalid sequences remain
// unclassified at this provider boundary.
func (r *GrowattLowVoltageBMSV104QualificationReplay) Apply(evidence Evidence) (GrowattLowVoltageBMSV104Projection, bool) {
	if r == nil || !growattV104SameInterface(r.source, evidence.Interface) {
		return GrowattLowVoltageBMSV104Projection{}, false
	}
	classification := r.registry.Classify(evidence)
	if classification.Profile != growattLowVoltageBMSV104Profile {
		return GrowattLowVoltageBMSV104Projection{}, false
	}
	projection, ok := classification.Projection.(GrowattLowVoltageBMSV104Projection)
	if !ok {
		return GrowattLowVoltageBMSV104Projection{}, false
	}
	return projection, true
}

func growattV104SameInterface(left, right canbus.InterfaceIdentity) bool {
	return left.Name() == right.Name() && left.Index() == right.Index()
}
