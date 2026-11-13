package canbusreg

import "testing"

// The gateway configures every non-parallel PCS profile with Bank "ordinary";
// the slot current must decode with the non-parallel offset for that value.
func TestSlotCurrentDecodesWithTheOrdinaryBank(t *testing.T) {
	payload := [8]byte{0x02, 0x14, 0xBE, 0x10, 0x9A, 0xDA, 0, 0}
	s, i := replayTestFixture(t)
	c := s.SelectionSpecValue()
	c.Release = "0011"
	c.Bank = "ordinary"
	s = replayTestReselect(t, &i, c)
	i.PCS.SlotValid, i.PCS.NativeSlot, i.PCS.GroupValid, i.PCS.NativeGroup = true, 2, true, 0
	member := s
	i.PCS.Member = &member
	replayTestFrame(t, &i, 0x326, false, payload)
	r := replayTestClassify(t, replayTestProfile(t, s, i), i.PCS.Evidence)
	for name, want := range map[string]struct {
		n, d int64
		unit string
	}{"slots/current": {250, 10, "A"}, "slots/voltage": {5310, 100, "V"}, "slots/soc_low": {218, 1, ""}} {
		f := r.Current.Fields[name]
		if f.Status != "accepted" || f.Numerator != want.n || f.Denominator != want.d || f.Unit != want.unit || f.Bank != "ordinary" || f.PhysicalQualified {
			t.Fatalf("%s: %+v", name, f)
		}
	}

	direct, j := slotTestFixture(t, 0x326, payload)
	direct, j.PCSStandardPCSInput = standardTestReselect(t, direct, j.PCSStandardPCSInput, func(c *SelectionSpec) { c.Bank = "ordinary" })
	slotTestWant(t, DecodePCSSlots(direct, j), "current", "accepted", 250, 10, "A")
}

func TestSelectionAcceptsOnlyTheOrdinaryAndParallelBanks(t *testing.T) {
	s, _ := standardTestFixture(t, 0x313, [8]byte{})
	rejected := "explicit bank ordinary or parallel required"
	for _, v := range []struct{ bank, want string }{
		{"ordinary", ""},
		{"parallel", ""},
		{"standard", rejected},
		{"Ordinary", rejected},
		{"other", rejected},
		{"", "explicit bank, endpoint and epoch required"},
	} {
		bank, want := v.bank, v.want
		c := s.SelectionSpecValue()
		c.Bank = bank
		_, err := NewSelection(c)
		if want == "" && err != nil {
			t.Fatalf("bank %q rejected: %v", bank, err)
		}
		if want != "" && (err == nil || err.Error() != want) {
			t.Fatalf("bank %q: got %v, want %q", bank, err, want)
		}
	}
}
