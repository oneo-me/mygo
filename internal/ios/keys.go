//go:build ios

package ios

import (
	"strings"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// The keys of hardware keyboards come as UIPresses, whose UIKey tells its
// HID usage, the characters it types and the modifier keys held. The
// surface and its text input view hear them as the first responder: keys
// typing text while the content edits go on to UIKit too, which gives
// them to input methods, and those come back through UITextInput.

// hidKeys maps the HID usages of the keys that type no character.
var hidKeys = map[int]platform.Key{
	0x28: platform.KeyEnter, 0x58: platform.KeyEnter, 0x29: platform.KeyEscape, 0x2A: platform.KeyBackspace,
	0x2B: platform.KeyTab, 0x2C: platform.KeySpace, 0x49: platform.KeyInsert, 0x4A: platform.KeyHome,
	0x4B: platform.KeyPageUp, 0x4C: platform.KeyDelete, 0x4D: platform.KeyEnd, 0x4E: platform.KeyPageDown,
	0x4F: platform.KeyRight, 0x50: platform.KeyLeft, 0x51: platform.KeyDown, 0x52: platform.KeyUp,
	0x3A: platform.KeyF1, 0x3B: platform.KeyF2, 0x3C: platform.KeyF3, 0x3D: platform.KeyF4,
	0x3E: platform.KeyF5, 0x3F: platform.KeyF6, 0x40: platform.KeyF7, 0x41: platform.KeyF8,
	0x42: platform.KeyF9, 0x43: platform.KeyF10, 0x44: platform.KeyF11, 0x45: platform.KeyF12,
	0x65: platform.KeyContextMenu,
}

// hidPositions maps the HID usages of the letter, digit and punctuation
// keys to the key at that position of an ANSI US keyboard.
var hidPositions = map[int]platform.Key{
	0x2D: platform.KeyMinus, 0x2E: platform.KeyEqual, 0x2F: platform.KeyBracketLeft, 0x30: platform.KeyBracketRight,
	0x31: platform.KeyBackslash, 0x33: platform.KeySemicolon, 0x34: platform.KeyQuote, 0x35: platform.KeyBackquote,
	0x36: platform.KeyComma, 0x37: platform.KeyPeriod, 0x38: platform.KeySlash,
}

// physicalKey returns the key at a HID usage's position of an ANSI US
// keyboard.
func physicalKey(code int) platform.Key {
	switch {
	case code >= 0x04 && code <= 0x1D:
		return platform.KeyA + platform.Key(code-0x04)
	case code >= 0x1E && code <= 0x26:
		return platform.Key1 + platform.Key(code-0x1E)
	case code == 0x27:
		return platform.Key0
	}
	if k, ok := hidKeys[code]; ok {
		return k
	}
	return hidPositions[code]
}

// layoutKey returns the key a press stands for: the keys typing no
// character by their usage, the others by what they type unshifted.
func layoutKey(code int, chars string) platform.Key {
	if k, ok := hidKeys[code]; ok {
		return k
	}
	for _, r := range chars {
		return platform.KeyForRune(r)
	}
	return platform.KeyUnknown
}

// modifierKey reports whether a HID usage is a modifier key's: Control,
// Shift, Option and Command, left and right, and Caps Lock.
func modifierKey(code int) bool { return code >= 0xE0 && code <= 0xE7 || code == 0x39 }

// typesText reports whether the key at a HID usage types text: not the
// keys of hidKeys but Space.
func typesText(code int) bool {
	_, other := hidKeys[code]
	return !other || code == 0x2C
}

// typedChars returns the text of a key's characters, without control
// characters.
func typedChars(chars string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r >= 0xF700 && r <= 0xF8FF {
			return -1
		}
		return r
	}, chars)
}

// keyDown tells the content of a key going down, and reports whether
// UIKit gets it too: a key typing text while an input method edits, or one
// the content leaves alone.
func (s *surface) keyDown(key id, mods platform.Modifiers) (pass bool) {
	code := sendInt(key, "keyCode")
	if modifierKey(code) {
		s.send(platform.SurfaceEvent{Kind: platform.ModifiersChanged, Mods: mods})
		return true
	}
	text := s.ti.Active && mods&(platform.ModSuper|platform.ModCtrl) == 0
	ime := text && !s.ti.NoInputMethod
	if ime && s.hasMarkedText() {
		// A key typed while the input method composes is its alone, as
		// Enter choosing a candidate.
		return true
	}
	k := layoutKey(code, goString(send(key, "charactersIgnoringModifiers")))
	ev := platform.SurfaceEvent{Kind: platform.KeyPressed, Key: k, PhysicalKey: physicalKey(code), Mods: mods}
	taken := s.send(ev)
	if text && !taken && typesText(code) {
		if ime {
			return true
		}
		// Input methods off: the key types the characters of the layout.
		if t := typedChars(goString(send(key, "characters"))); t != "" {
			s.insertText(t, nsRange{Location: nsNotFound})
		}
		return false
	}
	if !typesText(code) || mods&(platform.ModSuper|platform.ModCtrl|platform.ModAlt) != 0 {
		// UIKit repeats no key held: the surface does.
		s.repeat.start(s, code, ev)
	}
	return !taken && !text
}

