//go:build ios

package ios

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// File dialogs are the Files app's document picker
// (UIDocumentPickerViewController), and the photo library's picker a
// PHPickerViewController, presented by the window in front. One delegate,
// made with the first picker, answers them all, by the picker it is told
// about.

var errNoWindow = errors.New("mygo: no window shows to present the dialog from")

// pickerDelegate returns the delegate of the pickers, registering its
// class the first time: apps that pick nothing make no callbacks for it.
func (b *Backend) pickerDelegate() id {
	if b.pickers != 0 {
		return b.pickers
	}
	classDef("MyGoPickerDelegate", "NSObject", []string{"UIDocumentPickerDelegate", "PHPickerViewControllerDelegate"}, nil, []objc.MethodDef{
		method("documentPicker:didPickDocumentsAtURLs:", func(self id, _ objc.SEL, picker, urls id) {
			theBackend.picked(picker, urls)
		}),
		method("documentPickerWasCancelled:", func(self id, _ objc.SEL, picker id) {
			theBackend.picked(picker, 0)
		}),
		// The Photos picker reports a cancel as nothing picked, and leaves
		// it to its delegate to dismiss it.
		method("picker:didFinishPicking:", func(self id, _ objc.SEL, picker, results id) {
			send(picker, "dismissViewControllerAnimated:completion:", 1, 0)
			theBackend.picked(picker, results)
		}),
	})
	b.pickers = alloc("MyGoPickerDelegate")
	return b.pickers
}

// present shows a picker from the window in front, which calls done once
// with the array of what the user picked, 0 when canceled. It keeps the
// picker until then. It reports false when no window shows.
func (b *Backend) present(picker id, done func(picked id)) bool {
	vc := b.presenter()
	if vc == 0 {
		return false
	}
	b.byPicker[retain(picker)] = done
	send(picker, "setDelegate:", uintptr(b.pickerDelegate()))
	send(vc, "presentViewController:animated:completion:", uintptr(picker), 1, 0)
	return true
}

// picked hands what a picker picked to its caller, once.
func (b *Backend) picked(picker, picked id) {
	done, ok := b.byPicker[picker]
	if !ok {
		return
	}
	delete(b.byPicker, picker)
	done(picked)
	release(picker)
}

// utType returns the UTType of an identifier, as "public.folder".
func utType(identifier string) id {
	return send(class("UTType"), "typeWithIdentifier:", uintptr(nsString(identifier)))
}

// contentTypes returns the UTTypes of the files of the filters, any item
// without one, or for "*".
func contentTypes(filters []platform.FileFilter) id {
	var types []id
	for _, f := range filters {
		for _, ext := range f.Extensions {
			ext = strings.TrimPrefix(ext, ".")
			if ext == "" || ext == "*" {
				return nsArray(utType("public.item"))
			}
			if t := send(class("UTType"), "typeWithFilenameExtension:", uintptr(nsString(ext))); t != 0 {
				types = append(types, t)
			}
		}
	}
	if len(types) == 0 {
		types = append(types, utType("public.item"))
	}
	return nsArray(types...)
}

// pickedPaths returns the paths of an array of file URLs. Those the app
// does not own it accesses until it exits, as it may write them any time:
// a directory the user picked, a file it exports.
func pickedPaths(urls id, scoped bool) []string {
	var paths []string
	for _, u := range arrayItems(urls) {
		if scoped {
			sendBool(u, "startAccessingSecurityScopedResource")
		}
		paths = append(paths, goString(send(u, "path")))
	}
	return paths
}

// ShowOpenDialog picks files, of which the app gets copies, or a
// directory, which stays the user's own.
func (d dialogs) ShowOpenDialog(_ platform.Window, o *platform.OpenDialogOptions, cb func([]string, error)) {
	cls := class("UIDocumentPickerViewController")
	if !sendBool(cls, "instancesRespondToSelector:", uintptr(sel("initForOpeningContentTypes:asCopy:"))) {
		cb(nil, errUnsupported)
		return
	}
	dir := o.OpenDirectories
	var shown bool
	withPool(func() {
		types := contentTypes(o.Filters)
		if dir {
			types = nsArray(utType("public.folder"))
		}
		picker := send(send(cls, "alloc"), "initForOpeningContentTypes:asCopy:", uintptr(types), boolArg(!dir))
		defer release(picker)
		send(picker, "setAllowsMultipleSelection:", boolArg(o.Multiple))
		send(picker, "setShouldShowFileExtensions:", 1)
		if start := startDirectory(o.DefaultPath); start != "" {
			send(picker, "setDirectoryURL:", uintptr(fileURL(start)))
		}
		shown = d.b.present(picker, func(urls id) {
			var paths []string
			withPool(func() { paths = pickedPaths(urls, dir) })
			cb(paths, nil)
		})
	})
	if !shown {
		cb(nil, errNoWindow)
	}
}

