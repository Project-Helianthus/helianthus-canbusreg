//go:build linux

package can

import (
	"bytes"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

func TestGrowattLINKPackedCurrent(t *testing.T) {
	bus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := canbusreg.NewSelection(canbusreg.SelectionSpec{Interface: bus,
		Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006",
		Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02", Group: "2", Slot: "2",
		RouteSet: true, Source: 2, Destination: 0xf4, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := canbusreg.LINKInput{Epoch: 1, Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02", Group: "2", Slot: "2"}
	roles := exampleRoles(t)
	if err := roweval.ValidateTable(linkCurrentRows(), canDescriptors(), roles); err != nil {
		t.Fatal(err)
	}
	// LE64 bits 14–26 are 3900 and 4100 respectively; (code − 4000) / 10 A.
	for _, want := range []struct {
		name, data, amps string
		raw              uint64
	}{
		{"negative", "2321CF0B00000000", "-10", 3900},
		{"positive", "2321010C00000000", "10", 4100},
	} {
		t.Run(want.name, func(t *testing.T) {
			r := receiveVectors(t, wireVector{Identifier: 0x1805f402, Extended: true, DLC: 8, RawDLC: 8, Data: want.data})[0]
			a := acquired(r, roles)
			ev, native, err := evaluateLINKCurrent(r, s, input, signalSlice{14, 13, bitSlice, false}, a)
			if err != nil || ev.Reason != roweval.ReasonNoBinding || ev.Candidate != nil || ev.Subject != nil {
				t.Fatalf("expected native-only current: %+v, %v", ev, err)
			}
			assertNative(t, ev, "current", want.amps, a.Template)
			f := native.Fields["current"]
			if ev.Native.Raw != want.raw || ev.Native.Unit != "unit.ampere" || f.Status != "accepted" || f.Numerator != int64(want.raw)-4000 || f.Denominator != 10 || f.Unit != "A" || f.Grade != "Proven" || native.PhysicalQualified || f.PhysicalQualified {
				t.Fatalf("native current: %+v / %+v", ev.Native, f)
			}
			if native.Selection != s || native.Epoch != input.Epoch || native.Raw != f.Raw || native.Evidence != f.Evidence || !bytes.Equal(f.Evidence.RawRecord[:], r.Raw) || f.Evidence.Interface != r.Interface || f.Evidence.Sequence != r.Sequence || f.Evidence.Monotonic != r.Monotonic {
				t.Fatalf("lost receive evidence: %+v", f)
			}
		})
	}
	assertLINKSelectorNegatives(t, s, input, roles)
}

func assertLINKSelectorNegatives(t *testing.T, s canbusreg.Selection, input canbusreg.LINKInput, roles roweval.Roles) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		change func(*wireVector, *signalSlice)
	}{
		{"identifier", func(f *wireVector, _ *signalSlice) { f.Identifier = 0x1805f403 }},
		{"standard", func(f *wireVector, _ *signalSlice) { f.Extended = false }},
		{"effective-dlc", func(f *wireVector, _ *signalSlice) { f.DLC, f.RawDLC = 7, 7 }},
		{"raw-dlc", func(f *wireVector, _ *signalSlice) { f.RawDLC = 9 }},
		{"rtr", func(f *wireVector, _ *signalSlice) { f.RTR = true }},
		{"offset", func(_ *wireVector, at *signalSlice) { at.offset-- }},
		{"width", func(_ *wireVector, at *signalSlice) { at.width-- }},
		{"order", func(_ *wireVector, at *signalSlice) { at.order = bigEndian }},
		{"signedness", func(_ *wireVector, at *signalSlice) { at.signed = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := wireVector{Identifier: 0x1805f402, Extended: true, DLC: 8, RawDLC: 8, Data: "2321CF0B00000000"}
			slice := signalSlice{14, 13, bitSlice, false}
			tc.change(&frame, &slice)
			selected, placement := s, input
			if tc.name == "identifier" {
				// Keep the alternative source admissible to the native decoder so
				// the example's exact identifier, not a route mismatch, rejects it.
				spec := s.SelectionSpecValue()
				spec.Source, spec.Endpoint, placement.Endpoint = 3, "03", "03"
				var err error
				selected, err = canbusreg.NewSelection(spec)
				if err != nil {
					t.Fatal(err)
				}
			}
			r := receiveVectors(t, frame)[0]
			ev, native, err := evaluateLINKCurrent(r, selected, placement, slice, acquired(r, roles))
			if err != nil || ev.Reason != roweval.ReasonNoMatch || ev.Native != nil || ev.Candidate != nil || !bytes.Equal(native.Evidence.RawRecord[:], r.Raw) {
				t.Fatalf("unexpected evaluation or lost evidence: %+v, %v", ev, err)
			}
			if tc.name == "identifier" && native.Fields["current"].Status != "accepted" {
				t.Fatal("alternate address did not reach selector matching")
			}
		})
	}
	t.Run("multiplexor", func(t *testing.T) {
		table := linkCurrentRows()
		m := uint64(1)
		at := table.Native[0].At
		if at.Multiplexor != nil {
			t.Fatal("LINK fixed report is not multiplexed")
		}
		at.Multiplexor = &m
		ev, err := roweval.Evaluate(table, roweval.Acquired[Selector]{Selector: at, Descriptors: canDescriptors(), Payload: make([]byte, 8)})
		if err != nil || ev.Reason != roweval.ReasonNoMatch || ev.Native != nil {
			t.Fatalf("multiplexed observation selected a fixed row: %+v, %v", ev, err)
		}
	})
}
