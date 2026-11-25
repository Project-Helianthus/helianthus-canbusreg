package gree

import (
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// Coordinate is an exact dictionary type, start and coordinate kind.
type Coordinate struct {
	Type  uint8
	Start uint16
	Kind  reg.GreeVRFDictionaryKind
}

func (c Coordinate) String() string { return fmt.Sprintf("%02x:%02x", c.Type, c.Start) }

func coordinate(r reg.GreeVRFDictionaryRow) Coordinate {
	return Coordinate{r.Type, r.Coordinate, r.Kind}
}

// CommandRow describes one native method and its wire extent.
type CommandRow struct {
	Name                string
	Setting             *Coordinate
	Property            *uint8
	ClockID             uint32
	Width               int
	LabelOnly           bool
	Conflict            bool
	SynchronousReadback bool
}

var settingTypes = [...]uint8{0x6d, 0x6e, 0x6f, 0x71, 0x72, 0x73, 0x78, 0x79, 0x7b, 0x7c, 0x7d}

// CommandRows enumerates the encodable settings, map properties and fixed clocks.
func CommandRows() []CommandRow {
	var rows []CommandRow
	for _, typ := range settingTypes {
		image := reg.NewGreeVRFRegisterImage(reg.GreeVRFRegisterKey{Type: typ})
		for _, p := range image.TypedPoints() {
			r := p.Row
			if r.Kind == reg.GreeVRFDictionaryContainer || r.Width < 1 || r.Width > 2 || r.Coordinate > 255 {
				continue
			}
			c := coordinate(r)
			rows = append(rows, CommandRow{Name: "setting." + c.String(), Setting: &c, Width: r.Width + 2, LabelOnly: r.LabelOnly, Conflict: r.Conflict != "", SynchronousReadback: true})
		}
	}
	for id := range 256 {
		p := uint8(id)
		if f, ok := reg.EncodeGreeVRFCommand(0, 0, p, 0); ok {
			kind := reg.GreeVRFDictionaryByte
			typ := uint8(f.Identifier & 0x7f)
			if typ == 0x6f {
				kind = reg.GreeVRFDictionaryBit
			}
			r, _ := reg.LookupGreeVRFDictionary(typ, uint16(f.Data[0]), kind)
			rows = append(rows, CommandRow{Name: fmt.Sprintf("property.%02x", p), Property: &p, Width: len(f.Data), LabelOnly: r.LabelOnly, Conflict: r.Conflict != ""})
		}
	}
	return append(rows, CommandRow{Name: "time", ClockID: 0x04820000, Width: 8}, CommandRow{Name: "clock", ClockID: 0x04820084, Width: 8})
}

// SettingCall keeps the explicitly supplied deadline outside the wire setting.
type SettingCall struct {
	Setting  reg.GreeVRFAddressedSetting
	Deadline time.Time
}

// Execute observes passive readback through the native setting executor.
func (c SettingCall) Execute(ctx context.Context, reports <-chan reg.GreeVRFSettingReport, submit func(context.Context, reg.GreeVRFCommand) (reg.GreeVRFSubmission, error), monotonicAt func(time.Time) time.Duration) (reg.GreeVRFWriteOutcome, error) {
	return reg.ExecuteGreeVRFAddressedSetting(ctx, c.Deadline, c.Setting, submit, reports, monotonicAt)
}

type commandEncoder func(uint64) (reg.GreeVRFCommand, bool)

// SendClock encodes one fixed clock row and hands it to the caller’s sender.
func SendClock(name string, arg reg.GreeVRFTime, send func(reg.GreeVRFCommand) error) error {
	var encode func(reg.GreeVRFTime) (reg.GreeVRFCommand, bool)
	switch name {
	case "time":
		encode = reg.EncodeGreeVRFTimeCommand
	case "clock":
		encode = reg.EncodeGreeVRFClockCommand
	default:
		return fmt.Errorf("unknown clock row %q", name)
	}
	parts := [8]byte{arg.Year, arg.Month, arg.Day, arg.Hour, arg.Minute, arg.Second, arg.Weekday}
	f, err := invertCommand(name, 8, binary.LittleEndian.Uint64(parts[:]), func(raw uint64) (reg.GreeVRFCommand, bool) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], raw)
		return encode(reg.GreeVRFTime{Year: b[0], Month: b[1], Day: b[2], Hour: b[3], Minute: b[4], Second: b[5], Weekday: b[6]})
	})
	if err != nil {
		return err
	}
	return send(f)
}

