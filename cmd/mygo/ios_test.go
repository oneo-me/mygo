package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseIOSPlatform(t *testing.T) {
	for p, want := range map[string]iosTarget{
		"ios":                 {arch: "arm64"},
		"ios/arm64":           {arch: "arm64"},
		"ios-simulator":       {simulator: true, arch: runtime.GOARCH},
		"ios-simulator/amd64": {simulator: true, arch: "amd64"},
	} {
		if got, ok := parseIOSPlatform(p); !ok || got != want {
			t.Errorf("%s: %+v, %v; want %+v", p, got, ok, want)
		}
	}
	for _, p := range []string{"ios/amd64", "darwin/arm64", "web", "ios-simulator/386"} {
		if _, ok := parseIOSPlatform(p); ok {
			t.Errorf("%s is no iOS platform", p)
		}
	}
	if d := (iosTarget{simulator: true, arch: "arm64"}).dir(); d != "ios-simulator-arm64" {
		t.Errorf("the simulator's directory is %s", d)
	}
}

func TestIOSInfoPlist(t *testing.T) {
	c := &Config{Name: "A & B", Identifier: "com.example.ab", Version: "1.2.3", URLSchemes: []string{"ab"},
		IOS: IOS{MinimumOSVersion: "16.0", InfoPlist: map[string]any{"NSCameraUsageDescription": "Scan."}}}
	plist := string(iosInfoPlist(c, "A & B", iosTarget{arch: "arm64"}, true))
	for _, want := range []string{
		"<string>A &amp; B</string>",
		"<key>CFBundleIdentifier</key>\n\t<string>com.example.ab</string>",
		"<key>MinimumOSVersion</key>\n\t<string>16.0</string>",
		"<string>iPhoneOS</string>",
		"<key>UISceneDelegateClassName</key>",
		"<string>" + iosSceneDelegate + "</string>",
		"<string>AppIcon60x60</string>",
		"<key>CFBundleURLSchemes</key>",
		"<key>NSCameraUsageDescription</key>\n\t<string>Scan.</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("Info.plist missing %q:\n%s", want, plist)
		}
	}
	sim := string(iosInfoPlist(c, "A & B", iosTarget{simulator: true, arch: "arm64"}, false))
	if !strings.Contains(sim, "<string>iPhoneSimulator</string>") || strings.Contains(sim, "CFBundleIcons") {
		t.Errorf("the simulator's Info.plist:\n%s", sim)
	}
}

// TestIOSSceneDelegate checks that the scene manifest names the scene
// delegate the iOS backend registers.
func TestIOSSceneDelegate(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "ios", "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `classDef("`+iosSceneDelegate+`"`) {
		t.Errorf("internal/ios registers no class %s", iosSceneDelegate)
	}
}

func TestWriteIPA(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "My App.app")
	if err := os.MkdirAll(filepath.Join(app, "en.lproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"My App": "exe", "Info.plist": "plist", "en.lproj/InfoPlist.strings": "strings"} {
		mode := os.FileMode(0o644)
		if name == "My App" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(app, name), []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	ipa := filepath.Join(dir, "My App 1.0.ipa")
	if err := writeIPA(app, ipa); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(ipa)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
		if f.Name == "Payload/My App.app/My App" && f.Mode().Perm()&0o100 == 0 {
			t.Errorf("the executable lost its mode: %v", f.Mode())
		}
	}
	for _, want := range []string{"Payload/My App.app/", "Payload/My App.app/My App", "Payload/My App.app/Info.plist", "Payload/My App.app/en.lproj/InfoPlist.strings"} {
		if !slices.Contains(names, want) {
			t.Errorf("the .ipa has no %s: %v", want, names)
		}
	}
}
