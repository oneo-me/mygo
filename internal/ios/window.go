//go:build ios

package ios

import (
	"math"
	"slices"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// A window is a UIWindow of the scene, filling it, whose root view
// controller's view holds the surface the content draws on and, under
// it, the web views the content shows. Windows stack in the order they
// were shown, the last in front and the key window; the scene's size, not
// the options', sizes them. A window made before the scene connected
// shows once it has.
type window struct {
	b     *Backend
	h     platform.WindowHandler
	opts  *platform.WindowOptions
	title string
	win   id // the UIWindow, while the window is in a scene
	vc    id // MyGoViewController
	root  id // its view
	s     *surface

	visible, closed, key bool
}

// uiViewAutoresizingFlexibleSize is UIViewAutoresizingFlexibleWidth |
// UIViewAutoresizingFlexibleHeight.
const uiViewAutoresizingFlexibleSize = 2 | 16

func (b *Backend) NewWindow(o *platform.WindowOptions, h platform.WindowHandler) (platform.Window, error) {
	if !o.Surface {
		return nil, errNoPage
	}
	w := &window{b: b, h: h, opts: o, title: o.Title}
	withPool(func() {
		bounds := msgRect(send(class("UIScreen"), "mainScreen"), sel("bounds"))
		w.vc = alloc("MyGoViewController")
		w.root = msgInitRect(send(class("UIView"), "alloc"), sel("initWithFrame:"), bounds)
		send(w.root, "setAutoresizingMask:", uiViewAutoresizingFlexibleSize)
		w.setBackground(o.BackgroundColor, o.Transparent)
		send(w.vc, "setView:", uintptr(w.root))
		w.s = newSurface(w, bounds)
		send(w.root, "addSubview:", uintptr(w.s.view))
	})
	b.windows = append(b.windows, w)
	w.attach()
	return w, nil
}

// setBackground fills the root view, which shows until the content's
// first frame, with the window's color, else the system's background.
func (w *window) setBackground(c *platform.Color, transparent bool) {
	var color id
	switch {
	case transparent:
		color = send(class("UIColor"), "clearColor")
	case c != nil:
		color = uiColor(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255, float64(c.A)/255)
	default:
		color = send(class("UIColor"), "systemBackgroundColor")
	}
	send(w.root, "setBackgroundColor:", uintptr(color))
}

// attach puts the window in the scene, or on the main screen in an app
// without scenes, unless it is there or there is no scene yet.
func (w *window) attach() {
	b := w.b
	if w.win != 0 || w.closed || b.scene == 0 && (b.app == 0 || usesScenes()) {
		return
	}
	withPool(func() {
		if b.scene != 0 {
			w.win = send(send(class("UIWindow"), "alloc"), "initWithWindowScene:", uintptr(b.scene))
		} else {
			w.win = msgInitRect(send(class("UIWindow"), "alloc"), sel("initWithFrame:"), msgRect(send(class("UIScreen"), "mainScreen"), sel("bounds")))
		}
		send(w.win, "setRootViewController:", uintptr(w.vc))
		send(w.win, "setOverrideUserInterfaceStyle:", interfaceStyle(b.theme))
	})
	if w.visible {
		b.restack()
	}
}

// detach takes the window out of its scene, which went away.
func (w *window) detach() {
	if w.win == 0 {
		return
	}
	send(w.win, "setHidden:", 1)
	send(w.win, "setRootViewController:", 0)
	release(w.win)
	w.win = 0
	w.s.occludedChanged()
}

// restack orders the windows shown, the last in front, which takes the
// keyboard, and hides the others.
func (b *Backend) restack() {
	withPool(func() {
		for i, w := range b.shown {
			if w.win == 0 {
				continue
			}
			msgSetFloat(w.win, sel("setWindowLevel:"), float64(i))
			if sendBool(w.win, "isHidden") {
				send(w.win, "setHidden:", 0)
			}
		}
		if n := len(b.shown); n > 0 {
			if top := b.shown[n-1]; top.win != 0 && !sendBool(top.win, "isKeyWindow") {
				send(top.win, "makeKeyWindow")
			}
		}
	})
	for _, w := range b.windows {
		w.s.occludedChanged()
	}
}

// front reports whether the window is the one in front.
func (w *window) front() bool {
	n := len(w.b.shown)
	return n > 0 && w.b.shown[n-1] == w
}

func (w *window) keyChanged(key bool) {
	if key == w.key {
		return
	}
	w.key = key
	if key {
		w.h.Focused()
		if !w.s.hasResponder() {
			send(w.s.view, "becomeFirstResponder")
		}
	} else {
		w.h.Blurred()
	}
	w.s.checkFocus()
}

func (w *window) Handle() uintptr           { return uintptr(w.win) }
func (w *window) Surface() platform.Surface { return w.s }

func (w *window) SetTitle(title string) {
	w.title = title
	withPool(func() { send(w.vc, "setTitle:", uintptr(nsString(title))) })
}

func (w *window) Title() string { return w.title }

func (w *window) Show() {
	if w.closed {
		return
	}
	w.visible = true
	w.b.shown = append(slices.DeleteFunc(w.b.shown, func(x *window) bool { return x == w }), w)
	w.b.restack()
}

func (w *window) ShowInactive() { w.Show() }
func (w *window) Focus()        { w.Show() }

func (w *window) Hide() {
	if !w.visible {
		return
	}
	w.visible = false
	w.b.shown = slices.DeleteFunc(w.b.shown, func(x *window) bool { return x == w })
	if w.win != 0 {
		send(w.win, "setHidden:", 1)
	}
	w.b.restack()
}

func (w *window) IsVisible() bool { return w.visible }
func (w *window) IsFocused() bool { return w.key }

// Blur does nothing: the window in front has the keyboard.
func (w *window) Blur() {}

func (w *window) Close() {
	if w.closed {
		return
	}
	w.closed = true
	b := w.b
	b.windows = slices.DeleteFunc(b.windows, func(x *window) bool { return x == w })
	b.shown = slices.DeleteFunc(b.shown, func(x *window) bool { return x == w })
	w.s.destroy()
	if w.win != 0 {
		send(w.win, "setHidden:", 1)
		send(w.win, "setRootViewController:", 0)
		release(w.win)
		w.win = 0
	}
	release(w.vc)
	release(w.root)
	b.restack()
	w.h.Closed()
}

// Bounds is the window's frame on the screen, which it fills.
func (w *window) Bounds() platform.Rect {
	r := msgRect(w.root, sel("bounds"))
	if w.win != 0 {
		r = msgRect(w.win, sel("frame"))
	}
	return platform.Rect{
		X: int(math.Round(r.Origin.X)), Y: int(math.Round(r.Origin.Y)),
		Width: int(math.Round(r.Size.Width)), Height: int(math.Round(r.Size.Height)),
	}
}

func (w *window) ContentBounds() platform.Rect { return w.Bounds() }

func (w *window) SetBackgroundColor(c platform.Color) {
	withPool(func() { w.setBackground(&c, false) })
}

// The scene sizes and places windows, and their look is the system's:
// what sets them does nothing.
func (w *window) SetRepresentedFile(string)                  {}
func (w *window) SetDocumentEdited(bool)                     {}
func (w *window) SetBounds(platform.Rect)                    {}
func (w *window) SetContentBounds(platform.Rect)             {}
func (w *window) SetMinimumSize(platform.Size)               {}
func (w *window) SetMaximumSize(platform.Size)               {}
func (w *window) SetResizable(bool)                          {}
func (w *window) IsResizable() bool                          { return false }
func (w *window) SetMovable(bool)                            {}
func (w *window) IsMovable() bool                            { return false }
func (w *window) SetMinimizable(bool)                        {}
func (w *window) IsMinimizable() bool                        { return false }
func (w *window) SetMaximizable(bool)                        {}
func (w *window) IsMaximizable() bool                        { return false }
func (w *window) SetClosable(bool)                           {}
func (w *window) IsClosable() bool                           { return false }
func (w *window) SetAlwaysOnTop(bool)                        {}
func (w *window) IsAlwaysOnTop() bool                        { return false }
func (w *window) Minimize()                                  {}
func (w *window) IsMinimized() bool                          { return false }
func (w *window) Maximize()                                  {}
func (w *window) Unmaximize()                                {}
func (w *window) IsMaximized() bool                          { return false }
func (w *window) Restore()                                   {}
func (w *window) SetFullScreen(bool)                         {}
func (w *window) IsFullScreen() bool                         { return true }
func (w *window) Center()                                    {}
func (w *window) SetOpacity(float64)                         {}
func (w *window) Opacity() float64                           { return 1 }
func (w *window) SetHasShadow(bool)                          {}
func (w *window) HasShadow() bool                            { return false }
func (w *window) SetIgnoreMouseEvents(bool)                  {}
func (w *window) SetContentProtection(bool)                  {}
func (w *window) SetVibrancy(string)                         {}
func (w *window) SetProgressBar(string, float64)             {}
func (w *window) FlashFrame(bool)                            {}
func (w *window) SetSkipTaskbar(bool)                        {}
func (w *window) SetVisibleOnAllWorkspaces(bool)             {}
func (w *window) SetIcon([]byte) error                       { return nil }
func (w *window) SetMenu(*platform.Menu)                     {}
func (w *window) SetAutoHideMenu(bool)                       {}
func (w *window) TitleBar() platform.TitleBar                { return platform.TitleBar{} }
func (w *window) StartDrag()                                 {}
func (w *window) TitleBarDoubleClicked()                     {}
func (w *window) SetMaximizeButton(platform.RectF)           {}
func (w *window) PlaceFlyout(platform.Flyout, platform.Size) {}

// The page of a window is native UI's: none to load.
func (w *window) WebViewHandle() uintptr                                   { return 0 }
func (w *window) LoadURL(string)                                           {}
func (w *window) LoadHTML(string, string)                                  {}
func (w *window) LoadFile(string, string)                                  {}
func (w *window) Reload(bool)                                              {}
func (w *window) StopLoading()                                             {}
func (w *window) GoBack()                                                  {}
func (w *window) GoForward()                                               {}
func (w *window) CanGoBack() bool                                          { return false }
func (w *window) CanGoForward() bool                                       { return false }
func (w *window) URL() string                                              { return "" }
func (w *window) IsLoading() bool                                          { return false }
func (w *window) Eval(string)                                              {}
func (w *window) SetUserScripts([]platform.UserScript)                     {}
func (w *window) SetZoom(float64)                                          {}
func (w *window) Zoom() float64                                            { return 1 }
func (w *window) SetUserAgent(string)                                      {}
func (w *window) UserAgent() string                                        { return "" }
func (w *window) OpenDevTools()                                            {}
func (w *window) CloseDevTools()                                           {}
func (w *window) IsDevToolsOpened() bool                                   { return false }
func (w *window) DroppedFiles() []string                                   { return nil }
func (w *window) Print()                                                   {}
func (w *window) CapturePage(cb func([]byte, error))                       { cb(nil, errNoPage) }
func (w *window) PrintToPDF(_ platform.PDFOptions, cb func([]byte, error)) { cb(nil, errNoPage) }

func (w *window) CallAsyncFunction(_ string, cb func(string, error)) { cb("", errNoPage) }

func interfaceStyle(theme string) uintptr {
	switch theme {
	case "light":
		return 1 // UIUserInterfaceStyleLight
	case "dark":
		return 2 // UIUserInterfaceStyleDark
	}
	return 0 // UIUserInterfaceStyleUnspecified
}

func registerWindowClasses() {
	classDef("MyGoViewController", "UIViewController", nil, nil, []objc.MethodDef{
		// A new appearance, or another size of text the user chose.
		method("traitCollectionDidChange:", func(self id, cmd objc.SEL, previous id) {
			sendSuper(self, "MyGoViewController", cmd, uintptr(previous))
			now := send(self, "traitCollection")
			if previous == 0 || sendBool(now, "hasDifferentColorAppearanceComparedToTraitCollection:", uintptr(previous)) ||
				sendInt(now, "userInterfaceStyle") != sendInt(previous, "userInterfaceStyle") {
				theBackend.h.ThemeChanged()
			}
		}),
	})
}
