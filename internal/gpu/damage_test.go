package gpu

import (
	"fmt"
	"image"
	"slices"
	"testing"
)

func TestAges(t *testing.T) {
	var forgot []uintptr
	a := Ages{Forget: func(id uintptr) { forgot = append(forgot, id) }}
	caret := image.Rect(10, 10, 12, 30)
	clock := image.Rect(1000, 0, 1040, 20)
	// Three buffers in turn, each new: drawn whole.
	for id := uintptr(1); id <= 3; id++ {
		a.Add([]image.Rectangle{caret}, false)
		if _, known := a.Take(id); known {
			t.Fatalf("buffer %d, new, is known", id)
		}
	}
	// Buffer 1 lacks the two frames drawn since it was: the caret's and
	// the clock's, apart.
	a.Add([]image.Rectangle{clock}, false)
	if got, known := a.Take(1); !known || fmt.Sprint(got) != fmt.Sprint([]image.Rectangle{caret, clock}) {
		t.Errorf("buffer 1 lacks %v (known %v), want %v", got, known, []image.Rectangle{caret, clock})
	}
	// Buffer 2 lacks the frame drawn into 3, the clock's and nothing.
	a.Add(nil, false)
	if got, known := a.Take(2); !known || fmt.Sprint(got) != fmt.Sprint([]image.Rectangle{caret, clock}) {
		t.Errorf("buffer 2 lacks %v (known %v), want %v", got, known, []image.Rectangle{caret, clock})
	}
	// A frame changing everything makes every buffer drawn whole.
	a.Add(nil, true)
	if _, known := a.Take(3); known {
		t.Error("buffer 3 is known after a frame changing everything")
	}
	a.Add([]image.Rectangle{caret}, false)
	if _, known := a.Take(1); known {
		t.Error("buffer 1 is known after a frame changing everything")
	}
	// A fifth buffer takes the place of the one drawn least recently.
	a.Take(4)
	a.Take(5)
	if len(forgot) != 1 || forgot[0] != 2 {
		t.Errorf("forgot %v, want [2]", forgot)
	}
	a.Reset()
	if len(forgot) != 5 || a.Has(1) {
		t.Errorf("after Reset, forgot %v and tracks buffer 1: %v", forgot, a.Has(1))
	}
}

// TestJoin checks that rectangles stay apart from each other, joined when
// they overlap or their union adds few pixels, and no more than maxParts.
func TestJoin(t *testing.T) {
	var rects []image.Rectangle
	// Overlapping: one.
	rects = join(rects, image.Rect(0, 0, 10, 10))
	rects = join(rects, image.Rect(5, 5, 15, 15))
	if want := []image.Rectangle{image.Rect(0, 0, 15, 15)}; fmt.Sprint(rects) != fmt.Sprint(want) {
		t.Errorf("overlapping: %v, want %v", rects, want)
	}
	// Near: their union adds fewer pixels than joinSlack.
	rects = join(rects, image.Rect(20, 0, 30, 10))
	if want := []image.Rectangle{image.Rect(0, 0, 30, 15)}; fmt.Sprint(rects) != fmt.Sprint(want) {
		t.Errorf("near: %v, want %v", rects, want)
	}
	// Far: apart. Then one meeting both joins them all.
	rects = join(rects, image.Rect(500, 500, 510, 510))
	if len(rects) != 2 {
		t.Errorf("far: %v, want two", rects)
	}
	rects = join(rects, image.Rect(0, 0, 600, 600))
	if want := []image.Rectangle{image.Rect(0, 0, 600, 600)}; fmt.Sprint(rects) != fmt.Sprint(want) {
		t.Errorf("meeting both: %v, want %v", rects, want)
	}
	// Past maxParts, the two closest join, and none overlap.
	rects = nil
	for i := range maxParts + 1 {
		rects = join(rects, image.Rect(i*300, i*i*100, i*300+4, i*i*100+4))
	}
	if len(rects) != maxParts {
		t.Errorf("%d rectangles, want %d", len(rects), maxParts)
	}
	if !slices.Contains(rects, image.Rect(0, 0, 304, 104)) {
		t.Errorf("the closest did not join: %v", rects)
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				t.Errorf("%v overlaps %v", rects[i], rects[j])
			}
		}
	}
}

