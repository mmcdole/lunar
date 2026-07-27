package lua

import (
	"context"
	"errors"
	"math"
	"os"
)

type ioReadFormatKind uint8

const (
	ioReadLineFormat ioReadFormatKind = iota
	ioReadBytesFormat
	ioReadAllFormat
	ioReadNumberFormat
)

type ioReadFormat struct {
	kind  ioReadFormatKind
	count uint64
}

func ioRead(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireDefaultInputFile(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = readProcessFileArguments(
			frame,
			handle,
			0,
			ctx,
		)
	} else {
		outcome = readFileArguments(frame, handle, 0)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func fileRead(frame Frame) Outcome {
	lease, handle, failure, failed :=
		acquireFileArgument(frame)
	if failed {
		return failure
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = readProcessFileArguments(
			frame,
			handle,
			1,
			ctx,
		)
	} else {
		outcome = readFileArguments(frame, handle, 1)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func readFileArguments(
	frame Frame,
	handle *fileHandle,
	first int,
) Outcome {
	engine := newIOReadEngine(
		handle.input,
		&frame.thread.owner.strings,
	)
	if engine.bindContext(frame.thread) {
		defer engine.unbindContext()
	}
	argumentCount := frame.ArgumentCount()
	if argumentCount <= first {
		if err := handle.prepareRead(); err != nil {
			return ioFailureResult(frame, err)
		}
		value, present, err := engine.readLine()
		if err != nil {
			return ioReadFailure(frame, err)
		}
		if !present {
			value = nilSlot
		}
		return frame.returnOne(frame.activation(), value)
	}

	call := frame.activation()
	base := int(call.base)
	resultCount := 0
	prepared := false
	for index := first; index < argumentCount; index++ {
		value, _ := frame.argument(index)
		format, outcome, failed := parseIOReadFormat(
			frame,
			index,
			value,
		)
		if failed {
			return outcome
		}
		if !prepared {
			if err := handle.prepareRead(); err != nil {
				return ioFailureResult(frame, err)
			}
			prepared = true
		}
		value, present, err := format.read(&engine)
		if err != nil {
			return ioReadFailure(frame, err)
		}
		if !present {
			value = nilSlot
		}
		writeSlot(
			&frame.thread.values[base+resultCount],
			value,
		)
		resultCount++
		if !present {
			break
		}
	}
	return frame.returnCompactValues(
		[2]slot{},
		0,
		frame.thread.values[base:base+resultCount],
	)
}

func readProcessFileArguments(
	frame Frame,
	handle *fileHandle,
	first int,
	ctx context.Context,
) Outcome {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return readFileArguments(frame, handle, first)
}

func parseIOReadFormat(
	frame Frame,
	index int,
	format slot,
) (ioReadFormat, Outcome, bool) {
	switch format.kind() {
	case NumberKind:
		count := saturatingInt64(
			math.Float64frombits(format.bits),
		)
		if count < 0 {
			// PUC converts a negative Lua integer to size_t. That is
			// effectively an enormous fixed-count read, not *a: both
			// consume a finite remainder, but an empty stream produces
			// nil rather than an empty string.
			return ioReadFormat{
				kind:  ioReadBytesFormat,
				count: ^uint64(0),
			}, Outcome{}, false
		}
		return ioReadFormat{
			kind:  ioReadBytesFormat,
			count: uint64(count),
		}, Outcome{}, false
	case StringKind:
		text := stringSlotText(format)
		if len(text) == 0 || text[0] != '*' {
			return ioReadFormat{},
				baseArgumentError(
					frame,
					index,
					"invalid option",
				),
				true
		}
		if len(text) < 2 {
			return ioReadFormat{},
				baseArgumentError(
					frame,
					index,
					"invalid format",
				),
				true
		}
		switch text[1] {
		case 'a':
			return ioReadFormat{kind: ioReadAllFormat},
				Outcome{}, false
		case 'l':
			return ioReadFormat{kind: ioReadLineFormat},
				Outcome{}, false
		case 'n':
			return ioReadFormat{kind: ioReadNumberFormat},
				Outcome{}, false
		default:
			return ioReadFormat{},
				baseArgumentError(
					frame,
					index,
					"invalid format",
				),
				true
		}
	default:
		return ioReadFormat{},
			baseArgumentError(
				frame,
				index,
				"invalid option",
			),
			true
	}
}

func (format ioReadFormat) read(
	engine *ioReadEngine,
) (slot, bool, error) {
	switch format.kind {
	case ioReadLineFormat:
		return engine.readLine()
	case ioReadBytesFormat:
		return engine.readBytes(format.count)
	case ioReadAllFormat:
		return engine.readAll()
	case ioReadNumberFormat:
		return engine.readNumber()
	default:
		panic("lua: invalid IO read format")
	}
}

func ioReadFailure(frame Frame, failure error) Outcome {
	var luaFailure *Error
	if errors.As(failure, &luaFailure) {
		return frame.sealError(luaFailure)
	}
	if errors.Is(failure, errIOReadTooLarge) {
		return frame.sealError(
			newResourceError("resulting string too large"),
		)
	}
	if errors.Is(failure, errIONumberTooLong) {
		return frame.sealError(
			newResourceError(
				"numeric input exceeds %d bytes",
				maximumIONumberBytes,
			),
		)
	}
	return ioFailureResult(frame, failure)
}

func ioLines(frame Frame) Outcome {
	argument, present := frame.argument(0)
	if !present || argument.isNil() {
		data, ok := currentIOFile(
			frame,
			ioDefaultInput,
		)
		if !ok {
			return libraryError(
				frame,
				"attempt to use a closed file",
			)
		}
		if lease, open := acquireManagedResource(data); open {
			lease.release()
		} else {
			return libraryError(
				frame,
				"attempt to use a closed file",
			)
		}
		return newFileLineIterator(frame, data, false)
	}

	filename, ok := compactText(argument)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	filename = luaCString(filename)
	flags, _ := fileOpenFlags("r")
	file, err := os.OpenFile(filename, flags, 0o666)
	if err != nil {
		return baseArgumentError(
			frame,
			0,
			ioNamedFailureMessage(filename, err),
		)
	}
	metatable, err := frame.State().ensureFileMetatable()
	if err != nil {
		_ = file.Close()
		return libraryError(frame, "%s", err)
	}
	data, err := frame.State().newRegularFile(
		file,
		flags,
		metatable,
	)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	return newFileLineIterator(frame, data, true)
}

func fileLines(frame Frame) Outcome {
	data, present := frame.UserData(0)
	if !present || !isFileUserData(frame.thread.state, data) {
		return baseArgumentTypeError(
			frame,
			0,
			luaFileHandleRegistryKey,
		)
	}
	if lease, open := acquireManagedResource(data); open {
		lease.release()
	} else {
		return libraryError(
			frame,
			"attempt to use a closed file",
		)
	}
	return newFileLineIterator(frame, data, false)
}

func newFileLineIterator(
	frame Frame,
	data *UserData,
	autoClose bool,
) Outcome {
	function, err := frame.State().NewNativeFunction(
		fileLineIterator,
		data.Value(),
		Bool(autoClose),
	)
	if err != nil {
		if autoClose {
			_, closeErr := closeManagedResource(data)
			err = errors.Join(err, closeErr)
		}
		return libraryError(frame, "%s", err)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromFunction(function),
	)
}

func fileLineIterator(frame Frame) Outcome {
	captured := frame.nativeCapture(0)
	if !captured.isUserData() {
		return libraryError(frame, "file is already closed")
	}
	data := (*UserData)(captured.ref)
	if !isManagedUserDataClass(data, &fileResourceClass) {
		return libraryError(frame, "file is already closed")
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return libraryError(frame, "file is already closed")
	}
	handle, ok := lease.value.(*fileHandle)
	if !ok || handle == nil {
		lease.release()
		return libraryError(frame, "file is already closed")
	}
	ctx := processFileOperationContext(frame, handle)
	var outcome Outcome
	if ctx != nil {
		outcome = readProcessFileLine(
			frame,
			handle,
			data,
			ctx,
		)
	} else {
		outcome = readFileLine(frame, handle, data)
	}
	if ctx != nil {
		finishFileOperation(ctx, lease, handle)
	} else {
		lease.release()
	}
	return outcome
}

func readFileLine(
	frame Frame,
	handle *fileHandle,
	data *UserData,
) Outcome {
	if err := handle.prepareRead(); err != nil {
		return libraryError(frame, "%s", ioFailureMessage(err))
	}
	engine := newIOReadEngine(
		handle.input,
		&frame.thread.owner.strings,
	)
	if engine.bindContext(frame.thread) {
		defer engine.unbindContext()
	}
	value, present, err := engine.readLine()
	if err != nil {
		if errors.Is(err, errIOReadTooLarge) {
			return frame.sealError(
				newResourceError("resulting string too large"),
			)
		}
		var luaFailure *Error
		if errors.As(err, &luaFailure) {
			return frame.sealError(luaFailure)
		}
		return libraryError(frame, "%s", ioFailureMessage(err))
	}
	if present {
		return frame.returnOne(frame.activation(), value)
	}

	autoClose := truthySlot(frame.nativeCapture(1))
	if autoClose {
		_, _ = closeManagedResource(data)
	}
	return frame.Return()
}

func readProcessFileLine(
	frame Frame,
	handle *fileHandle,
	data *UserData,
	ctx context.Context,
) Outcome {
	stopCancellation := handle.interruptProcessIO(ctx)
	if stopCancellation != nil {
		defer stopCancellation()
	}
	return readFileLine(frame, handle, data)
}

func acquireDefaultInputFile(
	frame Frame,
) (
	nativeResourceLease,
	*fileHandle,
	Outcome,
	bool,
) {
	data, ok := currentIOFile(frame, ioDefaultInput)
	if !ok {
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard input file is closed"),
			true
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard input file is closed"),
			true
	}
	handle, ok := lease.value.(*fileHandle)
	if !ok || handle == nil {
		lease.release()
		return nativeResourceLease{}, nil,
			libraryError(frame, "standard input file is closed"),
			true
	}
	return lease, handle, Outcome{}, false
}

func currentIOFile(
	frame Frame,
	index int,
) (*UserData, bool) {
	current, found := frame.Environment().rawIntSlot(index)
	if !found || !current.isUserData() {
		return nil, false
	}
	data := (*UserData)(current.ref)
	if !isFileUserData(frame.thread.state, data) {
		return nil, false
	}
	return data, true
}
