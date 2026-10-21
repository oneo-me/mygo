//go:build !js

package mygo

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// A backend without a photo library picks photos with its file dialog,
// of the kind's files, keeping no more than the limit.
func TestOpenPhotosFileDialog(t *testing.T) {
	var got *platform.OpenDialogOptions
	onMain(func() {
		fb.OnOpenDialog = func(o *platform.OpenDialogOptions) []string {
			got = o
			return []string{"/a.jpg", "/b.png", "/c.heic"}
		}
	})
	t.Cleanup(func() { onMain(func() { fb.OnOpenDialog = nil }) })

	paths, err := Dialog.OpenPhotos(PhotoDialogOptions{Title: "Avatar"})
	if err != nil || !slices.Equal(paths, []string{"/a.jpg"}) {
		t.Errorf("OpenPhotos = %v, %v, want the first path alone", paths, err)
	}
	if got.Title != "Avatar" || !got.OpenFiles || got.OpenDirectories || got.Multiple || len(got.Filters) != 1 {
		t.Fatalf("file dialog options = %+v", got)
	}
	if f := got.Filters[0]; f.Name != "Images" || !slices.Contains(f.Extensions, "heic") || slices.Contains(f.Extensions, "mov") {
		t.Errorf("images filter = %+v", f)
	}

	paths, _ = Dialog.OpenPhotos(PhotoDialogOptions{Kind: PhotoVideos, Multiple: true, Limit: 2})
	if !slices.Equal(paths, []string{"/a.jpg", "/b.png"}) {
		t.Errorf("OpenPhotos with Limit 2 = %v", paths)
	}
	if f := got.Filters[0]; !got.Multiple || f.Name != "Videos" || !slices.Contains(f.Extensions, "mov") || slices.Contains(f.Extensions, "jpg") {
		t.Errorf("videos dialog = %+v", got)
	}

	paths, _ = Dialog.OpenPhotos(PhotoDialogOptions{Kind: PhotoImagesAndVideos, Multiple: true})
	if len(paths) != 3 {
		t.Errorf("OpenPhotos without a limit = %v", paths)
	}
	if f := got.Filters[0]; f.Name != "Images and Videos" || !slices.Contains(f.Extensions, "mov") || !slices.Contains(f.Extensions, "jpg") {
		t.Errorf("images and videos filter = %+v", f)
	}
	if f := photoFilter(PhotoImagesAndVideos); len(f.Extensions) != len(imageExtensions)+len(videoExtensions) || len(imageExtensions) != cap(imageExtensions) {
		t.Errorf("photoFilter changed the extensions: %+v", f)
	}
}

// A backend with a photo library shows its picker, with the options it
// needs.
func TestOpenPhotosPicker(t *testing.T) {
	var got []platform.PhotoPickerOptions
	onMain(func() {
		fb.OnOpenDialog = func(*platform.OpenDialogOptions) []string {
			t.Error("OpenPhotos showed the file dialog of a backend with a photo picker")
			return nil
		}
		fb.OnPhotoPicker = func(o *platform.PhotoPickerOptions) []string {
			got = append(got, *o)
			return []string{"/tmp/photo-1/IMG_0001.HEIC"}
		}
	})
	t.Cleanup(func() { onMain(func() { fb.OnOpenDialog, fb.OnPhotoPicker = nil, nil }) })

	for _, c := range []struct {
		opts PhotoDialogOptions
		want platform.PhotoPickerOptions
	}{
		{PhotoDialogOptions{}, platform.PhotoPickerOptions{Images: true, Limit: 1}},
		{PhotoDialogOptions{Limit: 5}, platform.PhotoPickerOptions{Images: true, Limit: 1}},
		{PhotoDialogOptions{Kind: PhotoVideos, Multiple: true}, platform.PhotoPickerOptions{Videos: true}},
		{PhotoDialogOptions{Kind: PhotoImagesAndVideos, Multiple: true, Limit: 3, Compatible: true}, platform.PhotoPickerOptions{Images: true, Videos: true, Limit: 3, Compatible: true}},
		{PhotoDialogOptions{Multiple: true, Limit: -1}, platform.PhotoPickerOptions{Images: true}},
	} {
		got = nil
		paths, err := Dialog.OpenPhotos(c.opts)
		if err != nil || len(paths) != 1 {
			t.Errorf("OpenPhotos(%+v) = %v, %v", c.opts, paths, err)
		}
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("OpenPhotos(%+v) asked the picker for %+v, want %+v", c.opts, got, c.want)
		}
	}
}

// Save names a file without a default path, for the backends that need
// one, in the app's language.
func TestSaveUntitled(t *testing.T) {
	var got *platform.SaveDialogOptions
	onMain(func() {
		fb.OnSaveDialog = func(o *platform.SaveDialogOptions) string {
			got = o
			return "/notes.txt"
		}
	})
	t.Cleanup(func() { onMain(func() { fb.OnSaveDialog = nil }) })
	if path, err := Dialog.Save(SaveDialogOptions{}); err != nil || path != "/notes.txt" {
		t.Errorf("Save = %q, %v", path, err)
	}
	if got.Untitled != "Untitled" {
		t.Errorf("Untitled = %q, want Untitled", got.Untitled)
	}
}
