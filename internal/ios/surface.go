//go:build ios

package ios

import (
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/transfer"
)

// The surface of a window is a view filling its root view, over the web
// views, whose layer the Metal renderer puts its CAMetalLayer in. It turns
// touches and the keys of hardware keyboards into surface events, paces
// frames with a display link, and keeps the content clear of the status
// bar, the home indicator and the software keyboard (SafeArea). A text
// input view at the caret (textinput.go) has the keyboard while the
// content edits text, which shows the software keyboard.

// frameRateRange is CAFrameRateRange.
type frameRateRange struct {
	_                   structs.HostLayout
	Min, Max, Preferred float32
}

var (
	surfaceOnce       sync.Once
	msgFrameRateRange func(obj id, sel objc.SEL, r frameRateRange)
	cgColorSpaceSRGB  uintptr
	cgImageCreate     uintptr
	cgImageRelease    uintptr
	cgProviderCreate  uintptr
	cgProviderRelease uintptr
	cfDataCreate      uintptr
)

func loadSurface() {
	surfaceOnce.Do(func() {
		purego.RegisterFunc(&msgFrameRateRange, msgSendAddr)
		cgImageCreate = mustDlsym(libCG, "CGImageCreate")
		cgImageRelease = mustDlsym(libCG, "CGImageRelease")
		cgProviderCreate = mustDlsym(libCG, "CGDataProviderCreateWithCFData")
		cgProviderRelease = mustDlsym(libCG, "CGDataProviderRelease")
		cfDataCreate = mustDlsym(libCF, "CFDataCreate")
		p := mustDlsym(libCG, "kCGColorSpaceSRGB")
		name := **(**uintptr)(unsafe.Pointer(&p))
		cgColorSpaceSRGB, _, _ = purego.SyscallN(mustDlsym(libCG, "CGColorSpaceCreateWithName"), name)
	})
}

type surface struct {
	w     *window
	view  id // MyGoSurfaceView
	input id // MyGoTextInputView
	link  id // CADisplayLink

	// The display link runs while frames follow each other (linkRunning),
	// and due is whether its next tick draws one; deferred tells that the
	// content left the last paced frame to draw later (FrameNow).
	linkRunning, due, deferred bool

	// size is the width, height and scale of the last SurfaceResize, and
	// insets the safe area then.
	size   [3]float64
	insets UIEdgeInsets

	// focused tells that the content has the keyboard, which it was told;
	// occluded that nothing of it shows, which it can ask.
	focused, occluded bool

	// touch is the touch the surface follows, the first of a gesture, and
	// touchPointer that it is a mouse's or a trackpad's.
	touch        id
	touchPointer bool
	touchButton  int
	// keys are the keys down that UIKit has as well, by their HID usage,
	// and repeat the key that repeats while held (keys.go).
	keys   map[int]bool
	repeat keyRepeat

	// The text input (textinput.go): the state the content set, the input
	// method's composition and its selection in it.
	ti            platform.TextInputState
	marked        string
	markedSel     nsRange
	inputDelegate id
	tokenizer     id
	// editing tells that an input method's call is under way, whose
	// changes the input method knows of.
	editing bool

	// placed are where the content shows the window's web views, under
	// it, and webViews all of them.
	placed   []platform.WebViewPlacement
	webViews []*webView
	// pressedAt is the time of the touch event that last pressed a web
	// view, which tells the content once.
	pressedAt float64
	// scrolled is how far the scroll of a trackpad or a mouse wheel under
	// way went (scrollGesture).
	scrolled CGPoint

	// Accessibility (access.go): whether the content describes its
	// frames, the elements of its nodes, the NSArray of the elements and
	// web views VoiceOver reads, and the IDs of the nodes in it.
	accessOn    bool
	elements    map[uint64]*accessElement
	accessArray id
	accessObjs  []id
	accessIDs   []uint64
}

