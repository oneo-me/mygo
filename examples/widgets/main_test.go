package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/widget"
)

// The buttons count, and the state they change is kept for the next run.
func TestCounter(t *testing.T) {
	a := &app{dir: t.TempDir()}
	tt := ui.NewTester(a.view, 360, 320)
	for _, label := range []string{"Increment", "Increment", "Decrement"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if a.s.N != 1 || !tt.HasText("1") {
		t.Errorf("count %d after the buttons, texts %q", a.s.N, tt.Texts())
	}
	var kept state
	data, err := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if err != nil || json.Unmarshal(data, &kept) != nil || kept.N != 1 {
		t.Errorf("state kept = %s, %v", data, err)
	}
}

// Every family the widget comes in has a view, and a photo goes behind
// them.
func TestEntry(t *testing.T) {
	e := entry(state{N: 3})
	if e.View == (widget.View{}) || e.Background != (widget.View{}) || e.URL == "" {
		t.Errorf("entry without a photo = %+v", e)
	}
	for _, f := range []widget.Family{widget.Medium, widget.Circular, widget.Rectangular, widget.Inline} {
		if e.Families[f] == (widget.View{}) {
			t.Errorf("no view for %s", f)
		}
	}
	if e := entry(state{N: 3, Photo: "/photo.jpg"}); e.Background == (widget.View{}) {
		t.Error("no background with a photo")
	}
}
