package replay

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-canbus/receiver"
)

// Catalog is an ordered set of recorded interface scripts.
type Catalog struct {
	Scripts []Script
}

// Script is one interface's recorded frames, in replay order.
type Script struct {
	Name      string
	Interface string
	Index     int
	Frames    []Frame
}

// Frame is one recorded classic CAN frame. A set Raw record is replayed as
// those bytes. Otherwise ID, Extended and Data are encoded as one SocketCAN
// record. Sequence is used only when HasSequence is set; an omitted sequence
// is the one-based position in the script. Monotonic is the recorded receive
// time and may be zero.
type Frame struct {
	Package     string
	ID          uint32
	Extended    bool
	Data        []byte
	Monotonic   time.Duration
	Sequence    uint64
	HasSequence bool
	Raw         []byte
}

// Parse decodes a catalog. Unknown fields and trailing values are rejected.
func Parse(data []byte) (Catalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wire wireCatalog
	if err := dec.Decode(&wire); err != nil {
		return Catalog{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Catalog{}, errors.New("replay catalog has trailing data")
		}
		return Catalog{}, err
	}
	return wire.catalog()
}

// Write encodes a catalog in a stable form.
func Write(catalog Catalog) ([]byte, error) {
	wire, err := catalog.wire()
	if err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func (c Catalog) wire() (wireCatalog, error) {
	out := wireCatalog{Scripts: make([]wireScript, 0, len(c.Scripts))}
	for i, script := range c.Scripts {
		encoded, err := script.wire()
		if err != nil {
			return wireCatalog{}, fmt.Errorf("scripts[%d]: %w", i, err)
		}
		out.Scripts = append(out.Scripts, encoded)
	}
	return out, nil
}

func (s Script) wire() (wireScript, error) {
	if err := s.validate(); err != nil {
		return wireScript{}, err
	}
	out := wireScript{Name: s.Name, Interface: s.Interface, Index: s.Index, Frames: make([]wireFrame, 0, len(s.Frames))}
	for i, frame := range s.Frames {
		encoded, err := frame.wire()
		if err != nil {
			return wireScript{}, fmt.Errorf("frames[%d]: %w", i, err)
		}
		out.Frames = append(out.Frames, encoded)
	}
	return out, nil
}

func (s Script) validate() error {
	if s.Name == "" {
		return errors.New("script name is empty")
	}
	if !receiver.ValidInterfaceName(s.Interface) {
		return fmt.Errorf("invalid CAN interface name %q", s.Interface)
	}
	if _, err := canbus.NewInterfaceIdentity(s.Interface, s.Index); err != nil {
		return err
	}
	for i, frame := range s.Frames {
		if err := frame.validate(); err != nil {
			return fmt.Errorf("frames[%d]: %w", i, err)
		}
	}
	return nil
}

func (f Frame) validate() error {
	if f.Monotonic < 0 {
		return errors.New("negative monotonic time")
	}
	_, err := f.bytes()
	return err
}

func (f Frame) wire() (wireFrame, error) {
	if err := f.validate(); err != nil {
		return wireFrame{}, err
	}
	out := wireFrame{
		Package: f.Package, ID: strconv.FormatUint(uint64(f.ID), 16), Extended: f.Extended,
		Data: hex.EncodeToString(f.Data), Monotonic: int64(f.Monotonic),
	}
	if f.HasSequence {
		seq := f.Sequence
		out.Sequence = &seq
	}
	if len(f.Raw) > 0 {
		out.Raw = hex.EncodeToString(f.Raw)
	}
	return out, nil
}

func (f Frame) bytes() ([]byte, error) {
	if len(f.Raw) > 0 {
		if len(f.Raw) != canbus.SocketCANRecordSize {
			return nil, fmt.Errorf("raw record length %d", len(f.Raw))
		}
		raw := append([]byte(nil), f.Raw...)
		if _, err := canbus.DecodeSocketCANRecord(raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	if len(f.Data) > canbus.MaxClassicDataLength {
		return nil, fmt.Errorf("payload length %d", len(f.Data))
	}
	raw := make([]byte, canbus.SocketCANRecordSize)
	word := f.ID
	if f.Extended {
		word |= 0x80000000
	}
	binary.NativeEndian.PutUint32(raw[:4], word)
	raw[4] = byte(len(f.Data))
	copy(raw[8:], f.Data)
	if _, err := canbus.DecodeSocketCANRecord(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

type wireCatalog struct {
	Scripts []wireScript `json:"scripts"`
}

type wireScript struct {
	Name      string      `json:"name"`
	Interface string      `json:"interface"`
	Index     int         `json:"index"`
	Frames    []wireFrame `json:"frames"`
}

type wireFrame struct {
	Package   string  `json:"package,omitempty"`
	ID        string  `json:"id"`
	Extended  bool    `json:"extended"`
	Data      string  `json:"data"`
	Monotonic int64   `json:"monotonic_ns"`
	Sequence  *uint64 `json:"sequence,omitempty"`
	Raw       string  `json:"raw,omitempty"`
}

func (w wireCatalog) catalog() (Catalog, error) {
	out := Catalog{Scripts: make([]Script, 0, len(w.Scripts))}
	for i, script := range w.Scripts {
		decoded, err := script.script()
		if err != nil {
			return Catalog{}, fmt.Errorf("scripts[%d]: %w", i, err)
		}
		out.Scripts = append(out.Scripts, decoded)
	}
	return out, nil
}

func (w wireScript) script() (Script, error) {
	out := Script{Name: w.Name, Interface: w.Interface, Index: w.Index, Frames: make([]Frame, 0, len(w.Frames))}
	for i, frame := range w.Frames {
		decoded, err := frame.frame()
		if err != nil {
			return Script{}, fmt.Errorf("frames[%d]: %w", i, err)
		}
		out.Frames = append(out.Frames, decoded)
	}
	if err := out.validate(); err != nil {
		return Script{}, err
	}
	return out, nil
}

func (w wireFrame) frame() (Frame, error) {
	id, err := strconv.ParseUint(w.ID, 16, 32)
	if err != nil {
		return Frame{}, fmt.Errorf("identifier %q: %w", w.ID, err)
	}
	data, err := hex.DecodeString(w.Data)
	if err != nil {
		return Frame{}, fmt.Errorf("data: %w", err)
	}
	out := Frame{
		Package: w.Package, ID: uint32(id), Extended: w.Extended, Data: data,
		Monotonic: time.Duration(w.Monotonic),
	}
	if w.Sequence != nil {
		out.Sequence = *w.Sequence
		out.HasSequence = true
	}
	if w.Raw != "" {
		raw, err := hex.DecodeString(w.Raw)
		if err != nil {
			return Frame{}, fmt.Errorf("raw: %w", err)
		}
		out.Raw = raw
	}
	if err := out.validate(); err != nil {
		return Frame{}, err
	}
	return out, nil
}
