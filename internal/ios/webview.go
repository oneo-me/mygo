//go:build ios

package ios

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// A web view in a window of native UI (platform.Surface.NewWebView) is a
// WKWebView in a clip view, a subview of the window's root view under
// the surface. The surface's frames are transparent where the content
// shows the web view (holes), so what the content paints after it shows
// over the page, and its hitTest:withEvent: lets touches through to the
// page there. Its delegate, the navigation, UI and script message
// delegate in one object, hears only of its page.
type webView struct {
	b    *Backend
	host *surface
	h    platform.WindowHandler
	opts *platform.WindowOptions

	web, clip, delegate, ucc id
	// programmatic tells that the next navigation of the main frame is
	// one the app started (LoadURL), which it is not asked about.
	programmatic bool
	closed       bool
	downloads    map[id][2]string // WKDownload: URL and path
}

func (s *surface) NewWebView(o *platform.WindowOptions, h platform.WindowHandler) (platform.WebView, error) {
	b := s.w.b
	v := &webView{b: b, host: s, h: h, opts: o, downloads: map[id][2]string{}}
	withPool(func() {
		v.delegate = alloc("MyGoWebViewDelegate")
		v.create()
		v.clip = msgInitRect(send(class("UIView"), "alloc"), sel("initWithFrame:"), CGRect{})
		send(v.clip, "setClipsToBounds:", 1)
		send(v.clip, "setHidden:", 1)
		if o.BackgroundColor != nil && !o.Transparent {
			v.SetBackgroundColor(*o.BackgroundColor)
		}
		send(v.clip, "addSubview:", uintptr(v.web))
		send(s.w.root, "insertSubview:belowSubview:", uintptr(v.clip), uintptr(s.view))
	})
	b.byDelegate[v.delegate] = v
	b.byWebView[v.web] = v
	s.webViews = append(s.webViews, v)
	return v, nil
}

func (v *webView) create() {
	o := v.opts
	cfg := alloc("WKWebViewConfiguration")
	for _, scheme := range o.Schemes {
		send(cfg, "setURLSchemeHandler:forURLScheme:", uintptr(schemeHandler()), uintptr(nsString(scheme)))
	}
	prefs := send(cfg, "preferences")
	send(prefs, "setValue:forKey:", uintptr(nsBool(true)), uintptr(nsString("allowFileAccessFromFileURLs")))
	if respondsTo(prefs, "setElementFullscreenEnabled:") {
		send(prefs, "setElementFullscreenEnabled:", 1)
	}
	// Pages play their media inline, as in other platforms' web views,
	// rather than in the system's full-screen player.
	send(cfg, "setAllowsInlineMediaPlayback:", 1)
	v.ucc = alloc("WKUserContentController")
	send(v.ucc, "addScriptMessageHandler:name:", uintptr(v.delegate), uintptr(nsString("mygo")))
	v.addUserScripts()
	send(cfg, "setUserContentController:", uintptr(v.ucc))
	if o.UserAgentSuffix != "" {
		send(cfg, "setApplicationNameForUserAgent:", uintptr(nsString(o.UserAgentSuffix)))
	}

	v.web = msgInitRectID(send(class("WKWebView"), "alloc"), sel("initWithFrame:configuration:"), CGRect{}, cfg)
	release(cfg)
	send(v.web, "setNavigationDelegate:", uintptr(v.delegate))
	send(v.web, "setUIDelegate:", uintptr(v.delegate))
	if o.Transparent || o.BackgroundColor != nil {
		// What is under the page shows until it paints.
		send(v.web, "setOpaque:", 0)
		send(v.web, "setBackgroundColor:", uintptr(send(class("UIColor"), "clearColor")))
		send(send(v.web, "scrollView"), "setBackgroundColor:", uintptr(send(class("UIColor"), "clearColor")))
	}
	if respondsTo(v.web, "setInspectable:") {
		send(v.web, "setInspectable:", boolArg(o.DevTools))
	}
	if o.UserAgent != "" {
		ua := o.UserAgent
		if o.UserAgentSuffix != "" {
			ua += " " + o.UserAgentSuffix
		}
		send(v.web, "setCustomUserAgent:", uintptr(nsString(ua)))
	}
	if o.Zoom > 0 && o.Zoom != 1 && respondsTo(v.web, "setPageZoom:") {
		msgSetFloat(v.web, sel("setPageZoom:"), o.Zoom)
	}
	send(v.web, "addObserver:forKeyPath:options:context:", uintptr(v.delegate), uintptr(nsString("title")), 1, 0)
}

