//go:build ios

package ios

import (
	"reflect"
	"structs"
	"unicode/utf16"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// The text input view is the first responder while the content edits
// text, which shows the software keyboard, and the UITextInput client of
// input methods. Their document is the text around the caret of the
// content's TextInputState, with the marked text, the composition, in
// place of the selection while there is one, as internal/darwin's
// NSTextInputClient has it; a TextInputClient's document is its own.
// Positions are offsets in UTF-16 units of that document, in objects of
// MyGoTextPosition, and ranges MyGoTextRange.

// nsRange is NSRange.
type nsRange struct {
	_                structs.HostLayout
	Location, Length uint
}

const nsNotFound = uint(1<<63 - 1)

var (
	positionOffset objc.Ivar
	rangeFrom      objc.Ivar
	rangeTo        objc.Ivar
)

// textPosition returns an autoreleased position at offset n.
func textPosition(n int) id {
	p := alloc("MyGoTextPosition")
	p.SetIvar(positionOffset, id(max(n, 0)))
	return autorelease(p)
}

// offsetOf returns the offset of a position, 0 for one of another class.
func offsetOf(p id) int {
	if p == 0 || !sendBool(p, "isKindOfClass:", uintptr(class("MyGoTextPosition"))) {
		return 0
	}
	return int(p.GetIvar(positionOffset))
}

// textRange returns an autoreleased range from a to b.
func textRange(a, b int) id {
	a, b = min(a, b), max(a, b)
	r := alloc("MyGoTextRange")
	r.SetIvar(rangeFrom, id(max(a, 0)))
	r.SetIvar(rangeTo, id(max(b, 0)))
	return autorelease(r)
}

// rangeOf returns the offsets of a range, and false for none.
func rangeOf(r id) (from, to int, ok bool) {
	if r == 0 || !sendBool(r, "isKindOfClass:", uintptr(class("MyGoTextRange"))) {
		return 0, 0, false
	}
	return int(r.GetIvar(rangeFrom)), int(r.GetIvar(rangeTo)), true
}

func nsRangeOf(from, to int) nsRange {
	return nsRange{Location: uint(from), Length: uint(max(0, to-from))}
}

// document returns the text input methods see, and where the selection,
// or the marked text, starts and ends in it, in runes.
func (s *surface) document() (doc []rune, start, end int) {
	text := []rune(s.ti.Text)
	start = max(0, min(s.ti.Start, len(text)))
	end = max(start, min(s.ti.End, len(text)))
	if s.marked == "" {
		return text, start, end
	}
	m := []rune(s.marked)
	doc = make([]rune, 0, len(text)-(end-start)+len(m))
	doc = append(append(append(doc, text[:start]...), m...), text[end:]...)
	return doc, start, start + len(m)
}

// units returns how many UTF-16 units runes take.
func units(runes []rune) int {
	n := 0
	for _, r := range runes {
		n += utf16.RuneLen(r)
	}
	return n
}

// runeAt returns the rune of runes that UTF-16 unit u starts, or the end.
func runeAt(runes []rune, u int) int {
	for i, r := range runes {
		if u <= 0 {
			return i
		}
		u -= utf16.RuneLen(r)
	}
	return len(runes)
}

// runesBefore converts a UTF-16 offset in s into a rune offset.
func runesBefore(s string, n int) int {
	u := utf16.Encode([]rune(s))
	n = max(0, min(n, len(u)))
	return len(utf16.Decode(u[:n]))
}

// docLen returns the length of the document, in UTF-16 units.
func (s *surface) docLen() int {
	if c := s.ti.Client; c != nil {
		const far = 1 << 30
		_, r := c.TextForRange(platform.TextRange{Start: far, End: far})
		return max(r.End, 0)
	}
	doc, _, _ := s.document()
	return units(doc)
}

// docText returns the text of the document from a to b, UTF-16 units.
func (s *surface) docText(a, b int) string {
	if c := s.ti.Client; c != nil {
		t, _ := c.TextForRange(platform.TextRange{Start: a, End: b})
		return t
	}
	doc, _, _ := s.document()
	u := utf16.Encode(doc)
	a = max(0, min(a, len(u)))
	b = max(a, min(b, len(u)))
	return string(utf16.Decode(u[a:b]))
}

func (s *surface) markedRange() (from, to int, ok bool) {
	if c := s.ti.Client; c != nil {
		r, ok := c.MarkedRange()
		return r.Start, r.End, ok
	}
	if s.marked == "" {
		return 0, 0, false
	}
	doc, start, end := s.document()
	return units(doc[:start]), units(doc[:end]), true
}

func (s *surface) selectedRange() (from, to int) {
	if c := s.ti.Client; c != nil {
		r := platform.NormalizeTextRange(c.Selection().Range)
		return r.Start, r.End
	}
	doc, start, end := s.document()
	if s.marked != "" {
		at := units(doc[:start]) + int(s.markedSel.Location)
		return at, at + int(s.markedSel.Length)
	}
	return units(doc[:start]), units(doc[:end])
}

// textRangeOf converts a range of the document, in UTF-16 units, into runes
// of TextInputState.Text: a range in the marked text stands for the
// selection it replaces.
func (s *surface) textRangeOf(r nsRange) (from, to int) {
	doc, start, end := s.document()
	lo := runeAt(doc, int(min(r.Location, uint(1<<31))))
	hi := runeAt(doc, int(min(r.Location+r.Length, uint(1<<31))))
	if s.marked == "" {
		return lo, hi
	}
	n := len([]rune(s.ti.Text))
	sel := max(0, min(s.ti.End, n)) - max(0, min(s.ti.Start, n))
	conv := func(i int, after bool) int {
		switch {
		case i <= start:
			return i
		case i < end: // in the marked text
			if after {
				return start + max(sel, 0)
			}
			return start
		}
		return i - (end - start) + max(sel, 0)
	}
	return conv(lo, false), conv(hi, true)
}

func clientRange(r nsRange) *platform.TextRange {
	if r.Location == nsNotFound {
		return nil
	}
	v := platform.TextRange{Start: int(min(r.Location, uint(1<<31))), End: int(min(r.Location+r.Length, uint(1<<31)))}
	return &v
}

func (s *surface) hasMarkedText() bool {
	_, _, ok := s.markedRange()
	return ok
}

// edit runs an input method's change, which it knows of: the content's
// new state tells its delegate nothing.
func (s *surface) edit(fn func()) {
	s.editing = true
	defer func() { s.editing = false }()
	fn()
}

func (s *surface) setMarkedText(text string, selected, replacement nsRange) {
	s.edit(func() {
		if c := s.ti.Client; c != nil {
			c.SetMarkedText(clientRange(replacement), text, platform.TextRange{Start: int(selected.Location), End: int(selected.Location + selected.Length)})
			return
		}
		ev := platform.SurfaceEvent{Kind: platform.TextComposition, Text: text, Caret: runesBefore(text, int(selected.Location))}
		if replacement.Location != nsNotFound {
			ev.Replace = true
			ev.From, ev.To = s.textRangeOf(replacement)
		}
		s.marked, s.markedSel = text, selected
		s.send(ev)
	})
}

func (s *surface) insertText(text string, replacement nsRange) {
	s.edit(func() {
		if c := s.ti.Client; c != nil {
			c.ReplaceText(clientRange(replacement), text)
			return
		}
		ev := platform.SurfaceEvent{Kind: platform.TextInput, Text: text}
		if replacement.Location != nsNotFound {
			ev.Replace = true
			ev.From, ev.To = s.textRangeOf(replacement)
		}
		if s.marked != "" {
			s.marked = ""
			s.send(platform.SurfaceEvent{Kind: platform.TextComposition})
		}
		if text != "" || ev.Replace {
			s.send(ev)
		}
	})
}

// unmarkText commits the composition as it is, as UIKit's own text views
// do when the input method lets go of it.
func (s *surface) unmarkText() {
	s.edit(func() {
		if c := s.ti.Client; c != nil {
			c.UnmarkText()
			return
		}
		if m := s.marked; m != "" {
			s.marked = ""
			s.send(platform.SurfaceEvent{Kind: platform.TextInput, Text: m})
		}
	})
}

// typeText takes the text the software keyboard types: its Return and
// Tab are keys, which the content handles as a hardware keyboard's.
func (s *surface) typeText(text string) {
	switch text {
	case "\n", "\r":
		s.tapKey(platform.KeyEnter)
	case "\t":
		s.tapKey(platform.KeyTab)
	default:
		s.insertText(text, nsRange{Location: nsNotFound})
	}
}

// tapKey presses and releases a key of the software keyboard.
func (s *surface) tapKey(k platform.Key) {
	s.edit(func() {
		if !s.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: k, PhysicalKey: k}) && k == platform.KeyBackspace && s.ti.Client != nil {
			// A client deletes what the content's key did not.
			if a, b := s.selectedRange(); a != b {
				s.ti.Client.ReplaceText(&platform.TextRange{Start: a, End: b}, "")
			} else if a > 0 {
				prev := []rune(s.docText(0, a))
				n := 1
				if len(prev) > 0 && utf16.RuneLen(prev[len(prev)-1]) == 2 {
					n = 2
				}
				s.ti.Client.ReplaceText(&platform.TextRange{Start: a - n, End: a}, "")
			}
		}
		s.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: k, PhysicalKey: k})
	})
}

