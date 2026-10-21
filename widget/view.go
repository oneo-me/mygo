package widget

import (
	"fmt"
	"time"
)

// View is an element of what a widget shows, which the widget extension
// draws with SwiftUI. Functions make views, and their methods return
// copies with a modifier, as SwiftUI's do:
//
//	widget.Row(widget.Symbol("drop.fill"), widget.Text("Water")).Gap(6).TextColor(widget.Accent)
//
// A view's padding is inside its background, as in package ui. The zero
// View shows nothing.
type View struct{ n *node }

// node is a view as the widget extension reads it, in JSON.
type node struct {
	T         string   `json:"t"`
	Text      string   `json:"text,omitempty"`
	Symbol    string   `json:"symbol,omitempty"`
	Image     string   `json:"image,omitempty"`
	Fit       string   `json:"fit,omitempty"`
	Date      *float64 `json:"date,omitempty"`
	DateStyle string   `json:"dateStyle,omitempty"`
	Value     *float64 `json:"value,omitempty"`
	Children  []*node  `json:"children,omitempty"`
	Gap       *float64 `json:"gap,omitempty"`
	// Items and Justify place the children across and along a stack's
	// axis, and the content in a view larger than it: "start",
	// "center" or "end".
	Items     string      `json:"items,omitempty"`
	Justify   string      `json:"justify,omitempty"`
	Font      *font       `json:"font,omitempty"`
	Color     *Color      `json:"color,omitempty"`
	BG        *Color      `json:"bg,omitempty"`
	Padding   *[4]float64 `json:"padding,omitempty"` // top, end, bottom, start
	Width     *float64    `json:"width,omitempty"`
	Height    *float64    `json:"height,omitempty"`
	FillW     bool        `json:"fillW,omitempty"`
	FillH     bool        `json:"fillH,omitempty"`
	Radius    *float64    `json:"radius,omitempty"`
	Opacity   *float64    `json:"opacity,omitempty"`
	Link      string      `json:"link,omitempty"`
	MaxLines  *int        `json:"maxLines,omitempty"`
	MinScale  *float64    `json:"minScale,omitempty"`
	TextAlign string      `json:"textAlign,omitempty"`
	Accent    bool        `json:"accent,omitempty"`

	// imageData and imagePath are an image's source, which Update writes
	// beside the timeline, naming it in Image.
	imageData []byte
	imagePath string
}

// font is a text's font: a size, or a text style, which follows the text
// size of the system, with a weight, a design and variants. Views take
// the font of the view holding them, and change parts of it.
type font struct {
	Size   *float64 `json:"size,omitempty"`
	Style  string   `json:"style,omitempty"`
	Weight int      `json:"weight,omitempty"`
	Design string   `json:"design,omitempty"`
	Italic bool     `json:"italic,omitempty"`
	Digits bool     `json:"digits,omitempty"`
}

func leaf(t string) View { return View{&node{T: t}} }

// with returns a copy of v changed by fn.
func (v View) with(fn func(n *node)) View {
	if v.n == nil {
		return v
	}
	c := *v.n
	if c.Font != nil {
		f := *c.Font
		c.Font = &f
	}
	fn(&c)
	return View{&c}
}

func (v View) withFont(fn func(f *font)) View {
	return v.with(func(n *node) {
		if n.Font == nil {
			n.Font = &font{}
		}
		fn(n.Font)
	})
}

// Text shows a text.
func Text(s string) View {
	v := leaf("text")
	v.n.Text = s
	return v
}

// Textf shows a text formatted as fmt.Sprintf does.
func Textf(format string, args ...any) View { return Text(fmt.Sprintf(format, args...)) }

// Symbol shows one of Apple's SF Symbols by name, as "star.fill", sized
// by the font and colored by the text color.
func Symbol(name string) View {
	v := leaf("symbol")
	v.n.Symbol = name
	return v
}

// Image shows an image of PNG, JPEG, HEIF or any format Apple's systems
// read, scaled to fit its space (Fit). The extension reads it at the
// size the widget shows it, as the system bounds the memory of widgets.
func Image(data []byte) View {
	v := leaf("image")
	v.n.imageData = data
	return v
}

// ImageFile is Image of the file at path, which Update reads.
func ImageFile(path string) View {
	v := leaf("image")
	v.n.imagePath = path
	return v
}

// DateStyle is how Date shows a time.
type DateStyle string

