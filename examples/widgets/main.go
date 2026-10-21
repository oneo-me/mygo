// Widgets is a counter in native UI whose widgets show the count: on the
// Mac's desktop and in Notification Center, and on the Home Screen and
// the Lock Screen of an iPhone or an iPad, over a photo picked from the
// library. Its medium widget counts too, with links that open the app.
//
//	go run ./cmd/mygo dev examples/widgets
//	go run ./cmd/mygo dev -ios examples/widgets
package main

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/widget"
)

// state is what the app keeps between runs, in its user data.
type state struct {
	N int `json:"n"`
	// Photo is the file of the photo behind the widgets, "" for none.
	Photo string `json:"photo,omitempty"`
}

type app struct {
	mu  sync.Mutex // the dialog's goroutine changes the state too
	s   state
	dir string // the user data
	msg string // what went wrong last
	win *mygo.Window
}

func (a *app) view(c *ui.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(16).Padding(24).Children(func() {
		ui.Textf(c, "%d", a.s.N).FontSize(56).Bold()
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("decrement"), "−").Label("Decrement").Width(44).Clicked() {
				a.count(a.s.N - 1)
			}
			if ui.Button(c.Key("reset"), "Reset").Disabled(a.s.N == 0).Clicked() {
				a.count(0)
			}
			if ui.PrimaryButton(c.Key("increment"), "+").Label("Increment").Width(44).Clicked() {
				a.count(a.s.N + 1)
			}
		})
		label := "Choose a Photo"
		if a.s.Photo != "" {
			label = "Change the Photo"
		}
		if ui.Button(c.Key("photo"), label).Clicked() {
			go a.pickPhoto()
		}
		if a.msg != "" {
			ui.Text(c, a.msg).FontSize(12).TextColor(t.TextMuted).MaxLines(3)
		} else {
			ui.Text(c, "Add the Counter widget from the system's widgets").FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// count sets the count, which the widgets show. The caller holds a.mu.
func (a *app) count(n int) {
	a.s.N = n
	a.save()
}

// pickPhoto puts a photo the user picks behind the widgets.
func (a *app) pickPhoto() {
	paths, err := mygo.Dialog.OpenPhotos(mygo.PhotoDialogOptions{Parent: a.win})
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.win.Invalidate()
	if err != nil || len(paths) == 0 {
		a.setError(err)
		return
	}
	// The picker's file is a copy in the temporary directory: keep it.
	dst := filepath.Join(a.dir, "photo"+filepath.Ext(paths[0]))
	if err := os.Rename(paths[0], dst); err != nil {
		a.setError(err)
		return
	}
	if a.s.Photo != "" && a.s.Photo != dst {
		os.Remove(a.s.Photo)
	}
	a.s.Photo = dst
	a.save()
}

func (a *app) setError(err error) {
	a.msg = ""
	if err != nil {
		a.msg = err.Error()
	}
}

// save keeps the state and shows it in the widgets. The caller holds a.mu.
func (a *app) save() {
	data, _ := json.Marshal(a.s)
	if err := os.WriteFile(filepath.Join(a.dir, "state.json"), data, 0o644); err != nil {
		a.setError(err)
		return
	}
	a.setError(widget.Update("counter", entry(a.s)))
}

// entry is what the widgets show of the state, in each family.
func entry(s state) widget.Entry {
	text, fill := widget.Primary, widget.RGBA(120, 120, 128, 0.2)
	e := widget.Entry{URL: "mygo-widgets://open"}
	if s.Photo != "" {
		// White text over the photo, darkened so that it reads.
		text, fill = widget.RGB(255, 255, 255), widget.RGBA(255, 255, 255, 0.25)
		e.Background = widget.Stack(
			widget.ImageFile(s.Photo).Fit(widget.Cover),
			widget.Rectangle(widget.RGBA(0, 0, 0, 0.35)),
		)
	}
	count := widget.Column(
		widget.Text("Count").TextStyle(widget.Caption).Opacity(0.8),
		widget.Spacer(),
		widget.Textf("%d", s.N).FontSize(52).Bold().FontDesign(widget.Rounded).MonospacedDigits().MinScale(0.5),
	).AlignItems(widget.Start).Fill().TextColor(text)
	button := func(label, link string) widget.View {
		return widget.Text(label).TextStyle(widget.Headline).FillWidth().PaddingY(8).
			Background(fill).Radius(10).Link(link)
	}
	e.View = count
	e.Families = map[widget.Family]widget.View{
		widget.Medium: widget.Row(
			count,
			widget.Column(button("+1", "mygo-widgets://increment"), button("Reset", "mygo-widgets://reset")).Gap(8).Width(110).TextColor(text),
		).Gap(12),
		widget.Circular: widget.Stack(
			widget.Gauge(float64(s.N%10)/10).Fill(),
			widget.Textf("%d", s.N).FontSize(18).Bold().MinScale(0.5),
		),
		widget.Rectangular: widget.Row(
			widget.Symbol("number.circle.fill").FontSize(28).Accented(),
			widget.Column(widget.Text("Count").TextStyle(widget.Caption), widget.Textf("%d", s.N).TextStyle(widget.Title2).Bold()).AlignItems(widget.Start),
			widget.Spacer(),
		).Gap(8),
		widget.Inline: widget.Textf("Count: %d", s.N),
	}
	return e
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		var err error
		if a.dir, err = mygo.App.Path(mygo.PathUserData); err == nil {
			err = os.MkdirAll(a.dir, 0o755)
		}
		if err != nil {
			log.Fatal(err)
		}
		if data, err := os.ReadFile(filepath.Join(a.dir, "state.json")); err == nil {
			_ = json.Unmarshal(data, &a.s)
		}
		a.mu.Lock()
		a.save() // the widgets show the state from the start
		a.mu.Unlock()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "Widgets",
			Width:   360,
			Height:  320,
			Content: ui.View(a.view),
		})
	})
	// The medium widget's links.
	mygo.App.OnOpenURL(func(link string) {
		u, err := url.Parse(link)
		if err != nil {
			return
		}
		a.mu.Lock()
		switch u.Host {
		case "increment":
			a.count(a.s.N + 1)
		case "reset":
			a.count(0)
		}
		a.mu.Unlock()
		if a.win != nil {
			a.win.Invalidate()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
