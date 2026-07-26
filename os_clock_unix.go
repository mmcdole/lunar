//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package lua

import "syscall"

func hostProcessCPUSeconds() (float64, bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	user := float64(usage.Utime.Sec) +
		float64(usage.Utime.Usec)/1_000_000
	system := float64(usage.Stime.Sec) +
		float64(usage.Stime.Usec)/1_000_000
	return user + system, true
}
