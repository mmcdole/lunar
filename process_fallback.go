//go:build ios || (!aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris && !windows)

package lua

import (
	"os"
	"os/exec"
)

func hostShellAvailable() bool {
	return false
}

func newHostShellCommand(string) (*exec.Cmd, error) {
	return nil, exec.ErrNotFound
}

func hostPopenSupported() bool { return false }

func hostProcessStatus(*os.ProcessState) int {
	return -1
}
