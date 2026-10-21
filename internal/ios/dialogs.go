//go:build ios

package ios

import (
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// Message boxes, the alerts of pages and context menus are
// UIAlertControllers, presented by the window in front.

const (
	uiAlertControllerStyleActionSheet = 0
	uiAlertControllerStyleAlert       = 1
	uiAlertActionStyleDefault         = 0
	uiAlertActionStyleCancel          = 1
)

// presenter returns the view controller to present from: the one the
// window in front presents last, or that window's own.
func (b *Backend) presenter() id {
	if len(b.shown) == 0 {
		return 0
	}
	w := b.shown[len(b.shown)-1]
	if w.win == 0 {
		return 0
	}
	vc := w.vc
	for {
		next := send(vc, "presentedViewController")
		if next == 0 {
			return vc
		}
		vc = next
	}
}

// alertButton is a button of an alert, or an item of an action sheet.
type alertButton struct {
	title    string
	cancel   bool
	disabled bool
}

// alertOptions configures an alert.
type alertOptions struct {
	title, message string
	buttons        []alertButton
	sheet          bool
	// field shows a text field holding its text, which done gets.
	field *string
	// anchor is where an action sheet points from on an iPad, in the
	// surface of the window in front.
	anchor CGRect
}

// alert presents an alert, and calls done once with the index of the
// button chosen, -1 when it could not show, and the field's text.
func (b *Backend) alert(o alertOptions, done func(index int, text string)) {
	vc := b.presenter()
	if vc == 0 {
		done(-1, "")
		return
	}
	withPool(func() {
		style := uintptr(uiAlertControllerStyleAlert)
		if o.sheet {
			style = uiAlertControllerStyleActionSheet
		}
		var title, message id
		if o.title != "" {
			title = nsString(o.title)
		}
		if o.message != "" {
			message = nsString(o.message)
		}
		ac := send(class("UIAlertController"), "alertControllerWithTitle:message:preferredStyle:", uintptr(title), uintptr(message), style)
		var field id
		if o.field != nil {
			text := *o.field
			blk := newBlock(func(_ objc.Block, f id) {
				field = f
				send(f, "setText:", uintptr(nsString(text)))
			})
			send(ac, "addTextFieldWithConfigurationHandler:", uintptr(blk))
			blk.Release()
		}
		answered := false
		for i, btn := range o.buttons {
			actionStyle := uintptr(uiAlertActionStyleDefault)
			if btn.cancel {
				actionStyle = uiAlertActionStyleCancel
			}
			index := i
			blk := newBlock(func(_ objc.Block, action id) {
				if answered {
					return
				}
				answered = true
				text := ""
				if field != 0 {
					text = goString(send(field, "text"))
				}
				done(index, text)
			})
			action := send(class("UIAlertAction"), "actionWithTitle:style:handler:", uintptr(nsString(btn.title)), actionStyle, uintptr(blk))
			blk.Release()
			if btn.disabled {
				send(action, "setEnabled:", 0)
			}
			send(ac, "addAction:", uintptr(action))
		}
		if pop := send(ac, "popoverPresentationController"); pop != 0 {
			// An iPad shows action sheets as popovers, which need an
			// anchor.
			view := send(vc, "view")
			if n := len(b.shown); n > 0 {
				view = b.shown[n-1].s.view
			}
			send(pop, "setSourceView:", uintptr(view))
			r := o.anchor
			if r.Size.Width == 0 && r.Size.Height == 0 && r.Origin == (CGPoint{}) {
				bounds := msgRect(view, sel("bounds"))
				r = rect(bounds.Size.Width/2, bounds.Size.Height/2, 1, 1)
			}
			msgSetRect(pop, sel("setSourceRect:"), r)
		}
		send(vc, "presentViewController:animated:completion:", uintptr(ac), 1, 0)
	})
}

type dialogs struct{ b *Backend }

func (d dialogs) ShowMessageBox(_ platform.Window, o *platform.MessageBoxOptions, cb func(platform.MessageBoxResult, error)) {
	title, message := o.Title, o.Message
	if title == "" {
		title, message = o.Message, o.Detail
	} else if o.Detail != "" {
		message += "\n\n" + o.Detail
	}
	buttons := o.Buttons
	if len(buttons) == 0 {
		buttons = []string{platform.CurrentTexts().OK}
	}
	opts := alertOptions{title: title, message: message}
	for i, label := range buttons {
		opts.buttons = append(opts.buttons, alertButton{title: label, cancel: i == o.CancelID && len(buttons) > 1})
	}
	d.b.alert(opts, func(index int, _ string) {
		if index < 0 {
			cb(platform.MessageBoxResult{}, errUnsupported)
			return
		}
		cb(platform.MessageBoxResult{Response: index, CheckboxChecked: o.CheckboxChecked}, nil)
	})
}

// PopupMenu shows a context menu as an action sheet of its items, the
// items of its submenus after theirs, and waits until it is dismissed.
func (b *Backend) PopupMenu(m *platform.Menu, pw platform.Window, pos *platform.Point) {
	var ids []int
	opts := alertOptions{sheet: true}
	var add func(m *platform.Menu)
	add = func(m *platform.Menu) {
		for _, it := range m.Items {
			if !it.Visible {
				continue
			}
			switch it.Type {
			case platform.MenuItemSeparator:
			case platform.MenuItemSubmenu:
				if it.Submenu != nil {
					add(it.Submenu)
				}
			default:
				label := it.Label
				if it.Checked {
					label = "✓ " + label
				}
				opts.buttons = append(opts.buttons, alertButton{title: label, disabled: !it.Enabled})
				ids = append(ids, it.ID)
			}
		}
	}
	add(m)
	if len(ids) == 0 {
		return
	}
	opts.buttons = append(opts.buttons, alertButton{title: platform.CurrentTexts().Cancel, cancel: true})
	if w, ok := pw.(*window); ok && w != nil && pos != nil {
		opts.anchor = rect(float64(pos.X), float64(pos.Y), 1, 1)
	}
	done := false
	b.alert(opts, func(index int, _ string) {
		done = true
		if index >= 0 && index < len(ids) {
			id := ids[index]
			b.post(func() { b.h.MenuItemClicked(id) })
		}
	})
	for !done {
		b.Step()
	}
}
