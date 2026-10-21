package widget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The widget extension reads a kind's timeline from <dir>/timeline.json,
// in the app group's container shared with the app, and its images from
// the files beside it, named by their contents.
const (
	timelineFile = "timeline.json"
	imagePrefix  = "img-"
	// timelineVersion changes with what the extension cannot read of
	// earlier timelines.
	timelineVersion = 1
)

type timeline struct {
	V       int     `json:"v"`
	Entries []entry `json:"entries"`
}

type entry struct {
	Date     float64          `json:"date"`
	View     *node            `json:"view,omitempty"`
	Families map[Family]*node `json:"families,omitempty"`
	BG       *node            `json:"bg,omitempty"`
	URL      string           `json:"url,omitempty"`
}

// encoder turns the app's views into the nodes the extension reads,
// collecting their images.
type encoder struct {
	images map[string][]byte // by file name
}

// node returns the node of a view as the extension reads it: a copy,
// whose texts carry the font of the views holding them, and whose
// images are named.
func (e *encoder) node(n *node, parent *font) (*node, error) {
	if n == nil {
		return nil, nil
	}
	out := *n
	f := mergeFont(parent, n.Font)
	out.Font = nil
	switch n.T {
	case "text", "symbol", "date":
		out.Font = f
	case "image":
		data := n.imageData
		if n.imagePath != "" {
			var err error
			if data, err = os.ReadFile(n.imagePath); err != nil {
				return nil, err
			}
		}
		sum := sha256.Sum256(data)
		out.Image = imagePrefix + hex.EncodeToString(sum[:16])
		e.images[out.Image] = data
	}
	out.imageData, out.imagePath = nil, ""
	out.Children = nil
	for _, c := range n.Children {
		cn, err := e.node(c, f)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, cn)
	}
	return &out, nil
}

// mergeFont returns the font of a view whose own font is own, inside a
// view of the font parent; nil for the system's.
func mergeFont(parent, own *font) *font {
	if own == nil {
		return parent
	}
	var f font
	if parent != nil {
		f = *parent
	}
	switch {
	case own.Size != nil:
		f.Size, f.Style = own.Size, ""
	case own.Style != "":
		f.Size, f.Style = nil, own.Style
	}
	if own.Weight != 0 {
		f.Weight = own.Weight
	}
	if own.Design != "" {
		f.Design = own.Design
	}
	f.Italic = f.Italic || own.Italic
	f.Digits = f.Digits || own.Digits
	return &f
}

// writeTimeline writes the timeline of entries into dir, with its images,
// and removes the images of the timeline it replaces. The zero dates are
// now.
func writeTimeline(dir string, entries []Entry, now time.Time) error {
	e := &encoder{images: map[string][]byte{}}
	tl := timeline{V: timelineVersion, Entries: []entry{}}
	for _, en := range entries {
		date := en.Date
		if date.IsZero() {
			date = now
		}
		out := entry{Date: float64(date.UnixNano()) / 1e9, URL: en.URL}
		var err error
		if out.View, err = e.node(en.View.n, nil); err != nil {
			return err
		}
		if out.BG, err = e.node(en.Background.n, nil); err != nil {
			return err
		}
		for fam, v := range en.Families {
			n, err := e.node(v.n, nil)
			if err != nil {
				return err
			}
			if n != nil {
				if out.Families == nil {
					out.Families = map[Family]*node{}
				}
				out.Families[fam] = n
			}
		}
		tl.Entries = append(tl.Entries, out)
	}
	slices.SortStableFunc(tl.Entries, func(a, b entry) int {
		switch {
		case a.Date < b.Date:
			return -1
		case a.Date > b.Date:
			return 1
		}
		return 0
	})
	data, err := json.Marshal(tl)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, img := range e.images {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue // named by its contents
		}
		if err := writeAtomic(path, img); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(dir, timelineFile), data); err != nil {
		return err
	}
	old, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, f := range old {
		if name := f.Name(); strings.HasPrefix(name, imagePrefix) && e.images[name] == nil {
			os.Remove(filepath.Join(dir, name))
		}
	}
	return nil
}

// writeAtomic writes a file through a temporary one renamed over it, so
// that the extension never reads half of it.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
