//go:build linux

package can

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

type pcsSignalVector struct {
	FirstByte uint16 `json:"first_byte"`
	LastByte  uint16 `json:"last_byte"`
	Counts    int64
	Volts     string
}

type pcsFrozenField struct {
	Numerator, Denominator       int64
	Unit, Meaning, Grade, Status string
	PhysicalQualified            bool `json:"physical_qualified"`
}

type pcsFrozenResult struct {
	Frame                         wireVector
	Status, Scope, Bank, Endpoint string
	Group, Slot                   string
	Epoch, Sequence               uint64
	InterfaceName                 string `json:"interface_name"`
	InterfaceIndex                int    `json:"interface_index"`
	SocketCAN                     string `json:"socketcan_le_hex"`
	PhysicalQualified             bool   `json:"physical_qualified"`
	Fields                        map[string]pcsFrozenField
}

func comparePCSFrozen(t *testing.T, got canbusreg.NativeResult, r receiver.Record, s canbusreg.Selection, want pcsFrozenResult) {
	t.Helper()
	raw, err := hex.DecodeString(want.SocketCAN)
	if err != nil || len(raw) != canbus.SocketCANRecordSize {
		t.Fatalf("bad frozen wire evidence: %q (%v)", want.SocketCAN, err)
	}
	binary.NativeEndian.PutUint32(raw, binary.LittleEndian.Uint32(raw))
	frame := got.Evidence.Frame
	if !bytes.Equal(raw, want.Frame.record(t)) || !bytes.Equal(raw, got.Evidence.RawRecord[:]) || !bytes.Equal(raw, r.Raw) ||
		frame.ID().Value() != want.Frame.Identifier || frame.ID().Extended() != want.Frame.Extended ||
		frame.DLC() != want.Frame.DLC || frame.RawDLC() != want.Frame.RawDLC || !bytes.Equal(frame.Data(), raw[8:]) ||
		!bytes.Equal(got.Raw[:], raw[8:]) || got.Evidence.Interface.Name() != want.InterfaceName || got.Evidence.Interface.Index() != want.InterfaceIndex ||
		got.Evidence.Interface != r.Interface || got.Evidence.Sequence != want.Sequence || got.Evidence.Sequence != r.Sequence || got.Evidence.Monotonic != r.Monotonic {
		t.Fatalf("frozen and received evidence differ: %+v", got.Evidence)
	}
	if got.Selection != s || got.Status != want.Status || got.Epoch != want.Epoch || got.PhysicalQualified != want.PhysicalQualified || len(got.Fields) != len(want.Fields) {
		t.Fatalf("frozen native disposition differs: %+v", got)
	}
	for name, f := range want.Fields {
		actual, ok := got.Fields[name]
		if !ok || (pcsFrozenField{actual.Numerator, actual.Denominator, actual.Unit, actual.Meaning, actual.Grade, actual.Status, actual.PhysicalQualified}) != f ||
			actual.Raw != got.Raw || actual.Evidence != got.Evidence || actual.Epoch != want.Epoch || actual.Scope != want.Scope ||
			actual.Bank != want.Bank || actual.Endpoint != want.Endpoint || actual.Group != want.Group || actual.Slot != want.Slot || actual.Age != 0 {
			t.Fatalf("frozen native %s: got %+v, want %+v", name, actual, f)
		}
	}
}

