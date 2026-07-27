package lua

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type ioOperationShortWriter struct {
	calls int
	err   error
}

func (writer *ioOperationShortWriter) Write(
	buffer []byte,
) (int, error) {
	writer.calls++
	if writer.err != nil {
		return 0, writer.err
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	return len(buffer) - 1, nil
}

type ioOperationBuffer struct {
	bytes.Buffer
	writes int
}

func (writer *ioOperationBuffer) Write(
	buffer []byte,
) (int, error) {
	writer.writes++
	return writer.Buffer.Write(buffer)
}

func (writer *ioOperationBuffer) WriteString(
	text string,
) (int, error) {
	writer.writes++
	return writer.Buffer.WriteString(text)
}

type ioOperationFailingSeeker struct {
	calls int
	err   error
}

func (seeker *ioOperationFailingSeeker) Seek(
	int64,
	int,
) (int64, error) {
	seeker.calls++
	return 0, seeker.err
}

type ioOperationFaultingFile struct {
	file *os.File
	err  error
	done bool
}

func (file *ioOperationFaultingFile) Read(buffer []byte) (int, error) {
	count, err := file.file.Read(buffer)
	if !file.done && count != 0 {
		file.done = true
		return count, file.err
	}
	return count, err
}

func (file *ioOperationFaultingFile) Seek(
	offset int64,
	origin int,
) (int64, error) {
	return file.file.Seek(offset, origin)
}

func TestIOWriteUsesBinaryStringsAndLuaNumberSpelling(t *testing.T) {
	output := &ioOperationBuffer{}
	state := newStateWithIO(t, Options{Stdout: output})
	defer state.Close()

	function := ioOperationFunction(t, state, ioWrite)
	results, err := state.Call(
		function.Value(),
		state.String("a\x00b|"),
		Number(1.0/3.0),
		state.String("|"),
		Number(math.Inf(-1)),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))

	var expected [32]byte
	want := "a\x00b|" +
		string(appendLuaNumber(expected[:0], 1.0/3.0)) +
		"|-inf"
	if got := output.String(); got != want {
		t.Fatalf("io.write output = %q; want %q", got, want)
	}
}

