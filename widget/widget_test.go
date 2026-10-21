package widget

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// read returns the timeline written in dir, and the names of the images
// beside it.
func read(t *testing.T, dir string) (timeline, []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, timelineFile))
	if err != nil {
		t.Fatal(err)
	}
	var tl timeline
	if err := json.Unmarshal(data, &tl); err != nil {
		t.Fatal(err)
	}
	var images []string
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if strings.HasPrefix(f.Name(), imagePrefix) {
			images = append(images, f.Name())
		} else if f.Name() != timelineFile {
			t.Errorf("%s left in the kind's directory", f.Name())
		}
	}
	return tl, images
}

func TestTimeline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "counter")
	now := time.Unix(1_800_000_000, 500_000_000)
	later := now.Add(time.Hour)
	err := writeTimeline(dir, []Entry{
		{Date: later, View: Text("later"), URL: "counter://later"},
		{
			View: Column(
				Text("Count").TextStyle(Caption),
				Text("42").Bold().MonospacedDigits(),
				Row(Symbol("star.fill"), Text("x").FontSize(12)).Gap(4),
			).FontSize(20).FontDesign(Rounded).Gap(2).AlignItems(Start).Padding(8).Fill(),
			Families:   map[Family]View{Small: Text("small"), Large: {}},
			Background: Rectangle(Hex("#336699").Dark(RGB(0, 0, 0))),
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	tl, images := read(t, dir)
	if tl.V != timelineVersion || len(tl.Entries) != 2 || len(images) != 0 {
		t.Fatalf("timeline = %+v, images %v", tl, images)
	}
	first, second := tl.Entries[0], tl.Entries[1]
	if first.Date != 1_800_000_000.5 || second.Date != 1_800_003_600.5 || second.URL != "counter://later" {
		t.Errorf("entries out of order, or zero date not now: %+v", tl.Entries)
	}
	if len(first.Families) != 1 || first.Families[Small].Text != "small" {
		t.Errorf("families = %+v, want small alone", first.Families)
	}
	col := first.View
	if col.T != "column" || col.Font != nil || *col.Gap != 2 || col.Items != "start" || !col.FillW || !col.FillH || *col.Padding != [4]float64{8, 8, 8, 8} {
		t.Errorf("column = %+v", col)
	}
	label, count, row := col.Children[0], col.Children[1], col.Children[2]
	// Texts take the font of the views holding them.
	if f := label.Font; f.Size != nil || f.Style != "caption" || f.Design != "rounded" {
		t.Errorf("label font = %+v, want caption, rounded", f)
	}
	if f := count.Font; *f.Size != 20 || f.Weight != 700 || !f.Digits || f.Design != "rounded" {
		t.Errorf("count font = %+v, want 20 bold rounded with monospaced digits", f)
	}
	if f := row.Children[0].Font; *f.Size != 20 || f.Weight != 0 {
		t.Errorf("symbol font = %+v, want the column's", f)
	}
	if f := row.Children[1].Font; *f.Size != 12 {
		t.Errorf("small text font = %+v", f)
	}
	if bg := first.BG; bg.T != "rectangle" || bg.Color == nil {
		t.Errorf("background = %+v", bg)
	}
}

func TestImages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "photo")
	png := []byte("\x89PNG one")
	file := filepath.Join(t.TempDir(), "two.jpg")
	if err := os.WriteFile(file, []byte("\xff\xd8 two"), 0o644); err != nil {
		t.Fatal(err)
	}
	pic := Image(png).Fit(Cover)
	if err := writeTimeline(dir, []Entry{{View: Stack(pic, ImageFile(file), pic)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	tl, images := read(t, dir)
	kids := tl.Entries[0].View.Children
	if len(images) != 2 || kids[0].Image != kids[2].Image || kids[0].Image == kids[1].Image || !slices.Contains(images, kids[1].Image) {
		t.Fatalf("images %v for %+v, want one file per picture", images, kids)
	}
	if kids[0].Fit != "cover" || kids[1].Fit != "" {
		t.Errorf("fits = %q, %q", kids[0].Fit, kids[1].Fit)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, kids[1].Image)); string(data) != "\xff\xd8 two" {
		t.Errorf("image file holds %q", data)
	}

	// A new timeline takes away the images it no longer shows.
	if err := writeTimeline(dir, []Entry{{View: pic}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, images := read(t, dir); !slices.Equal(images, []string{kids[0].Image}) {
		t.Errorf("images after the update = %v, want %s alone", images, kids[0].Image)
	}
	if err := writeTimeline(dir, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if tl, images := read(t, dir); len(tl.Entries) != 0 || len(images) != 0 {
		t.Errorf("an empty timeline left %+v and %v", tl, images)
	}

	if err := writeTimeline(dir, []Entry{{View: ImageFile(filepath.Join(dir, "missing"))}}, time.Now()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an image that cannot be read = %v", err)
	}
}

// Modifiers change copies: a view used twice keeps its own.
func TestViewsAreValues(t *testing.T) {
	base := Text("a").FontSize(10).Padding(4)
	bold := base.Bold().PaddingX(1)
	if base.n.Font.Weight != 0 || *base.n.Padding != [4]float64{4, 4, 4, 4} {
		t.Errorf("base changed: %+v", base.n)
	}
	if bold.n.Font.Weight != 700 || *bold.n.Font.Size != 10 || *bold.n.Padding != [4]float64{4, 1, 4, 1} {
		t.Errorf("bold = %+v %+v", bold.n, bold.n.Font)
	}
	if v := (View{}).Bold().Padding(3); v.n != nil {
		t.Error("the zero view took modifiers")
	}
	if c := Column(Text("a"), View{}, Spacer()); len(c.n.Children) != 2 {
		t.Errorf("children = %d, want the zero view left out", len(c.n.Children))
	}
	if Progress(2).n.Value == nil || *Progress(2).n.Value != 1 || *Gauge(-1).n.Value != 0 {
		t.Error("values out of 0…1")
	}
}

func TestColors(t *testing.T) {
	for _, c := range []struct {
		c    Color
		want string
	}{
		{Hex("#fff"), `{"r":1,"g":1,"b":1,"a":1}`},
		{Hex("336699"), `{"r":0.2,"g":0.4,"b":0.6,"a":1}`},
		{Hex("#00000080"), `{"r":0,"g":0,"b":0,"a":0.502}`},
		{RGBA(255, 0, 0, 0.5).Dark(RGB(0, 255, 0).Dark(Hex("#fff"))), `{"r":1,"g":0,"b":0,"a":0.502,"dark":{"r":0,"g":1,"b":0,"a":1}}`},
		{Secondary, `{"r":0,"g":0,"b":0,"a":1,"name":"secondary"}`},
	} {
		got, err := json.Marshal(c.c)
		if err != nil || string(got) != c.want {
			t.Errorf("%+v = %s, %v, want %s", c.c, got, err, c.want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("Hex of no color did not panic")
		}
	}()
	Hex("#12")
}

func TestUpdate(t *testing.T) {
	if err := Update("no/slash"); err == nil {
		t.Error("Update took a kind with a slash")
	}
	err := Update("counter", Entry{View: Text("1")})
	switch runtime.GOOS {
	case "darwin", "ios":
		// A test is no app built with its widgets.
		if !errors.Is(err, ErrNoWidgets) || Available() {
			t.Errorf("Update outside an app = %v, want ErrNoWidgets", err)
		}
	default:
		if !errors.Is(err, errors.ErrUnsupported) || Available() {
			t.Errorf("Update on %s = %v, want ErrUnsupported", runtime.GOOS, err)
		}
	}
}
