package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWidgetsConfig(t *testing.T) {
	dir := t.TempDir()
	load := func(json string) (*Config, error) {
		if err := os.WriteFile(filepath.Join(dir, "mygo.json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
		return loadConfig(dir)
	}
	for json, want := range map[string]string{
		`{"widgets": [{"kind": "a/b"}]}`:                                           "is not a kind",
		`{"widgets": [{"kind": "a"}, {"kind": "a"}]}`:                              "listed twice",
		`{"widgets": [{"kind": "a", "families": ["huge"]}]}`:                       `"huge" is none of`,
		`{"ios": {"appGroup": "group example"}}`:                                   "is not an app group",
		`{"widgets": [` + strings.Repeat(`{"kind": "k"},`, 10) + `{"kind": "z"}]}`: "more than the 10",
	} {
		if _, err := load(json); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", json, err, want)
		}
	}
	c, err := load(`{"identifier": "com.example.app", "widgets": [{"kind": "counter"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	w := c.Widgets[0]
	if w.name() != "counter" || strings.Join(w.families(), ",") != "small,medium,large" {
		t.Errorf("defaults: name %q, families %v", w.name(), w.families())
	}
	if g := c.iosAppGroup(); g != "group.com.example.app" {
		t.Errorf("iOS app group = %q", g)
	}
	if s := c.macWidgetsStore(); s[widgetsPathKey] != filepath.Join("Library", "Application Support", "com.example.app", "Widgets") || s[appGroupKey] != nil {
		t.Errorf("macOS store without an app group = %v", s)
	}
	c.MacOS.AppGroup = "TEAMID.com.example.app"
	if s := c.macWidgetsStore(); s[appGroupKey] != "TEAMID.com.example.app" || s[widgetsPathKey] != nil {
		t.Errorf("macOS store with an app group = %v", s)
	}
}

func TestKindsSwift(t *testing.T) {
	c := &Config{file: "mygo.json", Widgets: []Widget{
		{Kind: "counter", Name: `Say "hi"`, Description: "Two\nlines \\ ok", Families: []string{"small", "circular"}},
		{Kind: "notes"},
	}}
	src := kindsSwift(c)
	for _, want := range []string{
		`StaticConfiguration(kind: "counter", provider: MyGoProvider(kind: "counter"))`,
		`MyGoEntryView(entry: entry, name: "Say \"hi\"", description: "Two\nlines \\ ok")`,
		`.supportedFamilies(families(["small", "circular"]))`,
		`.supportedFamilies(families(["small", "medium", "large"]))`,
		`.configurationDisplayName(Text(verbatim: "notes"))`,
		"@main\nstruct MyGoWidgets: WidgetBundle {\n    var body: some Widget {\n        MyGoWidget0()\n        MyGoWidget1()\n    }\n}\n",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Kinds.swift lacks %q:\n%s", want, src)
		}
	}
	if got := swiftString("a\tb\x01é"); got != `"a\tb\u{1}é"` {
		t.Errorf("swiftString = %s", got)
	}
}

func TestAtLeast(t *testing.T) {
	for _, c := range [][3]string{{"12.0", "11.0", "12.0"}, {"10.15", "11.0", "11.0"}, {"14", "14.0", "14"}, {"13.4", "14.0", "14.0"}, {"15.0", "14.0", "15.0"}} {
		if got := atLeast(c[0], c[1]); got != c[2] {
			t.Errorf("atLeast(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestWidgetsInfoPlists(t *testing.T) {
	c := &Config{Name: "App", Identifier: "com.example.app", Version: "1.0", MacOS: MacOS{MinimumSystemVersion: "12.0"}, IOS: IOS{MinimumOSVersion: "15.0"}}
	if p := string(infoPlist(c, "App", "", "")); strings.Contains(p, widgetsPathKey) || strings.Contains(p, appGroupKey) {
		t.Errorf("an app without widgets says where they are:\n%s", p)
	}
	c.Widgets = []Widget{{Kind: "counter"}}
	if p := string(infoPlist(c, "App", "", "")); !strings.Contains(p, "<key>"+widgetsPathKey+"</key>\n\t<string>Library/Application Support/com.example.app/Widgets</string>") {
		t.Errorf("the macOS Info.plist lacks the widgets' directory:\n%s", p)
	}
	if p := string(iosInfoPlist(c, "App", iosTarget{arch: "arm64"}, false)); !strings.Contains(p, "<key>"+appGroupKey+"</key>\n\t<string>group.com.example.app</string>") {
		t.Errorf("the iOS Info.plist lacks the app group:\n%s", p)
	}
}

// TestWidgetsAgree checks that the CLI, package widget and the widget
// extension name the same keys, files and function.
func TestWidgetsAgree(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	native, swift, reload, pkg := read("widget/native_darwin.go"), read("cmd/mygo/widgets/Widgets.swift"), read("cmd/mygo/widgets/Reload.swift"), read("widget/widget.go")
	for _, key := range []string{appGroupKey, widgetsPathKey} {
		if !strings.Contains(native, `"`+key+`"`) || !strings.Contains(swift, `"`+key+`"`) {
			t.Errorf("package widget or the extension does not read %s", key)
		}
	}
	for _, s := range []string{`"MyGoWidgets"`, `"timeline.json"`} {
		if !strings.Contains(swift, s) {
			t.Errorf("the extension does not read %s", s)
		}
	}
	if !strings.Contains(native, `filepath.Join(goString(send(url, "path")), "MyGoWidgets")`) || !strings.Contains(read("widget/encode.go"), `timelineFile = "timeline.json"`) {
		t.Error("package widget writes elsewhere than the extension reads")
	}
	if !strings.Contains(reload, `@_cdecl("mygo_widgets_reload")`) || !strings.Contains(native, `"mygo_widgets_reload"`) {
		t.Error("package widget calls another function than the reload library exports")
	}
	for _, lib := range []string{widgetKit + ".framework/" + widgetKit, widgetKitDylib} {
		if !strings.Contains(native, `"`+lib+`"`) {
			t.Errorf("package widget does not load %s", lib)
		}
	}
	if !strings.Contains(pkg, "regexp.MustCompile(`"+widgetKindRe.String()+"`)") {
		t.Error("package widget takes other kinds than mygo.json")
	}
}

// TestBuildWidgets builds an app with widgets for macOS and the iOS
// simulator, with Xcode's swiftc, and checks their bundles and signatures.
func TestBuildWidgets(t *testing.T) {
	if testing.Short() || runtime.GOOS != "darwin" {
		t.Skip("compiles Swift with Xcode")
	}
	if err := exec.Command("xcrun", "--sdk", "macosx", "swiftc", "--version").Run(); err != nil {
		t.Skipf("no swiftc: %v", err)
	}
	dir := testModule(t, map[string]string{
		"main.go":   "package main\n\nimport \"github.com/egoist/mygo/widget\"\n\nfunc main() { widget.Update(\"counter\") }\n",
		"mygo.json": `{"name": "Widget App", "identifier": "com.example.widgetapp", "widgets": [{"kind": "counter", "name": "Counter"}]}`,
	})
	c, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := buildOptions{sign: "-", skipDMG: true, work: t.TempDir()}
	if opts.pkg, err = packageDir(c); err != nil {
		t.Fatal(err)
	}
	if _, err := buildPlatform(c, "darwin", runtime.GOARCH, opts); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "dist", "darwin-"+runtime.GOARCH, "Widget App.app")
	appex := filepath.Join(app, "Contents", "PlugIns", widgetsAppex)
	for _, f := range []string{"Contents/MacOS/" + widgetsExec, "Contents/Info.plist"} {
		if !fileExists(filepath.Join(appex, filepath.FromSlash(f))) {
			t.Errorf("the widget extension has no %s", f)
		}
	}
	if !fileExists(filepath.Join(app, "Contents", "Frameworks", widgetKitDylib)) {
		t.Error("the app has no reload library")
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		t.Errorf("codesign --verify: %v\n%s", err, out)
	}
	ents, _ := exec.Command("codesign", "-d", "--entitlements", ":-", appex).Output()
	for _, want := range []string{"com.apple.security.app-sandbox", "/Library/Application Support/com.example.widgetapp/Widgets/"} {
		if !strings.Contains(string(ents), want) {
			t.Errorf("the extension's entitlements lack %s:\n%s", want, ents)
		}
	}
	info, _ := os.ReadFile(filepath.Join(appex, "Contents", "Info.plist"))
	for _, want := range []string{"com.example.widgetapp.widgets", "com.apple.widgetkit-extension", widgetsPathKey} {
		if !strings.Contains(string(info), want) {
			t.Errorf("the extension's Info.plist lacks %s:\n%s", want, info)
		}
	}

	if err := exec.Command("xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Run(); err != nil {
		t.Skipf("no iOS simulator SDK: %v", err)
	}
	if _, err := buildIOS(c, iosTarget{simulator: true, arch: runtime.GOARCH}, opts); err != nil {
		t.Fatal(err)
	}
	app = filepath.Join(dir, "dist", "ios-simulator-"+runtime.GOARCH, "Widget App.app")
	for _, f := range []string{"PlugIns/" + widgetsAppex + "/" + widgetsExec, "Frameworks/" + widgetKit + ".framework/" + widgetKit} {
		if !fileExists(filepath.Join(app, filepath.FromSlash(f))) {
			t.Errorf("the iOS app has no %s", f)
		}
	}
	for _, code := range []string{app, filepath.Join(app, "PlugIns", widgetsAppex)} {
		ents, _ := exec.Command("codesign", "-d", "--entitlements", ":-", code).Output()
		if !strings.Contains(string(ents), "group.com.example.widgetapp") {
			t.Errorf("%s is not signed with the app group:\n%s", filepath.Base(code), ents)
		}
	}
}
