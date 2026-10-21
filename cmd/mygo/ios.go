package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// iOS runs apps of native UI (package mygo's backend_ios.go): mygo build
// -platform ios makes an .app for devices, signed with ios.signingIdentity
// and ios.provisioningProfile, and an .ipa holding it; -platform
// ios-simulator makes one for the simulator, and mygo dev -ios runs the app
// in a simulator, rebuilding and relaunching it as its code changes. Go
// links iOS executables with the system's linker, which it does only with
// cgo on, though nothing imports "C": building for iOS needs a Mac with
// Xcode, whose clang and SDKs it uses.

// iosTarget is an iOS platform of -platform: devices (ios, ios/arm64), or
// the simulator (ios-simulator, on the Mac's architecture unless named).
type iosTarget struct {
	simulator bool
	arch      string
}

// parseIOSPlatform reports whether p names an iOS platform, and which.
func parseIOSPlatform(p string) (iosTarget, bool) {
	name, arch, _ := strings.Cut(p, "/")
	switch name {
	case "ios":
		if arch == "" || arch == "arm64" {
			return iosTarget{arch: "arm64"}, true
		}
	case "ios-simulator":
		if arch == "" {
			arch = runtime.GOARCH
		}
		if arch == "arm64" || arch == "amd64" {
			return iosTarget{simulator: true, arch: arch}, true
		}
	}
	return iosTarget{}, false
}

// sdk returns the name of the target's SDK.
func (t iosTarget) sdk() string {
	if t.simulator {
		return "iphonesimulator"
	}
	return "iphoneos"
}

// dir names the target's directory of the output: ios-arm64,
// ios-simulator-arm64.
func (t iosTarget) dir() string {
	if t.simulator {
		return "ios-simulator-" + t.arch
	}
	return "ios-" + t.arch
}

func (t iosTarget) String() string {
	if t.simulator {
		return "ios-simulator/" + t.arch
	}
	return "ios/" + t.arch
}

