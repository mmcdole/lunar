//go:build (aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris) && !ios

package lua

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func findHostShell() (string, error) {
	path := "/bin/sh"
	if runtime.GOOS == "android" {
		path = "/system/bin/sh"
	}
	if _, err := exec.LookPath(path); err != nil {
		return "", err
	}
	return path, nil
}

func hostShellAvailable() bool {
	_, err := findHostShell()
	return err == nil
}

func newHostShellCommand(command string) (*exec.Cmd, error) {
	path, err := findHostShell()
	if err != nil {
		return nil, err
	}
	return exec.Command(path, "-c", command), nil
}

func hostPopenSupported() bool { return true }

func hostProcessStatus(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return -1
	}
	return int(status)
}
