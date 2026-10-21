# Widgets

Widgets show a glance of the app outside it: on the Home Screen and the
Lock Screen of iPhones and iPads, and on the desktop and in Notification
Center of Macs. The app declares the kinds of its widgets in `mygo.json`,
and package `widget` gives each kind what to show, in views written in
Go, which the system shows whether the app runs or not.

```json
{
  "identifier": "com.example.counter",
  "urlSchemes": ["counter"],
  "widgets": [
    {
      "kind": "counter",
      "name": "Counter",
      "description": "Shows the count.",
      "families": ["small", "medium", "circular"]
    }
  ]
}
```

```go
import "github.com/egoist/mygo/widget"

err := widget.Update("counter", widget.Entry{
	View: widget.Column(
		widget.Text("Count").TextStyle(widget.Caption).TextColor(widget.Secondary),
		widget.Spacer(),
		widget.Textf("%d", n).FontSize(44).Bold().MonospacedDigits(),
	).AlignItems(widget.Start).Fill(),
	URL: "counter://open",
})
```

`mygo build` and `mygo dev` put a widget extension into the apps of macOS
and iOS they make, compiled with Xcode's Swift compiler, so building an
app with widgets needs a Mac with Xcode; a build for macOS elsewhere
leaves them out. The user adds the widgets from the system's gallery,
where they show `name` and `description`, in the app's name.
[`examples/widgets`](https://github.com/egoist/mygo/tree/main/examples/widgets)
is a counter whose widgets show its count over a photo picked from the
library.

## Updating widgets

`widget.Update` replaces what the widgets of a kind show and asks the
system to show it at once. It can be called from any goroutine, before
`App.Run` too, and returns an error where there are no widgets:
`widget.ErrUnsupported` on Linux, Windows and in browsers, and
`widget.ErrNoWidgets` in an app built without them, as with `go run`.
`widget.Available` reports whether updates can work.

The widget extension draws the views with SwiftUI and runs none of the
app's code: widgets show what the app wrote last, until it writes again.
Update them when what they show changes, and as the app starts. The
system lets an app in the foreground update its widgets as often as it
likes, and one in the background a few dozen times a day.

An update is a timeline: entries with the dates they show from, which
the widgets follow without the app, as an agenda's next events do. An
entry without a date shows at once; `Update` without entries brings back
the placeholder, the kind's name and description.

```go
widget.Update("agenda",
	widget.Entry{View: widget.Text("Stand-up at 10:00")},
	widget.Entry{Date: tenAM, View: widget.Text("Lunch at 12:30")},
)
```

`widget.Date` shows a time, and its styles `Relative`, `Offset` and
`Timer` change as time passes, without an entry for every second:
`widget.Date(deadline, widget.Timer)` counts down to the deadline.

## Views

Views have the vocabulary of [native UI](ui/README.md), built from
functions and changed by methods that return copies:

| Views | |
|---|---|
| `Text`, `Textf` | text |
| `Symbol` | one of Apple's SF Symbols, as `"drop.fill"`, sized by the font |
| `Image`, `ImageFile` | an image, PNG, JPEG, HEIF and the like, read at the size the widget shows it, up to 1024 pixels across |
| `Date` | a time, in the system's language and formats |
| `Column`, `Row`, `Stack` | views from top to bottom, from start to end, and over each other |
| `Spacer`, `Divider` | the space left, and a thin line |
| `Rectangle` | a color filling its space |
| `Progress`, `Gauge` | a bar and a ring filled to a value from 0 to 1 |

| Methods | |
|---|---|
| `Gap`, `AlignItems`, `Justify`, `Center` | the space between children, and where the content goes, across and along the axis; centered by default |
| `Padding`, `PaddingX`, `PaddingY` | the space inside the edges, inside the background |
| `Width`, `Height`, `Fill`, `FillWidth`, `FillHeight` | the size |
| `Background`, `Radius`, `Opacity` | a color behind the view, round corners |
| `TextColor`, `TextStyle`, `FontSize`, `FontWeight`, `Bold`, `Italic`, `FontDesign`, `MonospacedDigits` | the text, inherited by the views inside |
| `TextAlign`, `MaxLines`, `MinScale` | the lines of a text, and how far it shrinks to fit |
| `Fit` | how an image fills its space: `Contain` or `Cover` |
| `Link` | the URL a tap on the view opens |
| `Accented` | puts the view in the accent color where the system tints widgets |

Colors are sRGB (`RGB`, `RGBA`, `Hex`), with `Dark` for dark mode, or the
system's `Primary`, `Secondary` and `Accent`, which follow the appearance
and the tinting of widgets. `TextStyle` sizes text with the system's
styles, which follow the text size the user chose.

## Families

A widget's family is its size and place: `small`, `medium` and `large`
on the Home Screen and the desktop, `extraLarge` on iPads and Macs, and
`circular`, `rectangular` and `inline` on the Lock Screen (iOS 16 and
later). Each kind lists those it comes in (`small`, `medium` and `large`
by default). An entry's `View` shows in all of them, unless `Families`
gives a family a view of its own:

```go
widget.Entry{
	View: small,
	Families: map[widget.Family]widget.View{
		widget.Medium:   widget.Row(small, details),
		widget.Circular: widget.Stack(widget.Gauge(progress), widget.Textf("%d", n)),
		widget.Inline:   widget.Textf("%d steps", n),
	},
}
```

Lock Screen widgets are drawn in one color, tinted by the system:
`Accented` marks what takes its accent.

`Entry.Background` fills the widget behind its view, past the margins
the system keeps around the view: a `Rectangle` of a color, or an image
to `Cover`. Without one, widgets have the system's background.

## Taps

A tap on a widget opens the app. With `Entry.URL`, it opens the app with
a link of one of its URL schemes (`urlSchemes` in `mygo.json`), which
reaches [`App.OnOpenURL`](app.md#deep-links); `View.Link` gives a part of
a medium or large widget a link of its own, as buttons:

```go
widget.Text("+1").Padding(8).Background(widget.RGBA(120, 120, 128, 0.2)).Radius(8).Link("counter://increment")
```

## Where the content lives

On iOS, the app and its widget extension share an app group, whose
container holds what the widgets show: `group.<identifier>`, or
`ios.appGroup`. For the simulator, builds sign both with it. For
devices, enable App Groups for the app's identifier and for
`<identifier>.widgets` in your developer account, with the same group,
and give the extension a provisioning profile of its own,
`ios.widgetsProvisioningProfile`, beside the app's (see
[distribution](distribution.md#ios)).

On macOS, the app writes what its widgets show to
`~/Library/Application Support/<identifier>/Widgets`, which the
extension, in the App Sandbox as macOS runs widgets, is allowed to read.
This needs no app group, whose container macOS opens only to apps that a
provisioning profile grants it, so it works the same signed ad hoc, as
by `mygo dev`, and with a Developer ID. An app in the App Sandbox, as
those of the Mac App Store, shares them through an app group instead,
`macos.appGroup`, which its provisioning profiles must grant (see
[distribution](distribution.md#widgets-on-macos)).