func (s *surface) keyUp(key id, mods platform.Modifiers) {
	code := sendInt(key, "keyCode")
	if modifierKey(code) {
		s.send(platform.SurfaceEvent{Kind: platform.ModifiersChanged, Mods: mods})
		return
	}
	if s.repeat.code == code {
		s.repeat.stop()
	}
	k := layoutKey(code, goString(send(key, "charactersIgnoringModifiers")))
	s.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: k, PhysicalKey: physicalKey(code), Mods: mods})
}

// keyRepeat repeats the key held, after a delay, as a key event with
// Repeat, until it goes up.
type keyRepeat struct {
	code  int
	ev    platform.SurfaceEvent
	timer id
	held  bool
}

const (
	repeatDelay    = 0.4
	repeatInterval = 1.0 / 30
)

func (r *keyRepeat) start(s *surface, code int, ev platform.SurfaceEvent) {
	r.stop()
	r.code, r.ev, r.held = code, ev, true
	r.ev.Repeat = true
	withPool(func() {
		r.timer = retain(msgTimer(class("NSTimer"), sel("timerWithTimeInterval:target:selector:userInfo:repeats:"), repeatDelay, s.view, sel("mygoRepeat:"), 0, false))
		send(send(class("NSRunLoop"), "mainRunLoop"), "addTimer:forMode:", uintptr(r.timer), kCFRunLoopCommonModes)
	})
}

func (r *keyRepeat) fire(s *surface) {
	if !r.held {
		return
	}
	release(r.timer)
	r.timer = 0
	s.send(r.ev)
	if !r.held {
		return // the content stopped it, as a key closing the window
	}
	withPool(func() {
		r.timer = retain(msgTimer(class("NSTimer"), sel("timerWithTimeInterval:target:selector:userInfo:repeats:"), repeatInterval, s.view, sel("mygoRepeat:"), 0, false))
		send(send(class("NSRunLoop"), "mainRunLoop"), "addTimer:forMode:", uintptr(r.timer), kCFRunLoopCommonModes)
	})
}

func (r *keyRepeat) stop() {
	r.held = false
	r.code = 0
	if r.timer != 0 {
		send(r.timer, "invalidate")
		release(r.timer)
		r.timer = 0
	}
}

// keyMethods are the overrides of the responders that hear presses: the
// surface's view and its text input view, of className.
func keyMethods(className string) []objc.MethodDef {
	b := func() *Backend { return theBackend }
	presses := func(down bool) func(id, objc.SEL, id, id) {
		return func(self id, cmd objc.SEL, set, ev id) {
			s := b().surfaceOf(self)
			if s == nil {
				sendSuper(self, className, cmd, uintptr(set), uintptr(ev))
				return
			}
			mods := platform.Modifiers(0)
			if ev != 0 && respondsTo(ev, "modifierFlags") {
				mods = eventMods(sendInt(ev, "modifierFlags"))
			}
			var pass []id
			for _, p := range arrayItems(send(set, "allObjects")) {
				key := send(p, "key")
				if key == 0 {
					pass = append(pass, p)
					continue
				}
				code := sendInt(key, "keyCode")
				if down {
					if s.keyDown(key, mods) {
						s.keys[code] = true
						pass = append(pass, p)
					}
				} else {
					s.keyUp(key, mods)
					if s.keys[code] {
						delete(s.keys, code)
						pass = append(pass, p)
					}
				}
			}
			if len(pass) > 0 {
				withPool(func() {
					sendSuper(self, className, cmd, uintptr(send(class("NSSet"), "setWithArray:", uintptr(nsArray(pass...)))), uintptr(ev))
				})
			}
		}
	}
	return []objc.MethodDef{
		method("pressesBegan:withEvent:", presses(true)),
		method("pressesEnded:withEvent:", presses(false)),
		method("pressesCancelled:withEvent:", presses(false)),
	}
}
