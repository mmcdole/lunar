package lua

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	readerChunkBytes        = 8 << 10
	maxConsecutiveEmptyRead = 100
)

// ErrNilReader reports a nil Reader passed to Load.
var ErrNilReader = errors.New("lua: nil source reader")

// Load reads a Lua 5.1 source or native binary chunk from reader and returns a
// new Function in the executing Thread's global environment. Load does not
// execute the resulting chunk and never closes reader.
//
// Reader errors are returned unchanged. Data returned with an error is
// consumed before that error is reported. Transient reads returning (0, nil)
// are retried and eventually return io.ErrNoProgress.
func (state *State) Load(
	sourceName string,
	reader io.Reader,
) (*Function, error) {
	return state.loadReader(nil, sourceName, reader)
}

// LoadContext reads and loads a chunk like Load while observing ctx.
//
// A nil context returns ErrNilContext. Cancellation is returned as a *Error
// categorized ContextError. Cancellation is observed before and between
// Reader calls; it cannot interrupt a Reader already blocked inside Read.
// The resulting Function does not retain ctx.
func (state *State) LoadContext(
	ctx context.Context,
	sourceName string,
	reader io.Reader,
) (*Function, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	return state.loadReader(ctx, sourceName, reader)
}

func (state *State) loadReader(
	ctx context.Context,
	sourceName string,
	reader io.Reader,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, ErrNilReader
	}
	control, failure := newLoadControl(ctx, state.options.MaxLoadBytes)
	if failure != nil {
		return nil, failure
	}
	input, source := newReaderChunkInput(reader, &control, "")
	prototype, loadErr := loadInputPrototype(sourceName, input, &control)
	if readFailure := source.failure(); readFailure != nil {
		return nil, readFailure
	}
	if loadErr != nil {
		return nil, loadErr
	}
	return state.loadPrototypeObject(prototype).owningHandle(), nil
}

// LoadFile reads and loads a Lua 5.1 source or native binary file. The source
// name is "@" followed by path. A leading Unix interpreter line is ignored in
// the same way as Lua 5.1's loadfile. LoadFile closes the opened file on every
// outcome and does not execute the resulting chunk.
func (state *State) LoadFile(path string) (*Function, error) {
	return state.loadFile(nil, path)
}

// LoadFileContext reads and loads path like LoadFile while observing ctx.
//
// A nil context returns ErrNilContext. Cancellation is returned as a *Error
// categorized ContextError. Cancellation cannot interrupt an operating-system
// read already in progress. The resulting Function does not retain ctx.
func (state *State) LoadFileContext(
	ctx context.Context,
	path string,
) (*Function, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	return state.loadFile(ctx, path)
}

func (state *State) loadFile(
	ctx context.Context,
	path string,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	control, failure := newLoadControl(ctx, state.options.MaxLoadBytes)
	if failure != nil {
		return nil, failure
	}
	prototype, err := loadNamedFilePrototype(path, &control)
	if err != nil {
		return nil, err
	}
	return state.loadPrototypeObject(prototype).owningHandle(), nil
}

type readerChunkSource struct {
	reader      io.Reader
	control     *loadControl
	upstream    *inputEndpoint
	buffer      []byte
	readFailure error
}

func newReaderChunkInput(
	reader io.Reader,
	control *loadControl,
	prefix string,
) (*chunkInput, *readerChunkSource) {
	source := &readerChunkSource{
		reader:  reader,
		control: control,
	}
	input := newRefillableChunkInput(source.refill, control)
	if prefix != "" {
		input.piece = prefix
		input.windowEnd = len(prefix)
		input.pieceGeneration = 1
	}
	return input, source
}

func (source *readerChunkSource) refill() (string, error) {
	size := readerChunkBytes
	if control := source.control; control != nil && control.limit != 0 {
		remaining := uint64(0)
		if control.inputBytes < control.limit {
			remaining = control.limit - control.inputBytes
		}
		if remaining < uint64(size) {
			size = int(remaining) + 1
		}
	}

	if cap(source.buffer) < size {
		source.buffer = make([]byte, size)
	}
	buffer := source.buffer[:size]
	for emptyReads := 0; ; emptyReads++ {
		if failure := source.control.check(); failure != nil {
			return "", failure
		}
		count, err := source.reader.Read(buffer)
		if count < 0 || count > len(buffer) {
			failure := fmt.Errorf(
				"lua: Reader returned invalid byte count %d",
				count,
			)
			source.readFailure = failure
			return "", failure
		}
		if count != 0 {
			if err != nil && err != io.EOF {
				source.readFailure = err
			}
			// The Reader may reuse or mutate buffer on its next call.
			// chunkInput pieces are immutable because the lexer can borrow
			// token text until it crosses a refill boundary.
			return string(buffer[:count]), err
		}
		if err != nil {
			if err != io.EOF {
				source.readFailure = err
			}
			return "", err
		}
		if emptyReads+1 == maxConsecutiveEmptyRead {
			source.readFailure = io.ErrNoProgress
			return "", io.ErrNoProgress
		}
	}
}

