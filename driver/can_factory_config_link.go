package driver

import (
	"errors"
	"fmt"

	"github.com/Project-Helianthus/helianthus-canbus"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
)

const (
	coordinatorDirection   = "coordinator-to-module"
	coordinatorSource      = 0xF4
	coordinatorDestination = 0xF5
	coordinatorEndpoint    = "F4"
	coordinatorScope       = "aggregate"
)

// PCSSelection is the caller's explicit PCS selection of one instance. Flavor
// is the PCS flavor; the interface is the instance's own.
type PCSSelection struct {
	Flavor      string `json:"flavor"`
	ProjectCode string `json:"project_code"`
	Release     string `json:"release"`
	Choice      string `json:"choice,omitempty"`
	Direction   string `json:"direction"`
	Scope       string `json:"scope"`
	Bank        string `json:"bank"`
	Endpoint    string `json:"endpoint"`
	Group       string `json:"group,omitempty"`
	Slot        string `json:"slot,omitempty"`
	Epoch       uint64 `json:"epoch"`
}

// LINKSourceSelection is one typed per-source LINK entry. Its endpoint is its
// own source address as two uppercase hexadecimal digits. The coordinator
// route is its own entry: source F4, destination F5, endpoint F4, direction
// coordinator-to-module, scope aggregate, with no group or slot.
type LINKSourceSelection struct {
	PCSFlavor   string `json:"pcs_flavor"`
	ProjectCode string `json:"project_code"`
	Release     string `json:"release"`
	Choice      string `json:"choice,omitempty"`
	Direction   string `json:"direction"`
	Scope       string `json:"scope"`
	Bank        string `json:"bank"`
	Endpoint    string `json:"endpoint"`
	Group       string `json:"group,omitempty"`
	Slot        string `json:"slot,omitempty"`
	RouteSet    bool   `json:"route_set"`
	Destination uint8  `json:"destination"`
	Source      uint8  `json:"source"`
	Epoch       uint64 `json:"epoch"`
}

// LINKEntry is one bound per-source selection, in configured order.
type LINKEntry struct {
	Source      uint8
	Selection   canbusreg.Selection
	Coordinator bool
}

// LINKSelections holds the ordered per-source selections of one instance.
type LINKSelections struct {
	Entries []LINKEntry
}

// Lookup returns the entry configured for a source. A source with no entry
// binds nothing.
func (l LINKSelections) Lookup(source uint8) (LINKEntry, bool) {
	for _, e := range l.Entries {
		if e.Source == source {
			return e, true
		}
	}
	return LINKEntry{}, false
}

// NewPCSSelection passes the PCS selection to canbusreg.NewSelection and
// returns its result unchanged.
func NewPCSSelection(identity canbus.InterfaceIdentity, s PCSSelection) (canbusreg.Selection, error) {
	if s.Flavor == "link" {
		return canbusreg.Selection{}, errors.New("PCS selection: flavor link is the LINK selection")
	}
	return canbusreg.NewSelection(canbusreg.SelectionSpec{
		Interface: identity, Flavor: s.Flavor, ProjectCode: s.ProjectCode, Release: s.Release,
		Choice: s.Choice, Direction: s.Direction, Scope: s.Scope, Bank: s.Bank,
		Endpoint: s.Endpoint, Group: s.Group, Slot: s.Slot, Epoch: s.Epoch,
	})
}

func (e LINKSourceSelection) spec(identity canbus.InterfaceIdentity) canbusreg.SelectionSpec {
	return canbusreg.SelectionSpec{
		Interface: identity, Flavor: "link", PCSFlavor: e.PCSFlavor, ProjectCode: e.ProjectCode,
		Release: e.Release, Choice: e.Choice, Direction: e.Direction, Scope: e.Scope, Bank: e.Bank,
		Endpoint: e.Endpoint, Group: e.Group, Slot: e.Slot, RouteSet: e.RouteSet,
		Destination: e.Destination, Source: e.Source, Epoch: e.Epoch,
	}
}

// NewLINKSelections validates the ordered per-source list and builds one
// canbusreg.NewSelection result per entry, in the configured order. Any
// invalid configuration is reported, naming the entry, before any source binds.
func NewLINKSelections(identity canbus.InterfaceIdentity, entries []LINKSourceSelection) (LINKSelections, error) {
	seen := map[uint8]int{}
	for i, e := range entries {
		if first, dup := seen[e.Source]; dup {
			return LINKSelections{}, fmt.Errorf("%s: source duplicates link entry %d", linkName(i, e), first)
		}
		seen[e.Source] = i
	}
	out := make([]LINKEntry, 0, len(entries))
	for i, e := range entries {
		selection, err := canbusreg.NewSelection(e.spec(identity))
		if err != nil {
			return LINKSelections{}, fmt.Errorf("%s: %w", linkName(i, e), err)
		}
		if want := fmt.Sprintf("%02X", e.Source); e.Endpoint != want {
			return LINKSelections{}, fmt.Errorf("%s: endpoint %q must equal its own source %q", linkName(i, e), e.Endpoint, want)
		}
		coordinator := e.Direction == coordinatorDirection
		if coordinator {
			if e.Source != coordinatorSource || e.Destination != coordinatorDestination || e.Endpoint != coordinatorEndpoint ||
				e.Scope != coordinatorScope || e.Group != "" || e.Slot != "" {
				return LINKSelections{}, fmt.Errorf("%s: coordinator route requires source F4, destination F5, endpoint F4, scope aggregate and no group or slot", linkName(i, e))
			}
		}
		out = append(out, LINKEntry{Source: e.Source, Selection: selection, Coordinator: coordinator})
	}
	var module *LINKSourceSelection
	var moduleIndex int
	for i := range entries {
		if out[i].Coordinator {
			continue
		}
		if module == nil {
			module, moduleIndex = &entries[i], i
			continue
		}
		if err := sharedLINKMembers(moduleIndex, *module, i, entries[i]); err != nil {
			return LINKSelections{}, err
		}
	}
	if module != nil {
		for i, e := range entries {
			if out[i].Coordinator && e.Epoch != module.Epoch {
				return LINKSelections{}, fmt.Errorf("%s: epoch %d differs from link entry %d epoch %d", linkName(i, e), e.Epoch, moduleIndex, module.Epoch)
			}
		}
	}
	return LINKSelections{Entries: out}, nil
}

func sharedLINKMembers(ai int, a LINKSourceSelection, bi int, b LINKSourceSelection) error {
	for _, m := range []struct {
		name string
		same bool
	}{
		{"pcs_flavor", a.PCSFlavor == b.PCSFlavor},
		{"direction", a.Direction == b.Direction},
		{"scope", a.Scope == b.Scope},
		{"bank", a.Bank == b.Bank},
		{"route_set", a.RouteSet == b.RouteSet},
		{"destination", a.Destination == b.Destination},
		{"epoch", a.Epoch == b.Epoch},
	} {
		if !m.same {
			return fmt.Errorf("%s: shared member %s differs from link entry %d", linkName(bi, b), m.name, ai)
		}
	}
	return nil
}

func linkName(index int, e LINKSourceSelection) string {
	return fmt.Sprintf("link entry %d (source %02X)", index, e.Source)
}
