package lua

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	{name: "flush", entry: ioFlush},
	{name: "input", entry: ioInput},
	{name: "lines", entry: ioLines},
	{name: "open", entry: ioOpen},
	{name: "output", entry: ioOutput},
	{name: "popen", entry: ioPopen},
	{name: "read", entry: ioRead},
	{name: "tmpfile", entry: ioTempFile},
	{name: "type", entry: ioType},
	{name: "write", entry: ioWrite},
}

var fileLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "close", entry: fileClose},
	{name: "flush", entry: fileFlush},
	{name: "lines", entry: fileLines},
	{name: "read", entry: fileRead},
	{name: "seek", entry: fileSeek},
	{name: "setvbuf", entry: fileSetBuffering},
	{name: "write", entry: fileWrite},
	{name: "__gc", entry: fileCollect},
	{name: "__tostring", entry: fileToString},
}

// fileHandle is the one native object behind a Lua file userdata. Standard
// files borrow the State's shared endpoints; regular and process files own
// their endpoints and closer. Read, write, seek, and buffering operations
// extend this same object rather than introducing another representation.
type fileHandle struct {
	input       *inputEndpoint
	output      *outputEndpoint
	seeker      io.Seeker
	closer      io.Closer
	ownedInput  inputEndpoint
	ownedOutput outputEndpoint
	process     *childProcess
}

// OpenIO installs the Lua 5.1 IO library.
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

	environment := newTable(state, 2, 1)
	standardEnvironment := newTable(state, 0, 1)
	library := newTable(
		state,
		0,
		len(ioLibraryFunctions)+3,
	)

	closeFunction, err := state.newIOFunction(
		environment,
		fileClose,
	)
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
	if err := environment.rawSetStringSlot(
		"__close",
		slotFromFunctionObject(closeFunction),
	); err != nil {
		return err
	}
	if err := standardEnvironment.rawSetStringSlot(
		"__close",
		slotFromFunctionObject(noCloseFunction),
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
		if setErr := library.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(function),
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
		if setErr := metatable.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(function),
		); setErr != nil {
			return setErr
		}
	}
	if err := metatable.rawSetStringSlot(
		"__index",
		slotFromTableObject(metatable),
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
		slotFromUserDataObject(stdin),
	)
	environment.rawSetIntegerSlot(
		ioDefaultOutput,
		slotFromUserDataObject(stdout),
	)
	for _, field := range [...]struct {
		name string
		data *userDataObject
	}{
		{name: "stdin", data: stdin},
		{name: "stdout", data: stdout},
		{name: "stderr", data: stderr},
	} {
		library.rawSetSlot(
			stringSlot(state.runtime.strings.make(field.name)),
			slotFromUserDataObject(field.data),
		)
	}

	if err := state.globalEnvironment().rawSetStringSlot(
		"io",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "io", slotFromTableObject(library))
	return nil
}

func (state *State) ensureFileMetatable() (*tableObject, error) {
	if existing, found := state.registry.rawStringSlot(
		luaFileHandleRegistryKey,
	); found {
		if !existing.isTable() {
			return nil, fmt.Errorf(
				"lua: registry %s must be a table",
				luaFileHandleRegistryKey,
			)
		}
		return (*tableObject)(existing.ref), nil
	}
	metatable := newTable(
		state,
		0,
		len(fileLibraryFunctions)+1,
	)
	if err := state.registry.rawSetStringSlot(
		luaFileHandleRegistryKey,
		slotFromTableObject(metatable),
	); err != nil {
		return nil, err
	}
	return metatable, nil
}

func (state *State) newIOFunction(
	environment *tableObject,
	entry NativeFunc,
) (*functionObject, error) {
	function, err := state.newNativeFunctionObject(entry, nil)
	if err != nil {
		return nil, err
	}
	function.environment = environment
	return function, nil
}

func (state *State) newStandardFile(
	handle *fileHandle,
	metatable *tableObject,
	environment *tableObject,
) (*userDataObject, error) {
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
	flags int,
	metatable *tableObject,
) (*userDataObject, error) {
	return state.newOwnedFile(file, file, flags, metatable)
}

