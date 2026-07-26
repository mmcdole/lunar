//go:build windows

package lua

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type hostProcessControl struct{}

func findHostShell() (string, bool) {
	path := strings.TrimSpace(os.Getenv("COMSPEC"))
	if path != "" {
		path = strings.Trim(path, `"`)
	}
	var err error
	if path != "" {
		path, err = exec.LookPath(path)
	}
	if path == "" || err != nil {
		path, err = exec.LookPath("cmd.exe")
	}
	if err != nil {
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
	cmd := exec.Command(path)
	cmd.Args = nil
	line := `"` + path + `" /c `
	if command == "" {
		line += `""`
	} else {
		line += command
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return cmd, true
}

func configureHostProcess(*exec.Cmd, bool) {}

func hostProcessStatus(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return -1
	}
	return int(int32(status.ExitCode))
}

func newHostProcessControl(
	*os.Process,
	bool,
) hostProcessControl {
	return hostProcessControl{}
}

func (control *hostProcessControl) terminate(process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}

func (*hostProcessControl) close() {}
