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

type processPipeDirection uint8

const (
	processPipeRead processPipeDirection = iota
	processPipeWrite
)

// childProcess owns one started command-processor root and the result
// published by the only goroutine allowed to call Wait. That waiter retains
// the exec.Cmd until the root exits because Cmd.Wait owns os/exec's process
// bookkeeping. It retains no State, Lua value, or operation context.
type childProcess struct {
	process       *os.Process
	done          chan struct{}
	result        processResult
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

	cmd, err := newHostShellCommand(command)
	if err != nil {
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

	process, err := startChildProcess(cmd)
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

// openHostShellPipe starts a shell with one standard descriptor connected to
// a manually owned pipe. Using os.Pipe lets the permanent waiter call
// exec.Cmd.Wait immediately while Lua operates on the parent descriptor;
// os/exec does not need a copying goroutine.
func openHostShellPipe(
	ctx context.Context,
	command string,
	direction processPipeDirection,
) (
	*os.File,
	*childProcess,
	error,
	bool,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if done := ctx.Done(); done != nil {
		select {
		case <-done:
			return nil, nil, nil, true
		default:
		}
	}

	cmd, err := newHostShellCommand(command)
	if err != nil {
		return nil, nil, err, false
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, err, false
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	parent, child := reader, writer
	switch direction {
	case processPipeRead:
		cmd.Stdout = child
	case processPipeWrite:
		parent, child = writer, reader
		cmd.Stdin = child
	default:
		_ = reader.Close()
		_ = writer.Close()
		return nil, nil, os.ErrInvalid, false
	}

	process, startErr := startChildProcess(cmd)
	childCloseErr := child.Close()
	if startErr != nil {
		_ = parent.Close()
		return nil, nil, startErr, false
	}
	if childCloseErr != nil {
		_ = parent.Close()
		process.terminateAndWait()
		return nil, nil, childCloseErr, false
	}

	if done := ctx.Done(); done != nil {
		select {
		case <-done:
			_ = parent.Close()
			process.terminateAndWait()
			return nil, nil, nil, true
		default:
		}
	}
	return parent, process, nil, false
}

func completedProcessStatus(result processResult) int {
	if processWaitError(result) != nil {
		return -1
	}
	return hostProcessStatus(result.state)
}

// processWaitError reports only infrastructure failure. exec.Cmd.Wait returns
// ExitError for an ordinary nonzero or signalled child, so a published process
// state makes that error normal completion.
func processWaitError(result processResult) error {
	if result.state != nil {
		if result.waitErr == nil {
			return nil
		}
		var exitError *exec.ExitError
		if errors.As(result.waitErr, &exitError) {
			return nil
		}
	}
	if result.waitErr != nil {
		return result.waitErr
	}
	return os.ErrProcessDone
}

func startChildProcess(
	cmd *exec.Cmd,
) (*childProcess, error) {
	if cmd == nil {
		return nil, os.ErrInvalid
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	process := &childProcess{
		process: cmd.Process,
		done:    make(chan struct{}),
	}
	go func(command *exec.Cmd) {
		process.result.waitErr = command.Wait()
		process.result.state = command.ProcessState
		close(process.done)
	}(cmd)
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

// abandon terminates the command-processor root without blocking. The
// permanent waiter still reaps it after it exits.
func (process *childProcess) abandon() {
	if process != nil {
		process.terminate()
	}
}

func (process *childProcess) terminateAndWait() processResult {
	if process == nil {
		return processResult{}
	}
	process.terminate()
	<-process.done
	return process.result
}

func (process *childProcess) terminate() {
	if process == nil || process.process == nil {
		return
	}
	process.terminateOnce.Do(func() {
		_ = process.process.Kill()
	})
}
