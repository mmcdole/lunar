package lua

import (
	"context"
	"errors"
	"os"
	"syscall"
)

func (state *State) newProcessFile(
	pipe *os.File,
	process *childProcess,
	direction processPipeDirection,
	metatable *tableObject,
) (*userDataObject, error) {
	handle := &fileHandle{
		closer:  pipe,
		process: process,
	}
	switch direction {
	case processPipeRead:
		handle.ownedInput = newInputEndpoint(pipe)
		handle.input = &handle.ownedInput
	case processPipeWrite:
		handle.ownedOutput = newOutputEndpoint(pipe)
		handle.ownedOutput.mode = streamBufferFull
		handle.output = &handle.ownedOutput
	default:
		_ = closeFileHandle(handle, nativeRelease{
			reason: nativeReleaseStateClose,
		})
		return nil, os.ErrInvalid
	}

	data, err := state.newManagedUserData(
		handle,
		closeFileHandle,
	)
	if err != nil {
		_ = closeFileHandle(handle, nativeRelease{
			reason: nativeReleaseStateClose,
		})
		return nil, err
	}
	classifyManagedUserData(data, &fileResourceClass, metatable)
	return data, nil
}

func closeProcessFileHandle(
	handle *fileHandle,
	release nativeRelease,
) error {
	process := handle.process
	closer := handle.closer
	ctx := release.context
	if ctx == nil {
		ctx = context.Background()
	}

	var stopCancellation func() bool
	if release.reason == nativeReleaseExplicit &&
		ctx.Done() != nil {
		stopCancellation = context.AfterFunc(ctx, func() {
			if closer != nil {
				_ = closer.Close()
			}
			process.abandon()
		})
	}

	var outputErr error
	if handle.output != nil {
		if release.reason == nativeReleaseExplicit {
			outputErr = handle.output.detach()
		} else {
			handle.output.discard()
		}
	}
	if handle.input != nil {
		handle.input.detach()
	}
	var closeErr error
	if closer != nil {
		closeErr = closer.Close()
	}

	var waitErr error
	switch release.reason {
	case nativeReleaseExplicit:
		result, cancelled := process.wait(ctx)
		waitErr = processWaitError(result)
		if stopCancellation != nil {
			stopCancellation()
		}
		if cancelled || ctx.Err() != nil {
			process.terminate()
			clearFileHandle(handle)
			return newContextError(ctx, true)
		}
	case nativeReleaseStateClose:
		waitErr = processWaitError(process.terminateAndWait())
	case nativeReleaseCollected:
		process.abandon()
	default:
		waitErr = os.ErrInvalid
	}
	clearFileHandle(handle)
	return errors.Join(outputErr, closeErr, waitErr)
}

func clearFileHandle(handle *fileHandle) {
	handle.input = nil
	handle.output = nil
	handle.seeker = nil
	handle.closer = nil
	handle.process = nil
}

// interruptProcessIO closes an owned pipe and terminates its
// command-processor root when a context expires. Only the operating-system
// objects are touched from the callback; lifecycle-record mutation remains on
// the State executor.
func (handle *fileHandle) interruptProcessIO(
	ctx context.Context,
) func() bool {
	if handle == nil ||
		handle.process == nil ||
		handle.closer == nil ||
		ctx == nil ||
		ctx.Done() == nil {
		return nil
	}
	closer := handle.closer
	process := handle.process
	return context.AfterFunc(ctx, func() {
		_ = closer.Close()
		process.abandon()
	})
}

func processFileOperationContext(
	frame Frame,
	handle *fileHandle,
) context.Context {
	if handle == nil || handle.process == nil {
		return nil
	}
	state := frame.thread.state
	if state.ambientDone == nil {
		return nil
	}
	return state.ambient
}

func finishFileOperation(
	ctx context.Context,
	lease nativeResourceLease,
	handle *fileHandle,
) {
	if handle != nil &&
		handle.process != nil &&
		ctx != nil &&
		ctx.Done() != nil &&
		contextChannelClosed(ctx.Done()) {
		_, _ = closeManagedLeaseContext(
			lease,
			ctx,
		)
	}
	lease.release()
}

func ioPopen(frame Frame) Outcome {
	command, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	command = luaCString(command)

	mode := "r"
	if supplied, present := frame.argument(1); present &&
		!supplied.isNil() {
		mode, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		mode = luaCString(mode)
	}

	if !hostPopenSupported() {
		return libraryError(frame, "'popen' not supported")
	}

	var direction processPipeDirection
	switch mode {
	case "r":
		direction = processPipeRead
	case "w":
		direction = processPipeWrite
	default:
		return ioNamedFailureResult(
			frame,
			command,
			syscall.EINVAL,
		)
	}

	pipe, process, err, cancelled := openHostShellPipe(
		frame.Context(),
		command,
		direction,
	)
	if cancelled {
		failure := pollExecutionContext(frame.thread)
		if failure == nil {
			failure = newContextError(frame.Context(), true)
		}
		return frame.sealError(failure)
	}
	if err != nil {
		return ioNamedFailureResult(frame, command, err)
	}

	metatable, err := frame.State().ensureFileMetatable()
	if err != nil {
		_ = closeFileHandle(&fileHandle{
			closer:  pipe,
			process: process,
		}, nativeRelease{reason: nativeReleaseStateClose})
		return libraryError(frame, "%s", err)
	}
	data, err := frame.State().newProcessFile(
		pipe,
		process,
		direction,
		metatable,
	)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	if failure := pollExecutionContext(frame.thread); failure != nil {
		_, _ = closeManagedResourceContext(
			data,
			frame.Context(),
		)
		return frame.sealError(failure)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromUserDataObject(data),
	)
}
