package canbusreg

import (
	"fmt"
	"sync"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// ReplayInput associates finite native evidence with explicit selection and validity gates.
// Admission contracts, Profile selection / Data validity and consumer obligations:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Transport, addressing and data validity:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Status and Relationship (a descriptive classification supplies no selection):
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-monitor-addressed-profile-v1.md
// This adapter consumes finite caller-associated evidence only. Protocol grades
// remain candidates. Neither the classifier nor native identity selects a profile.
type ReplayInput struct {
	Selection                        CXSelection
	PCS                              PCSSlotPCSInput
	Parallel                         ParallelContext
	Summary                          PCSSummaryPCSInput
	TopologyAssociated               bool
	CellCount, SensorCount           int
	CellCountValid, SensorCountValid bool
	CellGates, SensorGates           [4]MeasurementGate
	BalanceGate                      MeasurementGate
	HardwareAgeKnown                 bool
	HardwareAge                      time.Duration
	MeanGate                         MeasurementGate
	CellContributors                 VectorSnapshot
	TemperatureSummary               SummaryGates
}

// Diagnostic retains the associated input, native record and rejection or descriptive status.
type Diagnostic struct {
	Input        LINKInput
	NativeRecord []byte
	Status       string
}

// ReplayProjection separates current, retained and last-known-good candidate evidence from diagnostics and physical qualification.
// Callers must check Current.Status == "descriptive" and each current native
// result and field status before using current values. RetainedNative and LastGood
// are historical values, including when Current reports rejection; their presence
// does not establish acceptance of the current evidence or physical qualification.
type ReplayProjection struct {
	Current           CXNativeResult
	Native            map[string]CXNativeResult
	RetainedNative    map[string]CXNativeResult
	LastGood          map[string]CXNativeField
	Diagnostics       []Diagnostic
	SummaryBits       map[string]SummaryBit
	PhysicalQualified bool
}

type replayEntry struct {
	input        LINKInput
	nativeRecord []byte
	parts        map[string]CXNativeResult
	bits         map[string]SummaryBit
	status       string
}

type replaySelectedProfile struct {
	mu                        sync.Mutex
	selection                 CXSelection
	replay                    map[Evidence]replayEntry
	retained                  map[string]CXNativeResult
	diagnostics               []Diagnostic
	last                      Evidence
	hasLast, identityConflict bool
}

// NewCX16SelectedProfile freezes a replay of 1..128 explicit associations.
// Registry.Classify has no epoch/context argument, so an unlisted observation
// is diagnostic-only. Register this profile explicitly; do not add it to a
// default registry. A diagnostic classification also participates in the existing
// collision rule, preventing an overlapping profile from supplying a fallback.
func NewCX16SelectedProfile(s CXSelection, replay []ReplayInput) (Profile, error) {
	validated, err := NewCXSelection(s.SelectionSpecValue())
	if err != nil || validated != s || s.SelectionGroup() == "" {
		return nil, fmt.Errorf("explicit supported selection required")
	}
	if len(replay) < 1 || len(replay) > 128 {
		return nil, fmt.Errorf("replay bound must be 1..128")
	}
	p := &replaySelectedProfile{selection: s, replay: make(map[Evidence]replayEntry), retained: make(map[string]CXNativeResult)}
	for _, in := range replay {
		i := in.PCS.LINKInput
		if len(in.PCS.NativeRecord) > 72 {
			return nil, fmt.Errorf("native record exceeds bounded diagnostic size")
		}
		if _, exists := p.replay[i.Evidence]; exists {
			return nil, fmt.Errorf("duplicate evidence association")
		}
		e := replayEntry{input: i, nativeRecord: append([]byte(nil), in.PCS.NativeRecord...), status: "invalid", parts: make(map[string]CXNativeResult)}
		if in.Selection == s && replayContext(s, i, in.PCS.NativeRecord) {
			e.status = "descriptive"
			if (s.SelectionSpecValue().Flavor == "pcs-standard" || s.SelectionSpecValue().Flavor == "pcs-parallel") && !in.PCS.PublisherResolved {
				e.status = "conflict"
			} else {
				e.parts, e.bits = fixture16Decode(s, in)
			}
		}
		p.replay[i.Evidence] = e
	}
	return p, nil
}

func replayContext(s CXSelection, i LINKInput, native []byte) bool {
	c, f := s.SelectionSpecValue(), i.Evidence.Frame
	if i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot || i.FieldAge < 0 || i.Evidence.Sequence == 0 || i.Evidence.Monotonic < 0 || f.DLC() != 8 || f.RawDLC() != 8 {
		return false
	}
	records := [][]byte{native}
	if i.Evidence.RawRecord != [canbus.SocketCANRecordSize]byte{} {
		records = append(records, i.Evidence.RawRecord[:])
	}
	for _, raw := range records {
		if len(raw) == 0 {
			continue
		}
		decoded, err := canbus.DecodeSocketCANRecord(raw)
		if err != nil || decoded != f {
			return false
		}
	}
	if c.Flavor == "pcs-standard" || c.Flavor == "pcs-parallel" {
		return !f.ID().Extended()
	}
	id := f.ID().Value()
	return f.ID().Extended() && c.RouteSet && uint8(id>>8) == c.Destination && uint8(id) == c.Source
}

func fixture16Decode(s CXSelection, in ReplayInput) (map[string]CXNativeResult, map[string]SummaryBit) {
	i := in.PCS.LINKInput
	r := make(map[string]CXNativeResult)
	var bits map[string]SummaryBit
	switch s.SelectionSpecValue().Flavor {
	case "pcs-standard":
		r["core"] = DecodePCSStandard(s, in.PCS.PCSStandardPCSInput).CXNativeResult
		r["gauge"] = DecodePCSGauge(s, in.PCS.PCSIdentityPCSInput)
		r["identity"] = DecodePCSIdentity(s, in.PCS.PCSIdentityPCSInput)
		r["slots"] = DecodePCSSlots(s, in.PCS)
		summary := in.Summary
		summary.PCSStandardPCSInput = in.PCS.PCSStandardPCSInput
		decoded := DecodePCSSummaries(s, summary)
		r["summary"], bits = decoded.CXNativeResult, decoded.Bits
	case "pcs-extended19":
		r["extended"] = DecodePCSExtended(s, i)
	case "pcs-parallel":
		r["parallel"] = ParallelIdentityDecodeParallel(s, i)
		r["release11"] = ParallelReleaseDecodeParallel(s, i, in.Parallel)
	case "link":
		r["link"] = CXDecodeLINKTelemetry(s, i)
		r["diagnostic"] = DecodeLINKDiagnostics(s, i)
		r["cells"] = NewLINKCells(s, in.CellCount, in.CellCountValid && in.CellCount >= 1 && in.CellCount <= 16).DecodeLINKVector(i, in.CellGates)
		r["sensors"] = NewLINKTemperatures(s, in.SensorCount, in.SensorCountValid && in.SensorCount >= 1 && in.SensorCount <= 4).DecodeLINKVector(i, in.SensorGates)
		r["balance"] = DecodeLINKBalance(s, i, in.CellCount, in.CellCountValid, in.BalanceGate, in.HardwareAgeKnown, in.HardwareAge)
		contributors := in.CellContributors
		if contributors.ConfiguredCount < 1 || contributors.ConfiguredCount > 16 {
			contributors.CountValid = false
			contributors.Fixed16Path = false
		}
		r["mean"] = DecodeLINKCellMean(s, i, in.MeanGate, contributors, false)
		temperature := in.TemperatureSummary
		if temperature.Contributors.ConfiguredCount < 1 || temperature.Contributors.ConfiguredCount > 4 {
			temperature.Contributors.CountValid = false
			temperature.Contributors.Fixed16Path = false
		}
		r["temperature"] = DecodeLINKTemperatureSummary(s, i, temperature)
	}
	r["confirmation"] = ConfirmNativeSelection(s, SelectionNativeIndication{Input: i, TopologyAssociated: in.TopologyAssociated})
	return r, bits
}

// Classify returns current candidate results or rejection diagnostics, potentially
// with retained historical values. Callers must check Current.Status == "descriptive"
// and the current native result and field statuses before using current values;
// RetainedNative and LastGood do not establish acceptance of the current evidence.
// Unlisted evidence with overlapping frame geometry returns a diagnostic
// classification that participates in Registry's collision rule, suppressing an
// overlapping profile's fallback. Other unlisted evidence returns an empty classification.
func (p *replaySelectedProfile) Classify(e Evidence) Classification {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, listed := p.replay[e]
	status := entry.status
	if !listed {
		status = "unlisted"
	}
	if status == "descriptive" {
		if p.identityConflict {
			status = "conflict"
		} else if p.hasLast && e.Sequence <= p.last.Sequence {
			status = "stale"
			if e.Sequence == p.last.Sequence && e != p.last {
				status = "conflict"
			}
		} else if p.hasLast && e.Monotonic < p.last.Monotonic {
			status = "stale"
		}
	}
	r := selectionResult(p.selection, LINKInput{Evidence: e, Epoch: entry.input.Epoch}, status)
	projection := ReplayProjection{Current: r, Native: make(map[string]CXNativeResult), RetainedNative: make(map[string]CXNativeResult), LastGood: make(map[string]CXNativeField), SummaryBits: make(map[string]SummaryBit)}
	if status == "descriptive" {
		// Only explicit native confirmation/conflict affects identity continuity.
		if entry.parts["link"].Status == "conflict" || entry.parts["confirmation"].Status == "conflict" {
			p.identityConflict = true
			projection.Current.Status = "conflict"
		} else {
			for prefix, part := range entry.parts {
				native := part
				native.Fields = make(map[string]CXNativeField, len(part.Fields))
				for name, f := range part.Fields {
					native.Fields[name] = f
					projection.Current.Fields[prefix+"/"+name] = f
				}
				projection.Native[prefix] = native
				if len(part.Fields) > 0 {
					key := replayRetentionKey(prefix, e, part)
					p.retained[key] = RetainAccepted(p.retained[key], part)
				}
			}
			for name, bit := range entry.bits {
				bit.Alternatives = append([]int(nil), bit.Alternatives...)
				bit.ConditionalAlternatives = append([]int(nil), bit.ConditionalAlternatives...)
				projection.SummaryBits[name] = bit
			}
		}
		p.last, p.hasLast = e, true
	}
	for prefix, part := range p.retained {
		retained := part
		retained.Fields = make(map[string]CXNativeField, len(part.Fields))
		for name, f := range part.Fields {
			retained.Fields[name] = f
			if f.Status == "accepted" || f.Status == "empty" {
				projection.LastGood[prefix+"/"+name] = f
			}
		}
		projection.RetainedNative[prefix] = retained
	}
	input := entry.input
	input.Evidence = e
	p.diagnostics = append(p.diagnostics, Diagnostic{Input: input, NativeRecord: append([]byte(nil), entry.nativeRecord...), Status: projection.Current.Status})
	if len(p.diagnostics) > 16 {
		p.diagnostics = p.diagnostics[len(p.diagnostics)-16:]
	}
	for _, diagnostic := range p.diagnostics {
		diagnostic.NativeRecord = append([]byte(nil), diagnostic.NativeRecord...)
		projection.Diagnostics = append(projection.Diagnostics, diagnostic)
	}
	if !listed && !replayOverlappingGeometry(e.Frame) {
		return Classification{}
	}
	return Classification{Profile: "growatt.project.selected", Projection: projection}
}

// cx16RetentionKey keys retained extended and gauge PCS values by native family
// so that families sharing field names never overwrite one another. Slot
// records without an associated member are kept apart from member-associated ones.
func replayRetentionKey(prefix string, e Evidence, part CXNativeResult) string {
	if prefix == "slots" && !part.HasMember {
		return prefix + "/unassociated"
	}
	if prefix == "parallel" || prefix == "release11" {
		return fmt.Sprintf("%s/%03x", prefix, e.Frame.ID().Value())
	}
	if prefix == "gauge" {
		return fmt.Sprintf("%s/%03x", prefix, e.Frame.ID().Value())
	}
	if prefix == "link" {
		// LINK record kinds share field names (cell and temperature indexes, spreads), so each kind keeps
		// its own retention key and a later kind never overwrites another kind's evidence.
		return fmt.Sprintf("%s/%04x", prefix, e.Frame.ID().Value()>>16)
	}
	if prefix != "extended" {
		return prefix
	}
	return fmt.Sprintf("%s/%04x", prefix, e.Frame.ID().Value()>>16)
}

// This is a collision shield, not recognition or a native meaning dictionary.
func replayOverlappingGeometry(f canbus.Frame) bool {
	id := f.ID().Value()
	if id >= 0x311 && id <= 0x334 || id == 0x37f || id == 0x3f7 || id >= 0x500 && id <= 0x6ef {
		return true
	}
	if f.ID().Extended() {
		return id>>24 == 0x18 || id>>24 == 0x19
	}
	return false
}
