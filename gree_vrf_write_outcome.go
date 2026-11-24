package canbusreg

import (
	"bytes"
	"context"
	"errors"
	"time"
)

// GreeVRFSubmission records local transport acceptance and its original
// monotonic receipt. Zero Monotonic carries no successful receipt evidence.
type GreeVRFSubmission struct {
	Accepted  bool
	Monotonic time.Duration
}

// GreeVRFSettingReport binds a decoded point from one report frame to the
// running instance generation. Point.Received is the original receipt offset.
type GreeVRFSettingReport struct {
	Generation uint64
	Evidence   Evidence
	Point      GreeVRFTypedPoint
}

// GreeVRFWriteStatus expresses passive readback, not hardware application.
type GreeVRFWriteStatus string

const (
	// GreeVRFReported indicates a matching eligible data report was observed.
	GreeVRFReported GreeVRFWriteStatus = "reported"
	// GreeVRFNotReported indicates the caller deadline passed without a match.
	GreeVRFNotReported GreeVRFWriteStatus = "not reported"
)

// GreeVRFWriteOutcome keeps local acceptance and the matching report separate.
type GreeVRFWriteOutcome struct {
	Submission GreeVRFSubmission
	Status     GreeVRFWriteStatus
	Report     *GreeVRFSettingReport
}

// DecodeGreeVRFSettingReports decodes settings observed in this frame only.
// A value retained from another report cannot gain this frame's receipt time.
func DecodeGreeVRFSettingReports(observation GreeVRFObservation) []GreeVRFSettingReport {
	e := observation.Evidence
	id, ok := ClassifyGreeVRFIdentifier(e.Frame.ID())
	if !ok || id.Family != GreeVRFData && id.Family != GreeVRFAddressedData {
		return nil
	}
	rows := greeVRFDictionary[id.Type]
	if len(rows) == 0 {
		return nil
	}
	image := NewGreeVRFRegisterImage(GreeVRFRegisterKey{e.Interface, id.Family, id.Unit, id.Type})
	if !image.Apply(e, rows[0].Kind != GreeVRFDictionaryByte) {
		return nil
	}
	var reports []GreeVRFSettingReport
	for _, row := range rows {
		if row.Kind == GreeVRFDictionaryContainer {
			continue
		}
		if _, ok := greeVRFSettingRow(id.Type, row.Coordinate); !ok {
			continue
		}
		if point := image.typedPoint(row); point.Present {
			reports = append(reports, GreeVRFSettingReport{observation.Generation, e, point})
		}
	}
	return reports
}

// ExecuteGreeVRFAddressedSetting submits once via the supplied callback and
// observes an already-installed decoded subscription. Neither transport
// acceptance nor a matching report establishes that the device applied a value.
func ExecuteGreeVRFAddressedSetting(ctx context.Context, deadline time.Time, setting GreeVRFAddressedSetting,
	submit func(context.Context, GreeVRFCommand) (GreeVRFSubmission, error), reports <-chan GreeVRFSettingReport,
	monotonicAt func(time.Time) time.Duration) (GreeVRFWriteOutcome, error) {
	var out GreeVRFWriteOutcome
	if deadline.IsZero() {
		return out, errors.New("gree VRF setting requires an explicit deadline")
	}
	if !time.Now().Before(deadline) {
		return out, context.DeadlineExceeded
	}
	if submit == nil || reports == nil || monotonicAt == nil || setting.Generation == 0 {
		return out, errors.New("gree VRF setting requires a submitter, subscription, generation and receipt clock")
	}
	command, point, ok := EncodeGreeVRFAddressedWrite(setting)
	if !ok {
		return out, errors.New("invalid gree VRF addressed setting")
	}
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	limit := monotonicAt(deadline)
	if err := callCtx.Err(); err != nil {
		return out, err
	}
	if !time.Now().Before(deadline) {
		return out, context.DeadlineExceeded
	}
	var err error
	out.Submission, err = submit(callCtx, command)
	if err != nil || !out.Submission.Accepted {
		return out, err
	}
	return waitGreeVRFSettingReport(ctx, callCtx, reports, setting, point.Value.Raw, limit, out)
}

func waitGreeVRFSettingReport(parent, call context.Context, reports <-chan GreeVRFSettingReport, setting GreeVRFAddressedSetting,
	raw []byte, limit time.Duration, out GreeVRFWriteOutcome) (GreeVRFWriteOutcome, error) {
	match := func(report GreeVRFSettingReport) bool {
		point := report.Point
		key := setting.Key
		key.Family = GreeVRFData
		if out.Submission.Monotonic <= 0 || point.Received <= out.Submission.Monotonic || point.Received > limit ||
			report.Generation != setting.Generation || point.Key != key || point.Row.Coordinate != setting.Coordinate ||
			!point.Present || !bytes.Equal(point.Value.Raw, raw) {
			return false
		}
		out.Status, out.Report = GreeVRFReported, &report
		return true
	}
	for {
		if call.Err() != nil {
			if err := parent.Err(); err != nil {
				return out, err
			}
			// Include the reports buffered before expiry, even if submit returned
			// after the deadline. The original receipt decides eligibility.
			for pending := len(reports); pending > 0; pending-- {
				if match(<-reports) {
					return out, nil
				}
			}
			out.Status = GreeVRFNotReported
			return out, nil
		}
		select {
		case report, open := <-reports:
			if !open {
				reports = nil
			} else if match(report) {
				return out, nil
			}
		case <-call.Done():
		}
	}
}