// addUserScripts adds the user scripts of the options to the content
// controller. Pool required.
func (v *webView) addUserScripts() {
	for _, s := range v.opts.UserScripts {
		injection := uintptr(0) // WKUserScriptInjectionTimeAtDocumentStart
		if s.AtDocumentEnd {
			injection = 1
		}
		script := send(send(class("WKUserScript"), "alloc"), "initWithSource:injectionTime:forMainFrameOnly:",
			uintptr(nsString(s.Source)), injection, boolArg(!s.AllFrames))
		send(v.ucc, "addUserScript:", uintptr(script))
		release(script)
	}
}

// PlaceWebViews shows the window's web views where the content shows
// them, in its paint order under the surface, and hides the others.
func (s *surface) PlaceWebViews(views []platform.WebViewPlacement) {
	if s.w.closed {
		return
	}
	s.placed = append(s.placed[:0], views...)
	shown := make([]*webView, 0, len(views))
	withPool(func() {
		for _, p := range views {
			v, ok := p.WebView.(*webView)
			if !ok || v.host != s || v.closed {
				continue
			}
			c, f := p.Clip, p.Frame
			msgSetRect(v.clip, sel("setFrame:"), rect(c.X, c.Y, c.W, c.H))
			msgSetRect(v.web, sel("setFrame:"), rect(f.X-c.X, f.Y-c.Y, f.W, f.H))
			if sendBool(v.clip, "isHidden") {
				send(v.clip, "setHidden:", 0)
			}
			shown = append(shown, v)
		}
		for _, v := range s.webViews {
			if !slices.Contains(shown, v) && !sendBool(v.clip, "isHidden") {
				if v.hasKeyboard() {
					// A hidden view keeps no keyboard: it goes back to the
					// content.
					send(s.view, "becomeFirstResponder")
				}
				send(v.clip, "setHidden:", 1)
			}
		}
		// In the order the content painted them, under the surface.
		for _, v := range shown {
			send(s.w.root, "insertSubview:belowSubview:", uintptr(v.clip), uintptr(s.view))
		}
	})
}

// hasKeyboard reports whether the page has the keyboard: the first
// responder is in the web view.
func (v *webView) hasKeyboard() bool {
	if v.host.w.win == 0 {
		return false
	}
	return sendBool(v.web, "isFirstResponder") || firstResponderIn(v.web)
}

// firstResponderIn reports whether a view or one of its subviews is the
// first responder.
func firstResponderIn(view id) bool {
	for _, sub := range arrayItems(send(view, "subviews")) {
		if sendBool(sub, "isFirstResponder") || firstResponderIn(sub) {
			return true
		}
	}
	return false
}

func (v *webView) Focus() {
	if !v.closed {
		send(v.web, "becomeFirstResponder")
	}
}

// TabInto gives the page the keyboard; iOS moves no focus by Tab into a
// page's first or last element.
func (v *webView) TabInto(bool) { v.Focus() }

func (v *webView) SetBackgroundColor(c platform.Color) {
	color := uiColor(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255, float64(c.A)/255)
	send(v.clip, "setBackgroundColor:", uintptr(color))
}

// Close closes the web view: by Close, or as its window closes.
func (v *webView) Close() { v.close() }

func (v *webView) close() {
	if v.closed {
		return
	}
	v.closed = true
	b := v.b
	withPool(func() {
		if v.hasKeyboard() {
			send(v.host.view, "becomeFirstResponder")
		}
		send(v.web, "removeObserver:forKeyPath:", uintptr(v.delegate), uintptr(nsString("title")))
		send(v.ucc, "removeScriptMessageHandlerForName:", uintptr(nsString("mygo")))
		send(v.ucc, "removeAllUserScripts")
		send(v.web, "stopLoading")
		send(v.web, "setNavigationDelegate:", 0)
		send(v.web, "setUIDelegate:", 0)
		send(v.clip, "removeFromSuperview")
		delete(b.byDelegate, v.delegate)
		delete(b.byWebView, v.web)
		release(v.ucc)
		release(v.web)
		release(v.clip)
		// WebKit may still hold the delegate as the page goes.
		autorelease(v.delegate)
	})
	s := v.host
	s.webViews = slices.DeleteFunc(s.webViews, func(x *webView) bool { return x == v })
	s.placed = slices.DeleteFunc(s.placed, func(p platform.WebViewPlacement) bool { return p.WebView == platform.WebView(v) })
}