func (s *surface) SetTextInput(t platform.TextInputState) {
	was := s.ti
	if was.Active && (!t.Active || t.Client != was.Client || t.NoInputMethod && !was.NoInputMethod) {
		// The composition goes with the input it was for.
		s.marked = ""
	}
	changed := t.Text != was.Text || t.Start != was.Start || t.End != was.End || t.Client != was.Client
	d := s.inputDelegate
	notify := d != 0 && !s.editing && t.Active && was.Active && changed
	if notify {
		send(d, "selectionWillChange:", uintptr(s.input))
		send(d, "textWillChange:", uintptr(s.input))
	}
	s.ti = t
	if notify {
		send(d, "textDidChange:", uintptr(s.input))
		send(d, "selectionDidChange:", uintptr(s.input))
	}
	s.placeInput()
	switch {
	case t.Active && !sendBool(s.input, "isFirstResponder") && s.w.key:
		send(s.input, "becomeFirstResponder")
	case !t.Active && sendBool(s.input, "isFirstResponder"):
		send(s.view, "becomeFirstResponder")
	case t.Active && was.Active && t.NoInputMethod != was.NoInputMethod:
		send(s.input, "reloadInputViews")
	}
}

// placeInput puts the text input view at the caret, where input methods
// show what they offer.
func (s *surface) placeInput() {
	c := s.ti.Caret
	msgSetRect(s.input, sel("setFrame:"), rect(c.X, c.Y, max(c.W, 1), max(c.H, 1)))
}

