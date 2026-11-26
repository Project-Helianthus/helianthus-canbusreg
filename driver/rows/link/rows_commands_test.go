package link

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
)

type clockVector struct {
	Name string `json:"name"`
	Args struct {
		Destination uint8  `json:"destination"`
		Source      uint8  `json:"source"`
		Year        uint16 `json:"year"`
		Month       uint8  `json:"month"`
		Day         uint8  `json:"day"`
		Hour        uint8  `json:"hour"`
		Minute      uint8  `json:"minute"`
		Second      uint8  `json:"second"`
		Extra       uint8  `json:"extra"`
	} `json:"args"`
	Identifier string `json:"identifier"`
	Payload    string `json:"payload"`
}

func loadClockVectors(t *testing.T) []clockVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/commands_188a.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Clock []clockVector `json:"clock"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Clock
}

func clockOf(v clockVector) ClockInput {
	a := v.Args
	return ClockInput{Destination: a.Destination, Source: a.Source, Year: a.Year, Month: a.Month, Day: a.Day, Hour: a.Hour, Minute: a.Minute, Second: a.Second, Extra: a.Extra}
}

func TestClockInputEncodesExactIdentifierAndBytes(t *testing.T) {
	for _, v := range loadClockVectors(t) {
		frame, err := EncodeClockInput(clockOf(v))
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(v.Identifier, "0x"), 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		want, err := hex.DecodeString(v.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if frame.ID().Value() != uint32(id) || !frame.ID().Extended() {
			t.Errorf("%s: identifier %#x extended=%v, want extended %s", v.Name, frame.ID().Value(), frame.ID().Extended(), v.Identifier)
		}
		if frame.DLC() != 8 || frame.RawDLC() != 8 {
			t.Errorf("%s: DLC %d raw %d, want 8", v.Name, frame.DLC(), frame.RawDLC())
		}
		if !bytes.Equal(frame.Data(), want) {
			t.Errorf("%s: data %x, want %x", v.Name, frame.Data(), want)
		}
	}
}

func TestClockInputPlacesEachArgumentInItsOwnPosition(t *testing.T) {
	cases := []struct {
		name string
		in   ClockInput
		id   uint32
		data string
	}{
		{"destination", ClockInput{Destination: 0xAB}, 0x188AAB00, "0000000000000000"},
		{"source", ClockInput{Source: 0xCD}, 0x188A00CD, "0000000000000000"},
		{"year low byte", ClockInput{Year: 0x00FE}, 0x188A0000, "fe00000000000000"},
		{"year high byte", ClockInput{Year: 0xFE00}, 0x188A0000, "00fe000000000000"},
		{"month", ClockInput{Month: 9}, 0x188A0000, "0000090000000000"},
		{"day", ClockInput{Day: 9}, 0x188A0000, "0000000900000000"},
		{"hour", ClockInput{Hour: 9}, 0x188A0000, "0000000009000000"},
		{"minute", ClockInput{Minute: 9}, 0x188A0000, "0000000000090000"},
		{"second", ClockInput{Second: 9}, 0x188A0000, "0000000000000900"},
		{"extra", ClockInput{Extra: 9}, 0x188A0000, "0000000000000009"},
	}
	for _, c := range cases {
		frame, err := EncodeClockInput(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		want, _ := hex.DecodeString(c.data)
		if frame.ID().Value() != c.id || !bytes.Equal(frame.Data(), want) {
			t.Errorf("%s: id %#x data %x, want %#x %x", c.name, frame.ID().Value(), frame.Data(), c.id, want)
		}
	}
}

func TestClockInputAppliesNoRangeOfItsOwn(t *testing.T) {
	frame, err := EncodeClockInput(ClockInput{Month: 0, Day: 0, Hour: 99, Minute: 99, Second: 99})
	if err != nil {
		t.Fatalf("value the page states no range for was refused: %v", err)
	}
	if frame.Data()[4] != 99 {
		t.Errorf("hour %d, want 99", frame.Data()[4])
	}
}

func TestOnlyTheClockFrameHasACommandRow(t *testing.T) {
	rows := Commands()
	if len(rows) != 1 || rows[0].Family != 0x188A || rows[0].ID == "" || rows[0].Layout == "" {
		t.Fatalf("command rows %v, want exactly the 188A row", rows)
	}
	listed := map[string]string{}
	for _, f := range UncoveredFamilies() {
		if f.Gap == "" || (f.Class != "C" && f.Class != "Q") {
			t.Errorf("family %s has class %q gap %q", f.Family, f.Class, f.Gap)
		}
		listed[f.Family] = f.Class
	}
	for _, want := range []string{"1860DFF4", "1870-1889", "188B", "188C", "18A0", "18FD", "18DF", "1894", "1899", "1892", "18FC", "18F0"} {
		if _, ok := listed[want]; !ok {
			t.Errorf("family %s is not listed", want)
		}
	}
	if _, ok := listed["188A"]; ok {
		t.Error("188A is listed as uncovered")
	}
}

func TestEncodingSendsNothing(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		if imp.Path.Value != `"github.com/Project-Helianthus/helianthus-canbus"` {
			t.Errorf("commands.go imports %s", imp.Path.Value)
		}
	}
	in := ClockInput{Destination: 1, Source: 2, Year: 2026, Month: 1, Day: 1}
	first, _ := EncodeClockInput(in)
	_, _ = EncodeClockInput(ClockInput{Year: 1})
	second, _ := EncodeClockInput(in)
	if first != second {
		t.Errorf("repeated encoding differs: %x vs %x", first.Data(), second.Data())
	}
}