func newSurface(w *window, bounds CGRect) *surface {
	loadSurface()
	s := &surface{w: w, keys: map[int]bool{}}
	s.view = msgInitRect(send(class("MyGoSurfaceView"), "alloc"), sel("initWithFrame:"), bounds)
	send(s.view, "setAutoresizingMask:", uiViewAutoresizingFlexibleSize)
	send(s.view, "setOpaque:", 0)
	send(s.view, "setBackgroundColor:", uintptr(send(class("UIColor"), "clearColor")))
	send(s.view, "setMultipleTouchEnabled:", 0)
	// The text input view, at the caret: input methods place their
	// candidates by it. It takes no touches.
	s.input = msgInitRect(send(class("MyGoTextInputView"), "alloc"), sel("initWithFrame:"), rect(0, 0, 1, 1))
	send(s.input, "setUserInteractionEnabled:", 0)
	send(s.input, "setBackgroundColor:", uintptr(send(class("UIColor"), "clearColor")))
	send(s.view, "addSubview:", uintptr(s.input))
	s.addPointerGestures()
	s.link = retain(send(class("CADisplayLink"), "displayLinkWithTarget:selector:", uintptr(s.view), uintptr(sel("mygoTick:"))))
	send(s.link, "setPaused:", 1)
	if respondsTo(s.link, "setPreferredFrameRateRange:") {
		// ProMotion's displays refresh up to 120 times a second, which
		// apps get with CADisableMinimumFrameDurationOnPhone.
		fps := float32(max(s.RefreshRate(), 60))
		msgFrameRateRange(s.link, sel("setPreferredFrameRateRange:"), frameRateRange{Min: 60, Max: fps, Preferred: fps})
	}
	send(s.link, "addToRunLoop:forMode:", uintptr(send(class("NSRunLoop"), "mainRunLoop")), kCFRunLoopCommonModes)
	w.b.bySurface[s.view] = s
	w.b.bySurface[s.input] = s
	return s
}

func (s *surface) destroy() {
	s.repeat.stop()
	s.destroyAccess()
	for len(s.webViews) > 0 {
		s.webViews[len(s.webViews)-1].close()
	}
	if s.link != 0 {
		send(s.link, "invalidate")
		release(s.link)
		s.link = 0
	}
	release(s.tokenizer)
	s.tokenizer = 0
	delete(s.w.b.bySurface, s.view)
	delete(s.w.b.bySurface, s.input)
	if sendBool(s.input, "isFirstResponder") {
		send(s.input, "resignFirstResponder")
	}
	send(s.input, "removeFromSuperview")
	release(s.input)
	send(s.view, "removeFromSuperview")
	release(s.view)
}

func (s *surface) send(ev platform.SurfaceEvent) bool {
	if s.w.closed {
		return false
	}
	return s.w.h.SurfaceEvent(ev)
}

func (s *surface) Native() platform.SurfaceNative {
	return platform.SurfaceNative{View: uintptr(s.view), Layer: uintptr(send(s.view, "layer"))}
}

func (s *surface) scale() float64 {
	if f := msgFloat(send(s.view, "traitCollection"), sel("displayScale")); f > 0 {
		return f
	}
	if f := msgFloat(send(class("UIScreen"), "mainScreen"), sel("scale")); f > 0 {
		return f
	}
	return 1
}

func (s *surface) Size() (float64, float64, float64) {
	b := msgRect(s.view, sel("bounds"))
	return b.Size.Width, b.Size.Height, s.scale()
}

// SafeArea is the view's safe area, past the status bar, the sensor
// housing and the home indicator, and above the software keyboard.
func (s *surface) SafeArea() (top, right, bottom, left float64) {
	in := s.safeArea()
	return in.Top, in.Right, in.Bottom, in.Left
}

