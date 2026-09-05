package canbusreg

import "github.com/Project-Helianthus/helianthus-canbus"

// GreeVRFQualificationReplay selects one source interface and one explicit
// offline map. It consumes already accepted receive-only evidence and has no
// transport, controller, or command handle.
type GreeVRFQualificationReplay struct {
	source     canbus.InterfaceIdentity
	mapProfile GreeVRFMapProfile
	productID  uint16
}

// GreeVRFQualificationReplayResult retains the candidate projection and the
// selected map result. State cells remain native and opaque.
type GreeVRFQualificationReplayResult struct {
	CandidateProfile string
	Candidate        GreeVRFCandidateProjection
	MapProfile       GreeVRFMapProfile
	ProductID        uint16
	State            GreeVRFMapState
	MatchedRows      []int
}

// NewGreeVRFQualificationReplay constructs an offline replay with explicit
// interface and map selection. The selection is not a device-identification
// or qualification result.
func NewGreeVRFQualificationReplay(source canbus.InterfaceIdentity, mapProfile GreeVRFMapProfile, productID uint16) (GreeVRFQualificationReplay, bool) {
	if source.Name() == "" || source.Index() <= 0 {
		return GreeVRFQualificationReplay{}, false
	}
	if _, ok := NewGreeVRFMapDecoder(mapProfile, productID).entries(); !ok {
		return GreeVRFQualificationReplay{}, false
	}
	return GreeVRFQualificationReplay{source: source, mapProfile: mapProfile, productID: productID}, true
}

// Apply classifies one accepted observation through the Gree candidate gate,
// then applies the selected in-memory map. Rejected evidence leaves the input
// state unchanged and has no Gree projection.
func (r GreeVRFQualificationReplay) Apply(initial GreeVRFMapState, evidence Evidence) (GreeVRFQualificationReplayResult, bool) {
	if !greeVRFSameInterface(r.source, evidence.Interface) {
		return GreeVRFQualificationReplayResult{State: initial}, false
	}

	classification := NewRegistry(GreeVRFCandidate()).Classify(evidence)
	if classification.Profile != GreeVRFCandidateProfile {
		return GreeVRFQualificationReplayResult{State: initial}, false
	}
	candidate, ok := classification.Projection.(GreeVRFCandidateProjection)
	if !ok {
		return GreeVRFQualificationReplayResult{State: initial}, false
	}

	state, rows, ok := NewGreeVRFMapDecoder(r.mapProfile, r.productID).Apply(initial, candidate.Opcode7, evidence.Frame.Data())
	if !ok {
		return GreeVRFQualificationReplayResult{State: initial}, false
	}
	return GreeVRFQualificationReplayResult{
		CandidateProfile: classification.Profile,
		Candidate:        candidate,
		MapProfile:       r.mapProfile,
		ProductID:        r.productID,
		State:            state,
		MatchedRows:      rows,
	}, true
}

func greeVRFSameInterface(left, right canbus.InterfaceIdentity) bool {
	return left.Name() == right.Name() && left.Index() == right.Index()
}
