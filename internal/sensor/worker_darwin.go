//go:build darwin

package sensor

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/Gojaehyeon/seismo/internal/shm"
)

type Config struct {
	AccelRing *shm.RingBuffer
	GyroRing  *shm.RingBuffer
	Restarts  uint32
}

type callbackState struct {
	accelRing *shm.RingBuffer
	gyroRing  *shm.RingBuffer
	accelDec  int
	gyroDec   int
}

var globalState *callbackState

var (
	accelCallbackPtr uintptr
	gyroCallbackPtr  uintptr
)

func init() {
	accelCallbackPtr = purego.NewCallback(accelCallback)
	gyroCallbackPtr = purego.NewCallback(gyroCallback)
}

func accelCallback(_ uintptr, _ int32, _ uintptr, _ int32, _ uint32, report *byte, length int) {
	if globalState == nil || globalState.accelRing == nil || length != IMUReportLen {
		return
	}
	globalState.accelDec++
	if globalState.accelDec < IMUDecimation {
		return
	}
	globalState.accelDec = 0
	data := unsafe.Slice(report, length)
	x, y, z := ParseIMUReport(data)
	globalState.accelRing.WriteSample(x, y, z)
}

func gyroCallback(_ uintptr, _ int32, _ uintptr, _ int32, _ uint32, report *byte, length int) {
	if globalState == nil || globalState.gyroRing == nil || length != IMUReportLen {
		return
	}
	globalState.gyroDec++
	if globalState.gyroDec < IMUDecimation {
		return
	}
	globalState.gyroDec = 0
	data := unsafe.Slice(report, length)
	x, y, z := ParseIMUReport(data)
	globalState.gyroRing.WriteSample(x, y, z)
}

// Run starts the sensor worker. Blocks forever (CFRunLoop).
// Must be called from a goroutine that will be locked to an OS thread.
func Run(cfg Config) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	globalState = &callbackState{
		accelRing: cfg.AccelRing,
		gyroRing:  cfg.GyroRing,
	}

	if cfg.AccelRing != nil {
		cfg.AccelRing.SetRestarts(cfg.Restarts)
	}

	if err := wakeSPUDrivers(); err != nil {
		return fmt.Errorf("waking SPU drivers: %w", err)
	}
	if err := registerHIDDevices(); err != nil {
		return fmt.Errorf("registering HID devices: %w", err)
	}

	for {
		cfRunLoopRunInMode(kCFRunLoopDefaultMode, 1.0, false)
	}
}

func wakeSPUDrivers() error {
	matching := ioServiceMatching(cStr("AppleSPUHIDDriver"))
	var it uint32
	kr := ioServiceGetMatchingServices(0, matching, &it)
	if kr != 0 {
		return fmt.Errorf("IOServiceGetMatchingServices returned %d", kr)
	}

	for {
		svc := ioIteratorNext(it)
		if svc == 0 {
			break
		}
		props := []struct {
			key string
			val int32
		}{
			{"SensorPropertyReportingState", 1},
			{"SensorPropertyPowerState", 1},
			{"ReportInterval", ReportIntervalUS},
		}
		for _, p := range props {
			ioRegistryEntrySetCFProp(svc, cfStr(p.key), cfNum32(p.val))
		}
		ioObjectRelease(svc)
	}
	return nil
}

var gcRoots []any

func registerHIDDevices() error {
	matching := ioServiceMatching(cStr("AppleSPUHIDDevice"))
	var it uint32
	kr := ioServiceGetMatchingServices(0, matching, &it)
	if kr != 0 {
		return fmt.Errorf("IOServiceGetMatchingServices returned %d", kr)
	}

	for {
		svc := ioIteratorNext(it)
		if svc == 0 {
			break
		}

		up, _ := propInt(svc, "PrimaryUsagePage")
		u, _ := propInt(svc, "PrimaryUsage")

		var cb uintptr
		switch {
		case up == PageVendor && u == UsageAccel:
			cb = accelCallbackPtr
		case up == PageVendor && u == UsageGyro && globalState.gyroRing != nil:
			cb = gyroCallbackPtr
		}

		if cb != 0 {
			hid := ioHIDDeviceCreate(kCFAllocatorDefault, svc)
			if hid != 0 {
				kr := ioHIDDeviceOpen(hid, 0)
				if kr == 0 {
					reportBuf := make([]byte, ReportBufSize)
					gcRoots = append(gcRoots, reportBuf)
					bufPtr := uintptr(unsafe.Pointer(&reportBuf[0]))
					ioHIDDeviceRegisterInputReport(hid, bufPtr, ReportBufSize, cb, 0)
					ioHIDDeviceScheduleWithRL(hid, cfRunLoopGetCurrent(), kCFRunLoopDefaultMode)
				}
			}
		}
		ioObjectRelease(svc)
	}
	return nil
}
