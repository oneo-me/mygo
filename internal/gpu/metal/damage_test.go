//go:build darwin

package metal

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/internal/gpu/gputest"
	"github.com/egoist/mygo/internal/scene"
)

// TestPartialFramesMatchWhole draws random scenes changing a little at a
// time into three targets in turn, as a CAMetalLayer's drawables are,
// each frame only where it changed since its target was drawn, and
// requires the bytes of each frame drawn whole: in BGRA8 and float16
// targets, over an opaque color and a translucent one.
func TestPartialFramesMatchWhole(t *testing.T) {
	seeds := uint64(60)
	if testing.Short() {
		seeds = 10
	}
	for _, wide := range []bool{false, true} {
		frames, partial := 0, 0
		for seed := range seeds {
			// A renderer of their own, as each has an atlas of its own.
			r, err := newRenderer()
			if err != nil {
				unavailable(t, err)
			}
			m := gputest.NewChanging(seed)
			if seed%3 == 2 {
				m.Clear = scene.Color{R: 20, G: 40, B: 200, A: 77}
			}
			s := m.Scene()
			t.Run(fmt.Sprintf("wide=%v/seed=%d", wide, seed), func(t *testing.T) {
				partial += partialFrames(t, r, s, wide, 30, func(int) string { return m.Change(s) })
				frames += 30
			})
			r.Release()
		}
		// Many frames change a little, though much of so small a scene:
		// those after the first three, one for each target, are drawn in
		// part but where they change half of it.
		if partial < frames/3 {
			t.Errorf("wide %v: %d frames of %d drawn in part", wide, partial, frames)
		}
	}
}

// TestPartialFramesApartMatchWhole is TestPartialFramesMatchWhole with
// changes far apart: the random scenes change in a corner of a larger
// frame, and each frame also changes a box under a lens reading its
// backdrop and a box alone, far from them, so that frames redraw several
// rectangles apart, effects among them.
func TestPartialFramesApartMatchWhole(t *testing.T) {
	seeds := uint64(30)
	if testing.Short() {
		seeds = 6
	}
	frames, several := 0, 0
	for seed := range seeds {
		r, err := newRenderer()
		if err != nil {
			unavailable(t, err)
		}
		m := gputest.NewChanging(seed)
		s := m.Scene()
		s.Width, s.Height = 900, 640
		s.Effects = append(s.Effects, scene.EffectOp{Effect: gputest.LensEffect, Blur: 3, Params: [5][4]float32{{2}, {0.2, 0.4, 0.9, 0.3}}})
		s.Ops = append(s.Ops,
			scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 700, Y: 500, W: 60, H: 40}, Radii: scene.Uniform(6), Color: scene.Color{R: 200, A: 255}},
			scene.Op{Kind: scene.OpEffect, Rect: scene.Rect{X: 690, Y: 490, W: 50, H: 30}, Radii: scene.Uniform(8), Start: int32(len(s.Effects) - 1)},
			scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 820, Y: 60, W: 20, H: 20}, Color: scene.Color{B: 200, A: 255}})
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			partialFrames(t, r, s, seed%2 == 1, 24, func(step int) string {
				if len(r.redrewParts) > 1 {
					several++
				}
				// The boxes far away, wherever the random changes left
				// them.
				for i := range s.Ops {
					if op := &s.Ops[i]; op.Kind == scene.OpFill && op.Rect.X >= 600 {
						op.Color.G = uint8(37 * step)
					}
				}
				return m.Change(s) + ", and far away"
			})
			frames += 24
		})
		r.Release()
	}
	if several < frames/3 {
		t.Errorf("%d frames of %d redrew rectangles apart", several, frames)
	}
}