func (s *surface) safeArea() UIEdgeInsets {
	in := msgInsets(s.view, sel("safeAreaInsets"))
	if kb := s.w.b.keyboard; kb.Size.Height > 0 && send(s.view, "window") != 0 {
		space := send(send(class("UIScreen"), "mainScreen"), "coordinateSpace")
		local := msgConvertRect(s.view, sel("convertRect:fromCoordinateSpace:"), kb, space)
		height := msgRect(s.view, sel("bounds")).Size.Height
		if over := height - local.Origin.Y; local.Origin.Y < height && over > in.Bottom {
			in.Bottom = over
		}
	}
	return in
}

// layout tells the content of a new size, or scale, and draws its frame
// at once, so that it shows with the change, as the screen rotates.
func (s *surface) layout() {
	w, h, scale := s.Size()
	insets := s.safeArea()
	if [3]float64{w, h, scale} == s.size && insets == s.insets {
		return
	}
	s.size, s.insets = [3]float64{w, h, scale}, insets
	s.placeInput()
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceResize})
	s.frameNow(false)
}

// insetsChanged tells the content that its safe area changed.
func (s *surface) insetsChanged() {
	if insets := s.safeArea(); insets != s.insets {
		s.insets = insets
		s.send(platform.SurfaceEvent{Kind: platform.SurfaceResize})
		s.RequestFrame()
	}
}

func (s *surface) RequestFrame() {
	if s.w.closed {
		return
	}
	s.due = true
	if !s.linkRunning {
		s.linkRunning = true
		send(s.link, "setPaused:", 0)
	}
}

// FrameNow draws a frame at once, for one the display link's tick left to
// draw until the renderer had a drawable.
func (s *surface) FrameNow() {
	if !s.w.closed {
		s.deferred = false
		s.frameNow(false)
	}
}

// frameNow draws a frame now. In the background, where the GPU takes no
// work from apps, it waits for the app to come back.
func (s *surface) frameNow(paced bool) {
	if s.w.b.background {
		s.due = true
		return
	}
	if s.send(platform.SurfaceEvent{Kind: platform.SurfaceFrame, Paced: paced}) && paced {
		// The content draws it once the display can take it (FrameNow).
		s.deferred = true
	}
}

func (s *surface) tick() {
	if !s.due && !s.deferred {
		// No frame was asked for since the last refresh, nor does the
		// content wait to draw the last one.
		send(s.link, "setPaused:", 1)
		s.linkRunning = false
		return
	}
	s.due = false
	s.frameNow(true)
}

// RefreshRate returns the most frames a second the screen shows.
func (s *surface) RefreshRate() float64 {
	return float64(sendInt(send(class("UIScreen"), "mainScreen"), "maximumFramesPerSecond"))
}

// Occluded reports whether nothing of the window shows: in the background,
// out of its scene, hidden, or under another window.
func (s *surface) Occluded() bool { return s.occluded }

func (s *surface) isOccluded() bool {
	w := s.w
	return w.closed || w.b.background || w.win == 0 || !w.visible || !w.front()
}

// occludedChanged tells the content once some of the window shows again.
func (s *surface) occludedChanged() {
	o := s.isOccluded()
	if o == s.occluded {
		return
	}
	s.occluded = o
	if !o {
		s.send(platform.SurfaceEvent{Kind: platform.SurfaceShown})
		if s.due {
			s.RequestFrame()
		}
	}
}

func (s *surface) PresentPixels(pix []byte, stride, width, height int) {
	if len(pix) < stride*height || width == 0 || height == 0 {
		return
	}
	data, _, _ := purego.SyscallN(cfDataCreate, 0, uintptr(unsafe.Pointer(&pix[0])), uintptr(stride*height))
	provider, _, _ := purego.SyscallN(cgProviderCreate, data)
	const bitmapInfo = 2 | 2<<12 // premultiplied alpha first, 32-bit little endian: BGRA
	img, _, _ := purego.SyscallN(cgImageCreate, uintptr(width), uintptr(height), 8, 32, uintptr(stride), cgColorSpaceSRGB, bitmapInfo, provider, 0, 0, 0)
	layer := send(s.view, "layer")
	msgSetFloat(layer, sel("setContentsScale:"), s.scale())
	send(layer, "setContents:", img)
	purego.SyscallN(cgImageRelease, img)
	purego.SyscallN(cgProviderRelease, provider)
	cfRelease(data)
}

