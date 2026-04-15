//go:build darwin

// Package shm provides POSIX shared memory ring buffers for IMU data.
// Vendored from github.com/taigrr/apple-silicon-accelerometer.
package shm

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	RingCap   = 8000
	RingEntry = 12
	SHMHeader = 16
	SHMSize   = SHMHeader + RingCap*RingEntry

	AccelScale = 65536.0
	GyroScale  = 65536.0

	NameAccel = "seismo_accel"
	NameGyro  = "seismo_gyro"
)

type Sample struct {
	X, Y, Z float64
}

type RingBuffer struct {
	buf  []byte
	name string
	fd   int
}

func CreateRing(name string) (*RingBuffer, error) {
	_ = shmUnlink(name)

	fd, err := shmOpen(name, unix.O_CREAT|unix.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("shm_open %s: %w", name, err)
	}

	if err := unix.Ftruncate(fd, SHMSize); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ftruncate %s: %w", name, err)
	}

	buf, err := unix.Mmap(fd, 0, SHMSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("mmap %s: %w", name, err)
	}

	clear(buf)
	return &RingBuffer{buf: buf, name: name, fd: fd}, nil
}

func (r *RingBuffer) WriteSample(x, y, z int32) {
	idx := binary.LittleEndian.Uint32(r.buf[0:4])
	off := SHMHeader + int(idx)*RingEntry

	binary.LittleEndian.PutUint32(r.buf[off:], uint32(x))
	binary.LittleEndian.PutUint32(r.buf[off+4:], uint32(y))
	binary.LittleEndian.PutUint32(r.buf[off+8:], uint32(z))

	binary.LittleEndian.PutUint32(r.buf[0:4], (idx+1)%RingCap)
	total := binary.LittleEndian.Uint64(r.buf[4:12])
	binary.LittleEndian.PutUint64(r.buf[4:12], total+1)
}

func (r *RingBuffer) SetRestarts(count uint32) {
	binary.LittleEndian.PutUint32(r.buf[12:16], count)
}

func (r *RingBuffer) ReadNew(lastTotal uint64, scale float64) ([]Sample, uint64) {
	total := binary.LittleEndian.Uint64(r.buf[4:12])
	nNew := int64(total) - int64(lastTotal)
	if nNew <= 0 {
		return nil, total
	}
	if nNew > RingCap {
		nNew = RingCap
	}

	idx := binary.LittleEndian.Uint32(r.buf[0:4])
	start := (int64(idx) - nNew + RingCap) % RingCap
	samples := make([]Sample, nNew)

	for i := int64(0); i < nNew; i++ {
		pos := (start + i) % RingCap
		off := SHMHeader + int(pos)*RingEntry
		x := int32(binary.LittleEndian.Uint32(r.buf[off:]))
		y := int32(binary.LittleEndian.Uint32(r.buf[off+4:]))
		z := int32(binary.LittleEndian.Uint32(r.buf[off+8:]))
		samples[i] = Sample{
			X: float64(x) / scale,
			Y: float64(y) / scale,
			Z: float64(z) / scale,
		}
	}
	return samples, total
}

func (r *RingBuffer) Close() error {
	if err := unix.Munmap(r.buf); err != nil {
		return err
	}
	return unix.Close(r.fd)
}

func (r *RingBuffer) Unlink() error {
	return shmUnlink(r.name)
}

func shmOpen(name string, flags int, mode uint32) (int, error) {
	return shmOpenDarwin(name, flags, mode)
}

func shmUnlink(name string) error {
	return shmUnlinkDarwin(name)
}
