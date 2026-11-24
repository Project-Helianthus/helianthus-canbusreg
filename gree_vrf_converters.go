package canbusreg

import "math/big"

// GreeVRFPresentationOptions selects a documented display form. It does not
// describe or modify a native register.
type GreeVRFPresentationOptions struct {
	Active, Inverse, IntegerInverse, Masked, Pressure bool
	Refrigerant                                       string
}

// GreeVRFPresentationValue is a display-layer number, not a decoded register value.
type GreeVRFPresentationValue struct {
	Value *big.Rat
	Unit  string
}

var greeVRFPresentationConverters = map[string]func(*big.Rat, GreeVRFPresentationOptions) (string, bool){
	"CeVsFa":    greeVRFTemperaturePresentation,
	"kWVskBtuh": greeVRFPowerPresentation,
	"kPaVsinWG": greeVRFPressurePresentation,
	"Blur":      greeVRFBlurPresentation,
	"CeVskPa":   greeVRFSaturationPresentation,
}

// ConvertGreeVRFPresentation applies a named display conversion to an exact
// numeric value. It leaves the native input unchanged.
func ConvertGreeVRFPresentation(name string, in GreeVRFValue, options GreeVRFPresentationOptions) (GreeVRFPresentationValue, bool) {
	convert, ok := greeVRFPresentationConverters[name]
	if !ok || in.Status != GreeVRFValueDecoded || in.Denominator == 0 {
		return GreeVRFPresentationValue{}, false
	}
	value := big.NewRat(in.Numerator, in.Denominator)
	unit, ok := convert(value, options)
	if !ok {
		return GreeVRFPresentationValue{}, false
	}
	return GreeVRFPresentationValue{value, unit}, true
}

func greeVRFTemperaturePresentation(x *big.Rat, o GreeVRFPresentationOptions) (string, bool) {
	if o.Inverse {
		if o.Active {
			x.Mul(x.Sub(x, big.NewRat(32, 1)), big.NewRat(5, 9))
		}
		greeVRFRoundDisplay(x, 1, greeVRFTruncate)
		return "", true
	}
	if o.Active {
		greeVRFFahrenheit(x)
	}
	greeVRFRoundDisplay(x, 1, greeVRFRoundHalfAway)
	if o.Active {
		return "°F", true
	}
	return "", true
}

func greeVRFFahrenheit(x *big.Rat) { x.Add(x.Mul(x, big.NewRat(9, 5)), big.NewRat(32, 1)) }

func greeVRFPowerPresentation(x *big.Rat, o GreeVRFPresentationOptions) (string, bool) {
	if o.Inverse {
		if o.Active {
			x.Mul(x, big.NewRat(2931, 10000))
		}
		greeVRFRoundDisplay(x, 1, greeVRFTruncate)
	} else if o.Active {
		x.Mul(x, big.NewRat(3412, 1000))
		greeVRFRoundDisplay(x, 100, greeVRFRoundHalfEven)
		return "kBtu/h", true
	}
	return "", true
}

func greeVRFPressurePresentation(x *big.Rat, o GreeVRFPresentationOptions) (string, bool) {
	if o.Inverse {
		if o.Active && !o.IntegerInverse {
			x.Quo(x, big.NewRat(4, 1))
		} else {
			greeVRFRoundDisplay(x, 1, greeVRFTruncate)
		}
	} else if o.Active {
		x.Mul(x, big.NewRat(4, 1))
		return "inWG", true
	}
	return "", true
}

func greeVRFBlurPresentation(x *big.Rat, o GreeVRFPresentationOptions) (string, bool) {
	if o.Inverse {
		greeVRFRoundDisplay(x, 1, greeVRFTruncate)
	} else if o.Masked {
		x.Quo(x, big.NewRat(10, 1))
		greeVRFRoundDisplay(x, 1, greeVRFTruncate)
		x.Mul(x, big.NewRat(10, 1))
	}
	return "", true
}

func greeVRFSaturationPresentation(x *big.Rat, o GreeVRFPresentationOptions) (string, bool) {
	greeVRFRoundDisplay(x, 1, greeVRFTruncate)
	if o.Inverse {
		return "", true
	}
	if !o.Pressure {
		if o.Active {
			greeVRFFahrenheit(x)
			return "°F", true
		}
		return "°C", true
	}
	table := greeVRFSaturationTables[o.Refrigerant]
	index := new(big.Int).Add(x.Num(), big.NewInt(table.offset))
	if !index.IsInt64() || index.Sign() < 0 || index.Int64() >= int64(len(table.values)) {
		return "", false
	}
	if _, ok := x.SetString(table.values[index.Int64()]); !ok {
		return "", false
	}
	if o.Active {
		x.Mul(x, big.NewRat(4, 1))
		return "inWG", true
	}
	return "kPa", true
}

type greeVRFDisplayRounding uint8

const (
	greeVRFTruncate greeVRFDisplayRounding = iota
	greeVRFRoundHalfAway
	greeVRFRoundHalfEven
)

func greeVRFRoundDisplay(x *big.Rat, scale int64, rounding greeVRFDisplayRounding) {
	divisor := big.NewInt(scale)
	n := new(big.Int).Mul(x.Num(), divisor)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, x.Denom(), r)
	if rounding != greeVRFTruncate {
		cmp := r.Abs(r).Lsh(r, 1).Cmp(x.Denom())
		if cmp > 0 || cmp == 0 && (rounding == greeVRFRoundHalfAway || q.Bit(0) != 0) {
			q.Add(q, big.NewInt(int64(n.Sign())))
		}
	}
	x.SetFrac(q, divisor)
}
