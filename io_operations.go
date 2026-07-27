package lua

import (
	"context"
	"io"
	"math"
	"syscall"
)

// resetReadAhead makes the next read start at the underlying stream's current
// position while retaining the lazily allocated bufio storage for reuse.
func (endpoint *inputEndpoint) resetReadAhead() {
	if endpoint == nil {
		return
	}
	if endpoint.buffered != nil {
		endpoint.buffered.Reset(&endpoint.source)
	}
	endpoint.source.pending = nil
	endpoint.source.deferred = nil
	endpoint.source.replay = nil
}

// prepareRead completes pending writes before the same file is read. Keeping
// this transition on fileHandle gives the read library and future low-level
// file API one cursor rule.
func (handle *fileHandle) prepareRead() error {
	if handle == nil {
		return ErrClosed
	}
	if handle.input == nil {
		return syscall.EBADF
	}
	if handle.output != nil {
		if err := handle.output.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// prepareWrite restores the underlying seek position to the Lua-visible read
// cursor before writing. A bufio.Reader may otherwise have advanced the
// operating-system cursor beyond bytes Lua has actually consumed.
func (handle *fileHandle) prepareWrite() error {
	if handle == nil {
		return ErrClosed
	}
	if handle.output == nil {
		return syscall.EBADF
	}
	if handle.input == nil {
		return nil
	}

	unread := handle.input.unreadBytes()
	if unread != 0 {
		if handle.seeker == nil {
			return syscall.ESPIPE
		}
		if _, err := handle.seeker.Seek(
			-int64(unread),
			io.SeekCurrent,
		); err != nil {
			return err
		}
	}
	handle.input.resetReadAhead()
	return nil
}

// seek flushes output, translates SEEK_CUR from the underlying cursor to the
// Lua-visible cursor, and discards stale read-ahead after a successful move.
func (handle *fileHandle) seek(
	offset int64,
	origin int,
) (int64, error) {
	if handle == nil {
		return 0, ErrClosed
	}
	if handle.output != nil {
		if err := handle.output.Flush(); err != nil {
			return 0, err
		}
	}
	if handle.seeker == nil {
		return 0, syscall.ESPIPE
	}

	if origin == io.SeekCurrent && handle.input != nil {
		unread := int64(handle.input.unreadBytes())
		if offset < math.MinInt64+unread {
			return 0, syscall.EINVAL
		}
		offset -= unread
	}
	position, err := handle.seeker.Seek(offset, origin)
	if err != nil {
		return 0, err
	}
	if handle.input != nil {
		handle.input.resetReadAhead()
	}
	return position, nil
}

func ioWrite(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireDefaultOutputFile(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = writeProcessFileArguments(
			frame,
			handle,
			0,
			ctx,
		)
	} else {
		outcome = writeFileArguments(frame, handle, 0)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func fileWrite(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireFileArgument(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = writeProcessFileArguments(
			frame,
			handle,
			1,
			ctx,
		)
	} else {
		outcome = writeFileArguments(frame, handle, 1)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func writeFileArguments(
	frame Frame,
	handle *fileHandle,
	first int,
) Outcome {
	argumentCount := frame.ArgumentCount()
	if argumentCount <= first {
		return frame.ReturnBool(true)
	}
	if err := handle.prepareWrite(); err != nil {
		return ioFailureResult(frame, err)
	}

	for index := first; index < argumentCount; index++ {
		value, _ := frame.argument(index)
		switch value.kind() {
		case NumberKind:
			if err := writeFileNumber(
				handle.output,
				math.Float64frombits(value.bits),
			); err != nil {
				return ioFailureResult(frame, err)
			}
		case StringKind:
			text := stringSlotText(value)
			if err := writeFileString(handle.output, text); err != nil {
				return ioFailureResult(frame, err)
			}
		default:
			return baseArgumentTypeError(frame, index, "string")
		}
	}
	return frame.ReturnBool(true)
}

func writeProcessFileArguments(
	frame Frame,
	handle *fileHandle,
	first int,
	ctx context.Context,
) Outcome {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return writeFileArguments(frame, handle, first)
}

// writeFileNumber uses storage owned by the endpoint in both buffering modes.
// Besides avoiding a copy into an active stream buffer, stable endpoint
// storage prevents an unbuffered io.Writer call from escaping a temporary.
func writeFileNumber(
	output *outputEndpoint,
	number float64,
) error {
	if output.mode != streamBufferNone {
		writer, err := output.buffer()
		if err != nil {
			return err
		}
		text := appendLuaNumber(
			writer.AvailableBuffer(),
			number,
		)
		written, err := writer.Write(text)
		if err == nil && written != len(text) {
			return io.ErrShortWrite
		}
		return err
	}

	return writeFileBytes(
		output,
		appendLuaNumber(output.numberScratch[:0], number),
	)
}

func writeFileBytes(
	output *outputEndpoint,
	text []byte,
) error {
	written, err := output.Write(text)
	if err == nil && written != len(text) {
		return io.ErrShortWrite
	}
	return err
}

func writeFileString(
	output *outputEndpoint,
	text string,
) error {
	written, err := output.WriteString(text)
	if err == nil && written != len(text) {
		return io.ErrShortWrite
	}
	return err
}

func ioFlush(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireDefaultOutputFile(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var err error
	if ctx != nil {
		err = flushProcessFile(handle, ctx)
	} else {
		err = flushFile(handle)
	}
	var outcome Outcome
	if err != nil {
		outcome = ioFailureResult(frame, err)
	} else {
		outcome = frame.ReturnBool(true)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func fileFlush(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireFileArgument(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var err error
	if ctx != nil {
		err = flushProcessFile(handle, ctx)
	} else {
		err = flushFile(handle)
	}
	var outcome Outcome
	if err != nil {
		outcome = ioFailureResult(frame, err)
	} else {
		outcome = frame.ReturnBool(true)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func flushFile(handle *fileHandle) error {
	if handle == nil {
		return ErrClosed
	}
	if handle.output == nil {
		return nil
	}
	return handle.output.Flush()
}

func flushProcessFile(
	handle *fileHandle,
	ctx context.Context,
) error {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return flushFile(handle)
}

func fileSeek(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireFileArgument(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = seekProcessFile(frame, handle, ctx)
	} else {
		outcome = seekFile(frame, handle)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func seekFile(
	frame Frame,
	handle *fileHandle,
) Outcome {
	mode := "cur"
	if value, present := frame.argument(1); present &&
		!value.isNil() {
		var ok bool
		mode, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		mode = luaCString(mode)
	}
	origin, ok := fileSeekOrigin(mode)
	if !ok {
		return baseArgumentError(
			frame,
			1,
			"invalid option '"+mode+"'",
		)
	}

	offset := int64(0)
	if value, present := frame.argument(2); present &&
		!value.isNil() {
		offset, ok = frame.positionArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}
	position, err := handle.seek(offset, origin)
	if err != nil {
		return ioFailureResult(frame, err)
	}
	return frame.ReturnNumber(float64(position))
}

func seekProcessFile(
	frame Frame,
	handle *fileHandle,
	ctx context.Context,
) Outcome {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return seekFile(frame, handle)
}

func fileSeekOrigin(mode string) (int, bool) {
	switch mode {
	case "set":
		return io.SeekStart, true
	case "cur":
		return io.SeekCurrent, true
	case "end":
		return io.SeekEnd, true
	default:
		return 0, false
	}
}

func fileSetBuffering(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireFileArgument(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = setProcessFileBuffering(
			frame,
			handle,
			ctx,
		)
	} else {
		outcome = setFileBuffering(frame, handle)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func setFileBuffering(
	frame Frame,
	handle *fileHandle,
) Outcome {
	modeText, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	modeText = luaCString(modeText)
	mode, ok := fileBufferMode(modeText)
	if !ok {
		return baseArgumentError(
			frame,
			1,
			"invalid option '"+modeText+"'",
		)
	}

	size := int64(defaultStreamBufferBytes)
	if value, present := frame.argument(2); present &&
		!value.isNil() {
		size, ok = frame.positionArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}
	if size < 0 ||
		uint64(size) > uint64(maxInt()) ||
		size > maximumStreamBufferBytes {
		return ioFailureResult(
			frame,
			syscall.EINVAL,
		)
	}
	if handle.output != nil {
		if err := handle.output.setBuffering(mode, int(size)); err != nil {
			return ioFailureResult(frame, err)
		}
	}
	if handle.input != nil {
		if err := handle.input.setBuffering(mode, int(size)); err != nil {
			return ioFailureResult(frame, err)
		}
	}
	return frame.ReturnBool(true)
}

func setProcessFileBuffering(
	frame Frame,
	handle *fileHandle,
	ctx context.Context,
) Outcome {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return setFileBuffering(frame, handle)
}

func fileBufferMode(mode string) (streamBufferMode, bool) {
	switch mode {
	case "no":
		return streamBufferNone, true
	case "full":
		return streamBufferFull, true
	case "line":
		return streamBufferLine, true
	default:
		return 0, false
	}
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func acquireDefaultOutputFile(
	frame Frame,
) (
	nativeResourceLease,
	*fileHandle,
	Outcome,
	bool,
) {
	current, found := frame.environmentObject().rawIntSlot(ioDefaultOutput)
	if !found || !current.isUserData() {
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard output file is closed"),
			true
	}
	data := userDataObjectFromSlot(current)
	if !isFileUserData(frame.thread.state, data) {
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard output file is closed"),
			true
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard output file is closed"),
			true
	}
	handle, ok := lease.value.(*fileHandle)
	if !ok || handle == nil {
		lease.release()
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard output file is closed"),
			true
	}
	return lease, handle, Outcome{}, false
}

func acquireFileArgument(
	frame Frame,
) (
	nativeResourceLease,
	*fileHandle,
	Outcome,
	bool,
) {
	data, present := frame.userDataObject(0)
	if !present || !isFileUserData(frame.thread.state, data) {
		return nativeResourceLease{}, nil,
			baseArgumentTypeError(
				frame,
				0,
				luaFileHandleRegistryKey,
			),
			true
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return nativeResourceLease{}, nil,
			libraryError(frame, "attempt to use a closed file"),
			true
	}
	handle, ok := lease.value.(*fileHandle)
	if !ok || handle == nil {
		lease.release()
		return nativeResourceLease{}, nil,
			libraryError(frame, "attempt to use a closed file"),
			true
	}
	return lease, handle, Outcome{}, false
}
