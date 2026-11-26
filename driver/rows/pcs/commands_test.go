package pcs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

type commandVectors struct {
	Clock []struct {
		Name       string           `json:"name"`
		RawArgs    map[string]uint8 `json:"args"`
		Identifier string           `json:"identifier"`
		Payload    string           `json:"payload"`
	} `json:"clock"`
	ClockRefused []struct {
		Name    string           `json:"name"`
		RawArgs map[string]uint8 `json:"args"`
	} `json:"clock_refused"`
	Discovery []struct {
		Name       string           `json:"name"`
		RawArgs    map[string]uint8 `json:"args"`
		Identifier string           `json:"identifier"`
		Payload    string           `json:"payload"`
	} `json:"discovery"`
}

func loadCommandVectors(t *testing.T) commandVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/commands_clock_discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	var v commandVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func clockFrom(a map[string]uint8) ClockInput {
	return ClockInput{YearOffset: a["year_offset"], Month: a["month"], Day: a["day"], Hour: a["hour"], Minute: a["minute"], Second: a["second"]}
}

func parseIdentifier(t *testing.T, s string) uint32 {
	t.Helper()
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return uint32(n)
}

func requireFrame(t *testing.T, name string, frame canbus.Frame, identifier, payload string) {
	t.Helper()
	want, err := hex.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if frame.ID().Value() != parseIdentifier(t, identifier) || frame.ID().Extended() {
		t.Errorf("%s: identifier %#x extended=%v, want standard %s", name, frame.ID().Value(), frame.ID().Extended(), identifier)
	}
	if frame.DLC() != 8 || frame.RawDLC() != 8 {
		t.Errorf("%s: DLC %d raw %d, want 8", name, frame.DLC(), frame.RawDLC())
	}
	if !bytes.Equal(frame.Data(), want) {
		t.Errorf("%s: data %x, want %x", name, frame.Data(), want)
	}
}

func TestClockInputEncodesExactIdentifierAndBytes(t *testing.T) {
	for _, c := range loadCommandVectors(t).Clock {
		frame, err := EncodeClockInput(clockFrom(c.RawArgs))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		requireFrame(t, c.Name, frame, c.Identifier, c.Payload)
	}

	// Each argument lands in its own byte and in no other: one field is set
	// alone, so a swapped or shifted position changes the expected payload.
	positions := []struct {
		name  string
		set   func(*ClockInput, uint8)
		index int
		base  ClockInput
	}{
		{"year offset", func(in *ClockInput, v uint8) { in.YearOffset = v }, 1, ClockInput{Month: 1, Day: 1}},
		{"month", func(in *ClockInput, v uint8) { in.Month = v }, 2, ClockInput{Day: 1}},
		{"day", func(in *ClockInput, v uint8) { in.Day = v }, 3, ClockInput{Month: 1}},
		{"hour", func(in *ClockInput, v uint8) { in.Hour = v }, 4, ClockInput{Month: 1, Day: 1}},
		{"minute", func(in *ClockInput, v uint8) { in.Minute = v }, 5, ClockInput{Month: 1, Day: 1}},
		{"second", func(in *ClockInput, v uint8) { in.Second = v }, 6, ClockInput{Month: 1, Day: 1}},
	}
	for _, p := range positions {
		in := p.base
		p.set(&in, 7)
		frame, err := EncodeClockInput(in)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		want := make([]byte, 8)
		want[2], want[3] = p.base.Month, p.base.Day
		want[p.index] = 7
		if !bytes.Equal(frame.Data(), want) {
			t.Errorf("%s: data %x, want %x", p.name, frame.Data(), want)
		}
		if frame.Data()[0] != 0 || frame.Data()[7] != 0 {
			t.Errorf("%s: unused bytes %x %x, want zero", p.name, frame.Data()[0], frame.Data()[7])
		}
	}
}

func TestClockInputRefusesArgumentsOutsideThePageRanges(t *testing.T) {
	for _, c := range loadCommandVectors(t).ClockRefused {
		frame, err := EncodeClockInput(clockFrom(c.RawArgs))
		if err == nil {
			t.Errorf("%s: accepted, frame %x", c.Name, frame.Data())
		}
		if frame.DLC() != 0 {
			t.Errorf("%s: refused input still produced a frame", c.Name)
		}
	}
}

func TestParallelInputsEncodeExactIdentifierAndBytes(t *testing.T) {
	v := loadCommandVectors(t)
	for _, c := range v.Discovery {
		frame, err := EncodeDiscoveryControlInput(DiscoveryControlInput{Phase: c.RawArgs["phase"], Parameter: c.RawArgs["parameter"]})
		if err != nil {
			t.Fatalf("6F0 %s: %v", c.Name, err)
		}
		requireFrame(t, "6F0 "+c.Name, frame, c.Identifier, c.Payload)
	}

	// 6F0 carries B0 and B1 only; B2 to B7 stay zero whatever the arguments.
	frame, err := EncodeDiscoveryControlInput(DiscoveryControlInput{Phase: 0xFF, Parameter: 0xFF})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Data()[2:], make([]byte, 6)) {
		t.Errorf("6F0 trailing bytes %x, want zero", frame.Data()[2:])
	}
}

func TestIncompleteInputsHaveNoCommandRow(t *testing.T) {
	rows := Commands()
	have := map[uint32]bool{}
	for _, r := range rows {
		have[r.Identifier] = true
		if r.ID == "" || r.Layout == "" {
			t.Errorf("row %#x has an empty id or layout", r.Identifier)
		}
	}
	for _, id := range []uint32{0x211, 0x6F0} {
		if !have[id] {
			t.Errorf("no command row for %#x", id)
		}
	}
	if len(rows) != 2 {
		t.Errorf("%d command rows, want 2", len(rows))
	}
	// 3F7 and 6FF are receive rows; 301, 212 and 37F are the incomplete inputs.
	for _, id := range []uint32{0x301, 0x212, 0x37F, 0x3F7, 0x6FF} {
		if have[id] {
			t.Errorf("command row for %#x", id)
		}
	}
	gaps := map[uint32]string{}
	for _, g := range IncompleteInputs() {
		gaps[g.Identifier] = g.Gap
	}
	if len(gaps) != 3 || gaps[0x301] == "" || gaps[0x212] == "" || gaps[0x37F] == "" {
		t.Errorf("incomplete inputs %v, want exactly 301, 212 and 37F with a gap", gaps)
	}
}

func TestEncodingSendsNothing(t *testing.T) {
	// The encoders take plain arguments and return a frame value: there is no
	// transport, clock, timer or goroutine in the file, so nothing is sent,
	// paced or retried. The import set of the file states that.
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
	// Encoding the same argument twice gives equal frames and no state carries over.
	first, err := EncodeDiscoveryControlInput(DiscoveryControlInput{Phase: 1, Parameter: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeDiscoveryControlInput(DiscoveryControlInput{Phase: 9, Parameter: 9}); err != nil {
		t.Fatal(err)
	}
	second, err := EncodeDiscoveryControlInput(DiscoveryControlInput{Phase: 1, Parameter: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("repeated encoding differs: %x vs %x", first.Data(), second.Data())
	}
}
