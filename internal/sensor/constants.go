//go:build darwin

// Package sensor reads accelerometer + gyroscope from Apple Silicon MacBooks
// via IOKit HID (AppleSPUHIDDevice, Bosch BMI286 IMU).
//
// Vendored from github.com/taigrr/apple-silicon-accelerometer, stripped to
// accel+gyro only and pinned to Go 1.25 compatibility.
package sensor

const (
	PageVendor = 0xFF00
	UsageAccel = 3
	UsageGyro  = 9
)

const (
	IMUReportLen     = 22
	IMUDecimation    = 8
	IMUDataOffset    = 6
	ReportBufSize    = 4096
	ReportIntervalUS = 1000
)

const (
	CFStringEncodingUTF8 = 0x08000100
	CFNumberSInt32Type   = 3
	CFNumberSInt64Type   = 4
)
