package sparkline

import "math"

// niceNumber returns a "nice" rounded number for axis labeling.
// Implements Heckbert's nice number algorithm.
// If round is true, rounds to nearest; if false, rounds to ceiling.
func niceNumber(value float64, round bool) float64 {
	if value == 0 {
		return 0
	}

	negative := value < 0
	if negative {
		value = -value
	}

	exponent := math.Floor(math.Log10(value))
	fraction := value / math.Pow(10, exponent)

	var nice float64
	if round {
		switch {
		case fraction < 1.5:
			nice = 1
		case fraction < 3:
			nice = 2
		case fraction < 7:
			nice = 5
		default:
			nice = 10
		}
	} else {
		switch {
		case fraction <= 1:
			nice = 1
		case fraction <= 2:
			nice = 2
		case fraction <= 5:
			nice = 5
		default:
			nice = 10
		}
	}

	result := nice * math.Pow(10, exponent)
	if negative {
		return -result
	}
	return result
}

// niceRange rounds min and max outward to clean axis boundaries.
// Returns rounded min (floor), max (ceil), and tick spacing.
func niceRange(min, max float64) (niceMin, niceMax, tickSpacing float64) {
	dataRange := max - min
	if dataRange == 0 {
		dataRange = 1
	}

	tickSpacing = niceNumber(dataRange/4, true)
	niceMin = math.Floor(min/tickSpacing) * tickSpacing
	niceMax = math.Ceil(max/tickSpacing) * tickSpacing

	return niceMin, niceMax, tickSpacing
}
