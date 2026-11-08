package canbusreg

import (
	"fmt"
	"sync"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Admission contracts, Profile selection / Data validity and consumer obligations:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Transport, addressing and data validity:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Status and Relationship (a descriptive classification supplies no selection):
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-monitor-addressed-profile-v1.md
// This adapter consumes finite caller-associated evidence only. Protocol grades
// remain candidates. Neither the classifier nor native identity selects a profile.
type CX16ReplayInput struct {
	Selection                        CXSelection
	PCS                              CX08PCSInput
	Parallel                         CX11ParallelContext
	Summary                          CX14PCSInput
	TopologyAssociated               bool
	CellCount, SensorCount           int
	CellCountValid, SensorCountValid bool
	CellGates, SensorGates           [4]CX12MeasurementGate
	BalanceGate                      CX12MeasurementGate
	HardwareAgeKnown                 bool
	HardwareAge                      time.Duration
	MeanGate                         CX12MeasurementGate
	CellContributors                 CX12VectorSnapshot
	TemperatureSummary               CX12SummaryGates
}

type CX16Diagnostic struct {
	Input        CX01LINKInput
	NativeRecord []byte
	Status       string
}

type CX16Projection struct {
	Current           CXNativeResult
	Native            map[string]CXNativeResult
	RetainedNative    map[string]CXNativeResult
	LastGood          map[string]CXNativeField
	Diagnostics       []CX16Diagnostic
	SummaryBits       map[string]CX14SummaryBit
	PhysicalQualified bool
}

type cx16Entry struct {
	input        CX01LINKInput
	nativeRecord []byte
	parts        map[string]CXNativeResult
	bits         map[string]CX14SummaryBit
	status       string
}

type cx16SelectedProfile struct {
	mu                        sync.Mutex
	selection                 CXSelection
	replay                    map[Evidence]cx16Entry
	retained                  map[string]CXNativeResult
	diagnostics               []CX16Diagnostic
	last                      Evidence
	hasLast, identityConflict bool
}

// NewCX16SelectedProfile freezes a replay of 1..128 explicit associations.
// Registry.Classify has no epoch/context argument, so an unlisted observation
// is diagnostic-only. Register this profile explicitly; do not add it to a
// default registry. A diagnostic classification also participates in the existing
// collision rule, preventing an overlapping profile from supplying a fallback.
func NewCX16SelectedProfile(s CXSelection, replay []CX16ReplayInput) (Profile, error) {
	validated, err := NewCXSelection(s.CX01Spec())
	if err != nil || validated != s || s.CX01Group() == "" {
		return nil, fmt.Errorf("explicit supported selection required")
	}
	if len(replay) < 1 || len(replay) > 128 {
		return nil, fmt.Errorf("replay bound must be 1..128")
	}
	p := &cx16SelectedProfile{selection: s, replay: make(map[Evidence]cx16Entry), retained: make(map[string]CXNativeResult)}
	for _, in := range replay {
		i := in.PCS.CX01LINKInput
		if len(in.PCS.NativeRecord) > 72 {
			return nil, fmt.Errorf("native record exceeds bounded diagnostic size")
		}
		if _, exists := p.replay[i.Evidence]; exists {
			return nil, fmt.Errorf("duplicate evidence association")
		}
		e := cx16Entry{input: i, nativeRecord: append([]byte(nil), in.PCS.NativeRecord...), status: "invalid", parts: make(map[string]CXNativeResult)}
		if in.Selection == s && cx16Context(s, i, in.PCS.NativeRecord) {
			e.status = "descriptive"
			if (s.CX01Spec().Flavor == "pcs-standard" || s.CX01Spec().Flavor == "pcs-parallel") && !in.PCS.PublisherResolved {
				e.status = "conflict"
			} else {
				e.parts, e.bits = cx16Decode(s, in)
			}
		}
		p.replay[i.Evidence] = e
	}
	return p, nil
}

func cx16Context(s CXSelection, i CX01LINKInput, native []byte) bool {
	c, f := s.CX01Spec(), i.Evidence.Frame
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

func cx16Decode(s CXSelection, in CX16ReplayInput) (map[string]CXNativeResult, map[string]CX14SummaryBit) {
	i := in.PCS.CX01LINKInput
	r := make(map[string]CXNativeResult)
	var bits map[string]CX14SummaryBit
	switch s.CX01Spec().Flavor {
	case "pcs-standard":
		r["core"] = CX05DecodePCSStandard(s, in.PCS.CX05PCSInput).CXNativeResult
		r["gauge"] = CX06DecodePCSGauge(s, in.PCS.CX06PCSInput)
		r["identity"] = CX06DecodePCSIdentity(s, in.PCS.CX06PCSInput)
		r["slots"] = CX08DecodePCSSlots(s, in.PCS)
		summary := in.Summary
		summary.CX05PCSInput = in.PCS.CX05PCSInput
		decoded := CX14DecodePCSSummaries(s, summary)
		r["summary"], bits = decoded.CXNativeResult, decoded.Bits
	case "pcs-extended19":
		r["extended"] = CX09DecodePCSExtended(s, i)
	case "pcs-parallel":
		r["parallel"] = CX10DecodeParallel(s, i)
		r["release11"] = CX11DecodeParallel(s, i, in.Parallel)
	case "link":
		r["link"] = CXDecodeLINKTelemetry(s, i)
		r["diagnostic"] = CX13DecodeLINKDiagnostics(s, i)
		r["cells"] = CX12NewLINKCells(s, in.CellCount, in.CellCountValid && in.CellCount >= 1 && in.CellCount <= 16).CX12Decode(i, in.CellGates)
		r["sensors"] = CX12NewLINKTemperatures(s, in.SensorCount, in.SensorCountValid && in.SensorCount >= 1 && in.SensorCount <= 4).CX12Decode(i, in.SensorGates)
		r["balance"] = CX12DecodeLINKBalance(s, i, in.CellCount, in.CellCountValid, in.BalanceGate, in.HardwareAgeKnown, in.HardwareAge)
		contributors := in.CellContributors
		if contributors.ConfiguredCount < 1 || contributors.ConfiguredCount > 16 {
			contributors.CountValid = false
			contributors.Fixed16Path = false
		}
		r["mean"] = CX12DecodeLINKCellMean(s, i, in.MeanGate, contributors, false)
		temperature := in.TemperatureSummary
		if temperature.Contributors.ConfiguredCount < 1 || temperature.Contributors.ConfiguredCount > 4 {
			temperature.Contributors.CountValid = false
			temperature.Contributors.Fixed16Path = false
		}
		r["temperature"] = CX12DecodeLINKTemperatureSummary(s, i, temperature)
	}
	r["confirmation"] = CX01ConfirmNativeSelection(s, CX01NativeIndication{Input: i, TopologyAssociated: in.TopologyAssociated})
	return r, bits
}

func (p *cx16SelectedProfile) Classify(e Evidence) Classification {
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
	r := cx01Result(p.selection, CX01LINKInput{Evidence: e, Epoch: entry.input.Epoch}, status)
	projection := CX16Projection{Current: r, Native: make(map[string]CXNativeResult), RetainedNative: make(map[string]CXNativeResult), LastGood: make(map[string]CXNativeField), SummaryBits: make(map[string]CX14SummaryBit)}
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
					key := cx16RetentionKey(prefix, e)
					p.retained[key] = CX05RetainAccepted(p.retained[key], part)
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
	p.diagnostics = append(p.diagnostics, CX16Diagnostic{Input: input, NativeRecord: append([]byte(nil), entry.nativeRecord...), Status: projection.Current.Status})
	if len(p.diagnostics) > 16 {
		p.diagnostics = p.diagnostics[len(p.diagnostics)-16:]
	}
	for _, diagnostic := range p.diagnostics {
		diagnostic.NativeRecord = append([]byte(nil), diagnostic.NativeRecord...)
		projection.Diagnostics = append(projection.Diagnostics, diagnostic)
	}
	if !listed && !cx16OverlappingGeometry(e.Frame) {
		return Classification{}
	}
	return Classification{Profile: "growatt.project.selected", Projection: projection}
}

// cx16RetentionKey keys retained extended and gauge PCS values by native family
// so that families sharing field names never overwrite one another.
func cx16RetentionKey(prefix string, e Evidence) string {
	if prefix == "gauge" {
		return fmt.Sprintf("%s/%03x", prefix, e.Frame.ID().Value())
	}
	if prefix != "extended" {
		return prefix
	}
	return fmt.Sprintf("%s/%04x", prefix, e.Frame.ID().Value()>>16)
}

// This is a collision shield, not recognition or a native meaning dictionary.
func cx16OverlappingGeometry(f canbus.Frame) bool {
	id := f.ID().Value()
	if id >= 0x311 && id <= 0x334 || id == 0x37f || id == 0x3f7 || id >= 0x500 && id <= 0x6ef {
		return true
	}
	if f.ID().Extended() {
		return id>>24 == 0x18 || id>>24 == 0x19
	}
	return false
}
