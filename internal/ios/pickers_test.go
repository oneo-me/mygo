//go:build ios

package ios

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/wait"
)

func TestSaveName(t *testing.T) {
	for _, c := range []struct {
		opts platform.SaveDialogOptions
		want string
	}{
		{platform.SaveDialogOptions{DefaultPath: "/docs/notes.txt"}, "notes.txt"},
		{platform.SaveDialogOptions{DefaultPath: "notes", Filters: []platform.FileFilter{{Extensions: []string{".md", "txt"}}}}, "notes.md"},
		{platform.SaveDialogOptions{Untitled: "未命名", Filters: []platform.FileFilter{{Extensions: []string{"*"}}}}, "未命名"},
		{platform.SaveDialogOptions{Filters: []platform.FileFilter{{Name: "Any"}, {Extensions: []string{"json"}}}}, "Untitled.json"},
		{platform.SaveDialogOptions{Filters: []platform.FileFilter{{Extensions: []string{"*"}}, {Extensions: []string{"json"}}}}, "Untitled"},
		{platform.SaveDialogOptions{Untitled: "Untitled", Filters: []platform.FileFilter{{Extensions: []string{"json"}}}}, "Untitled.json"},
	} {
		if got := saveName(&c.opts); got != c.want {
			t.Errorf("saveName(%+v) = %q, want %q", c.opts, got, c.want)
		}
	}
}

func TestStartDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	for path, want := range map[string]string{
		"":                                 "",
		dir:                                dir,
		file:                               dir, // need not exist
		filepath.Join(dir, "missing", "b"): "",
	} {
		if got := startDirectory(path); got != want {
			t.Errorf("startDirectory(%q) = %q, want %q", path, got, want)
		}
	}
}

// pickerResults are what testResult objects stand for: the Photos
// picker's results, of which copyPhotos reads the item providers.
var pickerResults = map[id]id{}

func testResult(provider id) id {
	if !hasClass("MyGoTestPickerResult") {
		classDef("MyGoTestPickerResult", "NSObject", nil, nil, []objc.MethodDef{
			method("itemProvider", func(self id, _ objc.SEL) id { return pickerResults[self] }),
		})
	}
	r := alloc("MyGoTestPickerResult")
	pickerResults[r] = provider
	return r
}

// The Photos picker's results are copied out of their item providers on
// the system's queues, kept in the order picked; what is not of the kinds
// asked for is reported, and the rest still copied.
func TestCopyPhotos(t *testing.T) {
	b := New()
	theBackend = b
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	png := []byte("\x89PNG\r\n\x1a\n not really")
	mov := []byte("not really a movie")
	var results id
	withPool(func() {
		// The results keep their providers, as the picker's do, which
		// suggest the names of the assets without their extensions.
		provider := func(path, suggested string) id {
			p := send(send(class("NSItemProvider"), "alloc"), "initWithContentsOfURL:", uintptr(fileURL(path)))
			send(p, "setSuggestedName:", uintptr(nsString(suggested)))
			return p
		}
		results = retain(nsArray(
			testResult(provider(write("a.png", png), "IMG_0002")),
			testResult(provider(write("b.mov", mov), "Clip")),
			testResult(provider(write("c.png", png), "IMG_0002")),
		))
	})
	defer release(results)

	type answer struct {
		paths []string
		err   error
	}
	pick := func(o platform.PhotoPickerOptions) answer {
		var got *answer
		b.copyPhotos(results, &o, func(paths []string, err error) { got = &answer{paths, err} })
		// The answer comes on the main thread, whose queue the test drains.
		wait.Until(t, "the photos to be copied", func() bool {
			drainMainQueue()
			return got != nil
		})
		return *got
	}
	names := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			out = append(out, filepath.Base(p))
			if !strings.HasPrefix(p, filepath.Clean(os.TempDir())+"/") {
				t.Errorf("%s is not in the temporary directory", p)
			}
			t.Cleanup(func() { os.RemoveAll(filepath.Dir(p)) })
		}
		return out
	}

	got := pick(platform.PhotoPickerOptions{Images: true, Videos: true})
	if got.err != nil || !slices.Equal(names(got.paths), []string{"IMG_0002.png", "Clip.mov", "IMG_0002.png"}) {
		t.Fatalf("copyPhotos of images and videos = %q, %v", got.paths, got.err)
	}
	for i, want := range [][]byte{png, mov, png} {
		if data, err := os.ReadFile(got.paths[i]); err != nil || !bytes.Equal(data, want) {
			t.Errorf("%s holds %q, %v", got.paths[i], data, err)
		}
	}

	got = pick(platform.PhotoPickerOptions{Images: true})
	if got.err == nil || !slices.Equal(names(got.paths), []string{"IMG_0002.png", "IMG_0002.png"}) {
		t.Errorf("copyPhotos of images = %q, %v, want both images and an error for the movie", got.paths, got.err)
	}

	got = pick(platform.PhotoPickerOptions{Images: true, Videos: true})
	if names(got.paths); len(got.paths) != 3 || got.paths[0] == got.paths[2] {
		t.Errorf("copies of files of the same name share a path: %q", got.paths)
	}

	var canceled bool
	withPool(func() {
		b.copyPhotos(nsArray(), &platform.PhotoPickerOptions{Images: true}, func(paths []string, err error) {
			canceled = paths == nil && err == nil
		})
	})
	if !canceled {
		t.Error("copyPhotos of no results did not answer nothing at once")
	}
}
