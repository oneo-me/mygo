//go:build ios

// Package ios is the iOS backend: windows of native UI (package ui) in the
// scenes of a UIKit app, drawn with Metal, and web views in them
// (WKWebView). Windows of web pages, menus, trays and the like have
// nothing on iOS to stand for them, and report so. Objective-C is driven
// through the runtime with purego, as in internal/darwin; iOS links
// externally, which Go only does with cgo enabled, but nothing imports "C".
package ios

import (
	"fmt"
	"runtime"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type id = objc.ID

// CGPoint, CGSize and CGRect are Core Graphics' structs (64-bit).
type CGPoint struct {
	_    structs.HostLayout
	X, Y float64
}

type CGSize struct {
	_             structs.HostLayout
	Width, Height float64
}

type CGRect struct {
	_      structs.HostLayout
	Origin CGPoint
	Size   CGSize
}

// UIEdgeInsets is UIKit's struct of the insets of a rectangle's edges.
type UIEdgeInsets struct {
	_                        structs.HostLayout
	Top, Left, Bottom, Right float64
}

func rect(x, y, w, h float64) CGRect {
	return CGRect{Origin: CGPoint{X: x, Y: y}, Size: CGSize{Width: w, Height: h}}
}

var (
	msgSendAddr, msgSendSuperAddr uintptr

	// Typed variants of objc_msgSend for float and struct arguments.
	msgRect          func(obj id, sel objc.SEL) CGRect
	msgSetRect       func(obj id, sel objc.SEL, r CGRect)
	msgInsets        func(obj id, sel objc.SEL) UIEdgeInsets
	msgFloat         func(obj id, sel objc.SEL) float64
	msgSetFloat      func(obj id, sel objc.SEL, v float64)
	msgPointInView   func(obj id, sel objc.SEL, view id) CGPoint
	msgInitRect      func(obj id, sel objc.SEL, r CGRect) id
	msgInitRectID    func(obj id, sel objc.SEL, r CGRect, a id) id
	msgColor         func(cls id, sel objc.SEL, r, g, b, a float64) id
	msgConvertRect   func(obj id, sel objc.SEL, r CGRect, view id) CGRect
	msgSuperHitTest  func(sup uintptr, sel objc.SEL, p CGPoint, ev id) id
	msgSetCenter     func(obj id, sel objc.SEL, p CGPoint)
	msgSetSize       func(obj id, sel objc.SEL, s CGSize)
	msgFloatID       func(obj id, sel objc.SEL, v float64) id
	msgAccessFrame   func(obj id, sel objc.SEL, r CGRect)
	msgSuperSetFrame func(sup uintptr, sel objc.SEL, r CGRect)
	msgTimer         func(cls id, sel objc.SEL, interval float64, target id, selector objc.SEL, info id, repeats bool) id

	blockCopy    func(block uintptr) uintptr
	blockRelease func(block uintptr)

	// Functions on hot paths, called by address: RegisterFunc's wrappers
	// use reflection and allocate on every call.
	poolPushFn, poolPopFn, mainNPFn uintptr

	libObjC, libCF, libCG, libUIKit, libWebKit, libFoundation, libSecurity uintptr
	loadOnce                                                               sync.Once
)

func mustDlsym(lib uintptr, name string) uintptr {
	p, err := purego.Dlsym(lib, name)
	if err != nil {
		panic(fmt.Sprintf("mygo: %v", err))
	}
	return p
}

func mustDlopen(path string) uintptr {
	h, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		panic(fmt.Sprintf("mygo: cannot load %s: %v", path, err))
	}
	return h
}

