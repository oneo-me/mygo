// Package widget shows the app's content in widgets: on the Home Screen
// and the Lock Screen of iPhones and iPads, and on the desktop and in
// Notification Center of Macs (WidgetKit).
//
// The app declares the kinds of its widgets in mygo.json ("widgets"),
// which `mygo build` and `mygo dev` turn into a widget extension of the
// macOS and iOS apps they make, and gives each kind what to show with
// Update: a timeline of views, which the system shows whether the app
// runs or not.
//
//	widget.Update("counter", widget.Entry{
//		View: widget.Column(
//			widget.Text("Count").TextStyle(widget.Caption).TextColor(widget.Secondary),
//			widget.Textf("%d", n).FontSize(44).Bold().MonospacedDigits(),
//		).Gap(4),
//		URL: "counter://open",
//	})
//
// The extension draws the views with SwiftUI and runs none of the app's
// code: a widget shows what the app wrote last, until it writes again,
// and taps open the app (Entry.URL, View.Link), whose URL schemes take
// them to mygo.App.OnOpenURL. Update can be called from any goroutine,
// before App.Run too. Elsewhere than macOS and iOS, and in an app not
// built with its widgets, it reports why it cannot show them.
package widget

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Family is a size and place a widget shows in.
type Family string

// The families of widgets. Small, Medium and Large show on the Home
// Screen and the Mac's desktop, ExtraLarge on iPads' Home Screen and
// Macs, and Circular, Rectangular and Inline on the Lock Screen (iOS
// 16 and later). mygo.json lists the families each kind supports.
const (
	Small       Family = "small"
	Medium      Family = "medium"
	Large       Family = "large"
	ExtraLarge  Family = "extraLarge"
	Circular    Family = "circular"
	Rectangular Family = "rectangular"
	Inline      Family = "inline"
)

// Entry is what widgets of a kind show from a date on.
type Entry struct {
	// Date is when the entry shows, until the next one's; the zero time
	// is the time of Update.
	Date time.Time
	// View is what the widgets show, and Families what those of a family
	// show in its place.
	View     View
	Families map[Family]View
	// Background fills the widget behind its view, past the margins the
	// system keeps around the view: a Rectangle of a color, or an image
	// to Cover. Without one, the widget has the system's background.
	Background View
	// URL opens the app with it when the widget is tapped, a link of one
	// of its URL schemes (urlSchemes in mygo.json) for App.OnOpenURL.
	// Without it, a tap opens the app.
	URL string
}

// ErrUnsupported is what Update reports where there are no widgets:
// elsewhere than macOS and iOS.
var ErrUnsupported = fmt.Errorf("widget: widgets need macOS or iOS: %w", errors.ErrUnsupported)

// ErrNoWidgets is what Update reports in an app that has no widget
// extension: one run with go run, or built without widgets in mygo.json.
var ErrNoWidgets = errors.New("widget: the app has no widgets: list them in mygo.json and build the app with mygo build or mygo dev")

var kindRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Update makes the widgets of a kind show a timeline of entries, from the
// one whose date passed last, and asks the system to show it at once.
// The kind is one of mygo.json's widgets. Without entries, the widgets
// show their placeholder: the kind's name and description.
//
// The system lets an app in the foreground update its widgets as often
// as it likes, and one in the background a few dozen times a day.
func Update(kind string, entries ...Entry) error {
	if !kindRe.MatchString(kind) {
		return fmt.Errorf("widget: %q is not a kind of widget: letters, digits, '.', '_' and '-', up to 64", kind)
	}
	dir, err := kindDir(kind)
	if err != nil {
		return err
	}
	if err := writeTimeline(dir, entries, time.Now()); err != nil {
		return fmt.Errorf("widget: %w", err)
	}
	return reload(kind)
}

// Available reports whether the app has widgets that Update can update:
// whether it runs on macOS or iOS, built with its widgets.
func Available() bool {
	_, err := containerDir()
	return err == nil
}