// iosEnv returns the environment that compiles the app for the target
// with Xcode's clang and SDK.
func iosEnv(c *Config, t iosTarget) ([]string, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("building for iOS needs macOS with Xcode")
	}
	xcrun := func(args ...string) (string, error) {
		out, err := exec.Command("xcrun", append([]string{"--sdk", t.sdk()}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("cannot find the %s SDK (does Xcode have it?): xcrun %s: %v", t.sdk(), strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	sdk, err := xcrun("--show-sdk-path")
	if err != nil {
		return nil, err
	}
	cc, err := xcrun("-f", "clang")
	if err != nil {
		return nil, err
	}
	arch := t.arch
	if arch == "amd64" {
		arch = "x86_64"
	}
	target := arch + "-apple-ios" + c.IOS.MinimumOSVersion
	if t.simulator {
		target += "-simulator"
	}
	flags := "-isysroot " + sdk + " -target " + target
	return []string{
		"GOOS=ios", "GOARCH=" + t.arch, "CGO_ENABLED=1", "CC=" + cc,
		"CGO_CFLAGS=" + flags, "CGO_LDFLAGS=" + flags,
	}, nil
}

// compileIOS compiles the app for the target into out.
func compileIOS(ctx context.Context, c *Config, tk *task, t iosTarget, out string, debug bool, overlay string) error {
	env, err := iosEnv(c, t)
	if err != nil {
		return err
	}
	ldflags := "-s -w" + packageFlags(c)
	if !debug {
		ldflags += " -X github.com/egoist/mygo.production=1"
	}
	flags := []string{"-trimpath", "-ldflags", ldflags}
	flags = append(flags, productionTags(debug)...)
	if overlay != "" {
		flags = append(flags, "-overlay", overlay)
	}
	return goBuild(ctx, c, tk, out, env, flags...)
}

// buildIOS builds and packages the app for an iOS target into
// <out>/<target dir>: the .app, and for devices the .ipa.
func buildIOS(c *Config, t iosTarget, opts buildOptions) ([]string, error) {
	out := c.path(c.Out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(out, ".staging-"+t.dir()+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o755); err != nil {
		return nil, err
	}
	if !opts.debug && keepInspector() {
		logf("Keeping the inspector of native UI (MYGO_INSPECTOR=1)")
	}
	bin := filepath.Join(stage, c.executableName())
	tk := con.start("Compiling " + t.String())
	overlay, err := writeOverlay(filepath.Join(opts.work, "overlay.json"), opts.overlay)
	if err == nil {
		err = compileIOS(context.Background(), c, tk, t, bin, opts.debug, overlay)
	}
	if err := tk.end(err, "Compiled "+t.String()); err != nil {
		return nil, err
	}
	app, err := writeIOSBundle(c, stage, bin, t)
	if err != nil {
		return nil, err
	}
	if len(c.Widgets) > 0 {
		tk := con.start("Compiling the widgets")
		if err := tk.end(addIOSWidgets(c, app, t), "Compiled the widgets"); err != nil {
			return nil, err
		}
	}
	artifacts := []string{app}
	if t.simulator {
		if err := signIOS(c, app, "-", ""); err != nil {
			return nil, err
		}
	} else {
		switch {
		case c.IOS.SigningIdentity == "":
			warnf("Not signing %s: set ios.signingIdentity and ios.provisioningProfile to install it on devices", filepath.Base(app))
		case c.IOS.ProvisioningProfile == "":
			return nil, errors.New("ios.signingIdentity needs ios.provisioningProfile: devices run apps their profile allows")
		default:
			tk := con.start("Signing " + filepath.Base(app))
			if err := tk.end(signIOS(c, app, c.IOS.SigningIdentity, opts.work), "Signed "+filepath.Base(app)); err != nil {
				return nil, err
			}
		}
		ipa := filepath.Join(stage, c.executableName()+" "+c.Version+".ipa")
		if err := writeIPA(app, ipa); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, ipa)
	}
	final := filepath.Join(out, t.dir())
	if err := replacePath(stage, final); err != nil {
		return nil, err
	}
	for i, a := range artifacts {
		artifacts[i] = filepath.Join(final, filepath.Base(a))
	}
	return artifacts, nil
}

// iosIconSizes are the app icons of an iOS bundle without an asset
// catalog, by file name, in pixels: the home screen's of iPhones and
// iPads, which name their files as Xcode did before asset catalogs.
var iosIconSizes = []struct {
	file string
	size int
}{
	{"AppIcon60x60@2x.png", 120},
	{"AppIcon60x60@3x.png", 180},
	{"AppIcon76x76@2x~ipad.png", 152},
	{"AppIcon83.5x83.5@2x~ipad.png", 167},
}

// writeIOSBundle assembles <dir>/<executable>.app around the executable
// bin, which is moved into it: the app's files are at the top of an iOS
// bundle, the resources (mygo.PathResources) beside the executable.
func writeIOSBundle(c *Config, dir, bin string, t iosTarget) (string, error) {
	name := c.executableName()
	app := filepath.Join(dir, name+".app")
	if err := os.RemoveAll(app); err != nil {
		return "", err
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(bin, filepath.Join(app, name)); err != nil {
		return "", err
	}
	reserved := []string{name, "Info.plist", "PkgInfo", "embedded.mobileprovision", "_CodeSignature"}
	icons := false
	if c.Icon != "" {
		src, err := os.ReadFile(c.path(c.Icon))
		if err != nil {
			return "", err
		}
		img, err := png.Decode(bytes.NewReader(src))
		if err != nil {
			return "", fmt.Errorf("icon %s: %w", c.Icon, err)
		}
		for _, ic := range iosIconSizes {
			var b bytes.Buffer
			if err := png.Encode(&b, resize(img, ic.size)); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(app, ic.file), b.Bytes(), 0o644); err != nil {
				return "", err
			}
			reserved = append(reserved, ic.file)
		}
		icons = true
	}
	res, err := c.resources("ios", t.arch, reserved...)
	if err != nil {
		return "", err
	}
	if err := copyResources(res, app); err != nil {
		return "", err
	}
	langs, err := c.languages()
	if err != nil {
		return "", err
	}
	if err := writeLocalizations(c, app, langs); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), iosInfoPlist(c, name, t, icons), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(app, "PkgInfo"), []byte("APPL????"), 0o644); err != nil {
		return "", err
	}
	return app, nil
}

// iosSceneDelegate is the class of the iOS backend's scene delegate, which
// the scene manifest names: UIKit connects the scenes windows show in.
const iosSceneDelegate = "MyGoSceneDelegate"

// iosInfoPlist returns the Info.plist of an iOS bundle.
func iosInfoPlist(c *Config, executable string, t iosTarget, icons bool) []byte {
	platform := "iPhoneOS"
	if t.simulator {
		platform = "iPhoneSimulator"
	}
	orientations := []any{"UIInterfaceOrientationPortrait", "UIInterfaceOrientationLandscapeLeft", "UIInterfaceOrientationLandscapeRight"}
	d := map[string]any{
		"CFBundleName":                  c.Name,
		"CFBundleDisplayName":           c.Name,
		"CFBundleIdentifier":            c.Identifier,
		"CFBundleVersion":               c.Version,
		"CFBundleShortVersionString":    c.Version,
		"CFBundleExecutable":            executable,
		"CFBundlePackageType":           "APPL",
		"CFBundleInfoDictionaryVersion": "6.0",
		"CFBundleSupportedPlatforms":    []any{platform},
		"DTPlatformName":                t.sdk(),
		"MinimumOSVersion":              c.IOS.MinimumOSVersion,
		"LSRequiresIPhoneOS":            true,
		"UIDeviceFamily":                []any{1.0, 2.0},
		// The launch screen is the system's background, which fills the
		// screen as the app's windows do.
		"UILaunchScreen": map[string]any{},
		// ProMotion's displays refresh up to 120 times a second.
		"CADisableMinimumFrameDurationOnPhone": true,
		"UIApplicationSceneManifest": map[string]any{
			"UIApplicationSupportsMultipleScenes": false,
			"UISceneConfigurations": map[string]any{
				"UIWindowSceneSessionRoleApplication": []any{map[string]any{
					"UISceneConfigurationName": "Default Configuration",
					"UISceneDelegateClassName": iosSceneDelegate,
				}},
			},
		},
		"UISupportedInterfaceOrientations":         orientations,
		"UISupportedInterfaceOrientations~ipad":    append(slices.Clone(orientations), "UIInterfaceOrientationPortraitUpsideDown"),
		"UIRequiresFullScreen":                     false,
		"UIApplicationSupportsIndirectInputEvents": true,
	}
	if langs, _ := c.languages(); len(langs) > 0 {
		d["CFBundleDevelopmentRegion"] = lprojName(langs[0].Tag)
		var names []any
		for _, l := range langs {
			if name := lprojName(l.Tag); !slices.Contains(names, any(name)) {
				names = append(names, name)
			}
		}
		d["CFBundleLocalizations"] = names
	}
	if icons {
		d["CFBundleIcons"] = map[string]any{"CFBundlePrimaryIcon": map[string]any{
			"CFBundleIconFiles": []any{"AppIcon60x60"},
		}}
		d["CFBundleIcons~ipad"] = map[string]any{"CFBundlePrimaryIcon": map[string]any{
			"CFBundleIconFiles": []any{"AppIcon60x60", "AppIcon76x76", "AppIcon83.5x83.5"},
		}}
	}
	if c.Copyright != "" {
		d["NSHumanReadableCopyright"] = c.Copyright
	}
	if len(c.URLSchemes) > 0 {
		var schemes []any
		for _, s := range c.URLSchemes {
			schemes = append(schemes, s)
		}
		d["CFBundleURLTypes"] = []any{map[string]any{"CFBundleURLName": c.Identifier, "CFBundleURLSchemes": schemes}}
	}
	if len(c.Widgets) > 0 {
		d[appGroupKey] = c.iosAppGroup()
	}
	for k, v := range c.IOS.InfoPlist {
		d[k] = v // the app's own keys win
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
`)
	writePlistValue(&b, d, "")
	b.WriteString("</plist>\n")
	return b.Bytes()
}

// signIOS signs an iOS bundle with identity: ad hoc ("-") for the
// simulator; for devices with the provisioning profile embedded, and the
// entitlements of ios.entitlements, else the profile's, written in work.
func signIOS(c *Config, app, identity, work string) error {
	args := []string{"--force", "--sign", identity}
	if appex := iosWidgetsAppex(app); appex != "" {
		dir, err := os.MkdirTemp(work, "mygo-sign-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		entitlements, err := signIOSWidgets(c, app, appex, identity, dir)
		if err != nil {
			return err
		}
		if entitlements != "" {
			args = append(args, "--entitlements", entitlements)
		}
	}
	if identity != "-" {
		if err := copyFile(c.path(c.IOS.ProvisioningProfile), filepath.Join(app, "embedded.mobileprovision"), 0o644); err != nil {
			return fmt.Errorf("provisioning profile %s: %w", c.IOS.ProvisioningProfile, err)
		}
		entitlements := ""
		if c.IOS.Entitlements != "" {
			entitlements = c.path(c.IOS.Entitlements)
		} else {
			var err error
			if entitlements, err = profileEntitlements(c.path(c.IOS.ProvisioningProfile), work); err != nil {
				return err
			}
		}
		args = append(args, "--entitlements", entitlements, "--generate-entitlement-der")
	}
	out, err := exec.Command("codesign", append(args, app)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codesign %s: %v\n%s", filepath.Base(app), err, out)
	}
	return nil
}

// profileEntitlements writes the entitlements a provisioning profile
// grants, which devices check the app's against, into a plist in work.
func profileEntitlements(profile, work string) (string, error) {
	decoded, err := exec.Command("security", "cms", "-D", "-i", profile).Output()
	if err != nil {
		return "", fmt.Errorf("cannot read the provisioning profile %s: %v", profile, err)
	}
	plist := filepath.Join(work, "profile.plist")
	if err := os.WriteFile(plist, decoded, 0o644); err != nil {
		return "", err
	}
	out := filepath.Join(work, "entitlements.plist")
	if msg, err := exec.Command("plutil", "-extract", "Entitlements", "xml1", "-o", out, plist).CombinedOutput(); err != nil {
		return "", fmt.Errorf("the provisioning profile %s has no entitlements: %v\n%s", profile, err, msg)
	}
	return out, nil
}

// writeIPA writes the .ipa of an app for devices: a zip of Payload/ holding
// the bundle, as the App Store and device installers take it.
func writeIPA(app, ipa string) error {
	f, err := os.Create(ipa)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	base := filepath.Dir(app)
	err = filepath.WalkDir(app, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = "Payload/" + filepath.ToSlash(rel)
		if d.IsDir() {
			h.Name += "/"
			_, err = z.CreateHeader(h)
			return err
		}
		h.Method = zip.Deflate
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, target)
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
	if cerr := z.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// simulator returns the UDID of the simulator mygo dev -ios runs the app
// in: ios.simulator, by name or UDID, else the one booted, else the first
// iPhone; it boots it when it is not.
func simulator(c *Config) (string, error) {
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "available").Output()
	if err != nil {
		return "", fmt.Errorf("cannot list the simulators (is Xcode installed?): %v", err)
	}
	type device struct {
		name, udid string
		booted     bool
	}
	var devices []device
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		// iPhone 17 Pro (1B813EA7-0F2E-48DC-BE25-8FE2284A8050) (Shutdown)
		open := strings.LastIndex(line, " (")
		if open < 0 || !strings.HasSuffix(line, ")") {
			continue
		}
		state := line[open+2 : len(line)-1]
		rest := line[:open]
		o := strings.LastIndex(rest, " (")
		if o < 0 || !strings.HasSuffix(rest, ")") {
			continue
		}
		devices = append(devices, device{name: rest[:o], udid: rest[o+2 : len(rest)-1], booted: state == "Booted"})
	}
	var pick *device
	for i := range devices {
		d := &devices[i]
		switch want := c.IOS.Simulator; {
		case want != "":
			if d.name == want || d.udid == want {
				pick = d
			}
		case d.booted:
			pick = d
		case pick == nil && strings.HasPrefix(d.name, "iPhone"):
			pick = d
		}
		if pick != nil && (pick.booted || c.IOS.Simulator != "") {
			break
		}
	}
	if pick == nil {
		if c.IOS.Simulator != "" {
			return "", fmt.Errorf("no simulator %q: see xcrun simctl list devices", c.IOS.Simulator)
		}
		return "", errors.New("no iPhone simulator: add one in Xcode")
	}
	if !pick.booted {
		t := con.start("Booting " + pick.name)
		msg, err := exec.Command("xcrun", "simctl", "boot", pick.udid).CombinedOutput()
		if err != nil && !strings.Contains(string(msg), "current state: Booted") {
			return "", t.end(fmt.Errorf("xcrun simctl boot: %v\n%s", err, msg), "")
		}
		t.done("Booted " + pick.name)
		// The device shows in Simulator.app, when there is one.
		_ = exec.Command("open", "-g", "-a", "Simulator").Run()
	}
	return pick.udid, nil
}

// runIOSDev develops the app in a simulator: it builds the development
// app for it, installs it and launches it, its output in the console,
// then rebuilds and relaunches it as its code changes.
func runIOSDev(ctx context.Context, c *Config, env []string) error {
	dc := devConfig(c)
	t := iosTarget{simulator: true, arch: runtime.GOARCH}
	udid, err := simulator(c)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "mygo-ios-dev-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	dir := filepath.Join(c.root, ".mygo", "dev", t.dir())
	var app *exec.Cmd // the console of the app launched
	stop := func() {
		if app != nil {
			_ = exec.Command("xcrun", "simctl", "terminate", udid, dc.Identifier).Run()
			terminate(app)
			app = nil
		}
	}
	defer stop()
	build := func(why string) {
		title := "Building " + dc.Name + " for the simulator"
		if why != "" {
			title += " (" + why + ")"
		}
		tk := con.start(title)
		err := func() error {
			if out, err := writeI18n(c); err != nil {
				return err
			} else {
				for _, f := range out.written {
					tk.set("wrote " + f)
				}
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			bin := filepath.Join(work, dc.executableName())
			if err := compileIOS(ctx, dc, tk, t, bin, true, ""); err != nil {
				return err
			}
			path, err := writeIOSBundle(dc, dir, bin, t)
			if err != nil {
				return err
			}
			if len(dc.Widgets) > 0 {
				tk.set("compiling the widgets")
				if err := addIOSWidgets(dc, path, t); err != nil {
					return err
				}
			}
			if err := signIOS(dc, path, "-", ""); err != nil {
				return err
			}
			stop()
			if msg, err := exec.Command("xcrun", "simctl", "install", udid, path).CombinedOutput(); err != nil {
				return fmt.Errorf("xcrun simctl install: %v\n%s", err, msg)
			}
			return nil
		}()
		switch {
		case errors.Is(err, context.Canceled):
			tk.stop()
			return
		case err != nil:
			tk.fail()
			con.details(err)
			con.println("  " + dim("Save a fix to try again: the last build keeps running."))
			return
		}
		tk.done("Built " + dc.Name + " for the simulator")
		// The app's environment: simctl passes on the variables named
		// SIMCTL_CHILD_*.
		cmd := exec.Command("xcrun", "simctl", "launch", "--console-pty", "--terminate-running-process", udid, dc.Identifier)
		cmd.Env = os.Environ()
		for _, e := range env {
			cmd.Env = append(cmd.Env, "SIMCTL_CHILD_"+e)
		}
		setProcessGroup(cmd)
		out, errOut := labeledOutput(cmd, "app")
		if err := cmd.Start(); err != nil {
			warnf("cannot launch %s: %v", dc.Name, err)
			return
		}
		app = cmd
		go func() {
			_ = cmd.Wait()
			out.flush()
			errOut.flush()
		}()
	}

	w := &watcher{}
	if in, err := listBuildInputs(c); err == nil {
		w.set(in)
	} else {
		warnf("%v", err)
	}
	changes := w.watch(ctx, 250*time.Millisecond)
	keys := readKeys()
	build("")
	msg := "  " + cyan(con.sym.arrow) + " Watching for changes\n"
	if keys != nil {
		msg += "  " + cyan(con.sym.arrow) + " " + dim("press ") + bold("h + enter") + dim(" to show help") + "\n"
	}
	con.println(msg)
	for {
		select {
		case <-ctx.Done():
			return nil
		case paths := <-changes:
			build(changeSummary(c.root, paths))
			if c2, err := loadConfig(c.root); err == nil {
				if in, err := listBuildInputs(c2); err == nil {
					w.set(in)
				}
			}
		case key, ok := <-keys:
			switch {
			case !ok:
				keys = nil
			case key == "r":
				build("")
			case key == "c":
				con.clearScreen()
			case key == "q":
				return nil
			case key == "h":
				con.println(iosDevHelp())
			}
		}
	}
}

func iosDevHelp() string {
	var b strings.Builder
	b.WriteString("\n  " + bold("Shortcuts") + "\n")
	for _, k := range [][2]string{
		{"r", "rebuild and relaunch the app"},
		{"c", "clear the console"},
		{"q", "quit"},
	} {
		b.WriteString("  " + dim("press ") + bold(k[0]+" + enter") + dim(" to "+k[1]) + "\n")
	}
	return b.String()
}
