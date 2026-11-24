package canbusreg

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type greeProviderResult struct {
	observation GreeVRFObservation
	err         error
}

type greeProviderSource struct {
	entered chan struct{}
	results chan greeProviderResult
	late    *GreeVRFObservation
}

func (s *greeProviderSource) Receive(ctx context.Context) (GreeVRFObservation, error) {
	select {
	case s.entered <- struct{}{}:
	case <-ctx.Done():
		return GreeVRFObservation{}, ctx.Err()
	}
	select {
	case result := <-s.results:
		return result.observation, result.err
	case <-ctx.Done():
		if s.late != nil {
			return *s.late, nil
		}
		return GreeVRFObservation{}, ctx.Err()
	}
}

func greeProviderNewSource() *greeProviderSource {
	return &greeProviderSource{entered: make(chan struct{}), results: make(chan greeProviderResult)}
}

func greeProviderNew(t *testing.T, mapProfile GreeVRFMapProfile, productID uint16) *GreeVRFProvider {
	t.Helper()
	provider, err := NewGreeVRFProvider(greeReplayInterface(t, "can7", 7), mapProfile, productID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Stop)
	return provider
}

func greeProviderStart(t *testing.T, provider *GreeVRFProvider, source *greeProviderSource, generation uint64) {
	t.Helper()
	if err := provider.Start(source, generation); err != nil {
		t.Fatal(err)
	}
	<-source.entered
}

// greeProviderSend hands one observation to the running receive loop and waits
// until the loop asks for the next one, so the observation has been applied.
func greeProviderSend(source *greeProviderSource, generation uint64, evidence Evidence) {
	source.results <- greeProviderResult{observation: GreeVRFObservation{Generation: generation, Evidence: evidence}}
	<-source.entered
}

func greeProviderApply(provider *GreeVRFProvider, generation uint64, evidence Evidence) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.greeVRFProviderApply(GreeVRFObservation{Generation: generation, Evidence: evidence})
}

// greeProviderFrame builds an admitted opcode 0x10 candidate frame for one
// native unit offset; the payload is the replay vector layout.
func greeProviderFrame(t *testing.T, unitOffset uint8, payload []byte, sequence uint64) Evidence {
	t.Helper()
	return greeReplayEvidence(t, greeReplayInterface(t, "can7", 7), 0x1effc010|uint32(unitOffset+8)<<7, payload, sequence)
}

func greeProviderUnitKey(offset uint8) GreeVRFUnitKey { return GreeVRFUnitKey{GreeVRFData, offset + 8} }

var greeProviderVector = []byte{0x02, 0x31, 0x32, 0x33, 0xa1, 0xa2}

