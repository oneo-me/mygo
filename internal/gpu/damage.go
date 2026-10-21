package gpu

import (
	"image"
	"math"
	"slices"

	"github.com/egoist/mygo/internal/scene"
)

// Partial redraws. A frame that changes little of a window (a caret
// blinking, a spinner turning, a button under the pointer) redraws only
// what changed (scene.Differ), into a buffer that holds an earlier frame:
// what changed since that one, scissored, after clearing it to the
// frame's color, over the rest kept as it was (loaded, not cleared). A
// surface's swap chain shows its buffers in turn, so a buffer the renderer
// gets holds the frame drawn into it some frames ago, as EGL's buffer age
// says: Ages keeps, for each buffer, what changed since it was drawn.
//
// What changed is a few rectangles apart from each other, not the one
// around them all: a spinner in a sidebar and a line of text shimmering
// at the bottom of the window redraw themselves, not the window between
// them.

// maxAges is how many buffers Ages tracks, the most a swap chain has (a
// CAMetalLayer three) and one more.
const maxAges = 4

// maxParts is how many rectangles apart from each other a frame redraws at
// most: past it, the two whose union adds the fewest pixels join.
const maxParts = 8

// joinSlack is how many pixels the union of two rectangles may add to
// theirs for them to join anyway: each rectangle costs a pass over the
// batches that meet it, and tile-based GPUs draw in tiles of 32×32 pixels.
const joinSlack = 64 * 64

// join adds r to rects, which are apart from each other, keeping them so:
// r joins each that it overlaps or that it adds few pixels to (joinSlack),
// and past maxParts the two that add the fewest join.
func join(rects []image.Rectangle, r image.Rectangle) []image.Rectangle {
	if r.Empty() {
		return rects
	}
	for i := 0; i < len(rects); i++ {
		if o := rects[i]; o.Overlaps(r) || added(o, r) <= joinSlack {
			// Their union may meet those before: from the start again.
			r = r.Union(o)
			rects[i] = rects[len(rects)-1]
			rects = rects[:len(rects)-1]
			i = -1
		}
	}
	rects = append(rects, r)
	for len(rects) > maxParts {
		bi, bj := 0, 1
		for i := range rects {
			for j := i + 1; j < len(rects); j++ {
				if added(rects[i], rects[j]) < added(rects[bi], rects[bj]) {
					bi, bj = i, j
				}
			}
		}
		u := rects[bi].Union(rects[bj])
		rects[bj] = rects[len(rects)-1]
		rects[bi] = rects[len(rects)-2]
		rects = join(rects[:len(rects)-2], u)
	}
	return rects
}

// added returns how many pixels the union of a and b, apart, holds beyond
// theirs.
func added(a, b image.Rectangle) int {
	u := a.Union(b)
	return u.Dx()*u.Dy() - a.Dx()*a.Dy() - b.Dx()*b.Dy()
}

// Ages keeps what changed in the frames of a window since each buffer of
// its swap chain was drawn, by an identity of the buffer (a number the
// Metal renderer gives the texture of a CAMetalLayer's drawable).
type Ages struct {
	// Forget, unless nil, is called with each buffer Ages stops tracking.
	Forget func(id uintptr)
	bufs   [maxAges]age
	n      int
	frame  uint64
	taken  []image.Rectangle
}

type age struct {
	id uintptr
	// changed is what changed since the buffer was drawn, unless whole:
	// rectangles apart from each other (join).
	changed []image.Rectangle
	whole   bool
	// used is the frame the buffer was last drawn in.
	used uint64
}

// Add notes a new frame, which changed the rectangles changed of the last,
// or everything when whole.
func (a *Ages) Add(changed []image.Rectangle, whole bool) {
	a.frame++
	for i := range a.bufs[:a.n] {
		b := &a.bufs[i]
		if b.whole = b.whole || whole; b.whole {
			b.changed = b.changed[:0]
			continue
		}
		for _, r := range changed {
			b.changed = join(b.changed, r)
		}
	}
}