// The styles of Date. Relative, Offset and Timer change by themselves as
// time passes, without entries for each second.
const (
	// TimeOfDay shows the time, as "11:23 PM".
	TimeOfDay DateStyle = "time"
	// Day shows the date, as "June 3, 2026".
	Day DateStyle = "date"
	// Relative shows the time from now, as "2 hr, 23 min".
	Relative DateStyle = "relative"
	// Offset shows the time from now with its sign, as "+2 hours".
	Offset DateStyle = "offset"
	// Timer counts the time from or to it, as "36:59:01".
	Timer DateStyle = "timer"
)

// Date shows a time, in the language and the formats of the system.
func Date(t time.Time, style DateStyle) View {
	v := leaf("date")
	sec := float64(t.UnixNano()) / 1e9
	v.n.Date, v.n.DateStyle = &sec, string(style)
	return v
}

// Column stacks views from top to bottom.
func Column(views ...View) View { return stack("column", views) }

// Row stacks views from the start to the end of the line, left to right
// in a language written so.
func Row(views ...View) View { return stack("row", views) }

// Stack lays views over each other, the last in front.
func Stack(views ...View) View { return stack("stack", views) }

func stack(t string, views []View) View {
	v := leaf(t)
	for _, c := range views {
		if c.n != nil {
			v.n.Children = append(v.n.Children, c.n)
		}
	}
	return v
}

// Spacer takes the space left along the axis of the row or column holding
// it, pushing its siblings apart.
func Spacer() View { return leaf("spacer") }

// Divider draws a thin line across the row or column holding it.
func Divider() View { return leaf("divider") }

// Rectangle fills its space with a color; Radius rounds its corners.
func Rectangle(c Color) View {
	v := leaf("rectangle")
	v.n.Color = &c
	return v
}

// Progress shows a bar filled to value, from 0 to 1, in the text color,
// as wide as its space.
func Progress(value float64) View {
	v := leaf("progress")
	value = min(max(value, 0), 1)
	v.n.Value = &value
	return v
}

// Gauge shows a ring filled to value, from 0 to 1, in the text color, 44
// points across unless sized.
func Gauge(value float64) View {
	v := leaf("gauge")
	value = min(max(value, 0), 1)
	v.n.Value = &value
	return v
}

// Align places views at the start, the center or the end of their space.
type Align uint8

const (
	// Start is the top of a column, the start of a row: its left in a
	// language written left to right.
	Start Align = iota
	Center
	End
)

func (a Align) String() string {
	switch a {
	case Start:
		return "start"
	case End:
		return "end"
	}
	return "center"
}

// Gap sets the space between the children of a row or a column.
func (v View) Gap(g float64) View { return v.with(func(n *node) { n.Gap = &g }) }

// AlignItems places the children of a column across it, those of a row up
// and down, and the layers of a Stack both ways; and the content of a view
// that fills more space than it needs, across. The default is Center.
func (v View) AlignItems(a Align) View { return v.with(func(n *node) { n.Items = a.String() }) }

// Justify places the content of a view that fills more space than it
// needs along its axis: down a column (and other views), along a row.
// The default is Center.
func (v View) Justify(a Align) View { return v.with(func(n *node) { n.Justify = a.String() }) }

// Center centers the content of a view both ways, as it is by default.
func (v View) Center() View { return v.AlignItems(Center).Justify(Center) }

// Padding sets the space inside a view's edges, around its content.
func (v View) Padding(p float64) View {
	return v.with(func(n *node) { n.Padding = &[4]float64{p, p, p, p} })
}

// PaddingX sets the space inside a view's start and end edges.
func (v View) PaddingX(p float64) View {
	return v.with(func(n *node) {
		e := padding(n)
		e[1], e[3] = p, p
	})
}

// PaddingY sets the space inside a view's top and bottom edges.
func (v View) PaddingY(p float64) View {
	return v.with(func(n *node) {
		e := padding(n)
		e[0], e[2] = p, p
	})
}

func padding(n *node) *[4]float64 {
	if n.Padding == nil {
		n.Padding = &[4]float64{}
	} else {
		e := *n.Padding
		n.Padding = &e
	}
	return n.Padding
}

// Width sets a view's width, its padding included, which Fill and
// FillWidth do not change.
func (v View) Width(w float64) View { return v.with(func(n *node) { n.Width = &w }) }

// Height sets a view's height, its padding included, which Fill and
// FillHeight do not change.
func (v View) Height(h float64) View { return v.with(func(n *node) { n.Height = &h }) }

// Fill makes a view as large as the space it is given.
func (v View) Fill() View { return v.with(func(n *node) { n.FillW, n.FillH = true, true }) }

// FillWidth makes a view as wide as the space it is given.
func (v View) FillWidth() View { return v.with(func(n *node) { n.FillW = true }) }

