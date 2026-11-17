package canbusreg

import (
	"errors"
	"testing"
)

// bankPairTestFlavor asserts a PCS flavor together with the bank it pairs with.
func bankPairTestFlavor(c *SelectionSpec, flavor string) {
	c.Flavor = flavor
	switch flavor {
	case "pcs-standard":
		c.Bank = "ordinary"
	case "pcs-parallel":
		c.Bank = "parallel"
	}
}

// bankPairTestRefused checks that varying the bank alone of a paired PCS
// selection is refused, so no second key with the other bank can exist.
func bankPairTestRefused(t *testing.T, c SelectionSpec) {
	t.Helper()
	if _, err := NewSelection(c); !errors.Is(err, ErrSelectionBankPairing) {
		t.Fatalf("%s with bank %s: got %v, want %v", c.Flavor, c.Bank, err, ErrSelectionBankPairing)
	}
}

// The pairing key is (Flavor, Bank). Every flavor value is crossed with both
// documented banks, so each production mutation fails one row:
//   - Flavor: dropping the pcs-standard clause admits standard/parallel;
//     dropping the pcs-parallel clause admits parallel/ordinary; reading
//     PCSFlavor instead of Flavor refuses the LINK rows; extending the rule to
//     pcs-extended19 refuses one of its rows.
//   - Bank: comparing against the wrong bank refuses the paired rows; skipping
//     the bank comparison admits the conflicting rows.
func TestSelectionVocabularyPairsThePCSFlavorWithTheBank(t *testing.T) {
	s, _ := standardTestFixture(t, 0x313, [8]byte{})
	for _, v := range []struct {
		flavor, pcsFlavor, bank string
		refused                 bool
	}{
		{"pcs-standard", "", "ordinary", false},
		{"pcs-standard", "", "parallel", true},
		{"pcs-parallel", "", "parallel", false},
		{"pcs-parallel", "", "ordinary", true},
		{"pcs-extended19", "", "ordinary", false},
		{"pcs-extended19", "", "parallel", false},
		{"link", "pcs-standard", "parallel", false},
		{"link", "pcs-parallel", "ordinary", false},
	} {
		c := s.SelectionSpecValue()
		c.Flavor, c.PCSFlavor, c.Bank = v.flavor, v.pcsFlavor, v.bank
		if v.flavor == "pcs-extended19" || v.flavor == "link" {
			c.RouteSet, c.Destination, c.Source = true, 0xf2, 1
		}
		got, err := NewSelection(c)
		if v.refused {
			if !errors.Is(err, ErrSelectionBankPairing) {
				t.Fatalf("%s/%s with bank %s: got %v, want %v", v.flavor, v.pcsFlavor, v.bank, err, ErrSelectionBankPairing)
			}
			if got.SelectionGroup() != "" || got.SelectionGrade() != "Unknown" {
				t.Fatalf("%s with bank %s resolved a contract: %s %s", v.flavor, v.bank, got.SelectionGroup(), got.SelectionGrade())
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s/%s with bank %s refused: %v", v.flavor, v.pcsFlavor, v.bank, err)
		}
	}
}

// A standard PCS asserted with the parallel bank would decode the documented
// 326 frame with the parallel offset as a large negative current. The pairing
// refuses that selection; the same frame decodes through each paired selection.
func TestSelectionBankPairingRefusesTheParallelOffsetForAStandardPCS(t *testing.T) {
	payload := [8]byte{2, 0x11, 0x70, 0x0f, 0xa0, 44}
	s, i := slotTestFixture(t, 0x326, payload)
	c := s.SelectionSpecValue()
	if c.Flavor != "pcs-standard" || c.Bank != "ordinary" {
		t.Fatalf("fixture is not the paired standard PCS: %s %s", c.Flavor, c.Bank)
	}
	c.Bank = "parallel"
	if _, err := NewSelection(c); !errors.Is(err, ErrSelectionBankPairing) {
		t.Fatalf("standard PCS with the parallel bank: got %v, want %v", err, ErrSelectionBankPairing)
	}
	slotTestWant(t, DecodePCSSlots(s, i), "current", "accepted", 0, 10, "A")

	p, j := slotTestFixture(t, 0x326, [8]byte{2, 0, 0, 0x27, 0x10})
	p, j.PCSStandardPCSInput = standardTestReselect(t, p, j.PCSStandardPCSInput, func(c *SelectionSpec) { c.Flavor, c.Bank = "pcs-parallel", "parallel" })
	slotTestWant(t, DecodePCSSlots(p, j), "current", "accepted", 0, 10, "A")
}
