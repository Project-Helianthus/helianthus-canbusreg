package canbusreg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func TestGreeVRFWriteOutcomeReceiptFences(t *testing.T) {
	iface := greeCandidateInterface(t)
	otherName, err := canbus.NewInterfaceIdentity("sim-other", iface.Index())
	if err != nil {
		t.Fatal(err)
	}
	otherIndex, err := canbus.NewInterfaceIdentity(iface.Name(), iface.Index()+1)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name                 string
		iface                canbus.InterfaceIdentity
		id                   uint32
		generation           uint64
		payload              []byte
		receipt              time.Duration
		lateReturn, reported bool
	}{
		{"after submit, before return", iface, 0x1effd073, 2, []byte{0, 0xe6, 0}, 21, false, true},
		{"deadline included", iface, 0x1effd073, 2, []byte{0, 0xe6, 0}, 100, true, true},
		{"pre-submit", iface, 0x1effd073, 2, []byte{0, 0xe6, 0}, 19, false, false},
		{"tied receipt", iface, 0x1effd073, 2, []byte{0, 0xe6, 0}, 20, false, false},
		{"after deadline", iface, 0x1effd073, 2, []byte{0, 0xe6, 0}, 101, false, false},
		{"old generation", iface, 0x1effd073, 1, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong interface name", otherName, 0x1effd073, 2, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong interface index", otherIndex, 0x1effd073, 2, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong unit", iface, 0x1effd0f3, 2, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong family", iface, 0x1cffd073, 2, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong register type", iface, 0x1effd072, 2, []byte{0, 0xe6, 0}, 21, false, false},
		{"wrong coordinate", iface, 0x1effd073, 2, []byte{2, 0xe6, 0}, 21, false, false},
		{"different value", iface, 0x1effd073, 2, []byte{0, 0xe7, 0}, 21, false, false},
		{"silent", iface, 0x1effd073, 2, nil, 0, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{iface, GreeVRFData, 0x20, 0x73}, Class: GreeVRFIndoorUnit, Generation: 2, RawValue: 230}
			reports := make(chan GreeVRFSettingReport, 4)
			deadline := time.Now().Add(150 * time.Millisecond)
			nowCalls, submits := 0, 0
			out, err := ExecuteGreeVRFAddressedSetting(context.Background(), deadline, setting, func(ctx context.Context, command GreeVRFCommand) (GreeVRFSubmission, error) {
				submits++
				if command.Identifier != 0x1cbfd073 {
					t.Fatal(command)
				}
				if row.payload != nil {
					e := greeCandidateEvidence(t, row.iface, row.id, row.payload)
					e.Monotonic = row.receipt
					decoded := DecodeGreeVRFSettingReports(GreeVRFObservation{row.generation, e})
					for _, report := range decoded {
						reports <- report
					}
					if len(decoded) == 0 {
						t.Fatal("fixture yielded no observation", row.name)
					}
				}
				if row.lateReturn {
					<-ctx.Done()
				}
				return GreeVRFSubmission{true, 20}, nil
			}, reports, func(arg time.Time) time.Duration {
				nowCalls++
				if !arg.Equal(deadline) {
					t.Fatal("converted wrong deadline")
				}
				return 100
			})
			want := GreeVRFNotReported
			if row.reported {
				want = GreeVRFReported
			}
			if err != nil || out.Status != want || !out.Submission.Accepted || out.Submission.Monotonic != 20 || submits != 1 || nowCalls != 1 {
				t.Fatalf("outcome = %+v, err = %v, submits = %d, conversions = %d", out, err, submits, nowCalls)
			}
			if !row.reported && time.Now().Before(deadline) {
				t.Fatal("returned before the caller deadline")
			}
		})
	}
}