func TestGreeVRFProviderStartReceiveSnapshotStop(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	if got := provider.Snapshot(); got.State != "stopped" || got.Generation != 0 || len(got.Units) != 0 || got.HasRaw {
		t.Fatalf("initial snapshot = %#v", got)
	}
	source := greeProviderNewSource()
	greeProviderStart(t, provider, source, 1)
	if got := provider.Snapshot(); got.State != "awaiting" || got.Generation != 1 {
		t.Fatalf("started snapshot = %#v", got)
	}

	first := greeProviderFrame(t, 0, greeProviderVector, 1)
	greeProviderSend(source, 1, first)
	got := provider.Snapshot()
	unit, ok := got.Units[greeProviderUnitKey(0)]
	if got.State != "receiving" || got.Admitted != 1 || !ok || unit.Generation != 1 || unit.Received != time.Millisecond {
		t.Fatalf("first snapshot = %#v", got)
	}
	if unit.Candidate.Evidence != first || unit.Candidate.Opaque7 != 0x7f || unit.Candidate.Unit7 != 8 || unit.Candidate.Opcode7 != 0x10 {
		t.Fatalf("native candidate evidence = %#v", unit.Candidate)
	}
	if !reflect.DeepEqual(unit.Candidate.Updates, []GreeVRFCandidateStateCell{{Cell: 0x0f, Value: 0xa1}, {Cell: 0x10, Value: 0xa2}}) {
		t.Fatalf("candidate cells = %#v", unit.Candidate.Updates)
	}
	if !reflect.DeepEqual(unit.State.Cells, map[uint8]uint8{0x00: 0x33, 0x01: 0xa1, 0x02: 0xa2, 0x03: 0x31, 0x04: 0x32}) || !reflect.DeepEqual(unit.MatchedRows, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("map state = %#v rows = %#v", unit.State.Cells, unit.MatchedRows)
	}

	greeProviderSend(source, 1, greeProviderFrame(t, 0, []byte{0x05, 0xb1, 0xb2}, 2))
	unit = provider.Snapshot().Units[greeProviderUnitKey(0)]
	if unit.State.Cells[0x01] != 0xb1 || unit.State.Cells[0x02] != 0xb2 || unit.State.Cells[0x03] != 0x31 || unit.Received != 2*time.Millisecond {
		t.Fatalf("generation state did not accumulate: %#v", unit)
	}

	greeProviderSend(source, 1, greeProviderFrame(t, 1, []byte{0x05, 0xc1, 0xc2}, 3))
	got = provider.Snapshot()
	other := got.Units[greeProviderUnitKey(1)]
	if len(got.Units) != 2 || other.Candidate.Evidence != (Evidence{}) || !reflect.DeepEqual(other.State.Cells, map[uint8]uint8{0x01: 0xc1, 0x02: 0xc2}) {
		t.Fatalf("units are not separated despite sharing the opaque field: %#v", got.Units)
	}

	unsupported := greeReplayEvidence(t, greeReplayInterface(t, "can7", 7), 0x1ee00010, greeProviderVector, 4)
	greeProviderSend(source, 1, unsupported)
	got = provider.Snapshot()
	if got.Unsupported != 1 || got.Admitted != 3 || !got.HasRaw || got.Raw != unsupported || got.RawGeneration != 1 || got.Units[greeProviderUnitKey(0)].Received != 2*time.Millisecond {
		t.Fatalf("unsupported evidence = %#v", got)
	}

	provider.Stop()
	got = provider.Snapshot()
	if got.State != "stopped" || len(got.Units) != 2 || got.Units[greeProviderUnitKey(0)].Generation != 1 {
		t.Fatalf("stop discarded history: %#v", got)
	}
}

func TestGreeVRFProviderMapsRegistersOutsideCandidateGate(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	greeProviderStart(t, provider, greeProviderNewSource(), 1)
	iface := greeReplayInterface(t, "can7", 7)
	for index, id := range []uint32{0x1effd01e, 0x1effd09e, 0x1cffd01e} {
		value := byte(1)
		if index == 1 {
			value = 0
		}
		greeProviderApply(provider, 1, greeReplayEvidence(t, iface, id, []byte{0x13, value}, uint64(index+1)))
	}
	snapshot := provider.Snapshot()
	if snapshot.Admitted != 3 || snapshot.Unsupported != 0 || len(snapshot.Units) != 3 {
		t.Fatalf("register admission: %+v", snapshot)
	}
	for _, row := range []struct {
		key  GreeVRFUnitKey
		want byte
	}{
		{GreeVRFUnitKey{GreeVRFData, 0x20}, 128},
		{GreeVRFUnitKey{GreeVRFData, 0x21}, 0},
		{GreeVRFUnitKey{GreeVRFAddressedData, 0x20}, 128},
	} {
		unit := snapshot.Units[row.key]
		if !reflect.DeepEqual(unit.MatchedRows, []int{25, 26, 27}) {
			t.Fatal(row.key, unit.MatchedRows)
		}
		if unit.State.Cells[0x32]&128 != row.want || unit.Candidate.Evidence != (Evidence{}) || unit.Images[0x1e] == nil {
			t.Fatalf("unit %+v: %+v", row.key, unit)
		}
	}
}

