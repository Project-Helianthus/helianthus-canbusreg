package driver

import (
	"errors"
	"strings"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
)

func linkIdentity(t *testing.T) canbus.InterfaceIdentity {
	t.Helper()
	id, err := canbus.NewInterfaceIdentity("can0", 3)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func moduleEntry(source uint8, endpoint, project, release, choice string) LINKSourceSelection {
	return LINKSourceSelection{
		PCSFlavor: "pcs-standard", ProjectCode: project, Release: release, Choice: choice,
		Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: endpoint,
		RouteSet: true, Destination: 0xF4, Source: source, Epoch: 7,
	}
}

func twoSources() []LINKSourceSelection {
	return []LINKSourceSelection{
		moduleEntry(1, "01", "QAAA", "0005", "legacy19"),
		moduleEntry(2, "02", "QAAa", "9011", "release11"),
	}
}

func coordinatorEntry() LINKSourceSelection {
	return LINKSourceSelection{
		PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0005", Choice: "legacy19",
		Direction: "coordinator-to-module", Scope: "aggregate", Bank: "ordinary", Endpoint: "F4",
		RouteSet: true, Destination: 0xF5, Source: 0xF4, Epoch: 7,
	}
}

func TestLINKSelectionsOrderedPerSource(t *testing.T) {
	id := linkIdentity(t)
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		in := twoSources()
		in = []LINKSourceSelection{in[order[0]], in[order[1]]}
		got, err := NewLINKSelections(id, in)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range got.Entries {
			if e.Source != in[i].Source {
				t.Fatalf("entry %d source %d, want %d", i, e.Source, in[i].Source)
			}
		}
		want := map[uint8]string{1: "L1", 2: "L8"}
		for src, group := range want {
			e, ok := got.Lookup(src)
			if !ok || e.Selection.SelectionGroup() != group || e.Selection.SelectionSpecValue().Endpoint != map[uint8]string{1: "01", 2: "02"}[src] {
				t.Fatalf("source %d: %+v ok=%v", src, e, ok)
			}
		}
		if _, ok := got.Lookup(3); ok {
			t.Fatal("unselected source bound")
		}
	}
}

func TestLINKSelectionsKeepNewSelectionResult(t *testing.T) {
	id := linkIdentity(t)
	in := []LINKSourceSelection{moduleEntry(1, "01", "ZZZZ", "1234", "")}
	got, err := NewLINKSelections(id, in)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canbusreg.NewSelection(in[0].spec(id))
	if err != nil || got.Entries[0].Selection != want || want.SelectionGroup() != "" {
		t.Fatalf("unknown identity must stay unresolved without error: %+v %v", got, err)
	}
	in[0].RouteSet = false
	_, err = NewLINKSelections(id, in)
	_, wantErr := canbusreg.NewSelection(in[0].spec(id))
	if err == nil || !errors.Is(err, errors.Unwrap(err)) || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("missing route: %v", err)
	}
}