// EncodeProperty evaluates a command-map row for the supplied target.
func EncodeProperty(property uint8, seed uint32, unit uint8, value uint16) (reg.GreeVRFCommand, error) {
	f, ok := reg.EncodeGreeVRFCommand(seed, unit, property, 0)
	if !ok {
		return reg.GreeVRFCommand{}, fmt.Errorf("unknown property or target")
	}
	return invertCommand(fmt.Sprintf("property.%02x", property), len(f.Data), uint64(value), func(raw uint64) (reg.GreeVRFCommand, bool) {
		return reg.EncodeGreeVRFCommand(seed, unit, property, uint16(raw))
	})
}

// EncodeSetting evaluates an addressed setting row for the supplied target.
func EncodeSetting(setting reg.GreeVRFAddressedSetting) (reg.GreeVRFCommand, error) {
	f, r, ok := reg.EncodeGreeVRFAddressedWrite(reg.GreeVRFAddressedSetting{Key: setting.Key, Class: setting.Class, Coordinate: setting.Coordinate})
	if !ok {
		return reg.GreeVRFCommand{}, fmt.Errorf("unknown setting or target")
	}
	return invertCommand("setting."+coordinate(r.Row).String(), len(f.Data), uint64(setting.RawValue), func(raw uint64) (reg.GreeVRFCommand, bool) {
		setting.RawValue = uint16(raw)
		frame, _, ok := reg.EncodeGreeVRFAddressedWrite(setting)
		return frame, ok
	})
}

func number(raw uint64) semreg.Decimal {
	n := semreg.Decimal{}
	for raw != 0 && raw%10 == 0 {
		raw /= 10
		n.Exponent10++
	}
	n.Coefficient = strconv.FormatUint(raw, 10)
	return n
}

func uintNumber(n semreg.Decimal) (uint64, error) {
	if n.Exponent10 < 0 || n.Exponent10 > 19 {
		return 0, fmt.Errorf("not an unsigned integer")
	}
	return strconv.ParseUint(n.Coefficient+strings.Repeat("0", int(n.Exponent10)), 10, 64)
}

func descriptors[S any](codec roweval.Codec) roweval.Descriptors[S] {
	ident := func(raw uint64, _ uint16) (uint64, error) { return raw, nil }
	order := map[string]roweval.Order{"identity": {Decode: ident, Encode: ident}}
	return roweval.Descriptors[S]{Codecs: map[string]roweval.Codec{"native": codec}, ByteOrders: order, WordOrders: order, BitOrders: order}
}

func wire(bytes int) roweval.Layout {
	return roweval.Layout{Width: uint16(bytes * 8), Codec: "native", ByteOrder: "identity", WordOrder: "identity", BitOrder: "identity"}
}

func invertCommand(name string, width int, arg uint64, encoder commandEncoder) (reg.GreeVRFCommand, error) {
	var identifier uint32
	d := descriptors[string](roweval.Codec{Decode: func(raw uint64, _ uint16) (semreg.Decimal, error) { return number(raw), nil }, Encode: func(n semreg.Decimal, _ uint16) (uint64, error) {
		raw, err := uintNumber(n)
		if err != nil {
			return 0, err
		}
		f, ok := encoder(raw)
		if !ok || len(f.Data) != width {
			return 0, fmt.Errorf("invalid %s argument", name)
		}
		identifier = f.Identifier
		var bytes [8]byte
		copy(bytes[:], f.Data)
		return binary.LittleEndian.Uint64(bytes[:]), nil
	}})
	const unit = "native.can.argument"
	const role = "native"
	operation := semreg.DefinitionRef{ID: semreg.DefinitionID(name)}
	t := roweval.Table[string]{Native: []roweval.NativeRow[string]{{ID: name, At: name, Unit: unit, Wire: wire(width)}}, Bindings: []roweval.BindingRow{{NativeID: name, SubjectRole: role, Write: &roweval.WriteRow{Operation: operation, Argument: unit, TargetRow: name}}}}
	out, err := roweval.Invert(t, roweval.Writing[string]{NativeID: name, SubjectRole: role, Operation: operation, Argument: semreg.TypedField{ID: unit, Value: quantity(number(arg), unit)}, Descriptors: d})
	if err != nil {
		return reg.GreeVRFCommand{}, err
	}
	if out.Reason != "" {
		return reg.GreeVRFCommand{}, fmt.Errorf("%s: %s", out.Reason, out.Detail)
	}
	return reg.GreeVRFCommand{Identifier: identifier, Data: out.Payload}, nil
}

func quantity(number semreg.Decimal, unit semreg.DefinitionID) semreg.Value {
	return semreg.Value{Kind: semreg.ValueQuantity, Quantity: &semreg.Quantity{Number: number, Unit: unit}}
}
