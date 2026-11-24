package canbusreg

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestGreeVRFDictionaryRegeneratesInMemory(t *testing.T) {
	for domain, compiled := range greeVRFDictionaryShards {
		t.Run(domain, func(t *testing.T) {
			data, err := os.ReadFile("driverdata/gree/dictionary/" + domain + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var slice struct {
				Slice   string
				Entries []map[string]any
			}
			if err := json.Unmarshal(data, &slice); err != nil {
				t.Fatal(err)
			}
			if slice.Slice != domain || len(slice.Entries) != len(compiled) {
				t.Fatal("dictionary domain or row count drift")
			}
			for j, row := range slice.Entries {
				var out bytes.Buffer
				encoder := json.NewEncoder(&out)
				encoder.SetEscapeHTML(false)
				if err := encoder.Encode(row); err != nil {
					t.Fatal(err)
				}
				if string(bytes.TrimSuffix(out.Bytes(), []byte{'\n'})) != compiled[j] {
					t.Fatalf("row %d drifted", j)
				}
			}
		})
	}
}

func TestGreeVRFDictionaryTypedPointMarkersAndPresence(t *testing.T) {
	iface := greeCandidateInterface(t)
	image := NewGreeVRFRegisterImage(GreeVRFRegisterKey{iface, GreeVRFData, 0x20, 0x71})
	if !image.Apply(greeCandidateEvidence(t, iface, 0x1effd071, []byte{0, 1, 0, 5}), false) ||
		!image.Apply(greeCandidateEvidence(t, iface, 0x1effd071, []byte{0x15, 0xff}), false) {
		t.Fatal("apply settings")
	}
	points := image.TypedPoints()
	seen := make(map[uint16]bool)
	for _, p := range points {
		switch p.Row.Coordinate {
		case 0:
			if !p.Present || p.Row.Label != "operating mode setting" || p.EnumerationLabel != "Cooling" || p.Value.Numerator != 1 || p.Value.Denominator != 1 {
				t.Fatal(p)
			}
		case 2:
			if !p.Present || p.Row.Conflict == "" {
				t.Fatal(p)
			}
		case 0x15:
			if !p.Present || !p.Row.LabelOnly || p.Row.Unit != "" || p.Value.Status != GreeVRFValueRaw || !bytes.Equal(p.Value.Raw, []byte{0xff}) {
				t.Fatal(p)
			}
		case 0x20:
			if p.Present || p.Value.Raw != nil {
				t.Fatal(p)
			}
		default:
			continue
		}
		seen[p.Row.Coordinate] = true
	}
	if len(seen) != 4 {
		t.Fatal(seen)
	}
}
