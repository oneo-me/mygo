//go:build ios

package ios

import (
	"slices"
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// Assistive technology, as VoiceOver, reads the surface as a container of
// elements: the nodes of the content's AccessTree that say or do
// something, in its order, each a MyGoAccessElement kept by its node's ID
// from frame to frame, and the web views the content shows, whose pages
// VoiceOver reads on its own. The first time VoiceOver asks for them, the
// surface sends AccessibilityOn; the content describes every frame from
// then on (UpdateAccessibility).

var (
	accessOnce sync.Once
	// traits are UIKit's UIAccessibilityTraits, by name.
	traits           = map[string]uint64{}
	postNotification func(n uint32, arg id)
	layoutChanged    uint32
	announcement     uint32
)

// accessibilityTrait returns UIAccessibilityTrait<name>, 0 where UIKit
// has none.
func accessibilityTrait(name string) uint64 { return traits[name] }

func loadAccess() {
	accessOnce.Do(func() {
		for _, name := range []string{"Button", "Link", "Header", "SearchField", "Image", "Selected", "StaticText",
			"NotEnabled", "UpdatesFrequently", "Adjustable", "TabBar", "ToggleButton", "KeyboardKey"} {
			if p := constAddr(libUIKit, "UIAccessibilityTrait"+name); p != 0 {
				traits[name] = **(**uint64)(unsafe.Pointer(&p))
			}
		}
		registerFunc(&postNotification, libUIKit, "UIAccessibilityPostNotification")
		if p := constAddr(libUIKit, "UIAccessibilityLayoutChangedNotification"); p != 0 {
			layoutChanged = **(**uint32)(unsafe.Pointer(&p))
		}
		if p := constAddr(libUIKit, "UIAccessibilityAnnouncementNotification"); p != 0 {
			announcement = **(**uint32)(unsafe.Pointer(&p))
		}
	})
}

// accessElement is the node an element of the surface stands for, and
// what it told UIKit of it last, which a frame tells again only when it
// changed.
type accessElement struct {
	s       *surface
	id      uint64
	actions platform.AccessActions
	obj     id

	label, value, hint string
	traits             uint64
	frame              platform.RectF
	told               bool
}

// containerRoles are the roles of nodes that only hold others: VoiceOver
// reads their children.
var containerRoles = []platform.AccessRole{
	platform.RoleGroup, platform.RoleList, platform.RoleScroll, platform.RoleTable, platform.RoleTree,
	platform.RoleToolbar, platform.RoleRadioGroup, platform.RoleMenu, platform.RoleMenuBar, platform.RoleRow,
	platform.RoleListItem, platform.RoleDialog, platform.RoleAlertDialog, platform.RolePopup, platform.RoleTabList,
}

// accessTraits returns the traits of a node.
func accessTraits(n *platform.AccessNode) uint64 {
	var t uint64
	switch n.Role {
	case platform.RoleButton, platform.RolePopUpButton, platform.RoleMenuButton, platform.RoleDisclosure,
		platform.RoleColorWell, platform.RoleMenuItem, platform.RoleMenuItemCheckBox, platform.RoleMenuItemRadio,
		platform.RoleCheckBox, platform.RoleRadio, platform.RoleTab, platform.RoleTreeItem:
		t |= accessibilityTrait("Button")
	case platform.RoleSwitch, platform.RoleToggleButton:
		if tb := accessibilityTrait("ToggleButton"); tb != 0 {
			t |= tb
		} else {
			t |= accessibilityTrait("Button")
		}
	case platform.RoleLink:
		t |= accessibilityTrait("Link")
	case platform.RoleImage:
		t |= accessibilityTrait("Image")
	case platform.RoleHeading:
		t |= accessibilityTrait("Header")
	case platform.RoleText, platform.RoleStatus, platform.RoleTooltip, platform.RoleCell, platform.RoleColumnHeader:
		t |= accessibilityTrait("StaticText")
	case platform.RoleSlider, platform.RoleStepper:
		t |= accessibilityTrait("Adjustable")
	case platform.RoleProgress, platform.RoleMeter:
		t |= accessibilityTrait("UpdatesFrequently")
	}
	if n.States&platform.AccessSearch != 0 {
		t |= accessibilityTrait("SearchField")
	}
	if n.States&(platform.AccessChecked|platform.AccessSelected) != 0 {
		t |= accessibilityTrait("Selected")
	}
	if n.States&platform.AccessDisabled != 0 {
		t |= accessibilityTrait("NotEnabled")
	}
	return t
}

// accessValue returns what VoiceOver reads as a node's value.
func accessValue(n *platform.AccessNode) string {
	if n.Role.Ranged() && n.Max > n.Min && n.Now >= n.Min {
		return strconv.Itoa(int((n.Now-n.Min)/(n.Max-n.Min)*100+0.5)) + "%"
	}
	if n.States&platform.AccessPassword != 0 {
		return ""
	}
	return n.Value
}

// shown reports whether a node is an element of its own for VoiceOver:
// one that says or does something, in view.
func shown(n *platform.AccessNode) bool {
	if n.States&platform.AccessOffscreen != 0 || n.Bounds.W <= 0 || n.Bounds.H <= 0 {
		return false
	}
	if n.WebView != nil {
		return true
	}
	if slices.Contains(containerRoles, n.Role) && n.Actions&(platform.ActionPress|platform.ActionIncrement) == 0 {
		return false
	}
	return n.Label != "" || n.Value != "" || n.Placeholder != "" || n.Actions&(platform.ActionPress|platform.ActionIncrement|platform.ActionSetValue) != 0
}

// UpdateAccessibility gives VoiceOver the content's elements, telling it
// when they are others than the last frame's.
func (s *surface) UpdateAccessibility(tree *platform.AccessTree) {
	if !s.accessOn || s.w.closed {
		return
	}
	loadAccess()
	b := s.w.b
	var ids []uint64
	var objs []id
	seen := make(map[uint64]bool, len(tree.Nodes))
	withPool(func() {
		for i := range tree.Nodes {
			n := &tree.Nodes[i]
			if !shown(n) {
				continue
			}
			if v, ok := n.WebView.(*webView); ok {
				if !v.closed {
					objs = append(objs, v.web)
				}
				continue
			}
			e := s.elements[n.ID]
			if e == nil {
				e = &accessElement{s: s, id: n.ID}
				e.obj = send(send(class("MyGoAccessElement"), "alloc"), "initWithAccessibilityContainer:", uintptr(s.view))
				s.elements[n.ID] = e
				b.byAccess[e.obj] = e
			}
			seen[n.ID] = true
			e.actions = n.Actions
			e.tell(n)
			ids = append(ids, n.ID)
			objs = append(objs, e.obj)
		}
		for id, e := range s.elements {
			if !seen[id] {
				delete(s.elements, id)
				delete(b.byAccess, e.obj)
				release(e.obj)
			}
		}
		if !slices.Equal(objs, s.accessObjs) {
			release(s.accessArray)
			s.accessArray = retain(nsArray(objs...))
			s.accessObjs = objs
		}
	})
	if !slices.Equal(ids, s.accessIDs) {
		s.accessIDs = ids
		postNotification(layoutChanged, 0)
	}
	for _, text := range tree.Announcements {
		withPool(func() { postNotification(announcement, nsString(text)) })
	}
}

// tell gives UIKit what changed of the element's node.
func (e *accessElement) tell(n *platform.AccessNode) {
	label := n.Label
	if label == "" && n.Value == "" {
		label = n.Placeholder
	}
	value, hint, traits := accessValue(n), n.Description, accessTraits(n)
	first := !e.told
	e.told = true
	if first || label != e.label {
		e.label = label
		send(e.obj, "setAccessibilityLabel:", uintptr(nsString(label)))
	}
	if first || value != e.value {
		e.value = value
		send(e.obj, "setAccessibilityValue:", uintptr(nsString(value)))
	}
	if first || hint != e.hint {
		e.hint = hint
		send(e.obj, "setAccessibilityHint:", uintptr(nsString(hint)))
	}
	if first || traits != e.traits {
		e.traits = traits
		send(e.obj, "setAccessibilityTraits:", uintptr(traits))
	}
	if r := n.Bounds; first || r != e.frame {
		e.frame = r
		msgAccessFrame(e.obj, sel("setAccessibilityFrameInContainerSpace:"), rect(r.X, r.Y, r.W, r.H))
	}
}

// accessibilityElements returns the elements of the surface, after
// turning the content's descriptions on.
func (s *surface) accessibilityElements() id {
	if !s.accessOn {
		s.accessOn = true
		s.elements = map[uint64]*accessElement{}
		// The content describes its last frame at once.
		s.send(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	}
	return s.accessArray
}

// destroyAccess lets go of the elements of a surface that closes.
func (s *surface) destroyAccess() {
	for _, e := range s.elements {
		delete(s.w.b.byAccess, e.obj)
		release(e.obj)
	}
	s.elements = nil
	release(s.accessArray)
	s.accessArray, s.accessObjs = 0, nil
}

// act performs an action of VoiceOver on the element's node, and reports
// whether the node takes it.
func (e *accessElement) act(kind platform.AccessActionKind) bool {
	want := map[platform.AccessActionKind]platform.AccessActions{
		platform.AccessPress: platform.ActionPress, platform.AccessFocus: platform.ActionFocus,
		platform.AccessIncrement: platform.ActionIncrement, platform.AccessDecrement: platform.ActionDecrement,
		platform.AccessScrollIntoView: platform.ActionScrollIntoView,
	}[kind]
	if e.actions&want == 0 || e.s.w.closed {
		return false
	}
	e.s.send(platform.SurfaceEvent{Kind: platform.AccessAction, ID: e.id, Action: kind})
	return true
}

func registerAccessClasses() {
	element := func(self id) *accessElement { return theBackend.byAccess[self] }
	classDef("MyGoAccessElement", "UIAccessibilityElement", nil, nil, []objc.MethodDef{
		// A double tap presses the element, or focuses a field.
		method("accessibilityActivate", func(self id, _ objc.SEL) bool {
			e := element(self)
			return e != nil && (e.act(platform.AccessPress) || e.act(platform.AccessFocus))
		}),
		method("accessibilityIncrement", func(self id, _ objc.SEL) {
			if e := element(self); e != nil {
				e.act(platform.AccessIncrement)
			}
		}),
		method("accessibilityDecrement", func(self id, _ objc.SEL) {
			if e := element(self); e != nil {
				e.act(platform.AccessDecrement)
			}
		}),
		// VoiceOver moving to an element scrolls it into view.
		method("accessibilityElementDidBecomeFocused", func(self id, _ objc.SEL) {
			if e := element(self); e != nil {
				e.act(platform.AccessScrollIntoView)
			}
		}),
	})
}

// accessViewMethods make the surface's view the container of its
// elements.
func accessViewMethods() []objc.MethodDef {
	return []objc.MethodDef{
		method("isAccessibilityElement", func(self id, _ objc.SEL) bool { return false }),
		method("accessibilityElements", func(self id, _ objc.SEL) id {
			if s := theBackend.surfaceOf(self); s != nil {
				return s.accessibilityElements()
			}
			return 0
		}),
	}
}
