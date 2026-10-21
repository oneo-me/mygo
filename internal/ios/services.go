//go:build ios

package ios

import (
	"errors"
	"math"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/transfer"
)

// clipboard is the general pasteboard. Its items are dictionaries of
// uniform type identifiers to strings or data; what the app writes is
// materialized at once, as iOS asks no provider for it later.
type clipboard struct{}

func pasteboard() id { return send(class("UIPasteboard"), "generalPasteboard") }

// pasteboardTypes are the uniform type identifiers of transfer's formats.
var pasteboardTypes = map[transfer.Format]string{
	transfer.Text:     "public.utf8-plain-text",
	transfer.HTML:     "public.html",
	transfer.URIList:  "public.url",
	transfer.PNG:      "public.png",
	transfer.Markdown: "net.daringfireball.markdown",
}

func pasteboardType(f transfer.Format) string {
	if t, ok := pasteboardTypes[f]; ok {
		return t
	}
	return string(f)
}

// pasteboardFormat returns the format of a type, "" for one of none.
func pasteboardFormat(t string) transfer.Format {
	switch t {
	case "public.utf8-plain-text", "public.plain-text", "public.text", "public.utf16-plain-text":
		return transfer.Text
	case "public.html":
		return transfer.HTML
	case "public.url":
		return transfer.URIList
	case "public.png":
		return transfer.PNG
	case "net.daringfireball.markdown":
		return transfer.Markdown
	}
	if strings.Contains(t, "/") {
		return transfer.Format(t) // a MIME type the app wrote
	}
	return ""
}

// valueBytes returns the bytes of a pasteboard item's value: an NSString's
// text, an NSURL's string, an NSData's bytes, a UIImage's PNG.
func valueBytes(v id) ([]byte, bool) {
	switch {
	case v == 0:
		return nil, false
	case sendBool(v, "isKindOfClass:", uintptr(class("NSString"))):
		return []byte(goString(v)), true
	case sendBool(v, "isKindOfClass:", uintptr(class("NSURL"))):
		return []byte(goString(send(v, "absoluteString"))), true
	case sendBool(v, "isKindOfClass:", uintptr(class("NSData"))):
		return goBytes(v), true
	case sendBool(v, "isKindOfClass:", uintptr(class("UIImage"))):
		return goBytes(uiImagePNG(v)), true
	}
	return nil, false
}

func (clipboard) ReadData(formats []transfer.Format) (transfer.Data, error) {
	var items []transfer.Item
	withPool(func() {
		for _, item := range arrayItems(send(pasteboard(), "items")) {
			var reps []transfer.Representation
			seen := map[transfer.Format]bool{}
			for _, key := range arrayItems(send(item, "allKeys")) {
				f := pasteboardFormat(goString(key))
				if f == "" || seen[f] || formats != nil && !containsFormat(formats, f) {
					continue
				}
				if b, ok := valueBytes(send(item, "objectForKey:", uintptr(key))); ok {
					seen[f] = true
					reps = append(reps, transfer.Bytes(f, b))
				}
			}
			if len(reps) > 0 {
				items = append(items, transfer.NewItem(reps...))
			}
		}
	})
	if len(items) == 0 && formats != nil {
		return transfer.Data{}, transfer.ErrFormat
	}
	return transfer.New(items...), nil
}

func containsFormat(formats []transfer.Format, f transfer.Format) bool {
	for _, x := range formats {
		if x == f {
			return true
		}
	}
	return false
}

func (c clipboard) WriteData(data transfer.Data, released func()) error {
	data, err := data.Materialize(data.Formats())
	if err != nil {
		return err
	}
	withPool(func() {
		var items []id
		for _, item := range data.Items() {
			dict := send(class("NSMutableDictionary"), "dictionary")
			for _, f := range item.Formats() {
				b, err := item.Read(f)
				if err != nil {
					continue
				}
				var v id
				switch f {
				case transfer.Text, transfer.HTML, transfer.Markdown:
					v = nsString(string(b))
				case transfer.URIList:
					v = nsURL(strings.TrimSpace(strings.Split(string(b), "\n")[0]))
				default:
					v = nsData(b)
				}
				if v != 0 {
					send(dict, "setObject:forKey:", uintptr(v), uintptr(nsString(pasteboardType(f))))
				}
			}
			items = append(items, dict)
		}
		send(pasteboard(), "setItems:", uintptr(nsArray(items...)))
	})
	if released != nil {
		released()
	}
	return nil
}

