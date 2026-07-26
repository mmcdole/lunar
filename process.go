package lua

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
)

type processResult struct {
	state   *os.ProcessState
	waitErr error
}

// childProcess owns one started process, its platform termination scope, and
// the result published by the only goroutine allowed to call Wait. It
// deliberately retains no State, Lua value, operation context, or exec.Cmd.
type childProcess struct {
	process       *os.Process
	control       hostProcessControl
	done          chan struct{}
	result        processResult
	controlMu     sync.Mutex
	terminateOnce sync.Once
}

// executeHostShell runs one command through the platform's ordinary command
// processor. It inherits process descriptors, environment, and working
// directory rather than the State's potentially virtual stream endpoints.
func executeHostShell(
	ctx context.Context,
	command string,
) (status int, cancelled bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	done := ctx.Done()
	if done != nil {
		select {
		case <-done:
			return -1, true
		default:
		}
	}

	cmd, available := newHostShellCommand(command)
	if !available {
		return -1, false
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if done == nil {
		err := cmd.Run()
		return completedProcessStatus(processResult{
			state:   cmd.ProcessState,
			waitErr: err,
		}), false
	}

	process, err := startChildProcess(cmd, true)
	if err != nil {
		if done != nil {
			select {
			case <-done:
				return -1, true
			default:
			}
		}
		return -1, false
	}
	result, cancelled := process.wait(ctx)
	if cancelled {
		return -1, true
	}
	return completedProcessStatus(result), false
}

func completedProcessStatus(result processResult) int {
	if result.state == nil {
		return -1
	}
	if result.waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(result.waitErr, &exitError) {
			return -1
		}
	}
	return hostProcessStatus(result.state)
}

func startChildProcess(
	cmd *exec.Cmd,
	ownTree bool,
) (*childProcess, error) {
	if cmd == nil {
		return nil, os.ErrInvalid
	}
	configureHostProcess(cmd, ownTree)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	process := &childProcess{
		process: cmd.Process,
		control: newHostProcessControl(cmd.Process, ownTree),
		done:    make(chan struct{}),
	}
	go func() {
		process.result.waitErr = cmd.Wait()
		process.result.state = cmd.ProcessState
		process.controlMu.Lock()
		process.control.close()
		process.controlMu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (process *childProcess) wait(
	ctx context.Context,
) (processResult, bool) {
	if process == nil {
		return processResult{}, false
	}
	if ctx == nil || ctx.Done() == nil {
		<-process.done
		return process.result, false
	}
	done := ctx.Done()
	select {
	case <-process.done:
		return process.result, false
	case <-done:
		// Completion already published wins a simultaneous cancellation.
		select {
		case <-process.done:
			return process.result, false
		default:
		}
		process.terminate()
		<-process.done
		return process.result, true
	}
}

// abandon terminates an owned process tree without blocking. The permanent
// waiter still reaps the root and releases platform control after it exits.
func (process *childProcess) abandon() {
	if process != nil {
		process.terminate()
	}
}

func (process *childProcess) terminate() {
	process.terminateOnce.Do(func() {
		process.controlMu.Lock()
		process.control.terminate(process.process)
		process.controlMu.Unlock()
	})
}
