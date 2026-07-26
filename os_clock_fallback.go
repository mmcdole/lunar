//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package lua

func hostProcessCPUSeconds() (float64, bool) {
	return 0, false
}