func TestGreeVRFProviderSnapshotIsDetached(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	greeProviderStart(t, provider, greeProviderNewSource(), 1)
	greeProviderApply(provider, 1, greeProviderFrame(t, 0, greeProviderVector, 1))
	mutated := provider.Snapshot()
	unit := mutated.Units[greeProviderUnitKey(0)]
	unit.State.Cells[0x03] = 0xee
	unit.State.SlotFlags[0] = 9
	unit.Candidate.Updates[0].Value = 0xee
	unit.MatchedRows[0] = 99
	unit.Candidate.Evidence.RawRecord[0] = 0xee
	mutated.Units[greeProviderUnitKey(0)] = unit
	mutated.Units[greeProviderUnitKey(9)] = GreeVRFUnitSnapshot{}
	got := provider.Snapshot()
	kept := got.Units[greeProviderUnitKey(0)]
	if len(got.Units) != 1 || kept.State.Cells[0x03] != 0x31 || kept.State.SlotFlags[0] != 0 || kept.Candidate.Updates[0].Value != 0xa1 ||
		kept.MatchedRows[0] != 0 || kept.Candidate.Evidence.RawRecord[0] != 0x10 {
		t.Fatalf("snapshot aliases provider state: %#v", got)
	}
}

func TestGreeVRFProviderAppliesSelectedMapProfileAndProduct(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		mapProfile GreeVRFMapProfile
		productID  uint16
		cells      map[uint8]uint8
	}{
		{name: "first family", mapProfile: GreeVRFMapM94, cells: map[uint8]uint8{0x00: 0x33, 0x01: 0xa1, 0x02: 0xa2, 0x03: 0x31, 0x04: 0x32}},
		{name: "second family", mapProfile: GreeVRFMapM115, cells: map[uint8]uint8{0x01: 0x33, 0x02: 0xa1, 0x03: 0x31, 0x04: 0x32}},
		{name: "second family cleanup product", mapProfile: GreeVRFMapM115, productID: 0x6098, cells: map[uint8]uint8{0x01: 0x33, 0x02: 0xa1, 0x03: 0x31, 0x04: 0x32, 0x06: 0, 0x11: 0, 0x42: 0, 0x44: 0}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := greeProviderNew(t, testCase.mapProfile, testCase.productID)
			greeProviderStart(t, provider, greeProviderNewSource(), 1)
			greeProviderApply(provider, 1, greeProviderFrame(t, 0, greeProviderVector, 1))
			got := provider.Snapshot()
			if got.MapProfile != testCase.mapProfile || got.ProductID != testCase.productID {
				t.Fatalf("selection = %#v", got)
			}
			replay, _ := NewGreeVRFQualificationReplay(greeReplayInterface(t, "can7", 7), testCase.mapProfile, testCase.productID)
			want, _ := replay.Apply(GreeVRFMapState{}, greeProviderFrame(t, 0, greeProviderVector, 1))
			if !reflect.DeepEqual(got.Units[greeProviderUnitKey(0)].State.Cells, testCase.cells) || !reflect.DeepEqual(got.Units[greeProviderUnitKey(0)].State, want.State) {
				t.Fatalf("cells = %#v, replay = %#v", got.Units[greeProviderUnitKey(0)].State.Cells, want.State.Cells)
			}
		})
	}
}