// startDirectory returns the directory a picker starts in for a default
// path: the path when it is a directory, else the directory holding it,
// "" when neither exists.
func startDirectory(path string) string {
	if path == "" {
		return ""
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return path
	}
	if fi, err := os.Stat(filepath.Dir(path)); err == nil && fi.IsDir() {
		return filepath.Dir(path)
	}
	return ""
}

// saveName is the name of the file a save dialog places: DefaultPath's,
// else the untitled name, with the first extension of the filters when
// it has none.
func saveName(o *platform.SaveDialogOptions) string {
	name := filepath.Base(o.DefaultPath)
	if o.DefaultPath == "" || name == "." || name == string(filepath.Separator) {
		name = o.Untitled
		if name == "" {
			name = "Untitled"
		}
	}
	if filepath.Ext(name) == "" {
		for _, f := range o.Filters {
			if len(f.Extensions) > 0 {
				if ext := strings.TrimPrefix(f.Extensions[0], "."); ext != "" && ext != "*" {
					name += "." + ext
				}
				break
			}
		}
	}
	return name
}

// ShowSaveDialog exports an empty file of the name chosen where the user
// picks, moving it there, and returns its new path, which the app writes:
// iOS's pickers choose places for files that exist.
func (d dialogs) ShowSaveDialog(_ platform.Window, o *platform.SaveDialogOptions, cb func(string, error)) {
	cls := class("UIDocumentPickerViewController")
	if !sendBool(cls, "instancesRespondToSelector:", uintptr(sel("initForExportingURLs:asCopy:"))) {
		cb("", errUnsupported)
		return
	}
	tmp, err := os.MkdirTemp("", "save-")
	if err != nil {
		cb("", err)
		return
	}
	file := filepath.Join(tmp, saveName(o))
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		os.RemoveAll(tmp)
		cb("", err)
		return
	}
	var shown bool
	withPool(func() {
		picker := send(send(cls, "alloc"), "initForExportingURLs:asCopy:", uintptr(nsArray(fileURL(file))), 0)
		defer release(picker)
		send(picker, "setShouldShowFileExtensions:", 1)
		if start := startDirectory(o.DefaultPath); start != "" {
			send(picker, "setDirectoryURL:", uintptr(fileURL(start)))
		}
		shown = d.b.present(picker, func(urls id) {
			os.RemoveAll(tmp) // the file moved out, or the user canceled
			var paths []string
			withPool(func() { paths = pickedPaths(urls, true) })
			if len(paths) == 0 {
				cb("", nil)
				return
			}
			cb(paths[0], nil)
		})
	})
	if !shown {
		os.RemoveAll(tmp)
		cb("", errNoWindow)
	}
}

var photosUI struct {
	once sync.Once
	ok   bool
}