func (clipboard) Formats() []transfer.Format {
	var out []transfer.Format
	withPool(func() {
		for _, t := range arrayItems(send(pasteboard(), "types")) {
			if f := pasteboardFormat(goString(t)); f != "" && !containsFormat(out, f) {
				out = append(out, f)
			}
		}
	})
	return out
}

func (clipboard) Flush() error { return nil }
func (clipboard) Close()       {}

func (clipboard) ReadText() string {
	var s string
	withPool(func() { s = goString(send(pasteboard(), "string")) })
	return s
}

func (clipboard) WriteText(text string) {
	withPool(func() { send(pasteboard(), "setString:", uintptr(nsString(text))) })
}

func (clipboard) ReadHTML() string {
	var s string
	withPool(func() {
		s = string(goBytes(send(pasteboard(), "dataForPasteboardType:", uintptr(nsString("public.html")))))
	})
	return s
}

func (clipboard) WriteHTML(markup string) {
	withPool(func() {
		send(pasteboard(), "setData:forPasteboardType:", uintptr(nsData([]byte(markup))), uintptr(nsString("public.html")))
	})
}

func (clipboard) ReadImage() []byte {
	var b []byte
	withPool(func() {
		if img := send(pasteboard(), "image"); img != 0 {
			b = goBytes(uiImagePNG(img))
		}
	})
	return b
}

func (clipboard) WriteImage(png []byte) error {
	withPool(func() {
		send(pasteboard(), "setData:forPasteboardType:", uintptr(nsData(png)), uintptr(nsString("public.png")))
	})
	return nil
}

func (clipboard) Clear() {
	withPool(func() { send(pasteboard(), "setItems:", uintptr(nsArray())) })
}

func (c clipboard) AvailableFormats() []string {
	var out []string
	for _, f := range c.Formats() {
		out = append(out, string(f))
	}
	return out
}

type shell struct{ b *Backend }

// OpenExternal opens a URL in the app that opens it: Safari, Mail, …
func (s shell) OpenExternal(url string) error {
	if s.b.app == 0 {
		return errUnsupported
	}
	var err error
	withPool(func() {
		u := nsURL(url)
		if u == 0 {
			err = errors.New("mygo: invalid URL " + url)
			return
		}
		send(s.b.app, "openURL:options:completionHandler:", uintptr(u), uintptr(send(class("NSDictionary"), "dictionary")), 0)
	})
	return err
}

func (shell) OpenPath(string) error                { return errUnsupported }
func (shell) ShowItemInFolder(string)              {}
func (shell) TrashItem(string) error               { return errUnsupported }
func (shell) Beep()                                {}
func (shell) FileIcon(string, int) ([]byte, error) { return nil, errUnsupported }
func (shell) OpenApplication(string) error         { return errUnsupported }

type screen struct{ b *Backend }

// Displays returns the main screen, whose work area is its safe area.
func (s screen) Displays() []platform.Display {
	var d platform.Display
	withPool(func() {
		main := send(class("UIScreen"), "mainScreen")
		r := msgRect(main, sel("bounds"))
		d = platform.Display{
			ID:          1,
			Bounds:      platform.Rect{Width: int(math.Round(r.Size.Width)), Height: int(math.Round(r.Size.Height))},
			ScaleFactor: msgFloat(main, sel("scale")),
			Internal:    true,
			Primary:     true,
		}
		d.WorkArea = d.Bounds
		if n := len(s.b.shown); n > 0 {
			in := msgInsets(s.b.shown[n-1].root, sel("safeAreaInsets"))
			d.WorkArea = platform.Rect{
				X: int(math.Round(in.Left)), Y: int(math.Round(in.Top)),
				Width:  int(math.Round(r.Size.Width - in.Left - in.Right)),
				Height: int(math.Round(r.Size.Height - in.Top - in.Bottom)),
			}
		}
	})
	return []platform.Display{d}
}

func (screen) CursorPoint() platform.Point { return platform.Point{} }

type theme struct{ b *Backend }

// IsDark reports the appearance the app asked for, else the system's.
func (t theme) IsDark() bool {
	switch t.b.theme {
	case "dark":
		return true
	case "light":
		return false
	}
	var dark bool
	withPool(func() {
		traits := send(send(class("UIScreen"), "mainScreen"), "traitCollection")
		dark = sendInt(traits, "userInterfaceStyle") == 2
	})
	return dark
}

