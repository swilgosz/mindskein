//go:build darwin

package proc

import (
	"encoding/binary"
	"syscall"
	"time"
	"unsafe"
)

// The sysctl MIB constants. They are spelled out rather than imported because
// the stdlib syscall package does not export them on darwin.
const (
	ctlKern      = 1
	kernProc     = 14
	kernProcPID  = 1
	kernBootTime = 21
)

// sysctlRaw returns the raw bytes a MIB reports. The stdlib's Sysctl returns a
// string truncated at the first NUL, which destroys the binary structs both
// callers here need.
func sysctlRaw(mib []int32) ([]byte, error) {
	size := uintptr(0)
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		0, uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return nil, errno
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return nil, errno
	}
	return buf[:size], nil
}

// timeval reads a struct timeval from the front of b: an int64 of seconds
// followed by an int32 of microseconds.
func timeval(b []byte) (time.Time, bool) {
	if len(b) < 12 {
		return time.Time{}, false
	}
	sec := int64(binary.LittleEndian.Uint64(b[0:8]))
	usec := int64(int32(binary.LittleEndian.Uint32(b[8:12])))
	if sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, usec*1000), true
}

func bootTime() (time.Time, error) {
	b, err := sysctlRaw([]int32{ctlKern, kernBootTime})
	if err != nil {
		return time.Time{}, err
	}
	t, ok := timeval(b)
	if !ok {
		return time.Time{}, ErrUnsupported
	}
	return t, nil
}

// startTime reads p_starttime out of the process's kinfo_proc.
//
// The field sits at offset 0: kinfo_proc opens with extern_proc, which opens
// with a union whose second member is the start time. That layout is ABI, not
// an implementation detail, so reading the first timeval is stable — and it
// avoids modelling the rest of a large struct to reach one field.
func startTime(pid int) (time.Time, error) {
	b, err := sysctlRaw([]int32{ctlKern, kernProc, kernProcPID, int32(pid)})
	if err != nil {
		return time.Time{}, err
	}
	// An empty answer is how the kernel reports a pid nobody holds.
	if len(b) == 0 {
		return time.Time{}, ErrNotRunning
	}
	t, ok := timeval(b)
	if !ok {
		return time.Time{}, ErrNotRunning
	}
	return t, nil
}
