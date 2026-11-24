package canbusreg

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGreeVRFRegisterImageVectors(t *testing.T) {
	var rows []struct {
		Name   string
		ID     uint32
		Bits   bool
		Frames []string
		Reads  []struct {
			Coordinate uint16
			Width      int
			Raw        string
			Receipt    int
		}
	}
	data, err := os.ReadFile("testdata/gree_vrf_register_model_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	iface := greeCandidateInterface(t)
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			id, ok := ClassifyGreeVRFIdentifier(mustGreeCandidateID(t, row.ID))
			if !ok {
				t.Fatal("unclassified")
			}
			image := NewGreeVRFRegisterImage(GreeVRFRegisterKey{iface, id.Family, id.Unit, id.Type})
			for index, text := range row.Frames {
				raw, err := hex.DecodeString(text)
				if err != nil {
					t.Fatal(err)
				}
				e := greeCandidateEvidence(t, iface, row.ID, raw)
				e.Monotonic = time.Duration(index+1) * time.Second
				if !image.Apply(e, row.Bits) {
					t.Fatal("rejected fragment")
				}
			}
			for _, read := range row.Reads {
				raw, received, present := image.Read(read.Coordinate, read.Width)
				if present != (read.Receipt != 0) || hex.EncodeToString(raw) != read.Raw || received != time.Duration(read.Receipt)*time.Second {
					t.Errorf("coordinate %x width %d: raw %x received %s present %v", read.Coordinate, read.Width, raw, received, present)
				}
			}
		})
	}
}

func TestGreeVRFRegisterImageKeyFences(t *testing.T) {
	iface := greeCandidateInterface(t)
	other, err := canbus.NewInterfaceIdentity("sim-other", iface.Index())
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := canbus.NewInterfaceIdentity(iface.Name(), iface.Index()+1)
	if err != nil {
		t.Fatal(err)
	}
	key := GreeVRFRegisterKey{iface, GreeVRFData, 0x20, 0x21}
	image := NewGreeVRFRegisterImage(key)
	first := greeCandidateEvidence(t, iface, 0x1effd021, []byte{0, 9})
	if !image.Apply(first, false) {
		t.Fatal("rejected matching key")
	}
	for _, row := range []struct {
		name  string
		iface canbus.InterfaceIdentity
		id    uint32
	}{
		{"interface name", other, 0x1effd021},
		{"interface index", indexed, 0x1effd021},
		{"family", iface, 0x1cffd021},
		{"unit", iface, 0x1effd0a1},
		{"type", iface, 0x1effd020},
	} {
		t.Run(row.name, func(t *testing.T) {
			if image.Apply(greeCandidateEvidence(t, row.iface, row.id, []byte{0, 5}), false) {
				t.Fatal("accepted mismatched key")
			}
			if got, _, ok := image.Read(0, 1); !ok || !reflect.DeepEqual(got, []byte{9}) {
				t.Fatalf("lost last known value: %x", got)
			}
		})
	}
}
