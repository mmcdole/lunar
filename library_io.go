package lua

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const luaFileHandleRegistryKey = "FILE*"

const (
	ioDefaultInput  = 1
	ioDefaultOutput = 2
)

var fileResourceClass nativeResourceClass

var ioLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "close", entry: ioClose},
	{name: "input", entry: ioInput},
	{name: "open", entry: ioOpen},
	{name: "output", entry: ioOutput},
	{name: "type", entry: ioType},
}

var fileLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "close", entry: ioClose},
	{name: "__gc", entry: fileCollect},
	{name: "__tostring", entry: fileToString},
}

// fileHandle is the one native object behind a Lua file userdata. Standard
// files borrow the State's shared endpoints; regular files own their endpoints
// and closer. Read, write, seek, and buffering operations extend this same
// object rather than introducing another representation.
type fileHandle struct {
	input       *inputEndpoint
	output      *outputEndpoint
	seeker      io.Seeker
	closer      io.Closer
	ownedInput  inputEndpoint
	ownedOutput outputEndpoint
}

// OpenIO installs the implemented Lua 5.1 IO library surface.
//
// Files are opaque runtime userdata. Standard files borrow the State streams;
// files returned by open own their operating-system handle. Opening again
// installs a fresh library, private defaults, functions, and standard userdata
// while preserving the registry's canonical FILE* metatable. Functions
// retained from an earlier opening keep their earlier default input and
// output.
func (state *State) OpenIO() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	metatable, err := state.ensureFileMetatable()
	if err != nil {
		return err
	}

	environment := newTable(state.runtime, 2, 1)
	standardEnvironment := newTable(state.runtime, 0, 1)
	library := newTable(
		state.runtime,
		0,
		len(ioLibraryFunctions)+3,
	)

	closeFunction, err := state.newIOFunction(environment, ioClose)
	if err != nil {
		return err
	}
	noCloseFunction, err := state.newIOFunction(
		environment,
		fileNoClose,
	)
	if err != nil {
		return err
	}
	if err := environment.RawSetString(
		"__close",
		closeFunction.Value(),
	); err != nil {
		return err
	}
	if err := standardEnvironment.RawSetString(
		"__close",
		noCloseFunction.Value(),
	); err != nil {
		return err
	}

	for _, definition := range ioLibraryFunctions {
		function, functionErr := state.newIOFunction(
			environment,
			definition.entry,
		)
		if functionErr != nil {
			return functionErr
		}
		if setErr := library.RawSetString(
			definition.name,
			function.Value(),
		); setErr != nil {
			return setErr
		}
	}
	for _, definition := range fileLibraryFunctions {
		function, functionErr := state.newIOFunction(
			environment,
			definition.entry,
		)
		if functionErr != nil {
			return functionErr
		}
		if setErr := metatable.RawSetString(
			definition.name,
			function.Value(),
		); setErr != nil {
			return setErr
		}
	}
	if err := metatable.RawSetString(
		"__index",
		metatable.Value(),
	); err != nil {
		return err
	}

	stdin, err := state.newStandardFile(
		&fileHandle{input: &state.streams.stdin},
		metatable,
		standardEnvironment,
	)
	if err != nil {
		return err
	}
	stdout, err := state.newStandardFile(
		&fileHandle{output: &state.streams.stdout},
		metatable,
		standardEnvironment,
	)
	if err != nil {
		return err
	}
	stderr, err := state.newStandardFile(
		&fileHandle{output: &state.streams.stderr},
		metatable,
		standardEnvironment,
	)
	if err != nil {
		return err
	}

	environment.rawSetIntegerSlot(
		ioDefaultInput,
		slotFromValue(stdin.Value()),
	)
	environment.rawSetIntegerSlot(
		ioDefaultOutput,
		slotFromValue(stdout.Value()),
	)
	for _, field := range [...]struct {
		name string
		data *UserData
	}{
		{name: "stdin", data: stdin},
		{name: "stdout", data: stdout},
		{name: "stderr", data: stderr},
	} {
		if err := library.RawSetString(
			field.name,
			field.data.Value(),
		); err != nil {
			return err
		}
	}

	if err := state.globalEnvironment().RawSetString(
		"io",
		library.Value(),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "io", slotFromTable(library))
	return nil
}

