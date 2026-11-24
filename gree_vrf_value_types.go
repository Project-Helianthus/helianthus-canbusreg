package canbusreg

import (
	"bytes"
	"encoding/binary"
)

// GreeVRFValueStatus separates interpretation from raw or incomplete evidence.
type GreeVRFValueStatus string

// The native value decoding outcomes.
const (
	GreeVRFValueIncomplete GreeVRFValueStatus = "incomplete"
	GreeVRFValueRaw        GreeVRFValueStatus = "raw"
	GreeVRFValueDecoded    GreeVRFValueStatus = "decoded"
)

// GreeVRFValue carries an exact rational, named bits, or just native bytes.
// A zero denominator means no numeric interpretation.
type GreeVRFValue struct {
	Status                 GreeVRFValueStatus
	Raw                    []byte
	Numerator, Denominator int64
	Bits                   map[uint8]bool
}

type greeVRFValueRule struct {
	width                       int
	multiplier, offset, divisor int64
	decode                      func([]byte) (int64, int64)
}

var greeVRFValueRules = map[string]greeVRFValueRule{
	"uint8":                     {1, 1, 0, 1, nil},
	"uint8M2":                   {1, 2, 0, 1, nil},
	"uint8Devide2":              {1, 1, 0, 2, nil},
	"uint8Minus100":             {1, 1, -100, 1, nil},
	"cfloat8base10":             {1, 1, 0, 10, nil},
	"uint16":                    {2, 1, 0, 1, nil},
	"uint16multi256":            {2, 1, 0, 1, nil},
	"uint16Devide10":            {2, 1, 0, 10, nil},
	"uint16Devide100":           {2, 1, 0, 100, nil},
	"uint16Multi10":             {2, 1, 0, 100, nil},
	"uint16Devide3600":          {2, 1, 0, 3600, nil},
	"uint16Multi1000":           {2, 1, 0, 3600, nil},
	"uint16Minus1000Devide10":   {2, 1, -1000, 10, nil},
	"uint16Minus32768Multi10":   {2, 1, -32768, 100, nil},
	"uint16Minus32768Multi1000": {2, 1, -32768, 3600, nil},
	"uint16DecimalsDevide256":   {2, 1, 0, 256, nil},
	"cfloat16base10":            {width: 2, decode: greeVRFBaseTen},
	"cfloat16base256":           {width: 2, decode: greeVRFRoundedFraction},
	"uint16Int":                 {width: 2, decode: greeVRFVariableFraction},
	"uint32":                    {4, 1, 0, 1, nil},
	"ubytes6":                   {width: 6},
	"bits":                      {width: 1},
	"uint8Multi2":               {width: 1},
	"uint8Devide10":             {width: 1},
	"uint8Minus100Devide10":     {width: 1},
}

// DecodeGreeVRFValue applies only the documented native rule for name. For
// bits, the caller supplies the defined bit positions in the container.
func DecodeGreeVRFValue(name string, raw []byte, definedBits ...uint8) GreeVRFValue {
	out := GreeVRFValue{Status: GreeVRFValueRaw, Raw: bytes.Clone(raw)}
	rule := greeVRFValueRules[name]
	if len(raw) < rule.width {
		out.Status = GreeVRFValueIncomplete
		return out
	}
	if name == "bits" && len(definedBits) != 0 {
		out.Bits = make(map[uint8]bool, len(definedBits))
		for _, bit := range definedBits {
			if bit < 8 {
				out.Bits[bit] = raw[0]&(1<<bit) != 0
			}
		}
		if len(out.Bits) != 0 {
			out.Status = GreeVRFValueDecoded
		}
		return out
	}
	if rule.decode != nil {
		out.Numerator, out.Denominator = rule.decode(raw)
	} else if rule.divisor != 0 {
		out.Numerator = greeVRFUnsigned(raw, rule.width)*rule.multiplier + rule.offset
		out.Denominator = rule.divisor
	}
	if out.Denominator != 0 {
		out.Status = GreeVRFValueDecoded
	}
	return out
}

func greeVRFUnsigned(raw []byte, width int) int64 {
	switch width {
	case 1:
		return int64(raw[0])
	case 2:
		return int64(binary.LittleEndian.Uint16(raw))
	case 4:
		return int64(binary.LittleEndian.Uint32(raw))
	default:
		return 0
	}
}

func greeVRFBaseTen(raw []byte) (int64, int64) { return int64(raw[1])*10 + int64(raw[0]), 10 }

func greeVRFRoundedFraction(raw []byte) (int64, int64) {
	n := int64(raw[0]) * 100
	q, r := n/256, n%256
	if r > 128 || (r == 128 && q%2 != 0) {
		q++
	}
	return int64(raw[1])*100 + q, 100
}

func greeVRFVariableFraction(raw []byte) (int64, int64) {
	divisor := int64(1000)
	switch {
	case raw[0] < 10:
		divisor = 10000
	case raw[0] < 100:
		divisor = 100
	}
	return int64(raw[1])*divisor + int64(raw[0]), divisor
}