func (v *webView) WebViewHandle() uintptr { return uintptr(v.web) }

func (v *webView) LoadURL(url string) {
	v.programmatic = true
	withPool(func() {
		req := send(class("NSURLRequest"), "requestWithURL:", uintptr(nsURL(url)))
		send(v.web, "loadRequest:", uintptr(req))
	})
}

func (v *webView) LoadHTML(html, baseURL string) {
	v.programmatic = true
	withPool(func() {
		var base id
		if baseURL != "" {
			base = nsURL(baseURL)
		}
		send(v.web, "loadHTMLString:baseURL:", uintptr(nsString(html)), uintptr(base))
	})
}

func (v *webView) LoadFile(path, readAccessDir string) {
	v.programmatic = true
	withPool(func() {
		send(v.web, "loadFileURL:allowingReadAccessToURL:", uintptr(fileURL(path)), uintptr(fileURL(readAccessDir)))
	})
}

func (v *webView) Reload(ignoreCache bool) {
	if ignoreCache {
		send(v.web, "reloadFromOrigin")
	} else {
		send(v.web, "reload")
	}
}

func (v *webView) StopLoading()       { send(v.web, "stopLoading") }
func (v *webView) GoBack()            { send(v.web, "goBack") }
func (v *webView) GoForward()         { send(v.web, "goForward") }
func (v *webView) CanGoBack() bool    { return sendBool(v.web, "canGoBack") }
func (v *webView) CanGoForward() bool { return sendBool(v.web, "canGoForward") }
func (v *webView) IsLoading() bool    { return sendBool(v.web, "isLoading") }

func (v *webView) URL() string {
	var s string
	withPool(func() { s = goString(send(send(v.web, "URL"), "absoluteString")) })
	return s
}

// SetUserScripts replaces the user scripts: a content controller can only
// remove all of its scripts, so they are all added again.
func (v *webView) SetUserScripts(scripts []platform.UserScript) {
	if v.closed {
		return
	}
	v.opts.UserScripts = scripts
	withPool(func() {
		send(v.ucc, "removeAllUserScripts")
		v.addUserScripts()
	})
}

func (v *webView) Eval(js string) {
	withPool(func() {
		send(v.web, "evaluateJavaScript:completionHandler:", uintptr(nsString(js)), 0)
	})
}

func (v *webView) CallAsyncFunction(body string, cb func(string, error)) {
	withPool(func() {
		blk := newBlock(func(_ objc.Block, result id, err id) {
			if err != 0 {
				cb("", nsError(err))
				return
			}
			cb(goString(result), nil)
		})
		defer blk.Release()
		send(v.web, "callAsyncJavaScript:arguments:inFrame:inContentWorld:completionHandler:",
			uintptr(nsString(body)), 0, 0, uintptr(send(class("WKContentWorld"), "pageWorld")), uintptr(blk))
	})
}

func (v *webView) SetZoom(f float64) {
	if respondsTo(v.web, "setPageZoom:") {
		msgSetFloat(v.web, sel("setPageZoom:"), f)
	}
}

func (v *webView) Zoom() float64 {
	if respondsTo(v.web, "pageZoom") {
		return msgFloat(v.web, sel("pageZoom"))
	}
	return 1
}

func (v *webView) SetUserAgent(ua string) {
	withPool(func() {
		var s id
		if ua != "" {
			if suffix := v.opts.UserAgentSuffix; suffix != "" {
				ua += " " + suffix
			}
			s = nsString(ua)
		}
		send(v.web, "setCustomUserAgent:", uintptr(s))
	})
}

func (v *webView) UserAgent() string {
	var s string
	withPool(func() {
		s = goString(send(v.web, "customUserAgent"))
		if s == "" && respondsTo(v.web, "_userAgent") {
			s = goString(send(v.web, "_userAgent"))
		}
	})
	return s
}