// Take returns what changed in the frames since buffer id was drawn, the
// buffer the frame is drawn into, rectangles apart from each other good
// until the next call, and false when that is unknown (the buffer is new,
// or everything may have changed): the frame draws it whole. It then
// holds the frame.
func (a *Ages) Take(id uintptr) (changed []image.Rectangle, known bool) {
	for i := range a.bufs[:a.n] {
		b := &a.bufs[i]
		if b.id == id {
			a.taken = append(a.taken[:0], b.changed...)
			known = !b.whole
			b.changed, b.whole, b.used = b.changed[:0], false, a.frame
			return a.taken, known
		}
	}
	// A new buffer takes the place of the one drawn least recently.
	i := a.n
	if a.n == maxAges {
		i = 0
		for j := range a.bufs {
			if a.bufs[j].used < a.bufs[i].used {
				i = j
			}
		}
		if a.Forget != nil {
			a.Forget(a.bufs[i].id)
		}
	} else {
		a.n++
	}
	a.bufs[i] = age{id: id, changed: a.bufs[i].changed[:0], used: a.frame}
	return nil, false
}

// Reset forgets every buffer, as a swap chain makes new ones.
func (a *Ages) Reset() {
	if a.Forget != nil {
		for i := range a.bufs[:a.n] {
			a.Forget(a.bufs[i].id)
		}
	}
	a.bufs, a.n = [maxAges]age{}, 0
}

// Parts finds the rectangles of a window's frames to redraw (Find),
// keeping its memory from frame to frame.
type Parts struct {
	area, next, grown []image.Rectangle
	out               []Scissor
}

// Find returns the rectangles of a w×h frame to redraw, apart from each
// other and good until the next call, given what changed in the buffer
// drawn into (Ages.Take), grown by grow (the backdrops of effects,
// scene.Differ.AddBackdrops, which may change what it is given), or false
// when the frame is better drawn whole: past half the frame, drawing it
// whole costs about the same. No rectangles redraw nothing.
func (p *Parts) Find(changed []image.Rectangle, w, h int, grow func([]image.Rectangle) []image.Rectangle) ([]Scissor, bool) {
	frame := image.Rect(0, 0, w, h)
	p.area = p.area[:0]
	for _, r := range changed {
		p.area = join(p.area, r.Intersect(frame))
	}
	for grow != nil && len(p.area) > 0 {
		// Rectangles joined may meet backdrops that those apart did not:
		// grown until what they grow by is in them.
		p.grown = grow(append(p.grown[:0], p.area...))
		p.next = p.next[:0]
		for _, r := range p.grown {
			p.next = join(p.next, r.Intersect(frame))
		}
		if within(p.next, p.area) {
			break
		}
		p.area, p.next = p.next, p.area
	}
	n := 0
	for _, r := range p.area {
		n += r.Dx() * r.Dy()
	}
	if 2*n >= w*h {
		return nil, false
	}
	p.out = p.out[:0]
	for _, r := range p.area {
		p.out = append(p.out, Scissor{int32(r.Min.X), int32(r.Min.Y), int32(r.Max.X), int32(r.Max.Y)})
	}
	// From the top, as they are read: few, sorted in place.
	for i := 1; i < len(p.out); i++ {
		for j := i; j > 0 && (p.out[j].Top < p.out[j-1].Top || p.out[j].Top == p.out[j-1].Top && p.out[j].Left < p.out[j-1].Left); j-- {
			p.out[j], p.out[j-1] = p.out[j-1], p.out[j]
		}
	}
	return p.out, true
}

// within reports whether each of rects is in one of area.
func within(rects, area []image.Rectangle) bool {
	for _, r := range rects {
		in := false
		for _, a := range area {
			if r.In(a) {
				in = true
				break
			}
		}
		if !in {
			return false
		}
	}
	return true
}

