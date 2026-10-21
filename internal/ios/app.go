//go:build ios

package ios

import (
	"errors"
	"os"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// errNoPage is what windows of web pages fail with: iOS shows windows of
// native UI only, which may hold web views (Surface.NewWebView).
var errNoPage = errors.New("mygo: iOS shows windows of native UI (WindowOptions.Content) only; put web pages in them with Window.NewWebView")

var errUnsupported = errors.New("mygo: iOS does not support this")

// Backend is the iOS implementation of platform.Backend.
//
// UIApplicationMain runs the app (Run) and never returns. Windows show in
// the first scene that connects, which also makes the app ready, or, in an
// app without a scene manifest in its Info.plist, at once. The windows of
// the app are UIWindows of the scene, the last shown in front.
type Backend struct {
	h          platform.AppHandler
	app        id
	delegate   id
	source     uintptr // runs AppHandler.Dispatch
	wakeSource uintptr // ends a Step
	mainLoop   uintptr

	// scene is the UIWindowScene the windows show in, once one connected.
	scene id
	ready bool
	// windows are the windows of the app, those shown in shown, front
	// last. Main thread only, as the lookup tables are.
	windows []*window
	shown   []*window

	bySurface  map[id]*surface // MyGoSurfaceView and MyGoTextInputView
	byDelegate map[id]*webView
	byWebView  map[id]*webView
	byAccess   map[id]*accessElement
	// byPicker are the pickers presented (pickers.go), with what they
	// call once the user picked or canceled; pickers is their delegate,
	// made with the first.
	byPicker map[id]func(picked id)
	pickers  id

	// theme is the appearance the app asked for: "system", "light" or
	// "dark".
	theme string
	// keyboard is the frame of the software keyboard in the screen's
	// coordinates, zero while it hides.
	keyboard CGRect
	// background tells that the app is in the background, where it shows
	// nothing and the GPU takes no work from it.
	background bool
	// awake counts KeepAwake's holds of the idle timer.
	awake int
	// badge is the label of SetBadge.
	badge string
}

// New creates the iOS backend.
func New() *Backend {
	load()
	return &Backend{
		bySurface:  map[id]*surface{},
		byDelegate: map[id]*webView{},
		byWebView:  map[id]*webView{},
		byAccess:   map[id]*accessElement{},
		byPicker:   map[id]func(id){},
		theme:      "system",
	}
}

var theBackend *Backend

func (b *Backend) Name() string { return "ios/uikit" }

func (b *Backend) IsMainThread() bool {
	r, _, _ := purego.SyscallN(mainNPFn)
	return int32(r) != 0
}

func (b *Backend) Init(h platform.AppHandler, _ platform.AppOptions) error {
	b.h = h
	theBackend = b
	registerClasses()

	// A run loop source drives the queue of functions posted from other
	// goroutines, in every mode, while UIKit tracks a touch too.
	b.mainLoop = cfRunLoopGetMain()
	var ctx [10]uintptr // CFRunLoopSourceContext; perform is the last field
	ctx[9] = dispatchCallback
	b.source = cfRunLoopSourceCreate(0, 0, unsafe.Pointer(&ctx[0]))
	cfRunLoopAddSource(b.mainLoop, b.source, kCFRunLoopCommonModes)
	// Another one, which does nothing, ends a Step: CFRunLoopRunInMode
	// returns once it handled a source.
	ctx[9] = wakeCallback
	b.wakeSource = cfRunLoopSourceCreate(0, 0, unsafe.Pointer(&ctx[0]))
	cfRunLoopAddSource(b.mainLoop, b.wakeSource, kCFRunLoopCommonModes)
	return nil
}

var (
	dispatchCallback = purego.NewCallback(func(info uintptr) {
		withPool(func() {
			drainMainQueue()
			theBackend.h.Dispatch()
		})
	})
	wakeCallback = purego.NewCallback(func(info uintptr) {})
)

// argv is what UIApplicationMain gets as the program's arguments, which
// UIKit may keep: it stays alive with the process.
var argv []*byte

// Run starts UIKit, which never returns: the system ends the app.
func (b *Backend) Run() error {
	args := make([][]byte, len(os.Args))
	argv = make([]*byte, len(os.Args)+1)
	for i, a := range os.Args {
		args[i] = append([]byte(a), 0)
		argv[i] = &args[i][0]
	}
	name := send(send(class("NSString"), "alloc"), "initWithUTF8String:", uintptr(unsafe.Pointer(unsafe.StringData("MyGoAppDelegate\x00"))))
	uiApplicationMain(int32(len(os.Args)), uintptr(unsafe.Pointer(&argv[0])), 0, name)
	return nil
}

// Quit ends the app once the core ran its quit sequence: iOS apps have no
// event loop to leave, the system ends them, so the process exits after
// the core finished (Terminating).
func (b *Backend) Quit() {
	b.post(func() {
		b.h.Terminating()
		exitFn(0)
	})
}

func (b *Backend) Signal() {
	if b.source != 0 {
		purego.SyscallN(cfRunLoopSourceSignalFn, b.source)
		purego.SyscallN(cfRunLoopWakeUpFn, b.mainLoop)
	}
}

// Step runs the main run loop until it handled a source, Wake's included,
// or for half a second.
func (b *Backend) Step() {
	withPool(func() { cfRunLoopRunInMode(kCFRunLoopDefaultMode, 0.5, true) })
}

// Wake makes a pending Step return. It is called from any goroutine.
func (b *Backend) Wake() {
	if b.wakeSource != 0 {
		purego.SyscallN(cfRunLoopSourceSignalFn, b.wakeSource)
		purego.SyscallN(cfRunLoopWakeUpFn, b.mainLoop)
	}
}

var (
	mainQueueMu sync.Mutex
	mainQueue   []func()
)

// runOnMain runs fn now on the main thread, else posts it there.
func (b *Backend) runOnMain(fn func()) {
	if b.IsMainThread() {
		fn()
		return
	}
	b.post(fn)
}

// post runs fn on the main thread after the current event, even when
// called there.
func (b *Backend) post(fn func()) {
	mainQueueMu.Lock()
	mainQueue = append(mainQueue, fn)
	mainQueueMu.Unlock()
	b.Signal()
}

func drainMainQueue() {
	mainQueueMu.Lock()
	q := mainQueue
	mainQueue = nil
	mainQueueMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func (b *Backend) App() platform.AppController   { return appController{b} }
func (b *Backend) Dialogs() platform.Dialogs     { return dialogs{b} }
func (b *Backend) Clipboard() platform.Clipboard { return clipboard{} }
func (b *Backend) Shell() platform.Shell         { return shell{b} }
func (b *Backend) Screen() platform.Screen       { return screen{b} }
func (b *Backend) Theme() platform.Theme         { return theme{b} }
func (b *Backend) Power() platform.Power         { return power{b} }
func (b *Backend) Secrets() platform.Secrets     { return secrets{} }

// SetApplicationMenu does nothing: iOS apps have no menu bar.
func (*Backend) SetApplicationMenu(*platform.Menu) {}
func (*Backend) UpdateMenuItem(*platform.MenuItem) {}

func (*Backend) NewTray(platform.TrayHandler) (platform.Tray, error) { return nil, errUnsupported }
func (*Backend) RegisterHotkey(int, string) error                    { return errUnsupported }
func (*Backend) UnregisterHotkey(int)                                {}
func (*Backend) Applications() ([]platform.Application, error)       { return nil, errUnsupported }
func (*Backend) NotificationsSupported() bool                        { return false }
func (*Backend) RemoveNotification(string)                           {}
func (*Backend) RemoveAllNotifications()                             {}

func (*Backend) ShowNotification(_ *platform.Notification, done func(error)) {
	done(errUnsupported)
}

// WebViewAvailable reports false: windows of web pages cannot work on
// iOS, though WKWebView is there for the web views of native UI.
func (*Backend) WebViewAvailable() bool { return false }

var classesOnce bool

func registerClasses() {
	if classesOnce {
		return
	}
	classesOnce = true
	registerAppDelegate()
	registerWindowClasses()
	registerSurfaceClasses()
	registerTextInputClasses()
	registerWebViewClasses()
	registerAccessClasses()
}

// usesScenes reports whether the app's Info.plist has a scene manifest:
// UIKit then connects scenes, which windows show in; without one, the app
// delegate's windows show on the main screen.
func usesScenes() bool {
	var ok bool
	withPool(func() {
		ok = send(send(class("NSBundle"), "mainBundle"), "objectForInfoDictionaryKey:", uintptr(nsString("UIApplicationSceneManifest"))) != 0
	})
	return ok
}

func registerAppDelegate() {
	b := func() *Backend { return theBackend }
	classDef("MyGoAppDelegate", "UIResponder", []string{"UIApplicationDelegate"}, nil, []objc.MethodDef{
		method("application:didFinishLaunchingWithOptions:", func(self id, _ objc.SEL, app, opts id) bool {
			b := b()
			b.app = app
			b.delegate = self
			b.observe(self)
			if !usesScenes() {
				b.becomeReady()
				if opts != 0 {
					if u := send(opts, "objectForKey:", uintptr(constString(libUIKit, "UIApplicationLaunchOptionsURLKey"))); u != 0 {
						b.openURLs([]id{u})
					}
				}
			}
			return true
		}),
		// Scenes of every session take the scene delegate, whatever the
		// manifest names.
		method("application:configurationForConnectingSceneSession:options:", func(self id, _ objc.SEL, app, session, opts id) id {
			cfg := send(send(class("UISceneConfiguration"), "alloc"), "initWithName:sessionRole:",
				uintptr(nsString("Default Configuration")), uintptr(send(session, "role")))
			send(cfg, "setDelegateClass:", uintptr(class("MyGoSceneDelegate")))
			return autorelease(cfg)
		}),
		method("applicationWillTerminate:", func(self id, _ objc.SEL, app id) {
			b().h.Terminating()
		}),
		method("application:openURL:options:", func(self id, _ objc.SEL, app, url, opts id) bool {
			b().openURLs([]id{url})
			return true
		}),
		method("applicationDidBecomeActive:", func(self id, _ objc.SEL, app id) {
			if !usesScenes() {
				b().h.DidBecomeActive()
			}
		}),
		method("applicationWillResignActive:", func(self id, _ objc.SEL, app id) {
			if !usesScenes() {
				b().h.DidResignActive()
			}
		}),
		method("applicationDidEnterBackground:", func(self id, _ objc.SEL, app id) {
			if !usesScenes() {
				b().enterBackground(true)
			}
		}),
		method("applicationWillEnterForeground:", func(self id, _ objc.SEL, app id) {
			if !usesScenes() {
				b().enterBackground(false)
			}
		}),
		method("mygoThemeChanged:", func(self id, _ objc.SEL, n id) {
			b().h.ThemeChanged()
		}),
		method("mygoLocaleChanged:", func(self id, _ objc.SEL, n id) {
			b().h.LocaleChanged()
		}),
		method("mygoKeyboardChanged:", func(self id, _ objc.SEL, n id) {
			b().keyboardChanged(n)
		}),
		method("mygoKeyChanged:", func(self id, _ objc.SEL, n id) {
			b().keyWindowChanged(send(n, "object"), goString(send(n, "name")) == "UIWindowDidBecomeKeyNotification")
		}),
	})

	classDef("MyGoSceneDelegate", "UIResponder", []string{"UIWindowSceneDelegate"}, nil, []objc.MethodDef{
		method("scene:willConnectToSession:options:", func(self id, _ objc.SEL, scene, session, opts id) {
			b := b()
			if b.scene != 0 || !sendBool(scene, "isKindOfClass:", uintptr(class("UIWindowScene"))) {
				return
			}
			b.scene = retain(scene)
			for _, w := range b.windows {
				w.attach()
			}
			b.becomeReady()
			if opts != 0 {
				var urls []id
				for _, c := range arrayItems(send(send(opts, "URLContexts"), "allObjects")) {
					urls = append(urls, send(c, "URL"))
				}
				if len(urls) > 0 {
					b.openURLs(urls)
				}
			}
		}),
		method("scene:openURLContexts:", func(self id, _ objc.SEL, scene, contexts id) {
			var urls []id
			for _, c := range arrayItems(send(contexts, "allObjects")) {
				urls = append(urls, send(c, "URL"))
			}
			b().openURLs(urls)
		}),
		method("sceneDidDisconnect:", func(self id, _ objc.SEL, scene id) {
			b := b()
			if scene != b.scene {
				return
			}
			for _, w := range b.windows {
				w.detach()
			}
			release(b.scene)
			b.scene = 0
		}),
		method("sceneDidBecomeActive:", func(self id, _ objc.SEL, scene id) {
			if scene == b().scene {
				b().h.DidBecomeActive()
			}
		}),
		method("sceneWillResignActive:", func(self id, _ objc.SEL, scene id) {
			if scene == b().scene {
				b().h.DidResignActive()
			}
		}),
		method("sceneDidEnterBackground:", func(self id, _ objc.SEL, scene id) {
			if scene == b().scene {
				b().enterBackground(true)
			}
		}),
		method("sceneWillEnterForeground:", func(self id, _ objc.SEL, scene id) {
			if scene == b().scene {
				b().enterBackground(false)
			}
		}),
	})
}

// enterBackground follows the app into the background, where its windows
// show nothing, and back, which tells the core it was activated.
func (b *Backend) enterBackground(background bool) {
	b.background = background
	if !background {
		b.h.Activated(len(b.shown) > 0)
	}
	for _, w := range b.windows {
		w.s.occludedChanged()
	}
}

// becomeReady tells the core that the app launched, once.
func (b *Backend) becomeReady() {
	if b.ready {
		return
	}
	b.ready = true
	b.h.Ready()
}

// openURLs passes the URLs the app was asked to open to the core: files
// shared with it, and links of its schemes.
func (b *Backend) openURLs(urls []id) {
	var files, links []string
	withPool(func() {
		for _, u := range urls {
			if sendBool(u, "isFileURL") {
				files = append(files, goString(send(u, "path")))
			} else {
				links = append(links, goString(send(u, "absoluteString")))
			}
		}
	})
	if len(files) > 0 {
		b.h.OpenFiles(files)
	}
	if len(links) > 0 {
		b.h.OpenURLs(links)
	}
}

// observe listens to what changes the content's appearance, its language,
// the keyboard and the key window.
func (b *Backend) observe(delegate id) {
	withPool(func() {
		center := send(class("NSNotificationCenter"), "defaultCenter")
		add := func(selector, name string) {
			n := constString(libUIKit, name)
			if n == 0 {
				n = constString(libFoundation, name)
			}
			if n == 0 {
				n = nsString(name)
			}
			send(center, "addObserver:selector:name:object:", uintptr(delegate), uintptr(sel(selector)), uintptr(n), 0)
		}
		for _, name := range []string{
			"UIAccessibilityReduceMotionStatusDidChangeNotification",
			"UIAccessibilityReduceTransparencyStatusDidChangeNotification",
			"UIAccessibilityDarkerSystemColorsStatusDidChangeNotification",
			"UIContentSizeCategoryDidChangeNotification",
		} {
			add("mygoThemeChanged:", name)
		}
		add("mygoLocaleChanged:", "NSCurrentLocaleDidChangeNotification")
		add("mygoKeyboardChanged:", "UIKeyboardWillChangeFrameNotification")
		add("mygoKeyboardChanged:", "UIKeyboardWillHideNotification")
		add("mygoKeyChanged:", "UIWindowDidBecomeKeyNotification")
		add("mygoKeyChanged:", "UIWindowDidResignKeyNotification")
	})
}

// keyboardChanged follows the software keyboard, which the content of the
// windows keeps clear of (surface.SafeArea).
func (b *Backend) keyboardChanged(n id) {
	if goString(send(n, "name")) == "UIKeyboardWillHideNotification" {
		b.keyboard = CGRect{}
	} else {
		info := send(n, "userInfo")
		key := constString(libUIKit, "UIKeyboardFrameEndUserInfoKey")
		if v := send(info, "objectForKey:", uintptr(key)); v != 0 {
			b.keyboard = msgRect(v, sel("CGRectValue"))
		}
	}
	for _, w := range b.shown {
		w.s.insetsChanged()
	}
}

// keyWindowChanged tells a window that it became, or ceased to be, the
// key window.
func (b *Backend) keyWindowChanged(win id, key bool) {
	for _, w := range b.windows {
		if w.win == win && !w.closed {
			w.keyChanged(key)
		}
	}
}

type appController struct{ b *Backend }

func (appController) SetActivationPolicy(string)                        {}
func (appController) Activate()                                         {}
func (appController) AllowForeground()                                  {}
func (appController) WithActivationToken(_ string, fn func())           { fn() }
func (appController) Hide()                                             {}
func (appController) Unhide()                                           {}
func (appController) Bounce(bool) int                                   { return 0 }
func (appController) CancelBounce(int)                                  {}
func (appController) SetDockIcon([]byte) error                          { return errUnsupported }
func (appController) SetDockMenu(*platform.Menu)                        {}
func (appController) ShowAboutPanel(platform.AboutPanelOptions)         {}
func (appController) RegisterURLScheme(string, string, string) error    { return errUnsupported }
func (appController) UnregisterURLScheme(string, string, string) error  { return errUnsupported }
func (appController) IsURLSchemeRegistered(string, string, string) bool { return false }
func (appController) SetOpenAtLogin(bool, string, string, string) error { return errUnsupported }
func (appController) OpenAtLogin(string, string, string) bool           { return false }
func (appController) OpenedAtLogin() bool                               { return false }
func (appController) AddRecentDocument(string) error                    { return errUnsupported }
func (appController) ClearRecentDocuments() error                       { return errUnsupported }
func (appController) SetJumpList(platform.JumpList) error               { return errUnsupported }

// IsHidden reports whether the app is in the background.
func (a appController) IsHidden() bool {
	const uiApplicationStateBackground = 2
	return a.b.app != 0 && sendInt(a.b.app, "applicationState") == uiApplicationStateBackground
}

// SetBadge shows a number on the app's icon, the label's, or none: iOS
// badges count. The user must have allowed the app's notifications to
// show badges.
func (a appController) SetBadge(label string) {
	a.b.badge = label
	n := 0
	for _, r := range label {
		if r < '0' || r > '9' {
			n = 0
			break
		}
		n = n*10 + int(r-'0')
	}
	if label != "" && n == 0 {
		n = 1 // a label that is no number still shows a badge
	}
	if a.b.app != 0 {
		send(a.b.app, "setApplicationIconBadgeNumber:", uintptr(n))
	}
}

func (a appController) Badge() string { return a.b.badge }

func (a appController) Package() (platform.PackageInfo, bool) {
	var info platform.PackageInfo
	ok := false
	withPool(func() {
		bundle := send(class("NSBundle"), "mainBundle")
		if !strings.HasSuffix(goString(send(bundle, "bundlePath")), ".app") {
			return
		}
		ok = true
		get := func(key string) string {
			return goString(send(bundle, "objectForInfoDictionaryKey:", uintptr(nsString(key))))
		}
		info.Name = get("CFBundleDisplayName")
		if info.Name == "" {
			info.Name = get("CFBundleName")
		}
		info.Version = get("CFBundleShortVersionString")
		info.Identifier = goString(send(bundle, "bundleIdentifier"))
	})
	return info, ok
}

func (a appController) ClearBrowsingData(done func(error)) {
	withPool(func() {
		store := send(class("WKWebsiteDataStore"), "defaultDataStore")
		types := send(class("WKWebsiteDataStore"), "allWebsiteDataTypes")
		blk := newBlock(func(_ objc.Block) { a.b.runOnMain(func() { done(nil) }) })
		send(store, "removeDataOfTypes:modifiedSince:completionHandler:", uintptr(types), uintptr(send(class("NSDate"), "distantPast")), uintptr(blk))
		blk.Release()
	})
}
