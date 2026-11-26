package pcs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

type extendedCommandCase struct {
	Name       string            `json:"name"`
	Args       map[string]uint32 `json:"args"`
	Identifier string            `json:"identifier"`
	Payload    string            `json:"payload"`
}

type extendedCommandVectors struct {
	Time            []extendedCommandCase `json:"time_190b"`
	TimeRefused     []extendedCommandCase `json:"time_190b_refused"`
	Stack           []extendedCommandCase `json:"stack_191a"`
	Secondary       []extendedCommandCase `json:"secondary_191c"`
	Update          []extendedCommandCase `json:"update_191e"`
	RefusedContract []string              `json:"refused_contracts"`
	Incomplete      []string              `json:"incomplete"`
}

func loadExtendedCommandVectors(t *testing.T) extendedCommandVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/commands_190a_1919.json")
	if err != nil {
		t.Fatal(err)
	}
	var v extendedCommandVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func timeFrom(a map[string]uint32) ExtendedTimeInput {
	return ExtendedTimeInput{YearOffset: uint8(a["year_offset"]), Month: uint8(a["month"]), Day: uint8(a["day"]), Hour: uint8(a["hour"]), Minute: uint8(a["minute"]), Second: uint8(a["second"])}
}

func requireExtendedFrame(t *testing.T, c extendedCommandCase, got interface {
	Data() []byte
}, id uint32, extended bool, dlc, rawDLC uint8) {
	t.Helper()
	want, err := hex.DecodeString(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if id != parseIdentifier(t, c.Identifier) || !extended {
		t.Errorf("%s: identifier %#x extended=%v, want extended %s", c.Name, id, extended, c.Identifier)
	}
	if dlc != 8 || rawDLC != 8 {
		t.Errorf("%s: DLC %d raw %d, want 8", c.Name, dlc, rawDLC)
	}
	if !bytes.Equal(got.Data(), want) {
		t.Errorf("%s: data %x, want %x", c.Name, got.Data(), want)
	}
}

func TestExtendedInputsEncodeExactIdentifierAndBytes(t *testing.T) {
	v := loadExtendedCommandVectors(t)
	legacy := ContractLegacyExtended19
	for _, c := range v.Time {
		f, err := EncodeExtendedTimeInput(legacy, timeFrom(c.Args))
		if err != nil {
			t.Fatalf("190B %s: %v", c.Name, err)
		}
		requireExtendedFrame(t, c, f, f.ID().Value(), f.ID().Extended(), f.DLC(), f.RawDLC())
	}
	for _, c := range v.Stack {
		f, err := EncodeStackStateInput(legacy, StackStateInput{CurrentCode: uint16(c.Args["current_code"]), StateOfCharge: uint16(c.Args["state_of_charge"])})
		if err != nil {
			t.Fatalf("191A %s: %v", c.Name, err)
		}
		requireExtendedFrame(t, c, f, f.ID().Value(), f.ID().Extended(), f.DLC(), f.RawDLC())
	}
	for _, c := range v.Secondary {
		f, err := EncodeSecondaryQuantityInput(legacy, SecondaryQuantityInput{Percentage: uint16(c.Args["percentage"]), ByteFour: uint8(c.Args["b4"])})
		if err != nil {
			t.Fatalf("191C %s: %v", c.Name, err)
		}
		requireExtendedFrame(t, c, f, f.ID().Value(), f.ID().Extended(), f.DLC(), f.RawDLC())
	}
	for _, c := range v.Update {
		f, err := EncodeUpdateCoordinationInput(legacy, UpdateCoordinationInput{Parameter: uint8(c.Args["parameter"])})
		if err != nil {
			t.Fatalf("191E %s: %v", c.Name, err)
		}
		requireExtendedFrame(t, c, f, f.ID().Value(), f.ID().Extended(), f.DLC(), f.RawDLC())
	}

	// One argument at a time lands in its own byte and in no other.
	for _, p := range []struct {
		name  string
		set   func(*ExtendedTimeInput, uint8)
		index int
	}{
		{"year offset", func(in *ExtendedTimeInput, x uint8) { in.YearOffset = x }, 0},
		{"month", func(in *ExtendedTimeInput, x uint8) { in.Month = x }, 1},
		{"day", func(in *ExtendedTimeInput, x uint8) { in.Day = x }, 2},
		{"hour", func(in *ExtendedTimeInput, x uint8) { in.Hour = x }, 3},
		{"minute", func(in *ExtendedTimeInput, x uint8) { in.Minute = x }, 4},
		{"second", func(in *ExtendedTimeInput, x uint8) { in.Second = x }, 5},
	} {
		in := ExtendedTimeInput{Month: 1, Day: 1}
		p.set(&in, 7)
		f, err := EncodeExtendedTimeInput(legacy, in)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		want := make([]byte, 8)
		want[1], want[2] = 1, 1
		want[p.index] = 7
		if !bytes.Equal(f.Data(), want) {
			t.Errorf("%s: data %x, want %x", p.name, f.Data(), want)
		}
	}
	// 190B is not 211: the same instant lands on different positions.
	clock, _ := EncodeClockInput(ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30})
	ext, _ := EncodeExtendedTimeInput(legacy, ExtendedTimeInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30})
	if bytes.Equal(clock.Data(), ext.Data()) || clock.ID().Extended() {
		t.Errorf("190B %x equals 211 %x", ext.Data(), clock.Data())
	}
}