func TestLINKSelectionsRejections(t *testing.T) {
	id := linkIdentity(t)
	cases := []struct {
		name   string
		mutate func([]LINKSourceSelection) []LINKSourceSelection
		want   string
	}{
		{"endpoint differs from source", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Endpoint = "01"; return e }, "must equal its own source"},
		{"lowercase endpoint", func(e []LINKSourceSelection) []LINKSourceSelection {
			e[0] = moduleEntry(0x0A, "0a", "QAAA", "0005", "legacy19")
			return e
		}, "uppercase hexadecimal"},
		{"FF endpoint", func(e []LINKSourceSelection) []LINKSourceSelection {
			e[1] = moduleEntry(0xFF, "FF", "QAAa", "9011", "release11")
			return e
		}, "other than FF"},
		{"synthetic endpoint", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Endpoint = "synthetic"; return e }, "uppercase hexadecimal"},
		{"group 8", func(e []LINKSourceSelection) []LINKSourceSelection { e[0].Group = "8"; return e }, "0 to 7"},
		{"slot 256", func(e []LINKSourceSelection) []LINKSourceSelection { e[0].Slot = "256"; return e }, "0 to 255"},
		{"duplicate source other release", func(e []LINKSourceSelection) []LINKSourceSelection {
			e[1] = moduleEntry(1, "01", "QAAA", "0006", "")
			return e
		}, "duplicates"},
		{"pcs flavor differs", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].PCSFlavor = "pcs-extended19"; return e }, "pcs_flavor"},
		{"direction differs", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Direction = "bms-to-pcs"; return e }, "direction"},
		{"scope differs", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Scope = "aggregate"; return e }, "scope"},
		{"bank differs", func(e []LINKSourceSelection) []LINKSourceSelection {
			e[1].PCSFlavor, e[0].PCSFlavor = "pcs-parallel", "pcs-parallel"
			e[1].Bank = "parallel"
			return e
		}, "bank"},
		{"destination differs", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Destination = 0xF5; return e }, "destination"},
		{"epoch differs", func(e []LINKSourceSelection) []LINKSourceSelection { e[1].Epoch = 8; return e }, "epoch"},
	}
	for _, c := range cases {
		got, err := NewLINKSelections(id, c.mutate(twoSources()))
		if err == nil || !strings.Contains(err.Error(), c.want) || len(got.Entries) != 0 {
			t.Errorf("%s: err=%v entries=%d, want error containing %q", c.name, err, len(got.Entries), c.want)
		}
	}
}

func TestLINKSelectionsAcceptGroupSlotPerSource(t *testing.T) {
	id := linkIdentity(t)
	in := twoSources()
	in[0].Group, in[0].Slot = "0", "0"
	in[1].Group, in[1].Slot = "7", "255"
	got, err := NewLINKSelections(id, in)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.Entries[1].Selection.SelectionSpecValue(); s.Group != "7" || s.Slot != "255" {
		t.Fatalf("%+v", s)
	}
	if s := got.Entries[0].Selection.SelectionSpecValue(); s.Group != "0" || s.Slot != "0" {
		t.Fatalf("%+v", s)
	}
}

func TestLINKCoordinatorRouteSeparateEntry(t *testing.T) {
	id := linkIdentity(t)
	got, err := NewLINKSelections(id, append(twoSources(), coordinatorEntry()))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got.Lookup(0xF4)
	if !ok || !e.Coordinator || e.Selection.SelectionGroup() != "" {
		t.Fatalf("%+v %v", e, ok)
	}
	if m, _ := got.Lookup(1); m.Coordinator || m.Selection.SelectionGroup() != "L1" {
		t.Fatalf("module changed: %+v", m)
	}
	mutations := map[string]func(*LINKSourceSelection){
		"destination": func(e *LINKSourceSelection) { e.Destination = 0xF4 },
		"scope":       func(e *LINKSourceSelection) { e.Scope = "local" },
		"group":       func(e *LINKSourceSelection) { e.Group = "1" },
		"slot":        func(e *LINKSourceSelection) { e.Slot = "1" },
		"epoch":       func(e *LINKSourceSelection) { e.Epoch = 9 },
		"source":      func(e *LINKSourceSelection) { e.Source, e.Endpoint = 0xF3, "F3" },
	}
	for name, mutate := range mutations {
		c := coordinatorEntry()
		mutate(&c)
		if _, err := NewLINKSelections(id, append(twoSources(), c)); err == nil {
			t.Errorf("%s: coordinator route accepted", name)
		}
	}
	if _, err := NewLINKSelections(id, append(twoSources(), coordinatorEntry(), moduleEntry(0xF4, "F4", "QAAA", "0005", "legacy19"))); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("module and coordinator sharing F4: %v", err)
	}
	dup := coordinatorEntry()
	dup.Source = 1
	dup.Endpoint = "01"
	if _, err := NewLINKSelections(id, append(twoSources(), dup)); err == nil {
		t.Error("coordinator sharing a module source accepted")
	}
}

func pcsSelection() PCSSelection {
	return PCSSelection{
		Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0005", Direction: "bms-to-pcs",
		Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Epoch: 4,
	}
}

