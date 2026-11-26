package link

import (
	"encoding/binary"
	"strings"
	"time"

	"github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

const qDateValid = "date-valid"

var calendarFields = map[string]bool{"year": true, "month": true, "day": true, "hour": true, "minute": true, "second": true}

// identitySpecs returns the rows of the clock report 1809 and of the serial
// fragment 18FB. The 1809 rows are the fields the telemetry decoder emits. The
// 18FB rows are the page's fragment layout: the header byte B0 with its length
// code (bits 0 to 4) and fragment number (bits 5 and 6), and the serial bytes
// B1 to B7, each kept raw. Reassembly into a serial stays code in the
// identity assembler.
func identitySpecs() []rowSpec {
	var s []rowSpec
	date := func(r rowSpec) { s = append(s, withConditions(r, qDestination, "F5")) }
	date(raw(0x1809, "year", littleWord(0)))
	for at, name := range []string{"month", "day", "hour", "minute", "second", "status"} {
		date(raw(0x1809, name, byteAt(uint16(at+2))))
	}
	serial := func(r rowSpec) { s = append(s, withConditions(r, qSerialGroup, "true")) }
	serial(raw(0x18fb, "serial_header", byteAt(0)))
	serial(raw(0x18fb, "serial_length_code", bitsAt(0, 5)))
	serial(raw(0x18fb, "serial_fragment", bitsAt(5, 2)))
	for at, name := range []string{"serial_byte_1", "serial_byte_2", "serial_byte_3", "serial_byte_4", "serial_byte_5", "serial_byte_6", "serial_byte_7"} {
		serial(raw(0x18fb, name, byteAt(uint16(at+1))))
	}
	return s
}

func calendarHook(in roweval.DetachedInputs, _ roweval.RowID, _ roweval.Direction, v roweval.TypedValue) (roweval.DerivedValue, roweval.Reason) {
	if in.Qualifiers().Values[qDateValid] != "true" {
		return roweval.DerivedValue{}, roweval.ReasonOutsideDomain
	}
	return roweval.DerivedValue{Value: v}, ""
}

// IdentityTable returns the native rows of the clock report and the serial
// fragment and the hooks those rows name. The calendar components are withheld
// with their raw words kept when the date is not a calendar date or the
// selection carries no contract group.
func IdentityTable() roweval.Table[Selector] {
	special := make(map[string]roweval.RowHook)
	for _, s := range identitySpecs() {
		if s.frame == 0x1809 && calendarFields[s.field] {
			special[s.rowID()] = calendarHook
		}
	}
	return tableOf(identitySpecs(), special)
}

func identityQualifiers(s canbusreg.Selection, in canbusreg.LINKInput) roweval.Qualifiers {
	q := Qualifiers(s, in)
	group := s.SelectionGroup()
	p := in.Evidence.Frame.Data()
	date := false
	if len(p) == 8 {
		year, month, day := int(binary.LittleEndian.Uint16(p[0:2])), time.Month(p[2]), int(p[3])
		t := time.Date(year, month, day, int(p[4]), int(p[5]), int(p[6]), 0, time.UTC)
		date = group != "" && year >= 1 && month >= 1 && month <= 12 && t.Year() == year && t.Month() == month && t.Day() == day && p[4] < 24 && p[5] < 60 && p[6] < 60
	}
	q.Values[qDateValid] = boolText(date)
	q.Values[qSerialGroup] = boolText(group != "" && group != "L0")
	return q
}

// EvaluateIdentity evaluates the native rows of one clock report or serial
// fragment observation. It admits only what the decoder admits: an
// observation outside the route, the frame geometry or the selection context
// gives the reason invalid and no rows. A valid clock report is accepted and
// an invalid calendar date keeps the raw words with the disposition invalid,
// as in DecodeLINKTelemetry. A serial fragment is raw evidence; its
// reassembly and continuity checks are the identity assembler's.
func EvaluateIdentity(s canbusreg.Selection, in canbusreg.LINKInput) (Result, roweval.Reason, error) {
	res := Result{
		Evidence:    in.Evidence,
		Attribution: Attribution{Scope: in.Scope, Bank: in.Bank, Endpoint: in.Endpoint, Group: in.Group, Slot: in.Slot, HasGroup: in.Group != "", HasSlot: in.Slot != ""},
	}
	module, date, coordinator := admissions(s, in)
	if !module && !date && !coordinator {
		res.Status = "invalid"
		return res, "invalid", nil
	}
	frame := in.Evidence.Frame
	family := uint16(frame.ID().Value() >> 16)
	res.Status = "unsupported"
	switch {
	case date && family == 0x1809:
		res.Status = "accepted"
	case module && family == 0x18fb && s.SelectionGroup() != "" && s.SelectionGroup() != "L0":
		res.Status = "raw"
	default:
		return res, roweval.ReasonNoMatch, nil
	}
	rows, err := evaluateRows(IdentityTable(), frame, identityQualifiers(s, in))
	if err != nil {
		return res, "", err
	}
	for id, ev := range rows {
		if ev.Native != nil && calendarFields[id[strings.LastIndex(id, ".")+1:]] && ev.Native.Disposition == dispositionRaw {
			ev.Native.Disposition = dispositionAccepted
		}
	}
	res.Rows = rows
	return res, "", nil
}