// BatchBounds appends to out, for each batch b built, the box around where
// its ops draw, given where each op of the scene built draws
// (scene.Differ.Bounds): a frame drawn in part draws a batch only where
// that box meets what it redraws, rather than wherever its scissor does,
// which is a clip around much more (the glyphs of a whole page), and an
// effect reading its backdrop outside what changed reads none. Without
// the bounds of each op, a batch's box is everything.
func (b *Builder) BatchBounds(bounds []image.Rectangle, out []Scissor) []Scissor {
	n := len(b.OpStart) - 1
	for _, bt := range b.Batches {
		lo, hi := int32(bt.Start), int32(bt.Start+bt.Count)
		if len(bounds) != n {
			out = append(out, Scissor{math.MinInt32, math.MinInt32, math.MaxInt32, math.MaxInt32})
			continue
		}
		// The first op with instances from lo on.
		k, _ := slices.BinarySearch(b.OpStart[1:], lo+1)
		var box image.Rectangle
		for ; k < n && b.OpStart[k] < hi; k++ {
			if b.OpStart[k+1] > b.OpStart[k] {
				box = box.Union(bounds[k])
			}
		}
		out = append(out, Scissor{int32(box.Min.X), int32(box.Min.Y), int32(box.Max.X), int32(box.Max.Y)})
	}
	return out
}

// Partial returns the area of a w×h frame to redraw, given what changed
// in the buffer drawn into, grown by grow (the backdrops of effects,
// scene.Differ.AddBackdrops), or false when the frame is better drawn
// whole: past half the frame, drawing it whole costs about the same. It
// is one rectangle around all that changed, for renderers that redraw one
// (OpenGL's, Direct3D's); Parts finds several apart.
func Partial(changed image.Rectangle, w, h int, grow func([]image.Rectangle) []image.Rectangle) (Scissor, bool) {
	frame := image.Rect(0, 0, w, h)
	area := changed.Intersect(frame)
	if area.Empty() {
		return Scissor{}, true
	}
	if grow != nil {
		var rects [1]image.Rectangle
		for {
			rects[0] = area
			grown := area
			for _, r := range grow(rects[:]) {
				grown = grown.Union(r)
			}
			grown = grown.Intersect(frame)
			if grown == area {
				break
			}
			area = grown
		}
	}
	if 2*area.Dx()*area.Dy() >= w*h {
		return Scissor{}, false
	}
	return Scissor{int32(area.Min.X), int32(area.Min.Y), int32(area.Max.X), int32(area.Max.Y)}, true
}

// Intersect returns the pixels both s and o take.
func (s Scissor) Intersect(o Scissor) Scissor { return s.intersect(o) }

// Has reports whether Ages tracks buffer id.
func (a *Ages) Has(id uintptr) bool {
	for i := range a.bufs[:a.n] {
		if a.bufs[i].id == id {
			return true
		}
	}
	return false
}

// Pipelines keeps, for each op of the frames a renderer builds, a hash of
// the pipelines (shaders, blending) its instances draw with. An op drawn
// with others than in the last frame (a batch specialized for other
// classes as batching changed around it, a pipeline made meanwhile, a
// clip cut by a mask) draws a unit in the last place apart here and
// there, which a frame drawn whole would change too: its bounds count as
// changed.
type Pipelines struct {
	keys, last []uint64
}

// Damage hashes the pipelines of the ops b built, pipe(i) being batch i's,
// and adds to damage, what changed since the last frame drawn as d found
// (unless not known), the bounds of the ops d matches with an op of the
// last frame drawn with other pipelines, and returns it.
func (p *Pipelines) Damage(b *Builder, pipe func(i int) uint64, d *scene.Differ, damage []image.Rectangle, known bool) []image.Rectangle {
	n := len(b.OpStart) - 1
	p.keys = slices.Grow(p.keys[:0], n)[:n]
	clear(p.keys)
	op := 0
	for i, bt := range b.Batches {
		lo, hi := int32(bt.Start), int32(bt.Start+bt.Count)
		for op < n && b.OpStart[op+1] <= lo {
			op++
		}
		key := pipe(i)
		for k := op; k < n && b.OpStart[k] < hi; k++ {
			if b.OpStart[k+1] > b.OpStart[k] {
				p.keys[k] = (p.keys[k] ^ key) * 1099511628211
			}
		}
	}
	if !known {
		return damage
	}
	bounds := d.Bounds()
	for j, key := range p.keys {
		if i, ok := d.Match(j); ok && i < len(p.last) && p.last[i] != key {
			damage = append(damage, bounds[j])
		}
	}
	return damage
}

// Remember keeps the hashes of the frame Damage was given last, drawn, to
// compare the next with.
func (p *Pipelines) Remember() { p.keys, p.last = p.last, p.keys }