// SetCursor does nothing: touches have no pointer to shape.
func (s *surface) SetCursor(platform.Cursor) {}

// Dragging data between apps is not supported yet.
func (s *surface) StartDataDrag(platform.DragRequest) {}
func (s *surface) CancelDataDrag()                    {}
func (s *surface) SetDropFormats([]transfer.Format)   {}

// hasResponder reports whether the surface or its text input view has the
// keyboard.
func (s *surface) hasResponder() bool {
	return sendBool(s.view, "isFirstResponder") || sendBool(s.input, "isFirstResponder")
}

// checkFocus tells the content whether it has the keyboard: the key
// window's surface does while it, or its text input view, is the first
// responder, which a web view's page takes.
func (s *surface) checkFocus() {
	f := s.w.key && !s.w.closed && s.hasResponder()
	if f == s.focused {
		return
	}
	s.focused = f
	kind := platform.SurfaceBlur
	if f {
		kind = platform.SurfaceFocus
	}
	s.send(platform.SurfaceEvent{Kind: kind})
}

// eventMods returns the modifier keys of UIKeyModifierFlags, which are
// NSEvent's.
func eventMods(flags int) platform.Modifiers {
	var m platform.Modifiers
	if flags&(1<<17) != 0 {
		m |= platform.ModShift
	}
	if flags&(1<<18) != 0 {
		m |= platform.ModCtrl
	}
	if flags&(1<<19) != 0 {
		m |= platform.ModAlt
	}
	if flags&(1<<20) != 0 {
		m |= platform.ModSuper
	}
	return m
}

const (
	uiTouchTypeIndirectPointer = 3
	uiEventButtonMaskSecondary = 2
)

// touchBegan starts following a touch, the gesture's first: a finger, or
// a pointer's button.
func (s *surface) touchBegan(t, ev id) {
	if s.touch != 0 {
		return
	}
	if !s.hasResponder() {
		send(s.view, "becomeFirstResponder")
	}
	s.touch = t
	s.touchPointer = sendInt(t, "type") == uiTouchTypeIndirectPointer
	s.touchButton = 0
	if s.touchPointer && respondsTo(ev, "buttonMask") && sendInt(ev, "buttonMask")&uiEventButtonMaskSecondary != 0 {
		s.touchButton = 1
	}
	p := msgPointInView(t, sel("locationInView:"), s.view)
	s.send(platform.SurfaceEvent{Kind: platform.PointerDown, X: p.X, Y: p.Y, Button: s.touchButton,
		Clicks: max(sendInt(t, "tapCount"), 1), Mods: s.touchMods(ev), Touch: !s.touchPointer})
}

func (s *surface) touchMoved(t, ev id) {
	if t != s.touch {
		return
	}
	p := msgPointInView(t, sel("locationInView:"), s.view)
	s.send(platform.SurfaceEvent{Kind: platform.PointerMove, X: p.X, Y: p.Y, Button: s.touchButton, Mods: s.touchMods(ev), Touch: !s.touchPointer})
}

// touchEnded ends the touch followed: lifted, or taken by the system,
// which cancels its press. A finger lifted hovers nothing.
func (s *surface) touchEnded(t, ev id, cancelled bool) {
	if t != s.touch {
		return
	}
	s.touch = 0
	p := msgPointInView(t, sel("locationInView:"), s.view)
	kind := platform.PointerUp
	if cancelled {
		kind = platform.PointerCancel
	}
	s.send(platform.SurfaceEvent{Kind: kind, X: p.X, Y: p.Y, Button: s.touchButton, Mods: s.touchMods(ev), Touch: !s.touchPointer})
	if !s.touchPointer {
		s.send(platform.SurfaceEvent{Kind: platform.PointerLeave, Touch: true})
	}
}

