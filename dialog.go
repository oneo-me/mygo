package mygo

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/internal/platform"
)

// DialogModule shows native dialogs. Its methods block until the dialog is
// dismissed; they can be called from any goroutine.
type DialogModule struct{}

// Dialog shows native file and message dialogs.
var Dialog DialogModule

// FileFilter limits the files a file dialog shows, e.g.
// {Name: "Images", Extensions: []string{"png", "jpg"}}.
type FileFilter struct {
	Name       string
	Extensions []string
}

// OpenDialogOptions configures Dialog.Open.
type OpenDialogOptions struct {
	// Parent attaches the dialog to a window (a sheet on macOS).
	Parent      *Window
	Title       string
	DefaultPath string
	// ButtonLabel replaces the label of the confirm button.
	ButtonLabel string
	// Message is shown above the file list (macOS).
	Message string
	Filters []FileFilter
	// Directory selects directories instead of files.
	Directory bool
	// Multiple allows selecting several entries.
	Multiple        bool
	ShowHiddenFiles bool
	// CreateDirectories shows a "New Folder" button (macOS).
	CreateDirectories bool
	// TreatPackagesAsDirectories lets the user browse into packages such as
	// .app bundles (macOS).
	TreatPackagesAsDirectories bool
}

// SaveDialogOptions configures Dialog.Save.
type SaveDialogOptions struct {
	Parent      *Window
	Title       string
	DefaultPath string
	ButtonLabel string
	Message     string
	// NameFieldLabel replaces the label in front of the file name field
	// (macOS).
	NameFieldLabel             string
	Filters                    []FileFilter
	ShowHiddenFiles            bool
	CreateDirectories          bool
	TreatPackagesAsDirectories bool
}

// PhotoKind selects what Dialog.OpenPhotos picks.
type PhotoKind int

// The kinds of media Dialog.OpenPhotos picks.
const (
	// PhotoImages picks images, Live Photos as their still images.
	PhotoImages PhotoKind = iota
	// PhotoVideos picks videos.
	PhotoVideos
	// PhotoImagesAndVideos picks either.
	PhotoImagesAndVideos
)

// PhotoDialogOptions configures Dialog.OpenPhotos.
type PhotoDialogOptions struct {
	// Parent attaches the dialog to a window, as Dialog.Open's.
	Parent *Window
	// Title is the title of the file dialog that stands in for the
	// photo library where there is none.
	Title string
	// Kind is what the user picks: images by default.
	Kind PhotoKind
	// Multiple allows picking several, up to Limit when it is above zero.
	Multiple bool
	Limit    int
	// Compatible asks the photo library for JPEG images and H.264
	// videos in place of the HEIF and HEVC it may keep, converting them
	// (iOS). Without it, the files are the originals.
	Compatible bool
}

// MessageType selects the icon of a message dialog.
type MessageType string

// Message dialog types.
const (
	MessageNone     MessageType = "none"
	MessageInfo     MessageType = "info"
	MessageWarning  MessageType = "warning"
	MessageError    MessageType = "error"
	MessageQuestion MessageType = "question"
)

// MessageOptions configures Dialog.Message.
type MessageOptions struct {
	Parent  *Window
	Type    MessageType
	Title   string
	Message string
	// Detail is shown below Message in a smaller font.
	Detail string
	// Buttons defaults to a single "OK" button.
	Buttons []string
	// DefaultButton is the index of the button activated with Enter.
	DefaultButton int
	// CancelButton is the index of the button activated with Escape. When
	// zero, the first button labeled "Cancel" or "No" is used.
	CancelButton    int
	CheckboxLabel   string
	CheckboxChecked bool
}

// MessageResult is the outcome of Dialog.Message.
type MessageResult struct {
	// Button is the index of the clicked button.
	Button int
	// CheckboxChecked is the final state of the checkbox.
	CheckboxChecked bool
}

func (w *Window) nativeOrNil() platform.Window {
	if w == nil {
		return nil
	}
	return w.native
}

func toFilters(fs []FileFilter) []platform.FileFilter {
	out := make([]platform.FileFilter, len(fs))
	for i, f := range fs {
		out[i] = platform.FileFilter(f)
	}
	return out
}

// Open shows a dialog to pick files or directories and returns the chosen
// paths, or nil when canceled. On iOS it is the document picker of the
// Files app, which gives the app copies of the files picked, in its
// temporary directory; a directory picked is the user's own, which the
// app may read and write while it runs. The options that iOS's picker
// has nothing for, as titles and labels, do nothing there.
func (DialogModule) Open(opts OpenDialogOptions) ([]string, error) {
	needsApp("Dialog.Open")
	type result struct {
		paths []string
		err   error
	}
	ch := make(chan result, 1)
	popts := &platform.OpenDialogOptions{
		Title:                      opts.Title,
		DefaultPath:                opts.DefaultPath,
		ButtonLabel:                opts.ButtonLabel,
		Message:                    opts.Message,
		Filters:                    toFilters(opts.Filters),
		OpenFiles:                  !opts.Directory,
		OpenDirectories:            opts.Directory,
		Multiple:                   opts.Multiple,
		ShowHiddenFiles:            opts.ShowHiddenFiles,
		CreateDirectories:          opts.CreateDirectories,
		TreatPackagesAsDirectories: opts.TreatPackagesAsDirectories,
	}
	if !postMain(func() {
		backend().Dialogs().ShowOpenDialog(opts.Parent.nativeOrNil(), popts, func(paths []string, err error) {
			deliver(ch, result{paths, err})
		})
	}) {
		return nil, errLoopStopped
	}
	r := await(ch)
	return r.paths, r.err
}