func TestExtendedInputsRefuseArgumentsOutsideThePageRanges(t *testing.T) {
	for _, c := range loadExtendedCommandVectors(t).TimeRefused {
		f, err := EncodeExtendedTimeInput(ContractLegacyExtended19, timeFrom(c.Args))
		if err == nil {
			t.Errorf("%s: accepted, frame %x", c.Name, f.Data())
		}
		if f.DLC() != 0 {
			t.Errorf("%s: refused input still produced a frame", c.Name)
		}
	}
	// The page states no range for the words and bytes of 191A, 191C and 191E:
	// the whole representable range is encodable.
	if _, err := EncodeStackStateInput(ContractLegacyExtended19, StackStateInput{CurrentCode: 0xFFFF, StateOfCharge: 0xFFFF}); err != nil {
		t.Error(err)
	}
	if _, err := EncodeSecondaryQuantityInput(ContractLegacyExtended19, SecondaryQuantityInput{Percentage: 0xFFFF, ByteFour: 0xFF}); err != nil {
		t.Error(err)
	}
	if _, err := EncodeUpdateCoordinationInput(ContractLegacyExtended19, UpdateCoordinationInput{Parameter: 0xFF}); err != nil {
		t.Error(err)
	}
}

func TestExtendedInputsRefuseProfilesTheSectionDoesNotAdmit(t *testing.T) {
	for _, name := range loadExtendedCommandVectors(t).RefusedContract {
		contract := ExtendedContract(name)
		if contract == ContractLegacyExtended19 {
			t.Fatal("the admitted contract is listed as refused")
		}
		encoders := map[string]func() (uint8, error){
			"190B": func() (uint8, error) {
				f, err := EncodeExtendedTimeInput(contract, ExtendedTimeInput{Month: 1, Day: 1})
				return f.DLC(), err
			},
			"191A": func() (uint8, error) {
				f, err := EncodeStackStateInput(contract, StackStateInput{1, 2})
				return f.DLC(), err
			},
			"191C": func() (uint8, error) {
				f, err := EncodeSecondaryQuantityInput(contract, SecondaryQuantityInput{1, 2})
				return f.DLC(), err
			},
			"191E": func() (uint8, error) {
				f, err := EncodeUpdateCoordinationInput(contract, UpdateCoordinationInput{1})
				return f.DLC(), err
			},
		}
		for frame, encode := range encoders {
			if dlc, err := encode(); err == nil || dlc != 0 {
				t.Errorf("%s accepted for contract %q (DLC %d)", frame, name, dlc)
			}
		}
	}
	for _, r := range ExtendedCommands() {
		if len(r.Profiles) != 1 || r.Profiles[0] != ContractLegacyExtended19 {
			t.Errorf("%s profiles %v, want only the legacy extended-19 contract", r.ID, r.Profiles)
		}
	}
}

func TestIncompleteExtendedInputsHaveNoCommandRow(t *testing.T) {
	rows := ExtendedCommands()
	have := map[uint32]bool{}
	for _, r := range rows {
		have[r.Identifier] = true
		if r.ID == "" || r.Layout == "" {
			t.Errorf("row %#x has an empty id or layout", r.Identifier)
		}
	}
	for _, id := range []uint32{0x190BDFF2, 0x191ADFF2, 0x191CDFF2, 0x191EDFF2} {
		if !have[id] {
			t.Errorf("no command row for %#x", id)
		}
	}
	if len(rows) != 4 {
		t.Errorf("%d extended command rows, want 4", len(rows))
	}
	want := map[uint32]bool{}
	for _, s := range loadExtendedCommandVectors(t).Incomplete {
		want[parseIdentifier(t, s)] = true
	}
	got := map[uint32]string{}
	for _, g := range IncompleteExtendedInputs() {
		got[g.Identifier] = g.Gap
	}
	if len(got) != len(want) {
		t.Errorf("incomplete inputs %v, want %v", got, want)
	}
	for id := range want {
		if got[id] == "" {
			t.Errorf("no gap for %#x", id)
		}
		for have := range have {
			if have>>16 == id {
				t.Errorf("command row %#x for the incomplete input %#x", have, id)
			}
		}
	}
	// The rows of the other sections are untouched.
	if len(Commands()) != 2 {
		t.Errorf("%d standard command rows, want 2", len(Commands()))
	}
}

func TestExtendedInputsAreNoWayBackOfBoundElements(t *testing.T) {
	for _, b := range append(append(BoundTable().Bindings, BoundExtendedTable().Bindings...), BoundParallelTable().Bindings...) {
		if b.Write != nil {
			t.Errorf("%s has a write row", b.NativeID)
		}
		for _, r := range ExtendedCommands() {
			if b.NativeID == r.ID {
				t.Errorf("%s is bound as %s", b.NativeID, r.ID)
			}
		}
	}
}

func TestExtendedEncodingSendsNothing(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{`"fmt"`: true, `"github.com/Project-Helianthus/helianthus-canbus"`: true}
	for _, imp := range file.Imports {
		if !allowed[imp.Path.Value] {
			t.Errorf("commands.go imports %s", imp.Path.Value)
		}
	}
	a, err := EncodeStackStateInput(ContractLegacyExtended19, StackStateInput{CurrentCode: 1, StateOfCharge: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeStackStateInput(ContractLegacyExtended19, StackStateInput{CurrentCode: 9, StateOfCharge: 9}); err != nil {
		t.Fatal(err)
	}
	b, err := EncodeStackStateInput(ContractLegacyExtended19, StackStateInput{CurrentCode: 1, StateOfCharge: 2})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("repeated encoding differs: %x vs %x", a.Data(), b.Data())
	}
}