// loadPhotosUI loads PhotosUI, the Photos picker's framework, the first
// time a picker shows, and reports whether the system has it (iOS 14).
func loadPhotosUI() bool {
	photosUI.once.Do(func() {
		_, err := purego.Dlopen("/System/Library/Frameworks/PhotosUI.framework/PhotosUI", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		photosUI.ok = err == nil && hasClass("PHPickerViewController")
	})
	return photosUI.ok
}

// The modes of PHPickerConfiguration.
const (
	phPickerRepresentationCurrent    = 1
	phPickerRepresentationCompatible = 2
	phPickerSelectionOrdered         = 1
)

// ShowPhotoPicker shows the Photos picker, then copies what the user
// picked into the temporary directory, off the main thread, as the
// library may have to download it first.
func (d dialogs) ShowPhotoPicker(_ platform.Window, o *platform.PhotoPickerOptions, cb func([]string, error)) {
	if !loadPhotosUI() {
		cb(nil, errUnsupported)
		return
	}
	var shown bool
	withPool(func() {
		cfg := alloc("PHPickerConfiguration")
		defer release(cfg)
		send(cfg, "setSelectionLimit:", uintptr(o.Limit))
		if o.Limit != 1 && respondsTo(cfg, "setSelection:") {
			send(cfg, "setSelection:", phPickerSelectionOrdered) // iOS 15
		}
		filters := class("PHPickerFilter")
		switch {
		case o.Images && o.Videos:
			send(cfg, "setFilter:", uintptr(send(filters, "anyFilterMatchingSubfilters:",
				uintptr(nsArray(send(filters, "imagesFilter"), send(filters, "videosFilter"))))))
		case o.Videos:
			send(cfg, "setFilter:", uintptr(send(filters, "videosFilter")))
		default:
			send(cfg, "setFilter:", uintptr(send(filters, "imagesFilter")))
		}
		mode := phPickerRepresentationCurrent
		if o.Compatible {
			mode = phPickerRepresentationCompatible
		}
		send(cfg, "setPreferredAssetRepresentationMode:", uintptr(mode))
		picker := send(send(class("PHPickerViewController"), "alloc"), "initWithConfiguration:", uintptr(cfg))
		defer release(picker)
		shown = d.b.present(picker, func(results id) {
			d.b.copyPhotos(results, o, cb)
		})
	})
	if !shown {
		cb(nil, errNoWindow)
	}
}

// copyPhotos copies the files of the Photos picker's results into the
// temporary directory and calls cb on the main thread with their paths,
// in the order picked, and the first error, once every copy is done.
func (b *Backend) copyPhotos(results id, o *platform.PhotoPickerOptions, cb func([]string, error)) {
	items := arrayItems(results)
	if len(items) == 0 {
		cb(nil, nil)
		return
	}
	var (
		mu      sync.Mutex
		paths   = make([]string, len(items))
		errs    = make([]error, len(items))
		pending = len(items)
	)
	finish := func(i int, path string, err error) {
		mu.Lock()
		paths[i], errs[i] = path, err
		pending--
		last := pending == 0
		mu.Unlock()
		if !last {
			return
		}
		b.post(func() {
			var out []string
			var first error
			for i, p := range paths {
				if p != "" {
					out = append(out, p)
				} else if first == nil {
					first = errs[i]
				}
			}
			cb(out, first)
		})
	}
	for i, r := range items {
		provider := send(r, "itemProvider")
		typ := ""
		switch {
		case o.Videos && sendBool(provider, "hasItemConformingToTypeIdentifier:", uintptr(nsString("public.movie"))):
			typ = "public.movie"
		case o.Images && sendBool(provider, "hasItemConformingToTypeIdentifier:", uintptr(nsString("public.image"))):
			typ = "public.image"
		default:
			finish(i, "", errors.New("mygo: the photo library has no file for an item picked"))
			continue
		}
		name := goString(send(provider, "suggestedName"))
		// The file exists until the handler returns, on a queue of the
		// system's.
		blk := newBlock(func(_ objc.Block, url, nsErr id) {
			if url == 0 {
				finish(i, "", nsError(nsErr))
				return
			}
			path, err := copyPhoto(url, name)
			finish(i, path, err)
		})
		send(provider, "loadFileRepresentationForTypeIdentifier:completionHandler:", uintptr(nsString(typ)), uintptr(blk))
		blk.Release()
	}
}

// copyPhoto copies the file at url into a directory of its own in the
// temporary directory, named as the library suggests with the file's
// extension, and returns its path.
func copyPhoto(url id, suggested string) (string, error) {
	var path string
	var err error
	withPool(func() {
		src := goString(send(url, "path"))
		name := filepath.Base(src)
		if suggested = strings.ReplaceAll(suggested, "/", "-"); suggested != "" {
			name = suggested
			if filepath.Ext(name) == "" {
				name += filepath.Ext(src)
			}
		}
		dir, e := os.MkdirTemp("", "photo-")
		if e != nil {
			err = e
			return
		}
		path = filepath.Join(dir, name)
		var nsErr id
		fm := send(class("NSFileManager"), "defaultManager")
		if !sendBool(fm, "copyItemAtURL:toURL:error:", uintptr(url), uintptr(fileURL(path)), uintptr(unsafe.Pointer(&nsErr))) {
			os.RemoveAll(dir)
			path, err = "", nsError(nsErr)
		}
	})
	return path, err
}
