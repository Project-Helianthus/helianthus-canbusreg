package canbusreg

const maxObservationDataBytes = 8

// Observation is one received CAN frame represented without transport-specific state.
type Observation struct {
	Extended bool
	ID       uint32
	Data     []byte
}

// Classification is the semantic result of matching an observation to a profile.
// An empty value represents opaque, unclassified evidence.
type Classification struct {
	Profile    string
	Projection any
}

// Profile can explicitly recognize an observation and supply its semantic projection.
type Profile interface {
	Classify(Observation) Classification
}

// Registry evaluates explicitly registered vendor profiles. It does not infer profiles
// from frame shape, identifier ranges, or payload contents.
type Registry struct {
	profiles []Profile
}

// NewRegistry creates a registry with the supplied explicit vendor profiles.
func NewRegistry(profiles ...Profile) Registry {
	return Registry{profiles: append([]Profile(nil), profiles...)}
}

// Classify returns an opaque result when the observation is malformed or no profile
// explicitly provides a projection.
func (r Registry) Classify(observation Observation) Classification {
	if !observationWellFormed(observation) {
		return Classification{}
	}

	for _, profile := range r.profiles {
		if profile == nil {
			continue
		}

		result := profile.Classify(observation)
		if result.Profile != "" && result.Projection != nil {
			return result
		}
	}

	return Classification{}
}

func observationWellFormed(observation Observation) bool {
	if len(observation.Data) > maxObservationDataBytes {
		return false
	}

	if observation.Extended {
		return observation.ID <= 0x1fffffff
	}

	return observation.ID <= 0x7ff
}

type greeVRFCandidate struct{}

// GreeVRFCandidate registers the first Gree VRF flavor. It deliberately has no
// frame classifier or projection until the flavor has explicit evidence.
func GreeVRFCandidate() Profile {
	return greeVRFCandidate{}
}

func (greeVRFCandidate) Classify(Observation) Classification {
	return Classification{}
}