// TestGreeVRFProviderFencesObservationIdentity varies every field of the
// admission fence: Generation (a mutation that drops the generation check),
// Interface name and index (a mutation that compares only one of them),
// Sequence equal and older (a mutation that uses <) and Monotonic older (a
// mutation that drops the bound). The zero sequence and the negative
// monotonic time are covered at the start of a generation below.
func TestGreeVRFProviderFencesObservationIdentity(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		generation uint64
		ifName     string
		ifIndex    int
		sequence   uint64
		monotonic  time.Duration
	}{
		{name: "generation", generation: 2, ifName: "can7", ifIndex: 7, sequence: 6, monotonic: 6 * time.Millisecond},
		{name: "interface name", generation: 1, ifName: "can8", ifIndex: 7, sequence: 6, monotonic: 6 * time.Millisecond},
		{name: "interface index", generation: 1, ifName: "can7", ifIndex: 8, sequence: 6, monotonic: 6 * time.Millisecond},
		{name: "equal sequence", generation: 1, ifName: "can7", ifIndex: 7, sequence: 5, monotonic: 6 * time.Millisecond},
		{name: "older sequence", generation: 1, ifName: "can7", ifIndex: 7, sequence: 4, monotonic: 6 * time.Millisecond},
		{name: "older monotonic", generation: 1, ifName: "can7", ifIndex: 7, sequence: 6, monotonic: 4 * time.Millisecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := greeProviderNew(t, GreeVRFMapM94, 0)
			greeProviderStart(t, provider, greeProviderNewSource(), 1)
			greeProviderApply(provider, 1, greeProviderFrame(t, 0, greeProviderVector, 5))
			before := provider.Snapshot()
			candidate := greeReplayEvidence(t, greeReplayInterface(t, testCase.ifName, testCase.ifIndex), 0x1effc410, []byte{0x05, 0xd1, 0xd2}, testCase.sequence)
			candidate.Monotonic = testCase.monotonic
			greeProviderApply(provider, testCase.generation, candidate)
			got := provider.Snapshot()
			if got.Rejected != 1 || got.Raw != before.Raw || !reflect.DeepEqual(got.Units, before.Units) {
				t.Fatalf("fenced observation published: %#v", got)
			}
			successor := greeProviderFrame(t, 0, []byte{0x05, 0xd1, 0xd2}, 6)
			successor.Monotonic = 5 * time.Millisecond
			greeProviderApply(provider, 1, successor)
			if got := provider.Snapshot(); got.Units[greeProviderUnitKey(0)].State.Cells[0x01] != 0xd1 || got.Admitted != 2 {
				t.Fatalf("valid successor rejected: %#v", got)
			}
		})
	}
}

// TestGreeVRFProviderFencesZeroSequenceAndNegativeTimeAtGenerationStart
// catches a sequence fence that uses < (it admits sequence zero against the
// reset fence) and a dropped monotonic bound (it admits a negative time).
func TestGreeVRFProviderFencesZeroSequenceAndNegativeTimeAtGenerationStart(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	greeProviderStart(t, provider, greeProviderNewSource(), 1)
	zero := greeProviderFrame(t, 0, greeProviderVector, 0)
	negative := greeProviderFrame(t, 0, greeProviderVector, 1)
	negative.Monotonic = -time.Millisecond
	greeProviderApply(provider, 1, zero)
	greeProviderApply(provider, 1, negative)
	if got := provider.Snapshot(); got.Rejected != 2 || got.HasRaw || len(got.Units) != 0 {
		t.Fatalf("fresh generation admitted a zero sequence or negative time: %#v", got)
	}
	greeProviderApply(provider, 1, greeProviderFrame(t, 0, greeProviderVector, 1))
	if got := provider.Snapshot(); got.Admitted != 1 || got.Units[greeProviderUnitKey(0)].Received != time.Millisecond {
		t.Fatalf("first valid observation rejected: %#v", got)
	}
}