// load opens the frameworks and prepares the typed message functions.
func load() {
	loadOnce.Do(func() {
		libObjC = mustDlopen("/usr/lib/libobjc.A.dylib")
		libFoundation = mustDlopen("/System/Library/Frameworks/Foundation.framework/Foundation")
		libCF = mustDlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		libCG = mustDlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		libUIKit = mustDlopen("/System/Library/Frameworks/UIKit.framework/UIKit")
		libWebKit = mustDlopen("/System/Library/Frameworks/WebKit.framework/WebKit")
		libSecurity = mustDlopen("/System/Library/Frameworks/Security.framework/Security")
		mustDlopen("/System/Library/Frameworks/QuartzCore.framework/QuartzCore")
		// Optional frameworks, loaded so their classes are registered.
		_, _ = purego.Dlopen("/System/Library/Frameworks/UniformTypeIdentifiers.framework/UniformTypeIdentifiers", purego.RTLD_GLOBAL|purego.RTLD_NOW)

		msgSendAddr = mustDlsym(libObjC, "objc_msgSend")
		msgSendSuperAddr = mustDlsym(libObjC, "objc_msgSendSuper")
		purego.RegisterFunc(&msgRect, msgSendAddr)
		purego.RegisterFunc(&msgSetRect, msgSendAddr)
		purego.RegisterFunc(&msgInsets, msgSendAddr)
		purego.RegisterFunc(&msgFloat, msgSendAddr)
		purego.RegisterFunc(&msgSetFloat, msgSendAddr)
		purego.RegisterFunc(&msgPointInView, msgSendAddr)
		purego.RegisterFunc(&msgInitRect, msgSendAddr)
		purego.RegisterFunc(&msgInitRectID, msgSendAddr)
		purego.RegisterFunc(&msgColor, msgSendAddr)
		purego.RegisterFunc(&msgConvertRect, msgSendAddr)
		purego.RegisterFunc(&msgSuperHitTest, msgSendSuperAddr)
		purego.RegisterFunc(&msgSetCenter, msgSendAddr)
		purego.RegisterFunc(&msgSetSize, msgSendAddr)
		purego.RegisterFunc(&msgFloatID, msgSendAddr)
		purego.RegisterFunc(&msgAccessFrame, msgSendAddr)
		purego.RegisterFunc(&msgSuperSetFrame, msgSendSuperAddr)
		purego.RegisterFunc(&msgTimer, msgSendAddr)

		poolPushFn = mustDlsym(libObjC, "objc_autoreleasePoolPush")
		poolPopFn = mustDlsym(libObjC, "objc_autoreleasePoolPop")
		purego.RegisterLibFunc(&blockCopy, libObjC, "_Block_copy")
		purego.RegisterLibFunc(&blockRelease, libObjC, "_Block_release")
		mainNPFn = mustDlsym(purego.RTLD_DEFAULT, "pthread_main_np")
		loadCF()
	})
}

var (
	selMu      sync.RWMutex
	selCache   = map[string]objc.SEL{}
	clsCache   = map[string]id{}
	superCache = map[string]id{}
)

// sel returns the selector for name, cached.
func sel(name string) objc.SEL {
	selMu.RLock()
	s, ok := selCache[name]
	selMu.RUnlock()
	if ok {
		return s
	}
	s = objc.RegisterName(name)
	selMu.Lock()
	selCache[name] = s
	selMu.Unlock()
	return s
}

// class returns the class object for name, cached. It panics for classes
// that do not exist since that is a programming error.
func class(name string) id {
	selMu.RLock()
	c, ok := clsCache[name]
	selMu.RUnlock()
	if ok {
		return c
	}
	c = id(objc.GetClass(name))
	if c == 0 {
		panic("mygo: Objective-C class " + name + " not found")
	}
	selMu.Lock()
	clsCache[name] = c
	selMu.Unlock()
	return c
}

// hasClass reports whether a class exists (for APIs of newer iOS versions).
func hasClass(name string) bool { return objc.GetClass(name) != 0 }

// send sends a message whose arguments are integers or pointers. Go
// pointers converted in the call, as in uintptr(unsafe.Pointer(&x)), are
// moved to the heap and kept alive until it returns (go:uintptrescapes).
//
//go:uintptrescapes
func send(obj id, selector string, args ...uintptr) id {
	var a [12]uintptr
	a[0], a[1] = uintptr(obj), uintptr(sel(selector))
	n := copy(a[2:], args)
	r, _, _ := purego.SyscallN(msgSendAddr, a[:n+2]...)
	return id(r)
}

