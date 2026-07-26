//go:build windows

package lua

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func findHostShell() (string, error) {
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
	cmd := exec.Command(path)
	cmd.Args = nil
	line := `"` + path + `" /c `
	if command == "" {
		line += `""`
	} else {
		line += command
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return cmd, nil
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
	return int(int32(status.ExitCode))
}