func (state *State) newOwnedFile(
	file *os.File,
	closer io.Closer,
	flags int,
	metatable *tableObject,
) (*userDataObject, error) {
	handle := &fileHandle{
		seeker: file,
		closer: closer,
	}
	readable, writable := fileOpenCapabilities(flags)
	if readable {
		handle.ownedInput = newFileInputEndpoint(file, flags)
		handle.input = &handle.ownedInput
	}
	if writable {
		handle.ownedOutput = newOutputEndpoint(file)
		// C file streams buffer regular output by default. Standard Go
		// writers remain unbuffered until setvbuf requests otherwise.
		handle.ownedOutput.mode = streamBufferFull
		handle.output = &handle.ownedOutput
	}
	data, err := state.newManagedUserData(
		handle,
		closeFileHandle,
	)
	if err != nil {
		_ = closer.Close()
		return nil, err
	}
	classifyManagedUserData(data, &fileResourceClass, metatable)
	return data, nil
}

type temporaryFileCloser struct {
	file *os.File
	path string
}

func (temporary *temporaryFileCloser) Close() error {
	if temporary == nil {
		return nil
	}
	var closeErr error
	if temporary.file != nil {
		closeErr = temporary.file.Close()
		temporary.file = nil
	}
	removeErr := os.Remove(temporary.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	temporary.path = ""
	return errors.Join(closeErr, removeErr)
}

func closeFileHandle(value any, release nativeRelease) error {
	handle, ok := value.(*fileHandle)
	if !ok || handle == nil {
		return nil
	}
	if handle.process != nil {
		return closeProcessFileHandle(handle, release)
	}
	return closeRegularFileHandle(handle)
}

func closeRegularFileHandle(handle *fileHandle) error {
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
	data, present := frame.userDataObject(0)
	if !present {
		if frame.Kind(0) == InvalidKind {
			return baseArgumentError(frame, 0, "value expected")
		}
		return frame.ReturnNil()
	}
	if !isFileUserData(frame.thread.state, data) {
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
		!supplied.isNil() {
		mode, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		mode = luaCString(mode)
	}

	flags, valid := fileOpenFlags(mode)
	if !valid {
		return ioNamedFailureResult(
			frame,
			filename,
			syscall.EINVAL,
		)
	}
	file, err := os.OpenFile(filename, flags, 0o666)
	if err != nil {
		return ioNamedFailureResult(frame, filename, err)
	}
	metatable, err := frame.State().ensureFileMetatable()
	if err != nil {
		_ = file.Close()
		return libraryError(frame, "%s", err)
	}
	data, err := frame.State().newRegularFile(file, flags, metatable)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromUserDataObject(data),
	)
}

func ioTempFile(frame Frame) Outcome {
	metatable, err := frame.State().ensureFileMetatable()
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	file, err := os.CreateTemp("", "lunik-")
	if err != nil {
		return ioFailureResult(frame, err)
	}
	closer := &temporaryFileCloser{
		file: file,
		path: file.Name(),
	}
	data, err := frame.State().newOwnedFile(
		file,
		closer,
		os.O_RDWR,
		metatable,
	)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromUserDataObject(data),
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

func fileOpenCapabilities(flags int) (readable, writable bool) {
	switch flags & (os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		return false, true
	case os.O_RDWR:
		return true, true
	default:
		return true, false
	}
}

func ioClose(frame Frame) Outcome {
	data, present := frame.userDataObject(0)
	if !present && frame.Kind(0) == InvalidKind {
		current, _ := frame.environmentObject().rawIntSlot(ioDefaultOutput)
		if current.isUserData() {
			data = userDataObjectFromSlot(current)
			present = true
		}
	}
	if !present || !isFileUserData(frame.thread.state, data) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	return closeFileUserData(frame, data)
}

func fileClose(frame Frame) Outcome {
	data, present := frame.userDataObject(0)
	if !present {
		// Lua 5.1's file:close path reports an absent receiver as nil,
		// unlike the other file methods, which report "no value".
		if frame.Kind(0) == InvalidKind {
			return baseArgumentError(
				frame,
				0,
				luaFileHandleRegistryKey+" expected, got nil",
			)
		}
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	if !isFileUserData(frame.thread.state, data) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	return closeFileUserData(frame, data)
}

func closeFileUserData(frame Frame, data *userDataObject) Outcome {
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
	_, err := closeManagedResourceContext(
		data,
		frame.Context(),
	)
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) {
			if failure.Category() == ContextError {
				if current := pollExecutionContext(frame.thread); current != nil {
					failure = current
				}
			}
			return frame.sealError(failure)
		}
		return ioFailureResult(frame, err)
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
	data, present := frame.userDataObject(0)
	if !present || !isFileUserData(frame.thread.state, data) {
		return baseArgumentTypeError(frame, 0, luaFileHandleRegistryKey)
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return frame.Return()
	}
	owned := lease.owned
	lease.release()
	if owned {
		// A process-backed file collected normally abandons its child, while
		// deterministic State shutdown terminates and reaps it. The shared
		// release seam records a close-time failure without turning __gc into
		// a Lua error or leaving the file visible to later finalizers.
		_, _ = releaseCollectedResource(frame.thread.state, data)
	}
	return frame.Return()
}

func fileToString(frame Frame) Outcome {
	data, present := frame.userDataObject(0)
	if !present || !isFileUserData(frame.thread.state, data) {
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
	if present && !argument.isNil() {
		var data *userDataObject
		if argument.isString() ||
			argument.isNumber() {
			filename, _ := compactText(argument)
			filename = luaCString(filename)
			flags, _ := fileOpenFlags(mode)
			file, err := os.OpenFile(filename, flags, 0o666)
			if err != nil {
				return baseArgumentError(
					frame,
					0,
					ioNamedFailureMessage(filename, err),
				)
			}
			metatable, metaErr :=
				frame.State().ensureFileMetatable()
			if metaErr != nil {
				_ = file.Close()
				return libraryError(frame, "%s", metaErr)
			}
			data, err = frame.State().newRegularFile(
				file,
				flags,
				metatable,
			)
			if err != nil {
				return libraryError(frame, "%s", err)
			}
		} else {
			if argument.isUserData() {
				data = userDataObjectFromSlot(argument)
			}
			if !isFileUserData(frame.thread.state, data) {
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
		frame.environmentObject().rawSetIntegerSlot(
			defaultIndex,
			slotFromUserDataObject(data),
		)
	}
	current, _ := frame.environmentObject().rawIntSlot(defaultIndex)
	return frame.returnOne(frame.activation(), current)
}

func isFileUserData(state *State, data *userDataObject) bool {
	if state == nil ||
		state.registry == nil ||
		!isManagedUserDataClass(data, &fileResourceClass) {
		return false
	}
	current, found := state.registry.rawStringSlot(
		luaFileHandleRegistryKey,
	)
	return found &&
		current.isTable() &&
		data.metatable == (*tableObject)(current.ref)
}

func ioFailureResult(
	frame Frame,
	failure error,
) Outcome {
	return ioFailureResultWithMessage(
		frame,
		ioFailureMessage(failure),
		failure,
	)
}

func ioNamedFailureResult(
	frame Frame,
	name string,
	failure error,
) Outcome {
	return ioFailureResultWithMessage(
		frame,
		ioNamedFailureMessage(name, failure),
		failure,
	)
}

func ioFailureResultWithMessage(
	frame Frame,
	message string,
	failure error,
) Outcome {
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

func ioFailureMessage(failure error) string {
	return ioFailureCause(failure).Error()
}

func ioNamedFailureMessage(name string, failure error) string {
	return name + ": " + ioFailureCause(failure).Error()
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
	var linkError *os.LinkError
	if errors.As(failure, &linkError) && linkError.Err != nil {
		return linkError.Err
	}
	var execError *exec.Error
	if errors.As(failure, &execError) && execError.Err != nil {
		return execError.Err
	}
	return failure
}