func TestPCSSelectionDocumentedValues(t *testing.T) {
	id := linkIdentity(t)
	for _, endpoint := range []string{"coordinator", "participant", "gateway"} {
		s := pcsSelection()
		s.Endpoint = endpoint
		got, err := NewPCSSelection(id, s)
		if err != nil || got.SelectionGroup() != "L1" {
			t.Fatalf("%s: %v %q", endpoint, err, got.SelectionGroup())
		}
	}
	for _, g := range []string{"", "0", "7"} {
		for _, sl := range []string{"", "0", "255"} {
			s := pcsSelection()
			s.Group, s.Slot = g, sl
			got, err := NewPCSSelection(id, s)
			if err != nil || got.SelectionSpecValue().Group != g || got.SelectionSpecValue().Slot != sl {
				t.Fatalf("group %q slot %q: %v", g, sl, err)
			}
		}
	}
	for _, flavor := range []string{"pcs-standard", "pcs-extended19", "pcs-parallel"} {
		s := pcsSelection()
		s.Flavor = flavor
		s.Bank = map[string]string{"pcs-parallel": "parallel"}[flavor]
		if s.Bank == "" {
			s.Bank = "ordinary"
		}
		if _, err := NewPCSSelection(id, s); err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
	}
}

func TestPCSSelectionInvalid(t *testing.T) {
	id := linkIdentity(t)
	cases := map[string]struct {
		mutate func(*PCSSelection)
		want   string
	}{
		"synthetic": {func(s *PCSSelection) { s.Endpoint = "synthetic" }, "coordinator, participant or gateway"},
		"group":     {func(s *PCSSelection) { s.Endpoint = "group" }, "coordinator, participant or gateway"},
		"slot":      {func(s *PCSSelection) { s.Endpoint = "slot" }, "coordinator, participant or gateway"},
		"FF":        {func(s *PCSSelection) { s.Endpoint = "FF" }, "coordinator, participant or gateway"},
		"group 8":   {func(s *PCSSelection) { s.Group = "8" }, "0 to 7"},
		"slot 256":  {func(s *PCSSelection) { s.Slot = "256" }, "0 to 255"},
		"bank pair": {func(s *PCSSelection) { s.Bank = "parallel" }, canbusreg.ErrSelectionBankPairing.Error()},
		"link":      {func(s *PCSSelection) { s.Flavor = "link" }, "LINK selection"},
	}
	for name, c := range cases {
		s := pcsSelection()
		c.mutate(&s)
		if _, err := NewPCSSelection(id, s); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	s := pcsSelection()
	s.Bank = "parallel"
	if _, err := NewPCSSelection(id, s); !errors.Is(err, canbusreg.ErrSelectionBankPairing) {
		t.Errorf("pairing error not preserved: %v", err)
	}
}

func TestPCSSelectionUnresolvedWithoutError(t *testing.T) {
	id := linkIdentity(t)
	for name, mutate := range map[string]func(*PCSSelection){
		"direction":     func(s *PCSSelection) { s.Direction = "module-to-coordinator" },
		"flavor":        func(s *PCSSelection) { s.Flavor = "pcs-other" },
		"unknown ident": func(s *PCSSelection) { s.ProjectCode, s.Release = "ZZZZ", "1234" },
		"incompatible":  func(s *PCSSelection) { s.Flavor, s.Bank = "pcs-parallel", "parallel" },
	} {
		s := pcsSelection()
		mutate(&s)
		got, err := NewPCSSelection(id, s)
		if err != nil || got.SelectionGroup() != "" {
			t.Errorf("%s: %v %q", name, err, got.SelectionGroup())
		}
	}
}

func TestLINKDirectionAndFlavorUnresolved(t *testing.T) {
	id := linkIdentity(t)
	for name, mutate := range map[string]func(*LINKSourceSelection){
		"direction": func(e *LINKSourceSelection) { e.Direction = "bms-to-pcs" },
		"flavor":    func(e *LINKSourceSelection) { e.PCSFlavor = "other" },
	} {
		e := moduleEntry(1, "01", "QAAA", "0005", "legacy19")
		mutate(&e)
		got, err := NewLINKSelections(id, []LINKSourceSelection{e})
		if err != nil || got.Entries[0].Selection.SelectionGroup() != "" {
			t.Errorf("%s: %v", name, err)
		}
	}
}