func (state *State) ensureFileMetatable() (*Table, error) {
	if existing, found := state.registry.rawStringSlot(
		luaFileHandleRegistryKey,
	); found {
		if existing.kind() != TableKind {
			return nil, fmt.Errorf(
				"lua: registry %s must be a table",
				luaFileHandleRegistryKey,
			)
		}
		return (*Table)(existing.ref), nil
	}
	metatable := newTable(
		state.runtime,
		0,
		len(fileLibraryFunctions)+1,
	)
	if err := state.registry.RawSetString(
		luaFileHandleRegistryKey,
		metatable.Value(),
	); err != nil {
		return nil, err
	}
	return metatable, nil
}

func (state *State) newIOFunction(
	environment *Table,
	entry NativeFunc,
) (*Function, error) {
	function, err := state.NewNativeFunction(entry)
	if err != nil {
		return nil, err
	}
	function.environment = environment
	return function, nil
}

func (state *State) newStandardFile(
	handle *fileHandle,
	metatable *Table,
	environment *Table,
) (*UserData, error) {
	data, err := state.newBorrowedUserData(handle)
	if err != nil {
		return nil, err
	}
	classifyManagedUserData(data, &fileResourceClass, metatable)
	data.environment = environment
	return data, nil
}

func (state *State) newRegularFile(
	file *os.File,
	metatable *Table,
) (*UserData, error) {
	handle := &fileHandle{
		seeker: file,
		closer: file,
	}
	handle.ownedInput = newInputEndpoint(file)
	handle.ownedOutput = newOutputEndpoint(file)
	handle.input = &handle.ownedInput
	handle.output = &handle.ownedOutput
	data, err := state.newManagedUserData(
		handle,
		closeFileHandle,
	)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	classifyManagedUserData(data, &fileResourceClass, metatable)
	return data, nil
}

func closeFileHandle(value any) error {
	handle, ok := value.(*fileHandle)
	if !ok || handle == nil {
		return nil
	}
	var outputErr error
	if handle.output != nil {
		outputErr = handle.output.detach()
	}
	if handle.input != nil {
		handle.input.detach()
	}
	var closeErr error
	if handle.closer != nil {
		closeErr = handle.closer.Close()
	}
	handle.input = nil
	handle.output = nil
	handle.seeker = nil
	handle.closer = nil
	return errors.Join(outputErr, closeErr)
}

func ioType(frame Frame) Outcome {
	data, present := frame.UserData(0)
	if !present {
		if frame.Kind(0) == InvalidKind {
			return baseArgumentError(frame, 0, "value expected")
		}
		return frame.ReturnNil()
	}
	if !isManagedUserDataClass(data, &fileResourceClass) {
		return frame.ReturnNil()
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return frame.ReturnString("closed file")
	}
	lease.release()
	return frame.ReturnString("file")
}

func ioOpen(frame Frame) Outcome {
	filename, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	filename = luaCString(filename)

	mode := "r"
	if supplied, present := frame.argument(1); present &&
		supplied.kind() != NilKind {
		mode, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		mode = luaCString(mode)
	}

	flags, valid := fileOpenFlags(mode)
	if !valid {
		return ioFailureResult(
			frame,
			filename,
			syscall.EINVAL,
		)
	}
	file, err := os.OpenFile(filename, flags, 0o666)
	if err != nil {
		return ioFailureResult(frame, filename, err)
	}
	metatable, err := frame.State().ensureFileMetatable()
	if err != nil {
		_ = file.Close()
		return libraryError(frame, "%s", err)
	}
	data, err := frame.State().newRegularFile(file, metatable)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromValue(data.Value()),
	)
}

func fileOpenFlags(mode string) (int, bool) {
	if mode == "" {
		return 0, false
	}
	base := mode[0]
	if base != 'r' && base != 'w' && base != 'a' {
		return 0, false
	}
	plus := false
	binary := false
	for index := 1; index < len(mode); index++ {
		switch mode[index] {
		case '+':
			if plus {
				return 0, false
			}
			plus = true
		case 'b':
			if binary {
				return 0, false
			}
			binary = true
		default:
			return 0, false
		}
	}

	switch base {
	case 'r':
		if plus {
			return os.O_RDWR, true
		}
		return os.O_RDONLY, true
	case 'w':
		flags := os.O_CREATE | os.O_TRUNC
		if plus {
			return flags | os.O_RDWR, true
		}
		return flags | os.O_WRONLY, true
	default:
		flags := os.O_CREATE | os.O_APPEND
		if plus {
			return flags | os.O_RDWR, true
		}
		return flags | os.O_WRONLY, true
	}
}

