package formatter

import "fmt"

// SignedPercent formats a percentage to one decimal place with an explicit
// sign for positive values. Readings that would round to zero are written as
// "0.0%" rather than a signed zero, so charts, alt text and post text all
// agree on how a flat reading reads.
func SignedPercent(v float64) string {
	switch {
	case v >= 0.05:
		return fmt.Sprintf("+%.1f%%", v)
	case v > -0.05:
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", v)
}