// sendBool sends a message returning BOOL.
//
//go:uintptrescapes
func sendBool(obj id, selector string, args ...uintptr) bool {
	return byte(send(obj, selector, args...)) != 0
}

// sendInt sends a message returning NSInteger.
//
//go:uintptrescapes
func sendInt(obj id, selector string, args ...uintptr) int {
	return int(send(obj, selector, args...))
}

// objcSuper mirrors struct objc_super.
type objcSuper struct {
	receiver   id
	superClass id
}

// superOf returns the struct objc_super that calls the superclass of
// className with self, pinned until pin is unpinned. The superclass is
// resolved from className, as internal/darwin's overrides do, never from
// the object's class, which key-value observing may replace.
func superOf(self id, className string, pin *runtime.Pinner) uintptr {
	sup := &objcSuper{receiver: self, superClass: superclass(className)}
	pin.Pin(sup)
	return uintptr(unsafe.Pointer(sup))
}

func superclass(name string) id {
	selMu.RLock()
	c, ok := superCache[name]
	selMu.RUnlock()
	if ok {
		return c
	}
	c = id(objc.Class(class(name)).SuperClass())
	selMu.Lock()
	superCache[name] = c
	selMu.Unlock()
	return c
}

// sendSuper calls the superclass implementation of a method overridden in
// className.
//
//go:uintptrescapes
func sendSuper(self id, className string, s objc.SEL, args ...uintptr) id {
	var pin runtime.Pinner
	defer pin.Unpin()
	var a [10]uintptr
	a[0], a[1] = superOf(self, className, &pin), uintptr(s)
	n := copy(a[2:], args)
	r, _, _ := purego.SyscallN(msgSendSuperAddr, a[:n+2]...)
	return id(r)
}

// sendSuperHitTest is sendSuper for hitTest:withEvent:, whose point
// travels in floating-point registers.
func sendSuperHitTest(self id, className string, s objc.SEL, p CGPoint, ev id) id {
	var pin runtime.Pinner
	defer pin.Unpin()
	return msgSuperHitTest(superOf(self, className, &pin), s, p, ev)
}

func respondsTo(obj id, selector string) bool {
	return obj != 0 && sendBool(obj, "respondsToSelector:", uintptr(sel(selector)))
}

func boolArg(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func retain(obj id) id {
	if obj == 0 {
		return 0
	}
	return send(obj, "retain")
}

func release(obj id) {
	if obj != 0 {
		send(obj, "release")
	}
}

func autorelease(obj id) id {
	if obj == 0 {
		return 0
	}
	return send(obj, "autorelease")
}

// alloc creates an owned (+1) instance of a class with init.
func alloc(className string) id {
	return send(send(class(className), "alloc"), "init")
}

// withPool runs fn inside an autorelease pool, on the thread that pushed
// it: popping a pool on another thread crashes.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(poolPushFn)
	defer purego.SyscallN(poolPopFn, pool)
	fn()
}

const nsUTF8StringEncoding = 4

// nsString returns an autoreleased NSString.
func nsString(s string) id {
	b := unsafe.StringData(s)
	str := send(send(class("NSString"), "alloc"), "initWithBytes:length:encoding:",
		uintptr(unsafe.Pointer(b)), uintptr(len(s)), nsUTF8StringEncoding)
	runtime.KeepAlive(s)
	return autorelease(str)
}

// goString copies an NSString into a Go string.
func goString(str id) string {
	if str == 0 {
		return ""
	}
	p := send(str, "UTF8String")
	if p == 0 {
		return ""
	}
	n := sendInt(str, "lengthOfBytesUsingEncoding:", nsUTF8StringEncoding)
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n))
}

// stringOf returns the text of an NSString or NSAttributedString.
func stringOf(obj id) string {
	if obj == 0 {
		return ""
	}
	if respondsTo(obj, "string") {
		obj = send(obj, "string")
	}
	return goString(obj)
}

// nsData returns an autoreleased NSData with a copy of b.
func nsData(b []byte) id {
	var p unsafe.Pointer
	if len(b) > 0 {
		p = unsafe.Pointer(&b[0])
	}
	d := send(class("NSData"), "dataWithBytes:length:", uintptr(p), uintptr(len(b)))
	runtime.KeepAlive(b)
	return d
}