func ioClose(frame Frame) Outcome {
	data, present := frame.UserData(0)
	if !present && frame.Kind(0) == InvalidKind {
		current, _ := frame.Environment().rawIntSlot(ioDefaultOutput)
		if current.kind() == UserDataKind {
			data = (*UserData)(current.ref)
			present = true
		}
	}
	if !present || !isManagedUserDataClass(data, &fileResourceClass) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}

	lease, open := acquireManagedResource(data)
	if !open {
		return libraryError(frame, "attempt to use a closed file")
	}
	if !lease.owned {
		lease.release()
		return frame.returnCompactValues(
			[2]slot{
				nilSlot,
				stringSlot(frame.thread.owner.strings.make(
					"cannot close standard file",
				)),
			},
			2,
			nil,
		)
	}
	lease.release()
	_, err := closeManagedResource(data)
	if err != nil {
		return ioFailureResult(frame, "", err)
	}
	return frame.ReturnBool(true)
}

func fileNoClose(frame Frame) Outcome {
	return frame.returnCompactValues(
		[2]slot{
			nilSlot,
			stringSlot(frame.thread.owner.strings.make(
				"cannot close standard file",
			)),
		},
		2,
		nil,
	)
}

func fileCollect(frame Frame) Outcome {
	data, present := frame.UserData(0)
	if !present || !isManagedUserDataClass(data, &fileResourceClass) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return frame.Return()
	}
	owned := lease.owned
	lease.release()
	if owned {
		_, _ = closeManagedResource(data)
	}
	return frame.Return()
}

func fileToString(frame Frame) Outcome {
	data, present := frame.UserData(0)
	if !present || !isManagedUserDataClass(data, &fileResourceClass) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return frame.ReturnString("file (closed)")
	}
	handle, ok := lease.value.(*fileHandle)
	if !ok || handle == nil {
		lease.release()
		return frame.ReturnString("file (closed)")
	}
	text := fmt.Sprintf("file (%p)", handle)
	lease.release()
	return frame.ReturnString(text)
}

func ioInput(frame Frame) Outcome {
	return ioDefaultFile(frame, ioDefaultInput, "r")
}

func ioOutput(frame Frame) Outcome {
	return ioDefaultFile(frame, ioDefaultOutput, "w")
}

func ioDefaultFile(
	frame Frame,
	defaultIndex int,
	mode string,
) Outcome {
	argument, present := frame.argument(0)
	if present && argument.kind() != NilKind {
		var data *UserData
		if argument.kind() == StringKind ||
			argument.kind() == NumberKind {
			filename, _ := compactText(argument)
			filename = luaCString(filename)
			flags, _ := fileOpenFlags(mode)
			file, err := os.OpenFile(filename, flags, 0o666)
			if err != nil {
				return baseArgumentError(
					frame,
					0,
					ioFailureMessage(filename, err),
				)
			}
			metatable, metaErr :=
				frame.State().ensureFileMetatable()
			if metaErr != nil {
				_ = file.Close()
				return libraryError(frame, "%s", metaErr)
			}
			data, err = frame.State().newRegularFile(file, metatable)
			if err != nil {
				return libraryError(frame, "%s", err)
			}
		} else {
			if argument.kind() == UserDataKind {
				data = (*UserData)(argument.ref)
			}
			if !isManagedUserDataClass(data, &fileResourceClass) {
				return baseArgumentTypeError(
					frame,
					0,
					luaFileHandleRegistryKey,
				)
			}
			lease, open := acquireManagedResource(data)
			if !open {
				return libraryError(
					frame,
					"attempt to use a closed file",
				)
			}
			lease.release()
		}
		frame.Environment().rawSetIntegerSlot(
			defaultIndex,
			slotFromValue(data.Value()),
		)
	}
	current, _ := frame.Environment().rawIntSlot(defaultIndex)
	return frame.returnOne(frame.activation(), current)
}

func ioFailureResult(
	frame Frame,
	filename string,
	failure error,
) Outcome {
	message := ioFailureMessage(filename, failure)
	code := ioFailureCode(failure)
	return frame.returnCompactValues(
		[2]slot{
			nilSlot,
			stringSlot(frame.thread.owner.strings.make(message)),
		},
		2,
		[]slot{numberSlot(float64(code))},
	)
}

func ioFailureMessage(filename string, failure error) string {
	cause := ioFailureCause(failure)
	if filename == "" {
		return cause.Error()
	}
	return filename + ": " + cause.Error()
}

func ioFailureCode(failure error) int {
	cause := ioFailureCause(failure)
	var errno syscall.Errno
	if errors.As(cause, &errno) {
		return int(errno)
	}
	return 0
}

func ioFailureCause(failure error) error {
	var pathError *os.PathError
	if errors.As(failure, &pathError) && pathError.Err != nil {
		return pathError.Err
	}
	return failure
}
