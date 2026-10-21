//go:build ios

package ios

import (
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestHIDKeys(t *testing.T) {
	for _, c := range []struct {
		code      int
		chars     string
		key, phys platform.Key
		text      bool
	}{
		{0x04, "a", platform.KeyA, platform.KeyA, true},
		// AZERTY's A is where QWERTY's Q is.
		{0x14, "a", platform.KeyA, platform.KeyQ, true},
		{0x27, "0", platform.Key0, platform.Key0, true},
		{0x1E, "1", platform.Key1, platform.Key1, true},
		{0x28, "\r", platform.KeyEnter, platform.KeyEnter, false},
		{0x2A, "\b", platform.KeyBackspace, platform.KeyBackspace, false},
		{0x2C, " ", platform.KeySpace, platform.KeySpace, true},
		{0x50, "", platform.KeyLeft, platform.KeyLeft, false},
		{0x45, "", platform.KeyF12, platform.KeyF12, false},
		{0x36, ",", platform.KeyComma, platform.KeyComma, true},
		// A layout typing no US key, as Cyrillic's.
		{0x05, "и", platform.KeyUnknown, platform.KeyB, true},
	} {
		if k := layoutKey(c.code, c.chars); k != c.key {
			t.Errorf("%#x %q: key %v, want %v", c.code, c.chars, k, c.key)
		}
		if k := physicalKey(c.code); k != c.phys {
			t.Errorf("%#x: physical key %v, want %v", c.code, k, c.phys)
		}
		if typesText(c.code) != c.text {
			t.Errorf("%#x types text: %v", c.code, !c.text)
		}
	}
	for _, code := range []int{0xE0, 0xE3, 0xE7, 0x39} {
		if !modifierKey(code) {
			t.Errorf("%#x is a modifier key", code)
		}
	}
	if got := typedChars("a\x7fé"); got != "aé" {
		t.Errorf("typed %q", got)
	}
}

func TestDocumentRanges(t *testing.T) {
	s := &surface{}
	// "ab😀cd" with "cd" selected, and nothing composed.
	s.ti = platform.TextInputState{Active: true, Text: "ab😀cd", Start: 3, End: 5}
	if a, b := s.selectedRange(); a != 4 || b != 6 {
		t.Errorf("selection in UTF-16 units: %d to %d", a, b)
	}
	if got := s.docText(1, 4); got != "b😀" {
		t.Errorf("text of 1 to 4: %q", got)
	}
	if n := s.docLen(); n != 6 {
		t.Errorf("length %d", n)
	}
	// Composing "nǐ" in place of the selection.
	s.marked, s.markedSel = "nǐ", nsRange{Location: 2}
	if a, b, ok := s.markedRange(); !ok || a != 4 || b != 6 {
		t.Errorf("marked range %d to %d (%v)", a, b, ok)
	}
	if a, b := s.selectedRange(); a != 6 || b != 6 {
		t.Errorf("selection in the composition: %d to %d", a, b)
	}
	// A range of the marked text stands for the selection it replaces;
	// one before it is the text's own.
	if from, to := s.textRangeOf(nsRange{Location: 4, Length: 1}); from != 3 || to != 5 {
		t.Errorf("a rune of the composition is runes %d to %d", from, to)
	}
	if from, to := s.textRangeOf(nsRange{Location: 0, Length: 2}); from != 0 || to != 2 {
		t.Errorf("the first two units are runes %d to %d", from, to)
	}
}

func TestAccessNodes(t *testing.T) {
	button := &platform.AccessNode{Role: platform.RoleButton, Label: "OK", Bounds: platform.RectF{W: 10, H: 10}, Actions: platform.ActionPress}
	if !shown(button) {
		t.Error("a button shows")
	}
	group := &platform.AccessNode{Role: platform.RoleGroup, Bounds: platform.RectF{W: 10, H: 10}}
	if shown(group) {
		t.Error("a group of nothing shows")
	}
	off := *button
	off.States = platform.AccessOffscreen
	if shown(&off) {
		t.Error("an element out of view shows")
	}
	slider := &platform.AccessNode{Role: platform.RoleSlider, Min: 0, Max: 200, Now: 50}
	if v := accessValue(slider); v != "25%" {
		t.Errorf("a slider's value is %q", v)
	}
	password := &platform.AccessNode{Role: platform.RoleTextField, Value: "secret", States: platform.AccessPassword}
	if v := accessValue(password); v != "" {
		t.Errorf("a password reads %q", v)
	}
}