// goBytes copies the contents of an NSData.
func goBytes(data id) []byte {
	if data == 0 {
		return nil
	}
	n := sendInt(data, "length")
	if n == 0 {
		return []byte{}
	}
	p := send(data, "bytes")
	return append([]byte(nil), unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n)...)
}

func nsURL(s string) id { return send(class("NSURL"), "URLWithString:", uintptr(nsString(s))) }

func fileURL(path string) id {
	return send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(path)))
}

// nsArray returns an autoreleased NSArray of objs.
func nsArray(objs ...id) id {
	arr := send(class("NSMutableArray"), "arrayWithCapacity:", uintptr(len(objs)))
	for _, o := range objs {
		send(arr, "addObject:", uintptr(o))
	}
	return arr
}

func arrayItems(arr id) []id {
	if arr == 0 {
		return nil
	}
	n := sendInt(arr, "count")
	out := make([]id, n)
	for i := range n {
		out[i] = send(arr, "objectAtIndex:", uintptr(i))
	}
	return out
}

func nsBool(v bool) id { return send(class("NSNumber"), "numberWithBool:", boolArg(v)) }

// callBlock invokes an Objective-C block received from the system, such as
// a WebKit completion handler.
func callBlock(block uintptr, args ...uintptr) {
	lit := *(**[3]uintptr)(unsafe.Pointer(&block))
	var a [8]uintptr
	a[0] = block
	n := copy(a[1:], args)
	purego.SyscallN(lit[2], a[:n+1]...)
}

// newBlock creates a block backed by a Go function, which takes
// objc.Block as its first parameter.
func newBlock(fn any) objc.Block { return objc.NewBlock(fn) }

// nsError describes an NSError, preferring the JavaScript exception message
// WebKit attaches.
func nsError(err id) error {
	if err == 0 {
		return fmt.Errorf("mygo: failed")
	}
	info := send(err, "userInfo")
	if msg := send(info, "objectForKey:", uintptr(nsString("WKJavaScriptExceptionMessage"))); msg != 0 {
		return fmt.Errorf("%s", goString(msg))
	}
	return fmt.Errorf("%s", goString(send(err, "localizedDescription")))
}

// constAddr returns the address of an exported constant, 0 for none.
func constAddr(lib uintptr, name string) uintptr {
	p, err := purego.Dlsym(lib, name)
	if err != nil {
		return 0
	}
	return p
}

// constString reads an exported NSString constant, such as
// UIKeyboardFrameEndUserInfoKey.
func constString(lib uintptr, name string) id {
	p, err := purego.Dlsym(lib, name)
	if err != nil || p == 0 {
		return 0
	}
	return **(**id)(unsafe.Pointer(&p))
}

// classDef builds an Objective-C class once. Protocols that do not exist
// at run time are left out.
func classDef(name, super string, protocols []string, ivars []objc.FieldDef, methods []objc.MethodDef) id {
	var ps []*objc.Protocol
	for _, p := range protocols {
		if proto := objc.GetProtocol(p); proto != nil {
			ps = append(ps, proto)
		}
	}
	c, err := objc.RegisterClass(name, objc.GetClass(super), ps, ivars, methods)
	if err != nil {
		panic(fmt.Sprintf("mygo: cannot register class %s: %v", name, err))
	}
	selMu.Lock()
	clsCache[name] = id(c)
	selMu.Unlock()
	return id(c)
}

func method(selector string, fn any) objc.MethodDef {
	return objc.MethodDef{Cmd: sel(selector), Fn: fn}
}

// registerFunc binds a C function of a library to fptr.
func registerFunc(fptr any, lib uintptr, name string) { purego.RegisterLibFunc(fptr, lib, name) }

// uiColor returns an autoreleased UIColor of an sRGB color.
func uiColor(r, g, b, a float64) id {
	return msgColor(class("UIColor"), sel("colorWithRed:green:blue:alpha:"), r, g, b, a)
}