// FillHeight makes a view as tall as the space it is given.
func (v View) FillHeight() View { return v.with(func(n *node) { n.FillH = true }) }

// Background fills a view's box, its padding included, with a color.
func (v View) Background(c Color) View { return v.with(func(n *node) { n.BG = &c }) }

// Radius rounds the corners of a view's background, and clips the view to
// them.
func (v View) Radius(r float64) View { return v.with(func(n *node) { n.Radius = &r }) }

// Opacity makes a view translucent, from 0 (invisible) to 1.
func (v View) Opacity(o float64) View {
	o = min(max(o, 0), 1)
	return v.with(func(n *node) { n.Opacity = &o })
}

// TextColor colors the text, the symbols, the bars and the rings of a
// view and of the views it holds.
func (v View) TextColor(c Color) View { return v.with(func(n *node) { n.Color = &c }) }

// TextStyle is a font of the system's styles, whose size follows the text
// size the user chose.
type TextStyle string

// The styles of text, from the largest.
const (
	LargeTitle  TextStyle = "largeTitle"
	Title       TextStyle = "title"
	Title2      TextStyle = "title2"
	Title3      TextStyle = "title3"
	Headline    TextStyle = "headline"
	Subheadline TextStyle = "subheadline"
	Body        TextStyle = "body"
	Callout     TextStyle = "callout"
	Footnote    TextStyle = "footnote"
	Caption     TextStyle = "caption"
	Caption2    TextStyle = "caption2"
)

// TextStyle sets the font's size to a style's, as Headline, in place of
// FontSize.
func (v View) TextStyle(s TextStyle) View {
	return v.withFont(func(f *font) { f.Style, f.Size = string(s), nil })
}

// FontSize sets the font's size in points, in place of TextStyle.
func (v View) FontSize(size float64) View {
	return v.withFont(func(f *font) { f.Size, f.Style = &size, "" })
}

// FontWeight sets the font's weight, from 100 (thin) to 900 (black); 400
// is regular and 700 bold.
func (v View) FontWeight(w int) View {
	w = min(max(w, 100), 900)
	return v.withFont(func(f *font) { f.Weight = w })
}

// Bold sets the font's weight to 700.
func (v View) Bold() View { return v.FontWeight(700) }

// Italic slants the font.
func (v View) Italic() View { return v.withFont(func(f *font) { f.Italic = true }) }

// FontDesign is a design of the system's font.
type FontDesign string

// The designs of the system's font.
const (
	DefaultDesign FontDesign = "default"
	Rounded       FontDesign = "rounded"
	Serif         FontDesign = "serif"
	Monospaced    FontDesign = "monospaced"
)

// FontDesign sets the design of the font, as Rounded.
func (v View) FontDesign(d FontDesign) View {
	return v.withFont(func(f *font) { f.Design = string(d) })
}

// MonospacedDigits gives the font's digits one width, so that numbers
// that change keep their width.
func (v View) MonospacedDigits() View { return v.withFont(func(f *font) { f.Digits = true }) }

// TextAlign aligns the lines of a text of several lines.
func (v View) TextAlign(a Align) View { return v.with(func(n *node) { n.TextAlign = a.String() }) }

// MaxLines limits a text to n lines, ending it with an ellipsis.
func (v View) MaxLines(lines int) View { return v.with(func(n *node) { n.MaxLines = &lines }) }

// MinScale lets a text shrink down to a fraction of its font's size to
// fit its space, before it is cut.
func (v View) MinScale(s float64) View {
	s = min(max(s, 0.01), 1)
	return v.with(func(n *node) { n.MinScale = &s })
}

// Fit says how an image fills its space.
type Fit uint8

const (
	// Contain scales the image to fit inside its space, keeping its
	// aspect ratio.
	Contain Fit = iota
	// Cover scales the image to cover all of its space, cropping it.
	Cover
)

// Fit sets how an image fills its space: Contain by default.
func (v View) Fit(f Fit) View {
	return v.with(func(n *node) {
		n.Fit = ""
		if f == Cover {
			n.Fit = "cover"
		}
	})
}

// Link opens the app with url when the view is tapped, as Entry.URL does
// for the whole widget. Small widgets and those of the Lock Screen open
// only their entry's URL.
func (v View) Link(url string) View { return v.with(func(n *node) { n.Link = url }) }

// Accented puts a view among those that take the accent color where the
// system tints widgets, as on the Lock Screen and in iOS 18's tinted Home
// Screen, where the others take its secondary color.
func (v View) Accented() View { return v.with(func(n *node) { n.Accent = true }) }
