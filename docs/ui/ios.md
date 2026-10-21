# Native UI on iOS

An app of native UI also runs on iPhones and iPads: its windows fill the
screen, drawn with Metal by the same renderer as on macOS, with text from
Core Text. Nothing changes in the app:

```go
func main() {
	s := &counter{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "Counter", Content: ui.View(s.view)})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

`mygo dev -ios` develops it in the iOS simulator: it builds the app for
it, installs it, launches it with its output in the console, and rebuilds
and relaunches it as you edit. `mygo build -platform ios` builds it for
devices into `<out>/ios-arm64`, an `.app` and an `.ipa`, and `-platform
ios-simulator` for the simulator (see [the CLI](../cli.md#mygo-build)).

```sh
mygo dev -ios
mygo build -platform ios
```

Both need a Mac with Xcode: Go links iOS apps only with the system's
linker, which it reaches through cgo, so builds for iOS turn cgo on and
use Xcode's clang, though MyGo calls UIKit without it. Without the CLI:

```sh
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$(xcrun -sdk iphoneos -f clang)" \
  CGO_CFLAGS="-isysroot $(xcrun -sdk iphoneos --show-sdk-path) -target arm64-apple-ios15.0" \
  CGO_LDFLAGS="-isysroot $(xcrun -sdk iphoneos --show-sdk-path) -target arm64-apple-ios15.0" \
  go build -o MyApp.app/MyApp .
```

and an Info.plist whose scene manifest names the scene delegate
`MyGoSceneDelegate`, which the one `mygo build` writes has.

## Devices and signing

A device runs an app signed for it: set `ios.signingIdentity`, an Apple
Development or Distribution certificate in your keychain, and
`ios.provisioningProfile`, a profile from your developer account that
allows the app's `identifier` (and, for development, your devices). The
app is signed with the entitlements the profile grants, or those of
`ios.entitlements`. Without an identity, `mygo build -platform ios`
leaves the app unsigned, and no device installs it.

```json
{
  "identifier": "com.example.counter",
  "ios": {
    "signingIdentity": "Apple Development: Jane Doe (TEAMID)",
    "provisioningProfile": "Counter_Development.mobileprovision"
  }
}
```

Install the app on a connected device with `xcrun devicectl device
install app --device <device> <out>/ios-arm64/<name>.app`; the `.ipa` is
what App Store Connect and other installers take. The icon comes from
`icon`, resized for iPhones and iPads; iOS shows no transparency in icons,
so give it a background. See [configuration](../configuration.md#ios).

## What works

- **Drawing** with Metal, at up to 120 frames a second on ProMotion
  displays, only when something changed.
- **Touch.** A tap clicks. A finger that moves scrolls the content under
  it, which follows the finger and flings on as it lets go, unless what
  it pressed takes the drag: a slider, a `Draggable` element, an element
  handling its input (which hears `InputEvent.Touch`). Touching what
  flings stops it. A finger hovers nothing: hover styles show only while
  it presses.
- **The safe area.** The view lays itself out past the status bar, the
  sensor housing and the home indicator, while its background fills the
  screen, and above the software keyboard, which shows while a text
  input has the focus.
- **Text input**: the software keyboard and input methods, as Pinyin,
  composing in place; Return and the delete key are `KeyEnter` and
  `KeyBackspace`. Autocorrection, capitalization and smart punctuation are
  off, as text may not be prose.
- **Hardware keyboards**, as on an iPad: keys with ⌘ for the shortcuts,
  repeating while held. A mouse or a trackpad points, hovers, clicks with
  its secondary button and scrolls.
- **Web views** ([`ui.WebView`](web-view.md)): pages beside native
  controls, which load the app's frontend, call bound Go methods and hear
  its events as on desktops; their alerts are the system's.
- **VoiceOver** reads the view's elements and acts on them.
- **Rotation, Dark Mode** and the text size of Settings (`TextScale`),
  which the view follows as they change; reduced motion, reduced
  transparency and darker colors too.
- `Dialog.Message` shows an alert; context menus show as action sheets;
  `Shell.OpenExternal` opens Safari or the app of the link; the clipboard,
  secrets (the app's Keychain), the network's status, cookies,
  `App.SetBadgeCount` and `Power.KeepAwake` (the screen stays on) work.

## What does not

Windows of web pages (`WindowOptions.URL` and the like) do not open:
show pages in [web views](web-view.md) of a window of native UI instead.
Window sizes, positions, title bars and their controls do nothing, as
windows fill the screen; the last shown is in front, and the app closes
windows, as the user cannot. Menus, trays, global shortcuts,
notifications, file dialogs, file watches, flyouts placed by an anchor
(they fill the screen), drags of data between apps and pointer shapes have
nothing on iOS to stand for them, or are not supported yet, and report
so. One scene: iPads show the app in one window. `App.Quit` ends the
process, as iOS apps are not meant to; leave that to the system.

## Under the hood

UIKit runs the app from `App.Run`, which never returns: Go's main
goroutine is UIKit's main thread. The architecture guide's
[iOS](../architecture.md#ios-internalios) section has the details.