// TestParts checks the rectangles of a frame to redraw.
func TestParts(t *testing.T) {
	var p Parts
	// Two small changes far apart, as a spinner in a sidebar and a line
	// shimmering at the bottom of a window, redraw each its own.
	spinner, shimmer := image.Rect(200, 40, 240, 52), image.Rect(470, 1460, 830, 1486)
	got, ok := p.Find([]image.Rectangle{spinner, shimmer}, 2400, 1536, nil)
	if want := []Scissor{{200, 40, 240, 52}, {470, 1460, 830, 1486}}; !ok || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("apart: %v %v, want %v", got, ok, want)
	}
	// Within the frame.
	if got, ok := p.Find([]image.Rectangle{image.Rect(-5, 10, 20, 30)}, 100, 100, nil); !ok || fmt.Sprint(got) != fmt.Sprint([]Scissor{{0, 10, 20, 30}}) {
		t.Errorf("clipped: %v %v", got, ok)
	}
	// Nothing changed: nothing to redraw.
	if got, ok := p.Find(nil, 100, 100, nil); !ok || len(got) != 0 {
		t.Errorf("nothing: %v %v", got, ok)
	}
	// Half the frame or more, together, is drawn whole.
	if _, ok := p.Find([]image.Rectangle{image.Rect(0, 0, 100, 30), image.Rect(0, 80, 100, 100)}, 100, 100, nil); ok {
		t.Error("half the frame drawn in part")
	}
	// Grown until what they grow by is in them: the left change meets a
	// backdrop from 40 to 60, which an effect reading one from 0 to 45
	// then meets; the right change, apart, grows by none.
	grow := func(d []image.Rectangle) []image.Rectangle {
		for _, b := range []image.Rectangle{image.Rect(40, 0, 60, 10), image.Rect(0, 0, 45, 10)} {
			for _, r := range d {
				if r.Overlaps(b) {
					d = append(d, b)
					break
				}
			}
		}
		return d
	}
	got, ok = p.Find([]image.Rectangle{image.Rect(50, 0, 55, 5), image.Rect(300, 300, 305, 305)}, 400, 400, grow)
	if want := []Scissor{{0, 0, 60, 10}, {300, 300, 305, 305}}; !ok || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("grown: %v %v, want %v", got, ok, want)
	}
}

func TestPartial(t *testing.T) {
	// Within the frame.
	if sc, ok := Partial(image.Rect(-5, 10, 20, 30), 100, 100, nil); !ok || sc != (Scissor{0, 10, 20, 30}) {
		t.Errorf("Partial = %v %v", sc, ok)
	}
	// Half the frame or more is drawn whole.
	if _, ok := Partial(image.Rect(0, 0, 100, 50), 100, 100, nil); ok {
		t.Error("half the frame drawn in part")
	}
	// Grown until what it grows by is in it: a backdrop from 40 to 60
	// meets the area, an effect reading one from 0 to 45 then meets that.
	grow := func(d []image.Rectangle) []image.Rectangle {
		for _, b := range []image.Rectangle{image.Rect(40, 0, 60, 10), image.Rect(0, 0, 45, 10)} {
			if d[0].Overlaps(b) {
				d = append(d, b)
			}
		}
		return d
	}
	if sc, ok := Partial(image.Rect(50, 0, 55, 5), 100, 100, grow); !ok || sc != (Scissor{0, 0, 60, 10}) {
		t.Errorf("grown: %v %v, want {0 0 60 10}", sc, ok)
	}
}
