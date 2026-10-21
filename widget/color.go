package widget

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Color is a color of a widget: an sRGB color, with another for dark
// mode, or one of the system's, which follow the appearance and the
// tinting of widgets.
type Color struct {
	r, g, b, a uint8
	dark       *Color
	name       string
}

// The system's colors.
var (
	// Primary is the color of text, black or white.
	Primary = Color{name: "primary"}
	// Secondary is the color of text of less importance.
	Secondary = Color{name: "secondary"}
	// Accent is the app's accent color, the system's blue by default.
	Accent = Color{name: "accent"}
	// Clear is transparent.
	Clear = Color{name: "clear"}
)

// RGB returns an opaque sRGB color.
func RGB(r, g, b uint8) Color { return Color{r: r, g: g, b: b, a: 255} }

// RGBA returns an sRGB color of an opacity from 0 to 1.
func RGBA(r, g, b uint8, alpha float64) Color {
	return Color{r: r, g: g, b: b, a: uint8(math.Round(min(max(alpha, 0), 1) * 255))}
}

// Hex parses "#rgb", "#rrggbb" or "#rrggbbaa", the # optional. It panics
// on anything else, as a mistake in the program.
func Hex(s string) Color {
	h := strings.TrimPrefix(s, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) == 6 {
		h += "ff"
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if len(h) != 8 || err != nil {
		panic(fmt.Sprintf("widget: Hex(%q) is not a color", s))
	}
	return Color{r: uint8(v >> 24), g: uint8(v >> 16), b: uint8(v >> 8), a: uint8(v)}
}

// Dark returns the color with another in dark mode.
func (c Color) Dark(d Color) Color {
	d.dark = nil
	c.dark = &d
	return c
}

func (c Color) MarshalJSON() ([]byte, error) {
	type rgba struct {
		R    float64 `json:"r"`
		G    float64 `json:"g"`
		B    float64 `json:"b"`
		A    float64 `json:"a"`
		Name string  `json:"name,omitempty"`
		Dark *Color  `json:"dark,omitempty"`
	}
	if c.name != "" {
		return json.Marshal(rgba{Name: c.name, A: 1, Dark: c.dark})
	}
	unit := func(v uint8) float64 { return math.Round(float64(v)/255*1e4) / 1e4 }
	return json.Marshal(rgba{R: unit(c.r), G: unit(c.g), B: unit(c.b), A: unit(c.a), Dark: c.dark})
}