// rangeRect returns where the text of a range is, in the surface: the
// client's bounds of it, else the caret.
func (s *surface) rangeRect(from, to int) CGRect {
	c := s.ti.Caret
	if client := s.ti.Client; client != nil {
		if r, _, ok := client.BoundsForRange(platform.TextRange{Start: from, End: to}); ok {
			c = r
		}
	}
	return rect(c.X, c.Y, max(c.W, 1), c.H)
}

// positionAt returns the offset of the document at x, y in the surface.
func (s *surface) positionAt(x, y float64) int {
	if c := s.ti.Client; c != nil {
		if i, ok := c.IndexForPoint(x, y); ok {
			return i
		}
	}
	a, _ := s.selectedRange()
	return a
}

func registerTextInputClasses() {
	b := func() *Backend { return theBackend }
	offset := []objc.FieldDef{{Name: "offset", Type: reflect.TypeFor[int](), Attribute: objc.ReadOnly}}
	pos := classDef("MyGoTextPosition", "UITextPosition", nil, offset, nil)
	positionOffset = objc.Class(pos).InstanceVariable("offset")
	ends := []objc.FieldDef{
		{Name: "from", Type: reflect.TypeFor[int](), Attribute: objc.ReadOnly},
		{Name: "to", Type: reflect.TypeFor[int](), Attribute: objc.ReadOnly},
	}
	rng := classDef("MyGoTextRange", "UITextRange", nil, ends, []objc.MethodDef{
		method("start", func(self id, _ objc.SEL) id { return textPosition(int(self.GetIvar(rangeFrom))) }),
		method("end", func(self id, _ objc.SEL) id { return textPosition(int(self.GetIvar(rangeTo))) }),
		method("isEmpty", func(self id, _ objc.SEL) bool { return self.GetIvar(rangeFrom) == self.GetIvar(rangeTo) }),
	})
	rangeFrom = objc.Class(rng).InstanceVariable("from")
	rangeTo = objc.Class(rng).InstanceVariable("to")

	surfaceOf := func(self id) *surface { return b().surfaceOf(self) }
	focus := func(self id, cmd objc.SEL, become bool) bool {
		ok := byte(sendSuper(self, "MyGoTextInputView", cmd)) != 0
		if s := surfaceOf(self); s != nil && ok {
			if become {
				s.checkFocus()
			} else {
				b().post(func() {
					if !s.w.closed {
						s.checkFocus()
					}
				})
			}
		}
		return ok
	}
	// A trait of the text: none of UIKit's own corrections, which edit
	// text the content may not show as prose.
	trait := func(name string, v int) objc.MethodDef {
		return method(name, func(self id, _ objc.SEL) int { return v })
	}
	classDef("MyGoTextInputView", "UIView", []string{"UITextInput"}, nil, append(keyMethods("MyGoTextInputView"), []objc.MethodDef{
		method("canBecomeFirstResponder", func(self id, _ objc.SEL) bool { return true }),
		method("becomeFirstResponder", func(self id, cmd objc.SEL) bool { return focus(self, cmd, true) }),
		method("resignFirstResponder", func(self id, cmd objc.SEL) bool { return focus(self, cmd, false) }),

		// UITextInputTraits
		trait("autocapitalizationType", 0), // none
		trait("autocorrectionType", 1),     // no
		trait("spellCheckingType", 1),      // no
		trait("smartQuotesType", 1),        // no
		trait("smartDashesType", 1),        // no
		trait("smartInsertDeleteType", 1),  // no
		trait("inlinePredictionType", 1),   // no
		trait("keyboardType", 0),
		trait("returnKeyType", 0),

		// UIKeyInput
		method("hasText", func(self id, _ objc.SEL) bool {
			s := surfaceOf(self)
			return s != nil && s.docLen() > 0
		}),
		method("insertText:", func(self id, _ objc.SEL, text id) {
			if s := surfaceOf(self); s != nil {
				s.typeText(goString(text))
			}
		}),
		method("deleteBackward", func(self id, _ objc.SEL) {
			if s := surfaceOf(self); s != nil {
				s.tapKey(platform.KeyBackspace)
			}
		}),

		// UITextInput
		method("textInputView", func(self id, _ objc.SEL) id {
			if s := surfaceOf(self); s != nil {
				return s.view
			}
			return self
		}),
		method("textInRange:", func(self id, _ objc.SEL, r id) id {
			s := surfaceOf(self)
			from, to, ok := rangeOf(r)
			if s == nil || !ok {
				return nsString("")
			}
			return nsString(s.docText(from, to))
		}),
		method("replaceRange:withText:", func(self id, _ objc.SEL, r, text id) {
			if s := surfaceOf(self); s != nil {
				if from, to, ok := rangeOf(r); ok {
					s.insertText(goString(text), nsRangeOf(from, to))
				}
			}
		}),
		method("selectedTextRange", func(self id, _ objc.SEL) id {
			s := surfaceOf(self)
			if s == nil || !s.ti.Active {
				return 0
			}
			return textRange(s.selectedRange())
		}),
		// The content keeps its selection: input methods moving it, as the
		// space bar's trackpad does, move nothing yet.
		method("setSelectedTextRange:", func(self id, _ objc.SEL, r id) {}),
		method("markedTextRange", func(self id, _ objc.SEL) id {
			if s := surfaceOf(self); s != nil {
				if from, to, ok := s.markedRange(); ok {
					return textRange(from, to)
				}
			}
			return 0
		}),
		method("markedTextStyle", func(self id, _ objc.SEL) id { return 0 }),
		method("setMarkedTextStyle:", func(self id, _ objc.SEL, style id) {}),
		method("setMarkedText:selectedRange:", func(self id, _ objc.SEL, text id, selected nsRange) {
			if s := surfaceOf(self); s != nil {
				s.setMarkedText(stringOf(text), selected, nsRange{Location: nsNotFound})
			}
		}),
		method("setAttributedMarkedText:selectedRange:", func(self id, _ objc.SEL, text id, selected nsRange) {
			if s := surfaceOf(self); s != nil {
				s.setMarkedText(stringOf(text), selected, nsRange{Location: nsNotFound})
			}
		}),
		method("unmarkText", func(self id, _ objc.SEL) {
			if s := surfaceOf(self); s != nil {
				s.unmarkText()
			}
		}),
		method("beginningOfDocument", func(self id, _ objc.SEL) id { return textPosition(0) }),
		method("endOfDocument", func(self id, _ objc.SEL) id {
			if s := surfaceOf(self); s != nil {
				return textPosition(s.docLen())
			}
			return textPosition(0)
		}),
		method("textRangeFromPosition:toPosition:", func(self id, _ objc.SEL, a, z id) id {
			return textRange(offsetOf(a), offsetOf(z))
		}),
		method("positionFromPosition:offset:", func(self id, _ objc.SEL, p id, n int) id {
			s := surfaceOf(self)
			at := offsetOf(p) + n
			if s == nil || at < 0 || at > s.docLen() {
				return 0
			}
			return textPosition(at)
		}),
		method("positionFromPosition:inDirection:offset:", func(self id, _ objc.SEL, p id, dir int, n int) id {
			s := surfaceOf(self)
			if s == nil {
				return 0
			}
			at := offsetOf(p)
			switch dir {
			case 2: // UITextLayoutDirectionRight
				at += n
			case 3: // UITextLayoutDirectionLeft
				at -= n
			}
			return textPosition(max(0, min(at, s.docLen())))
		}),
		method("comparePosition:toPosition:", func(self id, _ objc.SEL, a, z id) int {
			switch x, y := offsetOf(a), offsetOf(z); {
			case x < y:
				return -1 // NSOrderedAscending
			case x > y:
				return 1
			}
			return 0
		}),
		method("offsetFromPosition:toPosition:", func(self id, _ objc.SEL, a, z id) int {
			return offsetOf(z) - offsetOf(a)
		}),
		method("inputDelegate", func(self id, _ objc.SEL) id {
			if s := surfaceOf(self); s != nil {
				return s.inputDelegate
			}
			return 0
		}),
		// Weak, as UIKit's text views keep it.
		method("setInputDelegate:", func(self id, _ objc.SEL, d id) {
			if s := b().bySurface[self]; s != nil {
				s.inputDelegate = d
			}
		}),
		method("tokenizer", func(self id, _ objc.SEL) id {
			s := b().bySurface[self]
			if s == nil {
				return 0
			}
			if s.tokenizer == 0 {
				s.tokenizer = send(send(class("UITextInputStringTokenizer"), "alloc"), "initWithTextInput:", uintptr(self))
			}
			return s.tokenizer
		}),
		method("positionWithinRange:farthestInDirection:", func(self id, _ objc.SEL, r id, dir int) id {
			from, to, _ := rangeOf(r)
			if dir == 3 || dir == 4 { // left, up
				return textPosition(from)
			}
			return textPosition(to)
		}),
		method("characterRangeByExtendingPosition:inDirection:", func(self id, _ objc.SEL, p id, dir int) id {
			s := surfaceOf(self)
			at := offsetOf(p)
			if dir == 3 || dir == 4 {
				return textRange(max(at-1, 0), at)
			}
			end := at + 1
			if s != nil {
				end = min(end, s.docLen())
			}
			return textRange(at, end)
		}),
		method("baseWritingDirectionForPosition:inDirection:", func(self id, _ objc.SEL, p id, dir int) int {
			return -1 // NSWritingDirectionNatural
		}),
		method("setBaseWritingDirection:forRange:", func(self id, _ objc.SEL, dir int, r id) {}),
		method("firstRectForRange:", func(self id, _ objc.SEL, r id) CGRect {
			s := surfaceOf(self)
			if s == nil {
				return CGRect{}
			}
			from, to, _ := rangeOf(r)
			return s.rangeRect(from, to)
		}),
		method("caretRectForPosition:", func(self id, _ objc.SEL, p id) CGRect {
			s := surfaceOf(self)
			if s == nil {
				return CGRect{}
			}
			at := offsetOf(p)
			r := s.rangeRect(at, at)
			r.Size.Width = 2
			return r
		}),
		method("selectionRectsForRange:", func(self id, _ objc.SEL, r id) id { return nsArray() }),
		method("closestPositionToPoint:", func(self id, _ objc.SEL, p CGPoint) id {
			if s := surfaceOf(self); s != nil {
				return textPosition(s.positionAt(p.X, p.Y))
			}
			return textPosition(0)
		}),
		method("closestPositionToPoint:withinRange:", func(self id, _ objc.SEL, p CGPoint, r id) id {
			s := surfaceOf(self)
			from, to, _ := rangeOf(r)
			at := from
			if s != nil {
				at = max(from, min(s.positionAt(p.X, p.Y), to))
			}
			return textPosition(at)
		}),
		method("characterRangeAtPoint:", func(self id, _ objc.SEL, p CGPoint) id {
			at := 0
			if s := surfaceOf(self); s != nil {
				at = s.positionAt(p.X, p.Y)
			}
			return textRange(at, at)
		}),
	}...))
}