func TestGreeVRFWriteOutcomeRefusesMissingAndExpiredDeadline(t *testing.T) {
	setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{greeCandidateInterface(t), GreeVRFData, 0x20, 0x6f}, Class: GreeVRFIndoorUnit, Generation: 1, RawValue: 1}
	for _, deadline := range []time.Time{{}, time.Now().Add(-time.Second)} {
		out, err := ExecuteGreeVRFAddressedSetting(context.Background(), deadline, setting, func(context.Context, GreeVRFCommand) (GreeVRFSubmission, error) {
			t.Fatal("invalid deadline submitted a frame")
			return GreeVRFSubmission{}, nil
		}, make(chan GreeVRFSettingReport), func(time.Time) time.Duration { return 100 })
		if err == nil || out.Submission.Accepted {
			t.Fatalf("outcome = %+v, err = %v", out, err)
		}
	}
}

func TestGreeVRFWriteOutcomePreservesTransportError(t *testing.T) {
	setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{greeCandidateInterface(t), GreeVRFData, 0x20, 0x6f}, Class: GreeVRFIndoorUnit, Generation: 1, RawValue: 1}
	want := errors.New("synthetic transport error")
	out, err := ExecuteGreeVRFAddressedSetting(context.Background(), time.Now().Add(time.Second), setting, func(context.Context, GreeVRFCommand) (GreeVRFSubmission, error) {
		return GreeVRFSubmission{Accepted: true}, want
	}, make(chan GreeVRFSettingReport), func(time.Time) time.Duration { return 100 })
	if !errors.Is(err, want) || !out.Submission.Accepted || out.Status == GreeVRFReported {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
}

func TestGreeVRFWriteOutcomeReadsBackEachForm(t *testing.T) {
	iface := greeCandidateInterface(t)
	for _, row := range []struct {
		id   uint32
		typ  uint8
		raw  uint16
		data []byte
	}{
		{0x1effd06f, 0x6f, 1, []byte{0, 1}},
		{0x1effd071, 0x71, 4, []byte{0, 4}},
		{0x1effd073, 0x73, 230, []byte{0, 230, 0}},
	} {
		setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{iface, GreeVRFData, 0x20, row.typ}, Class: GreeVRFIndoorUnit, Generation: 1, RawValue: row.raw}
		reports := make(chan GreeVRFSettingReport, 8)
		out, err := ExecuteGreeVRFAddressedSetting(context.Background(), time.Now().Add(time.Second), setting, func(_ context.Context, command GreeVRFCommand) (GreeVRFSubmission, error) {
			if command.Identifier != 0x1cbfd000|uint32(row.typ) {
				t.Fatal(command)
			}
			e := greeCandidateEvidence(t, iface, row.id, row.data)
			e.Monotonic = 22
			for _, report := range DecodeGreeVRFSettingReports(GreeVRFObservation{1, e}) {
				reports <- report
			}
			return GreeVRFSubmission{true, 21}, nil
		}, reports, func(time.Time) time.Duration { return 100 })
		if err != nil || out.Status != GreeVRFReported || out.Report == nil || out.Report.Evidence.Frame.ID().Value() != row.id {
			t.Fatalf("%+v %v", out, err)
		}
	}
}

func TestGreeVRFWriteOutcomeMissingSubmitReceipt(t *testing.T) {
	iface := greeCandidateInterface(t)
	setting := GreeVRFAddressedSetting{Key: GreeVRFRegisterKey{iface, GreeVRFData, 0x20, 0x6f}, Class: GreeVRFIndoorUnit, Generation: 1, RawValue: 1}
	reports := make(chan GreeVRFSettingReport, 8)
	deadline := time.Now().Add(100 * time.Millisecond)
	out, err := ExecuteGreeVRFAddressedSetting(context.Background(), deadline, setting, func(_ context.Context, _ GreeVRFCommand) (GreeVRFSubmission, error) {
		e := greeCandidateEvidence(t, iface, 0x1effd06f, []byte{0, 1})
		e.Monotonic = 20
		for _, report := range DecodeGreeVRFSettingReports(GreeVRFObservation{1, e}) {
			reports <- report
		}
		return GreeVRFSubmission{Accepted: true}, nil
	}, reports, func(time.Time) time.Duration { return 100 })
	if err != nil || out.Status != GreeVRFNotReported || !out.Submission.Accepted || out.Report != nil || time.Now().Before(deadline) {
		t.Fatalf("%+v %v", out, err)
	}
}