func TestGreeVRFProviderRestartFencesSupersededGeneration(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	predecessor := greeProviderNewSource()
	greeProviderStart(t, provider, predecessor, 1)
	greeProviderSend(predecessor, 1, greeProviderFrame(t, 0, greeProviderVector, 1))
	for _, generation := range []uint64{0, 1} {
		if provider.Start(greeProviderNewSource(), generation) == nil {
			t.Fatalf("generation %d admitted", generation)
		}
	}
	if provider.Start(nil, 2) == nil {
		t.Fatal("nil source admitted")
	}

	late := GreeVRFObservation{Generation: 2, Evidence: greeProviderFrame(t, 5, greeProviderVector, 2)}
	predecessor.late = &late
	successor := greeProviderNewSource()
	greeProviderStart(t, provider, successor, 2)
	got := provider.Snapshot()
	if got.State != "awaiting" || got.Generation != 2 || got.Units[greeProviderUnitKey(0)].Generation != 1 || got.Rejected != 0 {
		t.Fatalf("restart snapshot = %#v", got)
	}
	if _, published := got.Units[greeProviderUnitKey(5)]; published {
		t.Fatal("superseded receive loop published after restart")
	}

	greeProviderSend(successor, 1, greeProviderFrame(t, 0, []byte{0x05, 0xe1, 0xe2}, 7))
	if got := provider.Snapshot(); got.Rejected != 1 || got.Units[greeProviderUnitKey(0)].Generation != 1 || got.Units[greeProviderUnitKey(0)].State.Cells[0x01] != 0xa1 {
		t.Fatalf("superseded generation published: %#v", got)
	}

	greeProviderSend(successor, 2, greeProviderFrame(t, 0, []byte{0x05, 0xc1, 0xc2}, 1))
	unit := provider.Snapshot().Units[greeProviderUnitKey(0)]
	if unit.Generation != 2 || !reflect.DeepEqual(unit.State.Cells, map[uint8]uint8{0x01: 0xc1, 0x02: 0xc2}) {
		t.Fatalf("successor mixed predecessor state: %#v", unit)
	}

	successor.results <- greeProviderResult{err: errors.New("synthetic source loss")}
	<-provider.done
	if got := provider.Snapshot(); got.State != "degraded" || got.Units[greeProviderUnitKey(0)].Generation != 2 {
		t.Fatalf("source loss discarded history or status: %#v", got)
	}
	greeProviderStart(t, provider, greeProviderNewSource(), 3)
	if got := provider.Snapshot(); got.State != "awaiting" || got.Generation != 3 {
		t.Fatalf("restart after loss = %#v", got)
	}
}

func TestGreeVRFProviderStopJoinsAndFencesLateReceive(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	source := greeProviderNewSource()
	greeProviderStart(t, provider, source, 1)
	greeProviderSend(source, 1, greeProviderFrame(t, 0, greeProviderVector, 1))
	late := GreeVRFObservation{Generation: 1, Evidence: greeProviderFrame(t, 0, []byte{0x05, 0xf1, 0xf2}, 2)}
	source.late = &late
	done := provider.done
	provider.Stop()
	select {
	case <-done:
	default:
		t.Fatal("receive loop not joined")
	}
	got := provider.Snapshot()
	if got.State != "stopped" || got.Units[greeProviderUnitKey(0)].State.Cells[0x01] != 0xa1 || got.Admitted != 1 || got.Rejected != 0 {
		t.Fatalf("post-stop receive published: %#v", got)
	}
	provider.Stop()
	if provider.Snapshot().State != "stopped" {
		t.Fatal("repeated stop changed state")
	}
}

func TestGreeVRFProviderBoundsUnitsLeastRecentlyReceived(t *testing.T) {
	provider := greeProviderNew(t, GreeVRFMapM94, 0)
	greeProviderStart(t, provider, greeProviderNewSource(), 1)
	sequence := uint64(0)
	feed := func(unitOffset uint8) {
		sequence++
		greeProviderApply(provider, 1, greeProviderFrame(t, unitOffset, greeProviderVector, sequence))
	}
	for unitOffset := 0; unitOffset <= GreeVRFProviderMaxUnits; unitOffset++ {
		feed(uint8(unitOffset))
	}
	got := provider.Snapshot()
	_, first := got.Units[greeProviderUnitKey(0)]
	_, last := got.Units[greeProviderUnitKey(GreeVRFProviderMaxUnits)]
	if len(got.Units) != GreeVRFProviderMaxUnits || first || !last || got.EvictedUnits != 1 {
		t.Fatalf("bound = %d first = %t last = %t evicted = %d", len(got.Units), first, last, got.EvictedUnits)
	}
	feed(1)
	feed(GreeVRFProviderMaxUnits + 1)
	got = provider.Snapshot()
	_, renewed := got.Units[greeProviderUnitKey(1)]
	_, oldest := got.Units[greeProviderUnitKey(2)]
	if len(got.Units) != GreeVRFProviderMaxUnits || !renewed || oldest || got.EvictedUnits != 2 {
		t.Fatalf("eviction is not least recently received: renewed = %t oldest = %t evicted = %d", renewed, oldest, got.EvictedUnits)
	}
}