func (source *readerChunkSource) failure() error {
	if source.readFailure != nil {
		failure := source.readFailure
		source.readFailure = nil
		return failure
	}
	if source.upstream != nil {
		return source.upstream.takeFailure()
	}
	return nil
}

type fileLoadError struct {
	operation string
	name      string
	cause     error
}

func (failure *fileLoadError) Error() string {
	detail := failure.cause
	if pathError, ok := failure.cause.(*os.PathError); ok {
		detail = pathError.Err
	}
	return fmt.Sprintf(
		"cannot %s %s: %v",
		failure.operation,
		failure.name,
		detail,
	)
}

func (failure *fileLoadError) Unwrap() error {
	return failure.cause
}

func loadNamedFilePrototype(
	path string,
	control *loadControl,
) (*Prototype, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, &fileLoadError{
			operation: "open",
			name:      path,
			cause:     err,
		}
	}
	defer file.Close()
	return loadFileReaderPrototype(
		"@"+path,
		path,
		file,
		control,
	)
}

func loadFileReaderPrototype(
	sourceName string,
	displayName string,
	reader io.Reader,
	control *loadControl,
) (*Prototype, error) {
	endpoint := newInputEndpoint(reader)
	return loadFileEndpointPrototype(
		sourceName,
		displayName,
		&endpoint,
		control,
	)
}

func loadFileEndpointPrototype(
	sourceName string,
	displayName string,
	endpoint *inputEndpoint,
	control *loadControl,
) (*Prototype, error) {
	input, source, err := newLuaFileInput(endpoint, control)
	if err != nil {
		if readFailure := endpoint.takeFailure(); readFailure != nil {
			err = readFailure
		}
		if _, luaFailure := err.(*Error); luaFailure {
			return nil, err
		}
		return nil, &fileLoadError{
			operation: "read",
			name:      displayName,
			cause:     err,
		}
	}
	prototype, loadErr := loadInputPrototype(
		sourceName,
		input,
		control,
	)
	if readFailure := source.failure(); readFailure != nil {
		return nil, &fileLoadError{
			operation: "read",
			name:      displayName,
			cause:     readFailure,
		}
	}
	return prototype, loadErr
}

// newLuaFileInput removes Lua 5.1's optional first-line interpreter directive.
// Text receives one synthetic newline so diagnostics retain their original
// line numbers; a binary chunk begins directly at its signature.
func newLuaFileInput(
	endpoint *inputEndpoint,
	control *loadControl,
) (*chunkInput, *readerChunkSource, error) {
	first, err := endpoint.ReadByte()
	if err == io.EOF {
		input, source := newReaderChunkInput(endpoint, control, "")
		source.upstream = endpoint
		return input, source, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if first != '#' {
		if err := endpoint.UnreadByte(); err != nil {
			panic("lua: failed to restore file lookahead")
		}
		input, source := newReaderChunkInput(endpoint, control, "")
		source.upstream = endpoint
		return input, source, nil
	}

	// Keep one byte uncharged. Text's synthetic newline consumes that credit;
	// a binary file charges it before exposing the signature.
	total := uint64(1)
	charged := uint64(0)
	chargeThrough := func(target uint64) error {
		if target <= charged {
			return nil
		}
		if failure := control.consume(target - charged); failure != nil {
			return failure
		}
		charged = target
		return nil
	}
	for {
		line, readErr := endpoint.ReadSlice('\n')
		total += uint64(len(line))
		if err := chargeThrough(total - 1); err != nil {
			return nil, nil, err
		}
		switch readErr {
		case nil:
			goto lineComplete
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			goto lineComplete
		default:
			return nil, nil, readErr
		}
	}

lineComplete:
	next, peekErr := endpoint.Peek(1)
	if peekErr != nil && peekErr != io.EOF {
		return nil, nil, peekErr
	}
	prefix := "\n"
	if len(next) != 0 && next[0] == 0x1b {
		if err := chargeThrough(total); err != nil {
			return nil, nil, err
		}
		prefix = ""
	}
	input, source := newReaderChunkInput(endpoint, control, prefix)
	source.upstream = endpoint
	return input, source, nil
}