func TestIOWriteStopsBeforeValidatingLaterArguments(t *testing.T) {
	writer := &ioOperationShortWriter{}
	state := newStateWithIO(t, Options{})
	defer state.Close()
	data := newIOOperationFile(
		t,
		state,
		&fileHandle{
			output: ioOperationOutput(writer),
		},
	)
	function := ioOperationFunction(t, state, fileWrite)
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(
		function.Value(),
		data.Value(),
		state.String("first"),
		table.Value(),
	)
	if err != nil {
		t.Fatalf("later invalid argument was inspected: %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d; want 1", writer.calls)
	}
	assertTestValues(t, results[:1], Nil())
	message, _ := results[1].AsString()
	if message != io.ErrShortWrite.Error() {
		t.Fatalf("short-write message = %q", message)
	}
	if code, _ := results[2].AsNumber(); code != 0 {
		t.Fatalf("short-write errno = %v; want 0", results[2])
	}

	writer.err = syscall.ENOSPC
	results, err = state.Call(
		function.Value(),
		data.Value(),
		state.String("second"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if code, _ := results[2].AsNumber(); code !=
		float64(syscall.ENOSPC) {
		t.Fatalf("ENOSPC errno = %v", results[2])
	}

	deferredWriter := &ioOperationShortWriter{
		err: syscall.ENOSPC,
	}
	deferredOutput := ioOperationOutput(deferredWriter)
	if err := deferredOutput.setBuffering(
		streamBufferFull,
		32,
	); err != nil {
		t.Fatal(err)
	}
	deferredData := newIOOperationFile(
		t,
		state,
		&fileHandle{output: deferredOutput},
	)
	results, err = state.Call(
		function.Value(),
		deferredData.Value(),
		state.String("deferred"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	flush := ioOperationFunction(t, state, fileFlush)
	results, err = state.Call(flush.Value(), deferredData.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if code, _ := results[2].AsNumber(); code !=
		float64(syscall.ENOSPC) {
		t.Fatalf("deferred flush errno = %v", results[2])
	}
}

func TestIOWriteRestoresTheLogicalReadCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	handle := newIOOperationOSHandle(file)
	defer closeFileHandle(handle, nativeRelease{
		reason: nativeReleaseExplicit,
	})

	first, err := handle.input.ReadByte()
	if err != nil || first != 'a' {
		t.Fatalf("first read = (%q, %v)", first, err)
	}
	if unread := handle.input.unreadBytes(); unread != 5 {
		t.Fatalf("read-ahead = %d; want 5", unread)
	}
	if err := handle.prepareWrite(); err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(handle.output, "X"); err != nil {
		t.Fatal(err)
	}
	if err := handle.output.Flush(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "aXcdef" {
		t.Fatalf("mixed read/write content = %q", got)
	}
	if unread := handle.input.unreadBytes(); unread != 0 {
		t.Fatalf("read-ahead after write transition = %d", unread)
	}
}

func TestIOSeekUsesTheLogicalCursorAndPreservesReadAheadOnFailure(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "seek")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	handle := newIOOperationOSHandle(file)
	defer closeFileHandle(handle, nativeRelease{
		reason: nativeReleaseExplicit,
	})

	buffer := make([]byte, 2)
	if _, err := io.ReadFull(handle.input, buffer); err != nil {
		t.Fatal(err)
	}
	position, err := handle.seek(1, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if position != 3 {
		t.Fatalf("logical SEEK_CUR position = %d; want 3", position)
	}
	next, err := handle.input.ReadByte()
	if err != nil || next != 'd' {
		t.Fatalf("post-seek read = (%q, %v); want d", next, err)
	}

	failure := errors.New("seek failed")
	failing := &ioOperationFailingSeeker{err: failure}
	handle.seeker = failing
	unread := handle.input.unreadBytes()
	if unread == 0 {
		t.Fatal("test did not establish input read-ahead")
	}
	if _, err := handle.seek(0, io.SeekCurrent); !errors.Is(
		err,
		failure,
	) {
		t.Fatalf("failed seek = %v", err)
	}
	if got := handle.input.unreadBytes(); got != unread {
		t.Fatalf(
			"failed seek discarded read-ahead: %d -> %d",
			unread,
			got,
		)
	}
	next, err = handle.input.ReadByte()
	if err != nil || next != 'e' {
		t.Fatalf("read after failed seek = (%q, %v); want e", next, err)
	}
}

func TestIOSeekCountsReplayAfterADeferredReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay-cursor")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sentinel := errors.New("deferred read failure")
	faulting := &ioOperationFaultingFile{
		file: file,
		err:  sentinel,
	}
	engine, _ := newTestIOReadEngine(
		faulting,
		defaultIOReadLimit,
	)
	if _, _, err := engine.readBytes(1); !errors.Is(err, sentinel) {
		t.Fatalf("faulting read = %v", err)
	}
	if unread := engine.input.unreadBytes(); unread != 5 {
		t.Fatalf("replay unread bytes = %d; want 5", unread)
	}

	handle := &fileHandle{
		input:  engine.input,
		seeker: faulting,
	}
	position, err := handle.seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if position != 1 {
		t.Fatalf("logical replay position = %d; want 1", position)
	}
	next, err := handle.input.ReadByte()
	if err != nil || next != 'b' {
		t.Fatalf("read after replay seek = (%q, %v); want b", next, err)
	}
}

func TestIOTransitionsFlushOutputAndAppendRemainsAnOSPolicy(
	t *testing.T,
) {
	output := &ioOperationBuffer{}
	endpoint := newOutputEndpoint(output)
	if err := endpoint.setBuffering(streamBufferFull, 16); err != nil {
		t.Fatal(err)
	}
	handle := &fileHandle{
		input:  ioOperationInput(strings.NewReader("x")),
		output: &endpoint,
	}
	if err := writeFileString(handle.output, "pending"); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("full-buffered output was visible before prepareRead")
	}
	if err := handle.prepareRead(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "pending" {
		t.Fatalf("prepareRead output = %q", output.String())
	}
	readFailure := errors.New("injected read failure")
	failedInput := newInputEndpoint(&streamErrorReader{
		err: readFailure,
	})
	failedReadHandle := &fileHandle{input: &failedInput}
	if err := failedReadHandle.prepareRead(); err != nil {
		t.Fatal(err)
	}
	if _, err := failedReadHandle.input.ReadByte(); !errors.Is(
		err,
		readFailure,
	) {
		t.Fatalf("read after transition = %v", err)
	}

	path := filepath.Join(t.TempDir(), "append")
	if err := os.WriteFile(path, []byte("begin"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(
		path,
		os.O_RDWR|os.O_APPEND,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	appendHandle := newIOOperationOSHandle(file)
	defer closeFileHandle(appendHandle, nativeRelease{
		reason: nativeReleaseExplicit,
	})
	if _, err := appendHandle.seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := appendHandle.prepareWrite(); err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(appendHandle.output, "-end"); err != nil {
		t.Fatal(err)
	}
	if err := appendHandle.output.Flush(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "begin-end" {
		t.Fatalf("append after seek = %q", got)
	}
}

func TestIOBufferingIsSharedWithPrintAndFlush(t *testing.T) {
	output := &ioOperationBuffer{}
	state := newStateWithIO(t, Options{Stdout: output})
	defer state.Close()

	setBuffering := ioOperationFunction(t, state, fileSetBuffering)
	write := ioOperationFunction(t, state, ioWrite)
	flush := ioOperationFunction(t, state, ioFlush)
	stdout := ioFileField(t, ioLibraryTable(t, state), "stdout")

	results, err := state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("full"),
		Number(32),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	if _, err := state.Call(
		write.Value(),
		state.String("buffered"),
	); err != nil {
		t.Fatal(err)
	}
	runIOChunk(t, state, `print(" print")`)
	if output.Len() != 0 {
		t.Fatalf("buffered output became visible: %q", output.String())
	}
	if _, err := state.Call(flush.Value()); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "buffered print\n" {
		t.Fatalf("flushed stdout = %q", got)
	}

	output.Reset()
	if _, err := state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("line"),
		Number(32),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(
		write.Value(),
		state.String("line\ntrailing"),
	); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "line\n" {
		t.Fatalf("line-buffered visible output = %q", got)
	}
	if _, err := state.Call(flush.Value()); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "line\ntrailing" {
		t.Fatalf("line-buffered flushed output = %q", got)
	}

	results, err = state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("no"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	output.Reset()
	if _, err := state.Call(
		write.Value(),
		state.String("immediate"),
	); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "immediate" {
		t.Fatalf("unbuffered output = %q", got)
	}

	results, err = state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("full"),
		Number(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	results, err = state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("full"),
		Number(-1),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if code, _ := results[2].AsNumber(); code !=
		float64(syscall.EINVAL) {
		t.Fatalf("negative setvbuf errno = %v", results[2])
	}

	beforeMode := state.streams.stdout.mode
	beforeSize := state.streams.stdout.bufferSize
	results, err = state.Call(
		setBuffering.Value(),
		stdout.Value(),
		state.String("full"),
		Number(math.Inf(1)),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if code, _ := results[2].AsNumber(); code !=
		float64(syscall.EINVAL) {
		t.Fatalf("huge setvbuf errno = %v", results[2])
	}
	if state.streams.stdout.mode != beforeMode ||
		state.streams.stdout.bufferSize != beforeSize {
		t.Fatal("rejected setvbuf changed the previous output policy")
	}
}

func TestIOSetBufferingReconfiguresInputWithoutLosingReadAhead(
	t *testing.T,
) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	input := newInputEndpoint(strings.NewReader("abcdef"))
	handle := &fileHandle{input: &input}
	data := newIOOperationFile(t, state, handle)
	setBuffering := ioOperationFunction(t, state, fileSetBuffering)
	read := ioOperationFunction(t, state, fileRead)

	results, err := state.Call(
		setBuffering.Value(),
		data.Value(),
		state.String("full"),
		Number(32),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	results, err = state.Call(
		read.Value(),
		data.Value(),
		Number(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("a"))
	if input.buffered == nil || input.buffered.Size() != 32 {
		t.Fatalf("full input buffer = %#v", input.buffered)
	}

	results, err = state.Call(
		setBuffering.Value(),
		data.Value(),
		state.String("no"),
		Number(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	if input.mode != streamBufferNone || input.buffered != nil {
		t.Fatal("input no-buffer mode did not release the old reader")
	}
	results, err = state.Call(
		read.Value(),
		data.Value(),
		Number(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("b"))

	results, err = state.Call(
		setBuffering.Value(),
		data.Value(),
		state.String("line"),
		Number(64),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	results, err = state.Call(
		read.Value(),
		data.Value(),
		state.String("*a"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("cdef"))
	if input.buffered == nil || input.buffered.Size() != 64 {
		t.Fatalf("line input buffer = %#v", input.buffered)
	}
}

func TestIOSeekReportsPendingFlushFailureBeforeSeeking(t *testing.T) {
	writeFailure := syscall.ENOSPC
	writer := &ioOperationShortWriter{err: writeFailure}
	output := newOutputEndpoint(writer)
	if err := output.setBuffering(streamBufferFull, 32); err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(&output, "pending"); err != nil {
		t.Fatal(err)
	}
	seeker := &ioOperationFailingSeeker{
		err: errors.New("seek should not run"),
	}
	handle := &fileHandle{
		output: &output,
		seeker: seeker,
	}
	if _, err := handle.seek(0, io.SeekStart); !errors.Is(
		err,
		writeFailure,
	) {
		t.Fatalf("seek flush failure = %v", err)
	}
	if seeker.calls != 0 {
		t.Fatalf("seeker was called %d times after flush failure", seeker.calls)
	}
}

func TestIOOperationClosedAndIdentityDiagnostics(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	write := ioOperationFunction(t, state, fileWrite)

	path := filepath.Join(t.TempDir(), "closed")
	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.newRegularFile(
		file,
		os.O_RDWR,
		fileMetatable(t, state),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closeManagedResource(data); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(
		write.Value(),
		data.owningValue(),
		state.String("x"),
	); err == nil || !strings.Contains(
		err.Error(),
		"attempt to use a closed file",
	) {
		t.Fatalf("closed file.write error = %v", err)
	}

	forged := newIOOperationFile(
		t,
		state,
		&fileHandle{
			output: ioOperationOutput(io.Discard),
		},
	)
	other, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	forged.runtimeObject().metatable = other
	if _, err := state.Call(
		write.Value(),
		forged.Value(),
		state.String("x"),
	); err == nil || !strings.Contains(
		err.Error(),
		"FILE* expected, got userdata",
	) {
		t.Fatalf("noncanonical file.write error = %v", err)
	}

	defaultWrite := ioOperationFunction(t, state, ioWrite)
	stdout := ioFileField(t, ioLibraryTable(t, state), "stdout")
	if _, err := stdout.runtimeObject().resource.resource.release(nativeRelease{
		reason: nativeReleaseExplicit,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(
		defaultWrite.Value(),
		state.String("x"),
	); err == nil || !strings.Contains(
		err.Error(),
		"standard output file is closed",
	) {
		t.Fatalf("closed default io.write error = %v", err)
	}
}

func TestIOSeekNativeArgumentsAndFailureTuple(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	seek := ioOperationFunction(t, state, fileSeek)

	path := filepath.Join(t.TempDir(), "native-seek")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.newRegularFile(
		file,
		os.O_RDWR,
		fileMetatable(t, state),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		seek.Value(),
		data.owningValue(),
		state.String("set\x00ignored"),
		state.String("2.9"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(2))

	failing := &ioOperationFailingSeeker{err: syscall.ESPIPE}
	failedData := newIOOperationFile(
		t,
		state,
		&fileHandle{seeker: failing},
	)
	results, err = state.Call(
		seek.Value(),
		failedData.Value(),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if code, _ := results[2].AsNumber(); code !=
		float64(syscall.ESPIPE) {
		t.Fatalf("seek failure errno = %v", results[2])
	}
}

func TestIOWarmWriteOperationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	output := &ioOperationBuffer{}
	state := newStateWithIO(t, Options{})
	defer state.Close()
	endpoint := ioOperationOutput(output)
	if err := endpoint.setBuffering(
		streamBufferFull,
		64,
	); err != nil {
		t.Fatal(err)
	}
	data := newIOOperationFile(
		t,
		state,
		&fileHandle{output: endpoint},
	)
	write := ioOperationFunction(t, state, fileWrite)
	arguments := [...]Value{data.Value(), Number(12.5)}
	var destination [1]Value

	run := func() {
		output.Reset()
		count, err := state.CallInto(
			write.Value(),
			arguments[:],
			destination[:],
		)
		if err != nil || count != 1 {
			panic("warm file write failed")
		}
		if err := endpoint.Flush(); err != nil {
			panic("warm file flush failed")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1_000, run); allocations != 0 {
		t.Fatalf(
			"warm compact file.write allocations = %.2f; want 0",
			allocations,
		)
	}
}

func TestIOWarmUnbufferedNumberWriteDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	output := &ioOperationBuffer{}
	state := newStateWithIO(t, Options{})
	defer state.Close()
	endpoint := ioOperationOutput(output)
	data := newIOOperationFile(
		t,
		state,
		&fileHandle{output: endpoint},
	)
	write := ioOperationFunction(t, state, fileWrite)
	arguments := [...]Value{data.Value(), Number(12.5)}
	var destination [1]Value

	run := func() {
		output.Reset()
		count, err := state.CallInto(
			write.Value(),
			arguments[:],
			destination[:],
		)
		if err != nil || count != 1 {
			panic("warm unbuffered file write failed")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1_000, run); allocations != 0 {
		t.Fatalf(
			"warm unbuffered numeric write allocations = %.2f; want 0",
			allocations,
		)
	}
}

func ioOperationFunction(
	t testing.TB,
	state *State,
	entry NativeFunc,
) *Function {
	t.Helper()
	anchor := ioFunctionField(t, ioLibraryTable(t, state), "open")
	environment, err := state.FunctionEnvironment(anchor)
	if err != nil {
		t.Fatal(err)
	}
	function, err := state.newIOFunction(environment, entry)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func newIOOperationFile(
	t testing.TB,
	state *State,
	handle *fileHandle,
) *UserData {
	t.Helper()
	data, err := state.newBorrowedUserData(handle)
	if err != nil {
		t.Fatal(err)
	}
	classifyManagedUserData(
		data,
		&fileResourceClass,
		fileMetatable(t, state),
	)
	return data.owningHandle()
}

func ioOperationInput(reader io.Reader) *inputEndpoint {
	endpoint := newInputEndpoint(reader)
	return &endpoint
}

func ioOperationOutput(writer io.Writer) *outputEndpoint {
	endpoint := newOutputEndpoint(writer)
	return &endpoint
}

func newIOOperationOSHandle(file *os.File) *fileHandle {
	handle := &fileHandle{
		seeker: file,
		closer: file,
	}
	handle.ownedInput = newInputEndpoint(file)
	handle.ownedOutput = newOutputEndpoint(file)
	handle.input = &handle.ownedInput
	handle.output = &handle.ownedOutput
	return handle
}
