package ui

import (
	"math"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

// A finger on a touch screen (platform.SurfaceEvent.Touch) presses as the
// pointer does, but has no wheel: one that moves before what it pressed
// takes the drag scrolls the content under it instead, as UIScrollView's
// pan does, the content following the finger, and flings it on as it lets
// go. What takes the drag keeps it: a Draggable element, a slider, an
// element handling its input (HandleInput), which hears that the events
// are a finger's (InputEvent.Touch).

// fingers handles the pointer events of fingers on touch screens, and
// PointerCancel, on the platforms whose backends send them (touch_on.go):
// the apps of others link none of it.
var fingers func(rt *engine, ev platform.SurfaceEvent, x, y float32)

// fingerEvent is fingers.
func fingerEvent(rt *engine, ev platform.SurfaceEvent, x, y float32) {
	switch ev.Kind {
	case platform.PointerMove:
		if !rt.touchMove(x, y) {
			rt.pointerMove(x, y)
		}
	case platform.PointerDown:
		touch := ev.Touch && ev.Button == 0
		stopped := touch && rt.stopFlings(x, y)
		rt.pointerMove(x, y)
		rt.pointerDown(x, y, ev.Button, Modifiers(ev.Mods), ev.Clicks)
		if touch {
			rt.touchDown(x, y, stopped)
		}
	case platform.PointerUp:
		if rt.touch.panning {
			rt.touchMove(x, y)
			rt.touchUp(false)
			return
		}
		rt.pointerMove(x, y)
		rt.pointerUp(ev.Button, ev.Clicks)
		rt.touchUp(false)
	case platform.PointerCancel:
		rt.cancelPress()
		rt.touchUp(true)
	}
}

// touchSlop is how far, in DIPs, a finger moves before the content under
// it scrolls: a tap wobbles.
const touchSlop = 10

// flingTau is the time constant of a fling's slowing down, in seconds, as
// UIScrollView's normal deceleration loses 0.2% of its speed each
// millisecond; flingDecay is how many of them a fling lasts, which leaves
// it at 1% of its distance to go.
const (
	flingTau   = 0.5
	flingDecay = 4.6
)

// flingAnimation is how a fling goes: fast, then slowing down as friction
// takes its speed.
var flingAnimation = Animation{Duration: time.Duration(flingTau * flingDecay * float64(time.Second)), Ease: flingEase}

// flingEase is an exponential ease out, which starts at the speed of the
// finger that let go: a fling of distance d starts at d·flingDecay/(1-e⁻ᵏ)
// over the animation's duration, flingDecay·flingTau, which is d/flingTau.
func flingEase(t float32) float32 {
	k := float64(flingDecay)
	return float32((1 - math.Exp(-k*float64(t))) / (1 - math.Exp(-k)))
}

// touchPan follows the finger down on the surface.
type touchPan struct {
	// down tells that a finger pressed what takes no drag: it may pan.
	down bool
	// panning tells that it moved far enough, and scrolls target, the
	// state of a scroll container, or handler, an element handling its
	// input, which takes the moves as scrolls.
	panning bool
	target  uint64
	handler uint64
	// x0, y0 is where the finger went down, and x, y where it was last.
	x0, y0, x, y float32
	chain        []uint64
	track        dragTrack
}

// takesDrag reports whether what the press went to takes its moves: a
// scroll bar's thumb, an element dragged or handling its input.
func (rt *engine) takesDrag() bool {
	if rt.scrollDrag.st != nil {
		return true
	}
	p := rt.pressed
	return p != nil && (p.input != nil || p.flags&flagDraggable != 0)
}

// touchDown starts following a finger that pressed at x, y. A finger
// stopping what it flung presses nothing, as on iOS.
func (rt *engine) touchDown(x, y float32, stopped bool) {
	t := &rt.touch
	*t = touchPan{chain: t.chain[:0], x0: x, y0: y, x: x, y: y}
	if stopped {
		rt.cancelPress()
	}
	if rt.takesDrag() {
		return
	}
	t.down = true
	t.chain = rt.appendHitChain(t.chain, x, y)
	t.track.reset(rt.now(), x, y)
}

// touchMove follows the finger to x, y, and reports whether it pans,
// which takes the move.
func (rt *engine) touchMove(x, y float32) bool {
	t := &rt.touch
	if !t.down {
		return false
	}
	if !t.panning {
		if dx, dy := x-t.x0, y-t.y0; dx*dx+dy*dy < touchSlop*touchSlop {
			return false
		}
		rt.startPan(x, y)
	}
	dx, dy := x-t.x, y-t.y
	t.x, t.y = x, y
	t.track.move(rt.now(), dx, dy, dx, dy)
	rt.panBy(-dx, -dy)
	return true
}

// startPan ends the press, which clicks nothing, and finds what the
// finger scrolls: the innermost container under the press that can go the
// way the finger moved, the most, else the innermost element handling
// its input.
func (rt *engine) startPan(x, y float32) {
	t := &rt.touch
	t.panning = true
	rt.cancelPress()
	rt.setHover(nil)
	dx, dy := x-t.x0, y-t.y0
	vertical := dx*dx < dy*dy
	for _, id := range t.chain {
		s := rt.states[id]
		if s == nil {
			continue
		}
		if t.handler == 0 && s.input != nil {
			t.handler = id
		}
		if canScroll(s, vertical) {
			t.target = id
			return
		}
	}
	for _, id := range t.chain {
		if s := rt.states[id]; s != nil && canScroll(s, !vertical) {
			t.target = id
			return
		}
	}
}

// canScroll reports whether a container scrolls vertically, or else
// horizontally, with content past its box.
func canScroll(s *state, vertical bool) bool {
	if vertical {
		return s.flags&flagScrollY != 0 && s.contentH > float64(s.h)
	}
	return s.flags&flagScrollX != 0 && s.contentW > float64(s.w)
}

// panBy scrolls what the finger pans by dx, dy, in the window's axes.
func (rt *engine) panBy(dx, dy float32) {
	t := &rt.touch
	if h := rt.states[t.handler]; h != nil && t.target == 0 {
		rt.pointerX, rt.pointerY = t.x0, t.y0
		rt.deliver(h, InputEvent{Kind: InputScroll, DX: dx, DY: dy, Mods: rt.mods, Precise: true, Touch: true})
		return
	}
	s := rt.states[t.target]
	if s == nil {
		return
	}
	sx, sy := s.vector(t.x0, t.y0, dx, dy)
	s.wheel = nil
	if scrollBy(s, sx, sy) {
		rt.requestFrame()
	}
}

// touchUp lets the finger go: what it panned flings on at its speed.
func (rt *engine) touchUp(cancel bool) {
	t := &rt.touch
	panned := t.panning
	if panned && !cancel {
		v := t.track.velocities(rt.now())
		rt.fling(-v[2], -v[3])
	}
	t.down, t.panning = false, false
	t.target, t.handler = 0, 0
}

// fling scrolls what the finger panned on, by as far as friction lets
// it go from a speed of vx, vy DIPs a second.
func (rt *engine) fling(vx, vy float32) {
	const least, most = 50, 8000 // DIPs a second
	s := rt.states[rt.touch.target]
	if s == nil || vx*vx+vy*vy < least*least {
		return
	}
	vx, vy = max(-most, min(vx, most)), max(-most, min(vy, most))
	sx, sy := s.vector(rt.touch.x0, rt.touch.y0, vx*flingTau, vy*flingTau)
	rt.glide(s, sx, sy, flingAnimation)
}

// stopFlings stops the containers gliding under a finger pressing x, y,
// as it touches them, and reports whether one was. Others glide on.
func (rt *engine) stopFlings(x, y float32) bool {
	if len(rt.wheeling) == 0 {
		return false
	}
	stopped := false
	for _, id := range rt.hitChain(x, y) {
		if s := rt.states[id]; s != nil && s.wheel != nil {
			s.wheel = nil
			stopped = true
		}
	}
	return stopped
}

// cancelPress ends the press under way without a click, as the system
// took it (platform.PointerCancel) or a finger pans: an element taking its
// input hears its release.
func (rt *engine) cancelPress() {
	rt.selection.dragging = false
	rt.scrollDrag.st = nil
	rt.dragCancel()
	rt.dragEnd()
	if p := rt.pressed; p != nil {
		if p.input != nil {
			rt.deliver(p, InputEvent{Kind: InputPointerUp, Button: rt.pressButton, Mods: rt.mods, Clicks: rt.pressClicks})
		}
		if p.editor != nil {
			p.editor.release()
		}
		p.pressed = false
		rt.pressed = nil
	}
	rt.requestFrame()
}