// partialFrames draws frames of s, changed by change before each but the
// first, into three targets in turn, each only where it changed, and
// requires the bytes of each frame drawn whole; it returns how many were
// drawn in part.
func partialFrames(t *testing.T, r *Renderer, s *scene.Scene, wide bool, frames int, change func(step int) string) (partial int) {
	t.Helper()
	format, bpp := uint(pixelFormatBGRA8Unorm), 4
	r.cur, r.wide = &r.formats[0], wide
	if wide {
		if !r.SetWide(true) {
			t.Skip("no float16 drawables")
		}
		r.cur, format, bpp = &r.formats[1], pixelFormatRGBA16Float, 8
	}
	cur := r.cur
	r.forgetFrames()
	defer r.forgetFrames()
	var targets [3]id
	pool(func() {
		for i := range targets {
			targets[i] = r.newTexture(s.Width, s.Height, format, usageRenderTarget|usageShaderRead, nil, 0)
		}
	})
	defer pool(func() {
		for i := range targets {
			release(&targets[i])
		}
	})
	read := func(tex id) (raw []byte, err error) {
		cb := send(r.queue, "commandBuffer")
		blit := send(cb, "blitCommandEncoder")
		send(blit, "synchronizeResource:", tex)
		send(blit, "endEncoding")
		send(cb, "commit")
		send(cb, "waitUntilCompleted")
		raw = make([]byte, s.Width*s.Height*bpp)
		msgGetBytes(tex, sel("getBytes:bytesPerRow:fromRegion:mipmapLevel:"), unsafe.Pointer(&raw[0]), uint(s.Width*bpp),
			mtlRegion{W: uint(s.Width), H: uint(s.Height), D: 1}, 0)
		return raw, nil
	}
	for step := range frames {
		what := "first"
		if step > 0 {
			what = change(step)
		}
		var got []byte
		var err error
		var part string
		pool(func() {
			r.waitLast()
			r.cur = cur
			tex := targets[step%len(targets)]
			r.diff.Wide = wide
			damage, known := r.diff.Diff(s)
			cb, e := r.drawFrame(s, damage, known, func() id { return tex })
			if e != nil {
				err = e
				return
			}
			part = "whole"
			if r.redrew == redrewPart {
				partial++
				part = redrewSizes(r)
			}
			send(cb, "commit")
			send(cb, "waitUntilCompleted")
			if int(send(cb, "status")) == statusError {
				err = errors.New("the frame failed")
				return
			}
			got, err = read(tex)
		})
		if err != nil {
			t.Fatal(err)
		}
		want := renderRaw(t, r, s, wide)
		if n := differing(got, want); n != 0 {
			var at []string
			for i := range got {
				if got[i] != want[i] && len(at) < 6 {
					px := i / bpp
					at = append(at, fmt.Sprintf("(%d,%d)c%d %d≠%d", px%s.Width, px/s.Width, i%bpp, got[i], want[i]))
				}
			}
			t.Fatalf("step %d (%s), redrew %s: %d bytes differ from the frame drawn whole, at %v", step, what, part, n, at)
		}
	}
	return partial
}

// TestPartialFramesOfOneChange checks that a caret blinking redraws its own
// box alone once each target was drawn whole.
func TestPartialFramesOfOneChange(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		unavailable(t, err)
	}
	defer r.Release()
	m := gputest.NewChanging(5)
	s := m.Scene()
	caret := scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 100, Y: 100, W: 2, H: 18}, Color: scene.Color{A: 255}}
	s.Ops = append(s.Ops, caret)
	var areas []string
	partialFrames(t, r, s, false, 8, func(step int) string {
		areas = append(areas, redrewSizes(r))
		if step%2 == 1 {
			s.Ops[len(s.Ops)-1].Color.A = 0
		} else {
			s.Ops[len(s.Ops)-1].Color.A = 255
		}
		return fmt.Sprint("caret ", step%2)
	})
	areas = append(areas, redrewSizes(r))
	// The three targets drawn whole, then each the caret's box, grown by
	// the reach of the GPU's quads.
	want := []string{"whole", "whole", "whole", "6×22", "6×22", "6×22", "6×22", "6×22"}
	if fmt.Sprint(areas) != fmt.Sprint(want) {
		t.Errorf("redrew %v, want %v", areas, want)
	}
}

// TestPartialFramesOfChangesApart checks that two changes far apart, as a
// spinner in a sidebar and a line of text shimmering at the bottom of a
// window, redraw each its own box rather than the one around both, with
// the bytes of the frames drawn whole.
func TestPartialFramesOfChangesApart(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		unavailable(t, err)
	}
	defer r.Release()
	m := gputest.NewChanging(5)
	s := m.Scene()
	spinner := scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 10, Y: 10, W: 12, H: 12}, Radii: [4]float32{6, 6, 6, 6}, Color: scene.Color{B: 255, A: 255}}
	shimmer := scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: float32(s.Width) - 60, Y: float32(s.Height) - 14, W: 40, H: 6}, Color: scene.Color{R: 120, A: 255}}
	s.Ops = append(s.Ops, spinner, shimmer)
	var areas []string
	partialFrames(t, r, s, false, 8, func(step int) string {
		areas = append(areas, redrewSizes(r))
		n := len(s.Ops)
		s.Ops[n-2].Color.G = uint8(40 * step)
		s.Ops[n-1].Rect.X = float32(s.Width) - 60 + float32(step%2)
		return fmt.Sprint("spinner and shimmer ", step)
	})
	areas = append(areas, redrewSizes(r))
	// The three targets drawn whole, then each the boxes of both, grown by
	// the reach of the GPU's quads: the shimmer's by the pixel it moved.
	want := []string{"whole", "whole", "whole", "16×16 and 45×10", "16×16 and 45×10", "16×16 and 45×10", "16×16 and 45×10", "16×16 and 45×10"}
	if fmt.Sprint(areas) != fmt.Sprint(want) {
		t.Errorf("redrew %v, want %v", areas, want)
	}
}

// redrewSizes describes how much of its last frame r redrew: whole, or the
// sizes of the parts, from the top.
func redrewSizes(r *Renderer) string {
	if r.redrew != redrewPart {
		return "whole"
	}
	var sizes []string
	for _, a := range r.redrewParts {
		sizes = append(sizes, fmt.Sprintf("%d×%d", a.Right-a.Left, a.Bottom-a.Top))
	}
	return strings.Join(sizes, " and ")
}