// OpenPhotos shows a dialog to pick images or videos from the user's photo
// library and returns the paths of copies of them in the temporary
// directory, which the app owns, or nil when canceled. On iOS it is the
// Photos picker, which needs no permission to read the library: the app
// sees only what the user picks. Elsewhere it is the file dialog of
// Dialog.Open, showing images, videos or both, whose paths are the
// files themselves.
func (DialogModule) OpenPhotos(opts PhotoDialogOptions) ([]string, error) {
	needsApp("Dialog.OpenPhotos")
	type result struct {
		paths []string
		err   error
	}
	limit := 1
	if opts.Multiple {
		limit = max(opts.Limit, 0)
	}
	picker := &platform.PhotoPickerOptions{
		Images:     opts.Kind != PhotoVideos,
		Videos:     opts.Kind != PhotoImages,
		Limit:      limit,
		Compatible: opts.Compatible,
	}
	ch := make(chan result, 1)
	done := func(paths []string, err error) {
		if limit > 0 && len(paths) > limit {
			paths = paths[:limit] // a file dialog takes any number
		}
		deliver(ch, result{paths, err})
	}
	if !postMain(func() {
		d := backend().Dialogs()
		if p, ok := d.(platform.PhotoPicker); ok {
			p.ShowPhotoPicker(opts.Parent.nativeOrNil(), picker, done)
			return
		}
		d.ShowOpenDialog(opts.Parent.nativeOrNil(), &platform.OpenDialogOptions{
			Title:     opts.Title,
			Filters:   []platform.FileFilter{photoFilter(opts.Kind)},
			OpenFiles: true,
			Multiple:  opts.Multiple,
		}, done)
	}) {
		return nil, errLoopStopped
	}
	r := await(ch)
	return r.paths, r.err
}

// The extensions of the images and videos of Dialog.OpenPhotos's file
// dialog: those photo libraries and cameras keep, and the web's.
var (
	imageExtensions = []string{"jpg", "jpeg", "png", "heic", "heif", "gif", "webp", "avif", "tif", "tiff", "bmp"}
	videoExtensions = []string{"mov", "mp4", "m4v", "webm", "mkv", "avi", "3gp"}
)

// photoFilter is the filter of the file dialog that stands in for the
// photo library.
func photoFilter(kind PhotoKind) platform.FileFilter {
	tx := menuMessages()
	switch kind {
	case PhotoVideos:
		return platform.FileFilter{Name: tx.Videos(), Extensions: videoExtensions}
	case PhotoImagesAndVideos:
		return platform.FileFilter{Name: tx.ImagesAndVideos(), Extensions: append(slices.Clone(imageExtensions), videoExtensions...)}
	}
	return platform.FileFilter{Name: tx.Images(), Extensions: imageExtensions}
}

// Save shows a dialog to choose where to save a file and returns the path,
// or "" when canceled. On iOS the document picker of the Files app moves
// an empty file named after DefaultPath where the user chooses, which the
// app then writes, while it runs.
func (DialogModule) Save(opts SaveDialogOptions) (string, error) {
	needsApp("Dialog.Save")
	type result struct {
		path string
		err  error
	}
	ch := make(chan result, 1)
	popts := &platform.SaveDialogOptions{
		Title:                      opts.Title,
		DefaultPath:                opts.DefaultPath,
		ButtonLabel:                opts.ButtonLabel,
		Message:                    opts.Message,
		NameFieldLabel:             opts.NameFieldLabel,
		Filters:                    toFilters(opts.Filters),
		ShowHiddenFiles:            opts.ShowHiddenFiles,
		CreateDirectories:          opts.CreateDirectories,
		TreatPackagesAsDirectories: opts.TreatPackagesAsDirectories,
		Untitled:                   menuMessages().Untitled(),
	}
	if !postMain(func() {
		backend().Dialogs().ShowSaveDialog(opts.Parent.nativeOrNil(), popts, func(path string, err error) {
			deliver(ch, result{path, err})
		})
	}) {
		return "", errLoopStopped
	}
	r := await(ch)
	return r.path, r.err
}

// Message shows a message dialog and reports which button was clicked.
func (DialogModule) Message(opts MessageOptions) (MessageResult, error) {
	needsApp("Dialog.Message")
	type result struct {
		res MessageResult
		err error
	}
	buttons := opts.Buttons
	if len(buttons) == 0 {
		buttons = []string{menuMessages().Ok()}
	}
	cancel := opts.CancelButton
	if cancel == 0 {
		for i, b := range buttons {
			if l := strings.ToLower(b); l == "cancel" || l == "no" || b == menuMessages().Cancel() {
				cancel = i
				break
			}
		}
	}
	popts := &platform.MessageBoxOptions{
		Type:            string(opts.Type),
		Title:           opts.Title,
		Message:         opts.Message,
		Detail:          opts.Detail,
		Buttons:         buttons,
		DefaultID:       opts.DefaultButton,
		CancelID:        cancel,
		CheckboxLabel:   opts.CheckboxLabel,
		CheckboxChecked: opts.CheckboxChecked,
	}
	ch := make(chan result, 1)
	if !postMain(func() {
		backend().Dialogs().ShowMessageBox(opts.Parent.nativeOrNil(), popts, func(r platform.MessageBoxResult, err error) {
			button := r.Response
			if button < 0 || button >= len(buttons) {
				button = cancel // dismissed without a button, e.g. when quitting
			}
			deliver(ch, result{MessageResult{Button: button, CheckboxChecked: r.CheckboxChecked}, err})
		})
	}) {
		return MessageResult{}, errLoopStopped
	}
	r := await(ch)
	return r.res, r.err
}

// Error shows a modal error dialog.
func (d DialogModule) Error(title, content string) {
	needsApp("Dialog.Error")
	_, _ = d.Message(MessageOptions{Type: MessageError, Message: title, Detail: content})
}
