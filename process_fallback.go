//go:build ios || (!aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris && !windows)

package lua

import (
	"os"
	"os/exec"
)

type hostProcessControl struct{}

func hostShellAvailable() bool {
	return false
}

func newHostShellCommand(string) (*exec.Cmd, bool) {
	return nil, false
}

func configureHostProcess(*exec.Cmd, bool) {}

func hostProcessStatus(*os.ProcessState) int {
	return -1
}

func newHostProcessControl(
	*os.Process,
	bool,
) hostProcessControl {
	return hostProcessControl{}
}

func (*hostProcessControl) terminate(process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}

func (*hostProcessControl) close() {}
