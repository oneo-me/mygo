package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

// Tests handle fingers wherever they run.
func init() { fingers = fingerEvent }

// finger sends a touch event of a finger at (x, y).
func (t *Tester) finger(kind platform.SurfaceEventKind, x, y float32) {
	t.send(platform.SurfaceEvent{Kind: kind, X: float64(x), Y: float64(y), Touch: true})
}

// touchList is a scroll container of buttons, tall past its box, which
// counts their clicks.
func touchList(s *ScrollState, clicks *int) func(c *context) {
	return func(c *context) {
		coreScroll(c).Fill().TrackScroll(s).Children(func() {
			for i := range 40 {
				if coreButton(c, "Row").Key(i).Height(40).Shrink(0).Clicked() {
					*clicks++
				}
			}
		})
	}
}

func TestTouchPanScrolls(t *testing.T) {
	var s ScrollState
	clicks := 0
	tt := coreNewTester(touchList(&s, &clicks), 200, 400)
	tt.finger(platform.PointerDown, 100, 300)
	// Within the slop, the finger still presses.
	tt.finger(platform.PointerMove, 100, 295)
	if s.Y != 0 {
		t.Fatalf("a wobble scrolled to %v", s.Y)
	}
	// Past it, the content follows the finger, from where it went down.
	tt.finger(platform.PointerMove, 100, 250)
	if s.Y != 50 {
		t.Errorf("a finger 50 up scrolled to %v", s.Y)
	}
	tt.finger(platform.PointerMove, 100, 200)
	tt.Advance(time.Second) // a finger that stops flings nothing
	tt.finger(platform.PointerUp, 100, 200)
	tt.Advance(3 * time.Second)
	if s.Y != 100 || clicks != 0 {
		t.Errorf("after the pan: at %v, %d clicks", s.Y, clicks)
	}
	// A tap clicks.
	tt.finger(platform.PointerDown, 100, 300)
	tt.finger(platform.PointerUp, 100, 300)
	if clicks != 1 {
		t.Errorf("a tap clicked %d times", clicks)
	}
}

func TestTouchFlings(t *testing.T) {
	var s ScrollState
	clicks := 0
	tt := coreNewTester(touchList(&s, &clicks), 200, 400)
	tt.finger(platform.PointerDown, 100, 300)
	for y := float32(280); y >= 200; y -= 20 {
		tt.Advance(10 * time.Millisecond)
		tt.finger(platform.PointerMove, 100, y)
	}
	tt.finger(platform.PointerUp, 100, 200)
	panned := s.Y
	tt.Advance(100 * time.Millisecond)
	if s.Y <= panned {
		t.Fatalf("a flick flung nothing: at %v after a pan to %v", s.Y, panned)
	}
	// A finger stops the fling, and clicks nothing.
	tt.finger(platform.PointerDown, 100, 300)
	stopped := s.Y
	tt.finger(platform.PointerUp, 100, 300)
	tt.Advance(3 * time.Second)
	if s.Y != stopped || clicks != 0 {
		t.Errorf("a finger stopping the fling: at %v (stopped at %v), %d clicks", s.Y, stopped, clicks)
	}
}

func TestTouchStopsOnlyFlingsUnderIt(t *testing.T) {
	var s ScrollState
	clicks := 0
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Fill().Children(func() {
			if coreButton(c, "Header").Height(40).Shrink(0).Clicked() {
				clicks++
			}
			coreScroll(c).Grow(1).TrackScroll(&s).Children(func() {
				coreColumn(c).Height(4000).Shrink(0)
			})
		})
	}, 200, 400)
	tt.finger(platform.PointerDown, 100, 300)
	for y := float32(280); y >= 200; y -= 20 {
		tt.Advance(10 * time.Millisecond)
		tt.finger(platform.PointerMove, 100, y)
	}
	tt.finger(platform.PointerUp, 100, 200)
	tt.Advance(50 * time.Millisecond)
	// A tap outside the container flinging clicks, and the fling goes on.
	at := s.Y
	tt.finger(platform.PointerDown, 100, 20)
	tt.finger(platform.PointerUp, 100, 20)
	tt.Advance(100 * time.Millisecond)
	if clicks != 1 || s.Y <= at {
		t.Errorf("a tap beside the fling: %d clicks, at %v from %v", clicks, s.Y, at)
	}
}

func TestTouchDragsSlider(t *testing.T) {
	var s ScrollState
	v := 0.0
	tt := coreNewTester(func(c *context) {
		coreScroll(c).Fill().TrackScroll(&s).Children(func() {
			coreSlider(c, &v, 0, 100).Width(200)
			coreColumn(c).Height(1000).Shrink(0)
		})
	}, 200, 400)
	// The slider is the first row, along the top: a finger dragging it
	// right, and a little up, which would scroll the container.
	tt.finger(platform.PointerDown, 2, 8)
	tt.finger(platform.PointerMove, 150, 0)
	tt.finger(platform.PointerUp, 150, 0)
	if v < 50 || s.Y != 0 {
		t.Errorf("a finger dragging a slider: value %v, scrolled to %v", v, s.Y)
	}
}

func TestPointerCancelClicksNothing(t *testing.T) {
	var s ScrollState
	clicks := 0
	tt := coreNewTester(touchList(&s, &clicks), 200, 400)
	tt.finger(platform.PointerDown, 100, 20)
	tt.finger(platform.PointerCancel, 100, 20)
	tt.finger(platform.PointerUp, 100, 20)
	if clicks != 0 {
		t.Errorf("a cancelled press clicked %d times", clicks)
	}
}

func TestSafeAreaPadsTheView(t *testing.T) {
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Fill().Children(func() { coreText(c, "Top") })
	}, 300, 600)
	tt.SetSafeArea(47, 0, 34, 0)
	b, ok := tt.Find("Top")
	if !ok || b.Y != 47 {
		t.Errorf("text at the top of the safe area: %v, %v", b, ok)
	}
	img := tt.Image()
	if c := img.RGBAAt(150, 10); c.A != 255 {
		t.Errorf("the background does not fill the status bar's room: %v", c)
	}
}
