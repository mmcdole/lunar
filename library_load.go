package lua

import (
	"io"
	"strings"
)

func baseLoadString(frame Frame) Outcome {
	source, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	name, outcome, failed := optionalLoadName(
		frame,
		1,
		luaCString(source),
	)
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}
	prototype, err := loadStringPrototype(name, source, &control)
	return returnLoadResult(frame, prototype, err)
}

func baseLoad(frame Frame) Outcome {
	reader, present := frame.argument(0)
	if !present || !reader.isFunction() {
		return baseArgumentTypeError(frame, 0, "function")
	}
	name, outcome, failed := optionalLoadName(frame, 1, "=(load)")
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}

	input := newRefillableChunkInput(func() (string, error) {
		value, callFailure := frame.callCompactOne(reader, nil)
		if callFailure != nil {
			return "", callFailure
		}
		if value.isNil() {
			return "", io.EOF
		}
		text, stringLike := compactText(value)
		if !stringLike {
			return "", libraryFailure(
				frame,
				"reader function must return a string",
			)
		}
		// chunkInput treats an empty piece as terminal. This keeps the first
		// terminal result sticky instead of reproducing PUC 5.1's preliminary
		// lookahead double-read quirk.
		return text, nil
	}, &control)
	prototype, err := loadInputPrototype(name, input, &control)
	return returnLoadResult(frame, prototype, err)
}

func baseLoadFile(frame Frame) Outcome {
	filename, standardInput, outcome, failed := loadFilename(frame)
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}

	prototype, err := loadFrameFilePrototype(
		frame,
		filename,
		standardInput,
		&control,
	)
	return returnLoadResult(frame, prototype, err)
}

func baseDoFile(frame Frame) Outcome {
	filename, standardInput, outcome, failed := loadFilename(frame)
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}

	prototype, err := loadFrameFilePrototype(
		frame,
		filename,
		standardInput,
		&control,
	)
	if err != nil {
		return raiseDoFileError(frame, err)
	}
	function := frame.thread.state.loadPrototypeObject(prototype)
	return frame.callCompactAllAndReturn(
		slotFromFunctionObject(function),
		nil,
	)
}

func loadFrameFilePrototype(
	frame Frame,
	filename string,
	standardInput bool,
	control *loadControl,
) (*Prototype, error) {
	state := frame.thread.state
	if !standardInput {
		return state.loadNamedSourcePrototype(
			frame.Context(),
			filename,
			control,
		)
	}
	if !state.source.stdin {
		return nil, &fileLoadError{
			operation: "open",
			name:      "stdin",
			cause:     ErrSourceLoadingDisabled,
		}
	}
	return loadFileEndpointPrototype(
		"=stdin",
		"stdin",
		&state.streams.stdin,
		control,
	)
}

func frameLoadControl(
	frame Frame,
) (loadControl, Outcome, bool) {
	control, failure := newLoadControl(
		frame.Context(),
		frame.thread.state.options.MaxLoadBytes,
	)
	if failure != nil {
		return loadControl{}, frame.sealError(failure), true
	}
	return control, Outcome{}, false
}

func optionalLoadName(
	frame Frame,
	index int,
	fallback string,
) (string, Outcome, bool) {
	value, present := frame.argument(index)
	if !present || value.isNil() {
		return fallback, Outcome{}, false
	}
	text, ok := compactText(value)
	if !ok {
		return "", baseArgumentTypeError(frame, index, "string"), true
	}
	return luaCString(text), Outcome{}, false
}

func loadFilename(
	frame Frame,
) (filename string, standardInput bool, outcome Outcome, failed bool) {
	value, present := frame.argument(0)
	if !present || value.isNil() {
		return "", true, Outcome{}, false
	}
	text, ok := compactText(value)
	if !ok {
		return "", false,
			baseArgumentTypeError(frame, 0, "string"),
			true
	}
	return luaCString(text), false, Outcome{}, false
}

func luaCString(text string) string {
	if end := strings.IndexByte(text, 0); end >= 0 {
		return text[:end]
	}
	return text
}

func returnLoadResult(
	frame Frame,
	prototype *Prototype,
	err error,
) Outcome {
	if err == nil {
		function := frame.thread.state.loadPrototypeObject(prototype)
		return frame.returnOne(
			frame.activation(),
			slotFromFunctionObject(function),
		)
	}

	failure, luaFailure := err.(*Error)
	if luaFailure && isHostControlFailure(failure) {
		return frame.RaiseError(failure)
	}
	errorValue := stringSlot(
		frame.thread.owner.strings.make(err.Error()),
	)
	if luaFailure {
		errorValue = failure.mustValueSlot(frame.thread.owner)
	}
	return frame.returnCompactValues(
		[2]slot{nilSlot, errorValue},
		2,
		nil,
	)
}

func raiseDoFileError(frame Frame, err error) Outcome {
	failure, luaFailure := err.(*Error)
	if !luaFailure {
		message := err.Error()
		return frame.sealError(&Error{
			value:       frame.thread.state.String(message),
			description: message,
			category:    RuntimeError,
			cause:       err,
		})
	}
	switch failure.category {
	case ContextError, ExitError, RuntimeError, ResourceError:
		return frame.RaiseError(failure)
	default:
		// Loading failures are values raised by dofile. Reclassify syntax
		// failures as ordinary runtime errors so pcall can catch them.
		return frame.raiseCompact(
			failure.mustValueSlot(frame.thread.owner),
		)
	}
}
