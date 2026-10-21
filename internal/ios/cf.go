//go:build ios

package ios

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	cfRunLoopGetMain      func() uintptr
	cfRunLoopSourceCreate func(alloc uintptr, order int, ctx unsafe.Pointer) uintptr
	cfRunLoopAddSource    func(rl, src, mode uintptr)
	cfRunLoopRunInMode    func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	cfRelease             func(obj uintptr)
	kCFRunLoopCommonModes uintptr
	kCFRunLoopDefaultMode uintptr

	uiApplicationMain func(argc int32, argv uintptr, principal, delegate id) int32
	exitFn            func(code int32)
)

// Called by address on every Signal and Wake.
var cfRunLoopSourceSignalFn, cfRunLoopWakeUpFn uintptr

func loadCF() {
	purego.RegisterLibFunc(&cfRunLoopGetMain, libCF, "CFRunLoopGetMain")
	purego.RegisterLibFunc(&cfRunLoopSourceCreate, libCF, "CFRunLoopSourceCreate")
	purego.RegisterLibFunc(&cfRunLoopAddSource, libCF, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&cfRunLoopRunInMode, libCF, "CFRunLoopRunInMode")
	cfRunLoopSourceSignalFn = mustDlsym(libCF, "CFRunLoopSourceSignal")
	cfRunLoopWakeUpFn = mustDlsym(libCF, "CFRunLoopWakeUp")
	purego.RegisterLibFunc(&cfRelease, libCF, "CFRelease")
	kCFRunLoopCommonModes = uintptr(constString(libCF, "kCFRunLoopCommonModes"))
	kCFRunLoopDefaultMode = uintptr(constString(libCF, "kCFRunLoopDefaultMode"))
	purego.RegisterLibFunc(&uiApplicationMain, libUIKit, "UIApplicationMain")
	purego.RegisterLibFunc(&exitFn, purego.RTLD_DEFAULT, "exit")
}
