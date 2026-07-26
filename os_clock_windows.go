//go:build windows

package lua

import "syscall"

func hostProcessCPUSeconds() (float64, bool) {
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(
		process,
		&creation,
		&exit,
		&kernel,
		&user,
	); err != nil {
		return 0, false
	}
	ticks := func(value syscall.Filetime) uint64 {
		return uint64(value.HighDateTime)<<32 |
			uint64(value.LowDateTime)
	}
	return float64(ticks(kernel)+ticks(user)) / 10_000_000, true
}