// The gesture recognizers' states.
const (
	gestureBegan   = 1
	gestureChanged = 2
)

// addPointerGestures follows a mouse or a trackpad of an iPad: its
// pointer hovering the surface, and its scrolls, which recognizers that
// take no touches hear.
func (s *surface) addPointerGestures() {
	withPool(func() {
		if hasClass("UIHoverGestureRecognizer") {
			hover := send(send(class("UIHoverGestureRecognizer"), "alloc"), "initWithTarget:action:", uintptr(s.view), uintptr(sel("mygoHover:")))
			send(s.view, "addGestureRecognizer:", uintptr(hover))
			release(hover)
		}
		pan := send(send(class("UIPanGestureRecognizer"), "alloc"), "initWithTarget:action:", uintptr(s.view), uintptr(sel("mygoScroll:")))
		if respondsTo(pan, "setAllowedScrollTypesMask:") {
			const uiScrollTypeMaskAll = 3 // continuous and discrete
			send(pan, "setAllowedScrollTypesMask:", uiScrollTypeMaskAll)
			send(pan, "setAllowedTouchTypes:", uintptr(nsArray()))
			send(pan, "setCancelsTouchesInView:", 0)
			send(s.view, "addGestureRecognizer:", uintptr(pan))
		}
		release(pan)
	})
}

// hoverGesture moves the pointer of a mouse or a trackpad over the surface.
func (s *surface) hoverGesture(g id) {
	p := msgPointInView(g, sel("locationInView:"), s.view)
	switch sendInt(g, "state") {
	case gestureBegan, gestureChanged:
		s.send(platform.SurfaceEvent{Kind: platform.PointerMove, X: p.X, Y: p.Y, Mods: s.gestureMods(g)})
	default:
		s.send(platform.SurfaceEvent{Kind: platform.PointerLeave})
	}
}

// scrollGesture scrolls by what a trackpad or a mouse wheel moved since
// the last change, the content following the fingers.
func (s *surface) scrollGesture(g id) {
	state := sendInt(g, "state")
	if state != gestureBegan && state != gestureChanged {
		s.scrolled = CGPoint{}
		return
	}
	at := msgPointInView(g, sel("translationInView:"), s.view)
	p := msgPointInView(g, sel("locationInView:"), s.view)
	dx, dy := at.X-s.scrolled.X, at.Y-s.scrolled.Y
	s.scrolled = at
	if dx != 0 || dy != 0 {
		s.send(platform.SurfaceEvent{Kind: platform.PointerScroll, X: p.X, Y: p.Y, DX: -dx, DY: -dy, Precise: true, Mods: s.gestureMods(g)})
	}
}

func (s *surface) gestureMods(g id) platform.Modifiers {
	if !respondsTo(g, "modifierFlags") {
		return 0
	}
	return eventMods(sendInt(g, "modifierFlags"))
}

func (s *surface) touchMods(ev id) platform.Modifiers {
	if ev == 0 || !respondsTo(ev, "modifierFlags") {
		return 0
	}
	return eventMods(sendInt(ev, "modifierFlags"))
}

// webViewAt returns the web view that takes touches at x, y in the
// surface: the last placed, as the content painted it over the others,
// whose placement takes it.
func (s *surface) webViewAt(x, y float64) *webView {
	for i := len(s.placed) - 1; i >= 0; i-- {
		if p := &s.placed[i]; p.At(x, y) {
			if v, ok := p.WebView.(*webView); ok && !v.closed {
				return v
			}
		}
	}
	return nil
}

func (b *Backend) surfaceOf(view id) *surface {
	s := b.bySurface[view]
	if s == nil || s.w.closed {
		return nil
	}
	return s
}

