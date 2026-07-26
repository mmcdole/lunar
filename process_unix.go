//go:build (aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris) && !ios

package lua

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

type hostProcessControl struct {
	pgid int
}

func findHostShell() (string, bool) {
	path := "/bin/sh"
	if runtime.GOOS == "android" {
		path = "/system/bin/sh"
	}
	if _, err := exec.LookPath(path); err != nil {
		return "", false
	}
	return path, true
}

func hostShellAvailable() bool {
	_, available := findHostShell()
	return available
}

func newHostShellCommand(command string) (*exec.Cmd, bool) {
	path, available := findHostShell()
	if !available {
		return nil, false
	}
	return exec.Command(path, "-c", command), true
}

func configureHostProcess(cmd *exec.Cmd, ownTree bool) {
	if ownTree {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

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

func newHostProcessControl(
	process *os.Process,
	ownTree bool,
) hostProcessControl {
	if ownTree && process != nil {
		return hostProcessControl{pgid: process.Pid}
	}
	return hostProcessControl{}
}

func (control *hostProcessControl) terminate(process *os.Process) {
	if process == nil {
		return
	}
	if control.pgid != 0 {
		if err := syscall.Kill(
			-control.pgid,
			syscall.SIGKILL,
		); err == nil {
			return
		} else if !errors.Is(err, syscall.ESRCH) {
			_ = process.Kill()
			return
		}
	}
	_ = process.Kill()
}

func (control *hostProcessControl) close() {
	control.pgid = 0
}