// The page's inspector is Safari's, on a Mac, for a web view that is
// inspectable (PageOptions.DevTools): the app cannot open it.
func (v *webView) OpenDevTools()          {}
func (v *webView) CloseDevTools()         {}
func (v *webView) IsDevToolsOpened() bool { return false }
func (v *webView) DroppedFiles() []string { return nil }
func (v *webView) Print()                 {}

func (v *webView) CapturePage(cb func([]byte, error)) {
	withPool(func() {
		blk := newBlock(func(_ objc.Block, img id, err id) {
			if err != 0 || img == 0 {
				cb(nil, nsError(err))
				return
			}
			var png []byte
			withPool(func() { png = goBytes(uiImagePNG(img)) })
			cb(png, nil)
		})
		defer blk.Release()
		send(v.web, "takeSnapshotWithConfiguration:completionHandler:", 0, uintptr(blk))
	})
}

// PrintToPDF makes a PDF of the page (createPDFWithConfiguration:), one
// page as long as the page's content; the options' paper is the
// system's to choose.
func (v *webView) PrintToPDF(_ platform.PDFOptions, cb func([]byte, error)) {
	if !respondsTo(v.web, "createPDFWithConfiguration:completionHandler:") {
		cb(nil, errUnsupported)
		return
	}
	withPool(func() {
		blk := newBlock(func(_ objc.Block, data id, err id) {
			if err != 0 || data == 0 {
				cb(nil, nsError(err))
				return
			}
			cb(goBytes(data), nil)
		})
		defer blk.Release()
		send(v.web, "createPDFWithConfiguration:completionHandler:", 0, uintptr(blk))
	})
}

var uiImagePNGRepresentation func(img id) id

// uiImagePNG returns the PNG data of a UIImage, autoreleased.
func uiImagePNG(img id) id {
	if uiImagePNGRepresentation == nil {
		purego.RegisterLibFunc(&uiImagePNGRepresentation, libUIKit, "UIImagePNGRepresentation")
	}
	return uiImagePNGRepresentation(img)
}

// securityOrigin renders a WKSecurityOrigin like a URL origin.
func securityOrigin(o id) string {
	origin := goString(send(o, "protocol")) + "://" + goString(send(o, "host"))
	if port := sendInt(o, "port"); port != 0 {
		origin += ":" + strconv.Itoa(port)
	}
	return origin
}

func (b *Backend) webViewFor(delegate id) *webView {
	v := b.byDelegate[delegate]
	if v == nil || v.closed {
		return nil
	}
	return v
}

func (v *webView) loadFailed(err id) {
	code := sendInt(err, "code")
	// Cancelled (-999) and "frame load interrupted" (102) happen when a
	// navigation is replaced or denied; they are not failures.
	if code == -999 || code == 102 {
		return
	}
	var url, desc string
	withPool(func() {
		info := send(err, "userInfo")
		url = goString(send(info, "objectForKey:", uintptr(nsString("NSErrorFailingURLStringKey"))))
		desc = goString(send(err, "localizedDescription"))
	})
	v.h.LoadFailed(url, code, desc)
}