func registerSurfaceClasses() {
	b := func() *Backend { return theBackend }
	touches := func(fn func(s *surface, t, ev id)) func(id, objc.SEL, id, id) {
		return func(self id, _ objc.SEL, set, ev id) {
			s := b().surfaceOf(self)
			if s == nil {
				return
			}
			for _, t := range arrayItems(send(set, "allObjects")) {
				fn(s, t, ev)
			}
		}
	}
	classDef("MyGoSurfaceView", "UIView", nil, nil, append(append(keyMethods("MyGoSurfaceView"), accessViewMethods()...), []objc.MethodDef{
		method("canBecomeFirstResponder", func(self id, _ objc.SEL) bool { return true }),
		method("becomeFirstResponder", func(self id, cmd objc.SEL) bool {
			ok := byte(sendSuper(self, "MyGoSurfaceView", cmd)) != 0
			if s := b().surfaceOf(self); s != nil && ok {
				s.checkFocus()
			}
			return ok
		}),
		method("resignFirstResponder", func(self id, cmd objc.SEL) bool {
			ok := byte(sendSuper(self, "MyGoSurfaceView", cmd)) != 0
			if s := b().surfaceOf(self); s != nil && ok {
				// Once the next responder took the keyboard, unless it
				// is the text input view.
				b().post(func() {
					if !s.w.closed {
						s.checkFocus()
					}
				})
			}
			return ok
		}),
		method("layoutSubviews", func(self id, cmd objc.SEL) {
			sendSuper(self, "MyGoSurfaceView", cmd)
			if s := b().surfaceOf(self); s != nil {
				s.layout()
			}
		}),
		method("safeAreaInsetsDidChange", func(self id, cmd objc.SEL) {
			sendSuper(self, "MyGoSurfaceView", cmd)
			if s := b().surfaceOf(self); s != nil {
				s.insetsChanged()
			}
		}),
		// Touches go through to the web views under the surface where the
		// content shows them (webview.go): the content hears that one was
		// pressed.
		method("hitTest:withEvent:", func(self id, cmd objc.SEL, p CGPoint, ev id) id {
			if s := b().surfaceOf(self); s != nil && len(s.placed) > 0 {
				if v := s.webViewAt(p.X, p.Y); v != nil {
					if ev != 0 && sendInt(ev, "type") == 0 { // UIEventTypeTouches
						if at := msgFloat(ev, sel("timestamp")); at != s.pressedAt {
							s.pressedAt = at
							x, y := p.X, p.Y
							b().post(func() {
								s.send(platform.SurfaceEvent{Kind: platform.WebViewPress, X: x, Y: y})
							})
						}
					}
					return 0
				}
			}
			return sendSuperHitTest(self, "MyGoSurfaceView", cmd, p, ev)
		}),
		method("touchesBegan:withEvent:", touches(func(s *surface, t, ev id) { s.touchBegan(t, ev) })),
		method("touchesMoved:withEvent:", touches(func(s *surface, t, ev id) { s.touchMoved(t, ev) })),
		method("touchesEnded:withEvent:", touches(func(s *surface, t, ev id) { s.touchEnded(t, ev, false) })),
		method("touchesCancelled:withEvent:", touches(func(s *surface, t, ev id) { s.touchEnded(t, ev, true) })),
		method("mygoTick:", func(self id, _ objc.SEL, link id) {
			if s := b().surfaceOf(self); s != nil {
				s.tick()
			} else {
				send(link, "setPaused:", 1)
			}
		}),
		method("mygoHover:", func(self id, _ objc.SEL, g id) {
			if s := b().surfaceOf(self); s != nil {
				s.hoverGesture(g)
			}
		}),
		method("mygoScroll:", func(self id, _ objc.SEL, g id) {
			if s := b().surfaceOf(self); s != nil {
				s.scrollGesture(g)
			}
		}),
		method("mygoRepeat:", func(self id, _ objc.SEL, timer id) {
			if s := b().surfaceOf(self); s != nil {
				s.repeat.fire(s)
			}
		}),
	}...))
}
