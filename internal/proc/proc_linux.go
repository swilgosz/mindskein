//go:build linux

package proc

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the kernel's USER_HZ, the unit /proc/<pid>/stat reports
// starttime in. Reading the real value needs sysconf(_SC_CLK_TCK) and so cgo;
// 100 is the value on every Linux this is likely to run on, and being wrong
// shifts a comparison both sides of which use the same constant.
const clockTicks = 100

func bootTime() (time.Time, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		sec, err := strconv.ParseInt(strings.TrimSpace(line[len("btime "):]), 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		return time.Unix(sec, 0), nil
	}
	if err := scan.Err(); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, ErrUnsupported
}

// startTime reads field 22 of /proc/<pid>/stat, the ticks since boot at which
// the process started.
//
// The fields are split from the last ')' rather than by whitespace: field 2 is
// the executable name in parentheses and may itself contain spaces and
// brackets, which is the classic way to misparse this file.
func startTime(pid int) (time.Time, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if os.IsNotExist(err) {
		return time.Time{}, ErrNotRunning
	}
	if err != nil {
		return time.Time{}, err
	}
	close := strings.LastIndexByte(string(data), ')')
	if close < 0 {
		return time.Time{}, ErrUnsupported
	}
	fields := strings.Fields(string(data)[close+1:])
	// Field 3 onwards follow the ')', so starttime (field 22) is index 19.
	const startIndex = 19
	if len(fields) <= startIndex {
		return time.Time{}, ErrUnsupported
	}
	ticks, err := strconv.ParseInt(fields[startIndex], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	boot, err := bootTime()
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks), nil
}