func registerWebViewClasses() {
	registerSchemeHandler()
	b := func() *Backend { return theBackend }
	classDef("MyGoWebViewDelegate", "NSObject",
		[]string{"WKNavigationDelegate", "WKUIDelegate", "WKScriptMessageHandler", "WKDownloadDelegate"}, nil,
		[]objc.MethodDef{
			// WKScriptMessageHandler
			method("userContentController:didReceiveScriptMessage:", func(self id, _ objc.SEL, ucc, msg id) {
				v := b().webViewFor(self)
				if v == nil {
					return
				}
				// The handler is reachable from every frame; only the
				// page's own bridge may talk to Go.
				if frame := send(msg, "frameInfo"); frame == 0 || !sendBool(frame, "isMainFrame") {
					return
				}
				body := send(msg, "body")
				if body == 0 || !sendBool(body, "isKindOfClass:", uintptr(class("NSString"))) {
					return
				}
				v.h.Message(goString(body))
			}),
			// Key-value observing of the page's title.
			method("observeValueForKeyPath:ofObject:change:context:", func(self id, _ objc.SEL, keyPath, object, change id, ctx uintptr) {
				if v := b().webViewFor(self); v != nil && goString(keyPath) == "title" {
					v.h.TitleChanged(goString(send(v.web, "title")))
				}
			}),

			// WKNavigationDelegate
			method("webView:decidePolicyForNavigationAction:decisionHandler:", func(self id, _ objc.SEL, web, action id, handler uintptr) {
				// A link with the download attribute.
				if respondsTo(action, "shouldPerformDownload") && sendBool(action, "shouldPerformDownload") {
					callBlock(handler, 2) // WKNavigationActionPolicyDownload
					return
				}
				allow := true
				if v := b().webViewFor(self); v != nil {
					frame := send(action, "targetFrame")
					navType := sendInt(action, "navigationType")
					nav := platform.Navigation{
						URL:           goString(send(send(send(action, "request"), "URL"), "absoluteString")),
						IsMainFrame:   frame != 0 && sendBool(frame, "isMainFrame"),
						UserInitiated: navType == 0 || navType == 1,
						Unasked:       navType == 2 || navType == 3,
						Reload:        navType == 3,
					}
					if nav.IsMainFrame && v.programmatic {
						v.programmatic = false
						nav.Unasked = true
					}
					allow = v.h.WillNavigate(nav)
				}
				callBlock(handler, boolArg(allow))
			}),
			// Responses the page cannot show, or sent as attachments, are
			// downloads.
			method("webView:decidePolicyForNavigationResponse:decisionHandler:", func(self id, _ objc.SEL, web, nav id, handler uintptr) {
				const cancel, allow, download = 0, 1, 2 // WKNavigationResponsePolicy
				resp := send(nav, "response")
				url := goString(send(send(resp, "URL"), "absoluteString"))
				disposition, custom := schemeDispositions[url]
				if !custom && respondsTo(resp, "allHeaderFields") {
					disposition = goString(send(send(resp, "allHeaderFields"), "objectForKey:", uintptr(nsString("Content-Disposition"))))
				}
				attachment := strings.HasPrefix(strings.ToLower(strings.TrimSpace(disposition)), "attachment")
				switch {
				case !attachment && sendBool(nav, "canShowMIMEType"):
					callBlock(handler, allow)
				case custom:
					// WebKit cannot turn what a scheme handler serves into a
					// download: serve it again into one.
					callBlock(handler, cancel)
					if v := b().webViewFor(self); v != nil {
						v.h.SchemeDownload(url)
					}
				case hasClass("WKDownload"):
					callBlock(handler, download)
				default:
					callBlock(handler, cancel)
				}
			}),
			method("webView:navigationAction:didBecomeDownload:", func(self id, _ objc.SEL, web, action, download id) {
				send(download, "setDelegate:", uintptr(self))
			}),
			method("webView:navigationResponse:didBecomeDownload:", func(self id, _ objc.SEL, web, nav, download id) {
				send(download, "setDelegate:", uintptr(self))
			}),
			// WKDownloadDelegate
			method("download:decideDestinationUsingResponse:suggestedFilename:completionHandler:", func(self id, _ objc.SEL, download, resp, suggested id, handler uintptr) {
				v := b().webViewFor(self)
				if v == nil {
					callBlock(handler, 0)
					return
				}
				url := goString(send(send(send(download, "originalRequest"), "URL"), "absoluteString"))
				path := v.h.DownloadStarted(url, goString(suggested))
				if path == "" {
					callBlock(handler, 0) // cancels the download
					return
				}
				os.Remove(path) // WebKit refuses to replace a file
				v.downloads[download] = [2]string{url, path}
				withPool(func() { callBlock(handler, uintptr(fileURL(path))) })
			}),
			method("downloadDidFinish:", func(self id, _ objc.SEL, download id) {
				if v := b().webViewFor(self); v != nil {
					if d, ok := v.downloads[download]; ok {
						delete(v.downloads, download)
						v.h.DownloadFinished(d[0], d[1], nil)
					}
				}
			}),
			method("download:didFailWithError:resumeData:", func(self id, _ objc.SEL, download, nserr, resume id) {
				if v := b().webViewFor(self); v != nil {
					if d, ok := v.downloads[download]; ok {
						delete(v.downloads, download)
						v.h.DownloadFinished(d[0], d[1], errors.New(goString(send(nserr, "localizedDescription"))))
					}
				}
			}),
			method("webView:didStartProvisionalNavigation:", func(self id, _ objc.SEL, web, nav id) {
				if v := b().webViewFor(self); v != nil {
					v.h.NavigationStarted(v.URL())
				}
			}),
			method("webView:didCommitNavigation:", func(self id, _ objc.SEL, web, nav id) {
				if v := b().webViewFor(self); v != nil {
					v.h.NavigationCommitted(v.URL())
				}
			}),
			method("webView:didFinishNavigation:", func(self id, _ objc.SEL, web, nav id) {
				if v := b().webViewFor(self); v != nil {
					v.h.LoadFinished()
				}
			}),
			method("webView:didFailProvisionalNavigation:withError:", func(self id, _ objc.SEL, web, nav, err id) {
				if v := b().webViewFor(self); v != nil {
					v.loadFailed(err)
				}
			}),
			method("webView:didFailNavigation:withError:", func(self id, _ objc.SEL, web, nav, err id) {
				if v := b().webViewFor(self); v != nil {
					v.loadFailed(err)
				}
			}),
			method("webViewWebContentProcessDidTerminate:", func(self id, _ objc.SEL, web id) {
				if v := b().webViewFor(self); v != nil {
					v.h.RenderProcessGone("terminated")
				}
			}),

			// WKUIDelegate
			method("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:", func(self id, _ objc.SEL, web, cfg, action, features id) id {
				v := b().webViewFor(self)
				if v == nil {
					return 0
				}
				num := func(key string) int {
					if n := send(features, key); n != 0 {
						return sendInt(n, "integerValue")
					}
					return 0
				}
				// iOS shows no windows of pages: the handler decides
				// what becomes of the URL, as opening it elsewhere.
				v.h.NewWindow(platform.NewWindowRequest{
					URL: goString(send(send(send(action, "request"), "URL"), "absoluteString")),
					X:   num("x"), Y: num("y"), Width: num("width"), Height: num("height"),
				})
				return 0
			}),
			method("webViewDidClose:", func(self id, _ objc.SEL, web id) {
				if v := b().webViewFor(self); v != nil {
					v.h.ClosedByPage()
				}
			}),
			method("webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:", func(self id, _ objc.SEL, web, origin, frame id, typ int, handler uintptr) {
				const grant, deny = 1, 2 // WKPermissionDecision
				decision := deny
				if v := b().webViewFor(self); v != nil {
					kinds := map[int][]string{0: {"camera"}, 1: {"microphone"}, 2: {"camera", "microphone"}}[typ]
					if v.h.PermissionRequested(kinds, securityOrigin(origin)) {
						decision = grant
					}
				}
				callBlock(handler, uintptr(decision))
			}),
			method("webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:", func(self id, _ objc.SEL, web, message, frame id, handler uintptr) {
				handler = blockCopy(handler)
				b().alert(alertOptions{message: goString(message), buttons: []alertButton{{title: platform.CurrentTexts().OK}}}, func(int, string) {
					callBlock(handler)
					blockRelease(handler)
				})
			}),
			method("webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:", func(self id, _ objc.SEL, web, message, frame id, handler uintptr) {
				handler = blockCopy(handler)
				tx := platform.CurrentTexts()
				b().alert(alertOptions{message: goString(message), buttons: []alertButton{{title: tx.OK}, {title: tx.Cancel, cancel: true}}}, func(index int, _ string) {
					callBlock(handler, boolArg(index == 0))
					blockRelease(handler)
				})
			}),
			method("webView:runJavaScriptTextInputPanelWithPrompt:defaultText:initiatedByFrame:completionHandler:", func(self id, _ objc.SEL, web, prompt, defaultText, frame id, handler uintptr) {
				handler = blockCopy(handler)
				tx := platform.CurrentTexts()
				text := goString(defaultText)
				b().alert(alertOptions{message: goString(prompt), field: &text, buttons: []alertButton{{title: tx.OK}, {title: tx.Cancel, cancel: true}}}, func(index int, typed string) {
					var result id
					if index == 0 {
						withPool(func() { callBlock(handler, uintptr(nsString(typed))) })
					} else {
						callBlock(handler, uintptr(result))
					}
					blockRelease(handler)
				})
			}),
		})
}
