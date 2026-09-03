package sparkline

import (
	"fmt"
	"image/color"
	"sync"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// Chart theme shared by the weekly and yearly sentiment charts.
//
// Colours follow a light chart surface with a blue/orange diverging pair for
// sentiment polarity (positive/negative) and a neutral gray midpoint. The pair
// was checked with a colour-vision-deficiency validator: adjacent-pair CVD
// ΔE 9.8, normal-vision ΔE 17.6, all ≥ 3:1 contrast on the surface.
var (
	themeSurface      = color.RGBA{252, 252, 251, 255} // #fcfcfb chart surface
	themeInkPrimary   = color.RGBA{11, 11, 11, 255}    // #0b0b0b titles, hero figure
	themeInkSecondary = color.RGBA{82, 81, 78, 255}    // #52514e labels, deltas
	themeInkMuted     = color.RGBA{137, 135, 129, 255} // #898781 axis ticks, footer
	themeGrid         = color.RGBA{225, 224, 217, 255} // #e1e0d9 hairline grid
	themeBaseline     = color.RGBA{195, 194, 183, 255} // #c3c2b7 axis / zero line
	themeNeutralBand  = color.RGBA{240, 239, 236, 255} // #f0efec neutral zone (-10%..+10%)

	themePositive     = color.RGBA{42, 120, 214, 255}  // #2a78d6 blue, trend above zero
	themePositiveSoft = color.RGBA{134, 182, 239, 255} // #86b6ef raw series above zero
	themeNegative     = color.RGBA{235, 104, 52, 255}  // #eb6834 orange, trend below zero
	themeNegativeSoft = color.RGBA{245, 169, 138, 255} // #f5a98a raw series below zero
	themeAverage      = color.RGBA{137, 135, 129, 255} // #898781 dashed average line
)

// polarityColor returns the strong (trend) and soft (raw) series colours for a value.
func polarityColor(v float64) (strong, soft color.RGBA) {
	if v < 0 {
		return themeNegative, themeNegativeSoft
	}
	return themePositive, themePositiveSoft
}

// Embedded fonts. The production container ships no system fonts, so the Go
// fonts are compiled into the binary rather than loaded from disk.
var (
	fontOnce    sync.Once
	fontRegular *truetype.Font
	fontBold    *truetype.Font
	fontErr     error

	faceMu    sync.Mutex
	faceCache = map[faceKey]font.Face{}
)

type faceKey struct {
	size float64
	bold bool
}

func loadFonts() {
	fontRegular, fontErr = truetype.Parse(goregular.TTF)
	if fontErr != nil {
		return
	}
	fontBold, fontErr = truetype.Parse(gobold.TTF)
}

// fontFace returns a cached face for the embedded Go font at the given size.
func fontFace(size float64, bold bool) (font.Face, error) {
	fontOnce.Do(loadFonts)
	if fontErr != nil {
		return nil, fmt.Errorf("parse embedded font: %w", fontErr)
	}
	key := faceKey{size: size, bold: bold}
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faceCache[key]; ok {
		return f, nil
	}
	ttf := fontRegular
	if bold {
		ttf = fontBold
	}
	f := truetype.NewFace(ttf, &truetype.Options{Size: size, DPI: 72, Hinting: font.HintingFull})
	faceCache[key] = f
	return f, nil
}

// setFont switches the drawing context to the embedded font. On failure the
// context keeps whatever face it already has (gg's built-in bitmap font).
func setFont(dc *gg.Context, size float64, bold bool) {
	if f, err := fontFace(size, bold); err == nil {
		dc.SetFontFace(f)
	}
}

// pctText formats a sentiment percentage with one decimal and a sign for
// positive values, matching the summary post ("+10.0%").
func pctText(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.1f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}

// tickText formats an axis tick; whole-number spacings drop the decimal.
func tickText(v, spacing float64) string {
	if spacing >= 1 {
		return fmt.Sprintf("%.0f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}