func TestGreeVRFProviderConstructorFailsClosed(t *testing.T) {
	identity := greeReplayInterface(t, "can7", 7)
	for _, testCase := range []struct {
		name       string
		identity   canbus.InterfaceIdentity
		mapProfile GreeVRFMapProfile
	}{
		{name: "zero interface", identity: canbus.InterfaceIdentity{}, mapProfile: GreeVRFMapM94},
		{name: "unselected map", identity: identity, mapProfile: 0},
		{name: "unknown map", identity: identity, mapProfile: GreeVRFMapProfile(99)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if provider, err := NewGreeVRFProvider(testCase.identity, testCase.mapProfile, 0); err == nil || provider != nil {
				t.Fatal("invalid selection admitted")
			}
		})
	}
}

func TestGreeVRFProviderHoldsNoTransmitCapability(t *testing.T) {
	providerType := reflect.TypeOf(&GreeVRFProvider{})
	methods := make([]string, 0, providerType.NumMethod())
	for index := 0; index < providerType.NumMethod(); index++ {
		methods = append(methods, providerType.Method(index).Name)
	}
	sort.Strings(methods)
	if !reflect.DeepEqual(methods, []string{"Snapshot", "Start", "Stop"}) {
		t.Fatalf("provider methods = %v", methods)
	}
	sourceType := reflect.TypeOf((*GreeVRFReceiveSource)(nil)).Elem()
	receive := reflect.TypeOf((func(context.Context) (GreeVRFObservation, error))(nil))
	if sourceType.NumMethod() != 1 || sourceType.Method(0).Name != "Receive" || sourceType.Method(0).Type != receive {
		t.Fatalf("receive source grants more than receive: %v", sourceType)
	}
	seen := map[reflect.Type]bool{}
	for _, root := range []reflect.Type{providerType, reflect.TypeOf(GreeVRFProviderSnapshot{}), reflect.TypeOf(GreeVRFObservation{})} {
		greeProviderNoTransmit(t, root, seen)
	}
}

// greeProviderNoTransmit walks every field type reachable from a provider type.
// Only the cancel function and the done channel are behavioral handles; no
// interface, command encoding, foreign transport type or write method is held.
func greeProviderNoTransmit(t *testing.T, current reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[current] {
		return
	}
	seen[current] = true
	canbusPath := reflect.TypeOf(canbus.Frame{}).PkgPath()
	switch {
	case current == reflect.TypeOf(context.CancelFunc(nil)), current == reflect.TypeOf(make(chan struct{})):
		return
	case current == reflect.TypeOf(GreeVRFCommand{}):
		t.Fatalf("provider holds a command encoding: %v", current)
	case current.PkgPath() == canbusPath && current != reflect.TypeOf(canbus.Frame{}) && current != reflect.TypeOf(canbus.Identifier{}) && current != reflect.TypeOf(canbus.InterfaceIdentity{}):
		t.Fatalf("provider holds a transport type: %v", current)
	}
	for index := 0; index < current.NumMethod(); index++ {
		name := strings.ToLower(current.Method(index).Name)
		for _, verb := range []string{"send", "write", "transmit", "encode", "configure"} {
			if strings.Contains(name, verb) {
				t.Fatalf("%v exposes %s", current, current.Method(index).Name)
			}
		}
	}
	switch current.Kind() {
	case reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		t.Fatalf("provider holds a behavioral handle: %v", current)
	case reflect.Pointer, reflect.Slice, reflect.Array:
		greeProviderNoTransmit(t, current.Elem(), seen)
	case reflect.Map:
		greeProviderNoTransmit(t, current.Key(), seen)
		greeProviderNoTransmit(t, current.Elem(), seen)
	case reflect.Struct:
		for index := 0; index < current.NumField(); index++ {
			greeProviderNoTransmit(t, current.Field(index).Type, seen)
		}
	}
}