func TestGrowattPCSAddressedPackVoltage(t *testing.T) {
	path := "testdata/growatt_pcs_addressed_voltage.json"
	v := readFixture[struct {
		wireVector
		Source, Destination                                             uint8
		Direction, Project, Release, Scope, Bank, Endpoint, Group, Slot string
		Pack, Port                                                      pcsSignalVector
		ByteOrder                                                       string `json:"byte_order"`
	}](t, path)
	operand, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("binding operand SHA-256 %x", sha256.Sum256(operand))
	frozen := readFixture[pcsFrozenResult](t, "testdata/pcs-addressed-voltage-native.json")
	roles := exampleRoles(t)
	table := pcsVoltageRows()
	if err := roweval.ValidateTable(table, canDescriptors(), roles); err != nil {
		t.Fatal(err)
	}
	packSlice := signalSlice{v.Pack.FirstByte * 8, (v.Pack.LastByte - v.Pack.FirstByte + 1) * 8, v.ByteOrder, false}
	portSlice := signalSlice{v.Port.FirstByte * 8, (v.Port.LastByte - v.Port.FirstByte + 1) * 8, v.ByteOrder, false}
	selectedBus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	spec := canbusreg.SelectionSpec{Interface: selectedBus, Flavor: "pcs-extended19", ProjectCode: v.Project, Release: v.Release, Direction: v.Direction, Scope: v.Scope, Bank: v.Bank, Endpoint: v.Endpoint, Group: v.Group, Slot: v.Slot, RouteSet: true, Destination: v.Destination, Source: v.Source, Epoch: 1}
	input := canbusreg.LINKInput{Epoch: 1, Direction: v.Direction, Scope: v.Scope, Bank: v.Bank, Endpoint: v.Endpoint, Group: v.Group, Slot: v.Slot}
	t.Run(v.Name, func(t *testing.T) {
		r := receiveVectors(t, v.wireVector)[0]
		a := acquired(r, roles)
		s, err := canbusreg.NewSelection(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []struct {
			name    string
			operand pcsSignalVector
			slice   signalSlice
		}{
			{"pack_voltage", v.Pack, packSlice}, {"port_voltage", v.Port, portSlice},
		} {
			ev, native, err := evaluatePCSVoltage(r, s, input, field.slice, a)
			if err != nil {
				t.Fatal(err)
			}
			assertNative(t, ev, field.name, field.operand.Volts, a.Template)
			comparePCSFrozen(t, native, r, s, frozen)
			f := frozen.Fields[field.name]
			if f.Numerator != field.operand.Counts || f.Denominator != 10 || uint64(f.Numerator) != ev.Native.Raw {
				t.Fatalf("frozen %s slice differs from the independent operand: %+v", field.name, f)
			}
			if field.name == "pack_voltage" {
				assertFact(t, ev, field.name, field.operand.Volts, a)
			} else if ev.Candidate != nil || ev.Subject != nil || ev.Reason != roweval.ReasonNoBinding {
				t.Fatal("port voltage entered the binding", ev)
			}
			t.Logf("%s: %d/%d %s (%s), raw %s", field.name, f.Numerator, f.Denominator, f.Unit, f.Grade, frozen.SocketCAN)
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*wireVector, *canbusreg.SelectionSpec, *canbusreg.LINKInput, *signalSlice)
	}{
		{"physical-direction", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) {
			i.Direction = "pcs-to-bms"
		}},
		{"selected-source", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) { s.Source = 3 }},
		{"selected-destination", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Destination = 0xF3
		}},
		{"route-not-applied", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.RouteSet = false
		}},
		{"other-addressed-source", func(f *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			f.Identifier++
			s.Source++
		}},
		{"other-message-kind", func(f *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			f.Identifier += 1 << 16
		}},
		{"standard", func(f *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			f.Extended = false
		}},
		{"rtr", func(f *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) { f.RTR = true }},
		{"effective-dlc", func(f *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			f.DLC, f.RawDLC = 7, 7
		}},
		{"raw-dlc", func(f *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) { f.RawDLC = 9 }},
		{"interface-name", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
		}},
		{"interface-index", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
		}},
		{"epoch", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) { i.Epoch++ }},
		{"scope", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) {
			i.Scope = "aggregate"
		}},
		{"bank", func(_ *wireVector, s *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) {
			s.Bank = "parallel"
			i.Bank = s.Bank
		}},
		{"endpoint", func(_ *wireVector, s *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) {
			s.Endpoint = "coordinator"
			i.Endpoint = s.Endpoint
		}},
		{"group", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) { i.Group = "3" }},
		{"slot", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) { i.Slot = "3" }},
		{"age", func(_ *wireVector, _ *canbusreg.SelectionSpec, i *canbusreg.LINKInput, _ *signalSlice) {
			i.FieldAge = -1
		}},
		{"project", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.ProjectCode = "QCAA"
		}},
		{"release", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Release = "0007"
		}},
		{"flavor", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Flavor = "pcs-standard"
		}},
		{"choice", func(_ *wireVector, s *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, _ *signalSlice) {
			s.Choice = "release11"
		}},
		{"byte-order", func(_ *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, f *signalSlice) {
			f.order = "big-endian"
		}},
		{"slice-offset", func(_ *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, f *signalSlice) { f.offset = 16 }},
		{"slice-width", func(_ *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, f *signalSlice) { f.width = 8 }},
		{"slice-sign", func(_ *wireVector, _ *canbusreg.SelectionSpec, _ *canbusreg.LINKInput, f *signalSlice) {
			f.signed = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, selection, placement, slice := v.wireVector, spec, input, packSlice
			tc.change(&frame, &selection, &placement, &slice)
			selected, err := canbusreg.NewSelection(selection)
			if err != nil {
				t.Fatal(err)
			}
			r := receiveVectors(t, frame)[0]
			ev, native, err := evaluatePCSVoltage(r, selected, placement, slice, acquired(r, roles))
			if err != nil || ev.Reason != roweval.ReasonNoMatch || ev.Candidate != nil || ev.Native != nil || ev.Subject != nil || fmt.Sprintf("%x", native.Evidence.RawRecord) != fmt.Sprintf("%x", r.Raw) {
				t.Fatalf("negative bound or lost the wire evidence: %+v %v", ev, err)
			}
		})
	}
	assertNoWrite(t, table, "pack_voltage", roweval.ReasonNoWrite)
	assertNoWrite(t, table, "port_voltage", roweval.ReasonNoMatch)
}