// SetSource gives the windows the appearance the app asked for, which
// UIKit's own views, and the status bar, take.
func (t theme) SetSource(source string) {
	t.b.theme = source
	for _, w := range t.b.windows {
		if w.win != 0 {
			send(w.win, "setOverrideUserInterfaceStyle:", interfaceStyle(source))
		}
	}
	t.b.h.ThemeChanged()
}

func (theme) UIFont() string                        { return "" }
func (theme) FontRendering() platform.FontRendering { return platform.FontRendering{} }

var (
	accessibilityReduceMotion       func() bool
	accessibilityReduceTransparency func() bool
	accessibilityDarkerColors       func() bool
)

// Preferences reads the settings of Accessibility, and the size of text
// the user chose, relative to the usual Large.
func (theme) Preferences() platform.Preferences {
	if accessibilityReduceMotion == nil {
		registerFunc(&accessibilityReduceMotion, libUIKit, "UIAccessibilityIsReduceMotionEnabled")
		registerFunc(&accessibilityReduceTransparency, libUIKit, "UIAccessibilityIsReduceTransparencyEnabled")
		registerFunc(&accessibilityDarkerColors, libUIKit, "UIAccessibilityDarkerSystemColorsEnabled")
	}
	p := platform.Preferences{
		ReduceMotion:       accessibilityReduceMotion(),
		ReduceTransparency: accessibilityReduceTransparency(),
		HighContrast:       accessibilityDarkerColors(),
		TextScale:          1,
	}
	withPool(func() {
		// The point size of the body style at the user's size, over its
		// 17 points at the usual one.
		style := constString(libUIKit, "UIFontTextStyleBody")
		if font := send(class("UIFont"), "preferredFontForTextStyle:", uintptr(style)); font != 0 {
			if size := msgFloat(font, sel("pointSize")); size > 0 {
				p.TextScale = size / 17
			}
		}
	})
	return p
}

type power struct{ b *Backend }

// Watch does nothing: an iOS app hears of no sleep but its own
// (DidResignActive).
func (power) Watch() {}

// KeepAwake keeps the screen from dimming and locking, the idle timer off,
// until every hold is released.
func (p power) KeepAwake(bool, string) func() {
	b := p.b
	b.runOnMain(func() {
		b.awake++
		if b.app != 0 {
			send(b.app, "setIdleTimerDisabled:", 1)
		}
	})
	released := false
	return func() {
		b.runOnMain(func() {
			if released {
				return
			}
			released = true
			if b.awake--; b.awake == 0 && b.app != 0 {
				send(b.app, "setIdleTimerDisabled:", 0)
			}
		})
	}
}

// OnBattery reports whether the device runs on its battery, not plugged in.
func (power) OnBattery() bool {
	var on bool
	withPool(func() {
		device := send(class("UIDevice"), "currentDevice")
		send(device, "setBatteryMonitoringEnabled:", 1)
		const unplugged = 1 // UIDeviceBatteryStateUnplugged
		on = sendInt(device, "batteryState") == unplugged
	})
	return on
}

func (power) IdleTime() time.Duration { return 0 }

// ReplaceFile replaces target with NSFileManager's
// replaceItemAtURL:withItemAtURL:…, which keeps the target's metadata.
func (*Backend) ReplaceFile(target, replacement string) error {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return os.Rename(replacement, target)
	}
	load()
	var err error
	withPool(func() {
		fm := send(class("NSFileManager"), "defaultManager")
		var nsErr id
		ok := sendBool(fm, "replaceItemAtURL:withItemAtURL:backupItemName:options:resultingItemURL:error:",
			uintptr(fileURL(target)), uintptr(fileURL(replacement)), 0, 0, 0, uintptr(unsafe.Pointer(&nsErr)))
		if !ok {
			msg := "cannot replace the file"
			if nsErr != 0 {
				msg = goString(send(nsErr, "localizedDescription"))
			}
			err = &os.LinkError{Op: "replace", Old: replacement, New: target, Err: errors.New(msg)}
		}
	})
	return err
}

// NewFileWatch is not supported: iOS has no FSEvents, and its apps see
// only their own files.
func (*Backend) NewFileWatch(func(platform.FileWatchNotice)) (platform.FileWatch, error) {
	return nil, platform.ErrUnsupported
}
