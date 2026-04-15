//go:build darwin

package sensor

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	iokit uintptr
	cf    uintptr
)

var (
	ioServiceMatching              func(name *byte) uintptr
	ioServiceGetMatchingServices   func(mainPort uint32, matching uintptr, existing *uint32) int32
	ioIteratorNext                 func(iterator uint32) uint32
	ioObjectRelease                func(object uint32) int32
	ioRegistryEntryCreateCFProp    func(entry uint32, key uintptr, allocator uintptr, options uint32) uintptr
	ioRegistryEntrySetCFProp       func(entry uint32, key uintptr, value uintptr) int32
	ioHIDDeviceCreate              func(allocator uintptr, service uint32) uintptr
	ioHIDDeviceOpen                func(device uintptr, options int32) int32
	ioHIDDeviceRegisterInputReport func(device uintptr, report uintptr, reportLen int, callback uintptr, context uintptr)
	ioHIDDeviceScheduleWithRL      func(device uintptr, runLoop uintptr, mode uintptr)
)

var (
	cfStringCreateWithCString func(alloc uintptr, cStr *byte, encoding uint32) uintptr
	cfNumberCreate            func(alloc uintptr, theType int32, valuePtr uintptr) uintptr
	cfNumberGetValue          func(number uintptr, theType int32, valuePtr uintptr) bool
	cfRunLoopGetCurrent       func() uintptr
	cfRunLoopRunInMode        func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
)

var (
	kCFAllocatorDefault   uintptr
	kCFRunLoopDefaultMode uintptr
)

func init() {
	var err error
	iokit, err = purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_LAZY)
	if err != nil {
		panic(fmt.Sprintf("dlopen IOKit: %v", err))
	}
	cf, err = purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		panic(fmt.Sprintf("dlopen CoreFoundation: %v", err))
	}

	purego.RegisterLibFunc(&ioServiceMatching, iokit, "IOServiceMatching")
	purego.RegisterLibFunc(&ioServiceGetMatchingServices, iokit, "IOServiceGetMatchingServices")
	purego.RegisterLibFunc(&ioIteratorNext, iokit, "IOIteratorNext")
	purego.RegisterLibFunc(&ioObjectRelease, iokit, "IOObjectRelease")
	purego.RegisterLibFunc(&ioRegistryEntryCreateCFProp, iokit, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioRegistryEntrySetCFProp, iokit, "IORegistryEntrySetCFProperty")
	purego.RegisterLibFunc(&ioHIDDeviceCreate, iokit, "IOHIDDeviceCreate")
	purego.RegisterLibFunc(&ioHIDDeviceOpen, iokit, "IOHIDDeviceOpen")
	purego.RegisterLibFunc(&ioHIDDeviceRegisterInputReport, iokit, "IOHIDDeviceRegisterInputReportCallback")
	purego.RegisterLibFunc(&ioHIDDeviceScheduleWithRL, iokit, "IOHIDDeviceScheduleWithRunLoop")

	purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfNumberCreate, cf, "CFNumberCreate")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")

	kCFAllocatorDefault = derefSymbol(cf, "kCFAllocatorDefault")
	kCFRunLoopDefaultMode = derefSymbol(cf, "kCFRunLoopDefaultMode")
}

//go:nosplit
func derefSymbol(lib uintptr, name string) uintptr {
	sym, _ := purego.Dlsym(lib, name)
	if sym == 0 {
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&sym))
}

func cfStr(s string) uintptr {
	return cfStringCreateWithCString(0, cStr(s), CFStringEncodingUTF8)
}

func cfNum32(v int32) uintptr {
	return cfNumberCreate(0, CFNumberSInt32Type, uintptr(unsafe.Pointer(&v)))
}

func cStr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

func propInt(service uint32, key string) (int64, bool) {
	ref := ioRegistryEntryCreateCFProp(service, cfStr(key), 0, 0)
	if ref == 0 {
		return 0, false
	}
	var val int64
	if !cfNumberGetValue(ref, CFNumberSInt64Type, uintptr(unsafe.Pointer(&val))) {
		return 0, false
	}
	return val, true
}

func ParseIMUReport(data []byte) (x, y, z int32) {
	if len(data) < IMUDataOffset+12 {
		return 0, 0, 0
	}
	off := IMUDataOffset
	x = int32(binary.LittleEndian.Uint32(data[off:]))
	y = int32(binary.LittleEndian.Uint32(data[off+4:]))
	z = int32(binary.LittleEndian.Uint32(data[off+8:]))
	return x, y, z
}
