package lua

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

const (
	defaultStreamBufferBytes = 8 << 10
	maximumStreamBufferBytes = 64 << 20
)

type streamBufferMode uint8

const (
	streamBufferNone streamBufferMode = iota
	streamBufferFull
	streamBufferLine
)

var (
	errInvalidStreamBufferMode = errors.New(
		"lua: invalid stream buffering mode",
	)
	errStreamBufferTooLarge = errors.New(
		"lua: stream buffer is too large",
	)
)

// standardStreams owns the State-local view of the process streams. The
// endpoints are separate from State so a future borrowed file userdata can
// retain one without retaining the complete Lua object graph.
type standardStreams struct {
	stdin  inputEndpoint
	stdout outputEndpoint
	stderr outputEndpoint
}

func newStandardStreams(
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) *standardStreams {
	return &standardStreams{
		stdin:  newInputEndpoint(stdin),
		stdout: newOutputEndpoint(stdout),
		stderr: newOutputEndpoint(stderr),
	}
}

func (streams *standardStreams) release() error {
	if streams == nil {
		return nil
	}
	streams.stdin.detach()
	stdoutErr := streams.stdout.detach()
	stderrErr := streams.stderr.detach()
	return errors.Join(stdoutErr, stderrErr)
}

// inputEndpoint gives every consumer of one input stream a single logical
// cursor. Its bufio.Reader and backing byte slice are allocated only on first
// use. readFailureRecorder preserves non-EOF Reader errors even when bufio
// has prefetched bytes and has not exposed them to its caller yet. During a
// context cancellation, the ContextError is reported first and a simultaneous
// source failure remains deferred for the next logical operation.
type inputEndpoint struct {
	source               readFailureRecorder
	buffered             *bufio.Reader
	bufferSize           int
	mode                 streamBufferMode
	allowRegularSizeHint bool
}

func newInputEndpoint(reader io.Reader) inputEndpoint {
	return inputEndpoint{
		source:     readFailureRecorder{reader: reader},
		mode:       streamBufferFull,
		bufferSize: defaultStreamBufferBytes,
	}
}

func newFileInputEndpoint(file *os.File, flags int) inputEndpoint {
	endpoint := newInputEndpoint(file)
	// Go leaves Seek behavior unspecified for files opened with O_APPEND.
	// Exact sizing queries the physical cursor, so append streams retain the
	// generic read path.
	endpoint.allowRegularSizeHint = flags&os.O_APPEND == 0
	return endpoint
}

func (endpoint *inputEndpoint) reader() (*bufio.Reader, error) {
	if endpoint == nil || endpoint.source.reader == nil {
		return nil, ErrClosed
	}
	if endpoint.buffered == nil {
		size := endpoint.bufferSize
		if endpoint.mode == streamBufferNone {
			size = 1
		}
		endpoint.buffered = bufio.NewReaderSize(
			&endpoint.source,
			size,
		)
	}
	return endpoint.buffered, nil
}

// setBuffering changes future input read-ahead without changing the logical
// cursor. Bytes already fetched by the old reader move into endpoint-owned
// replay storage before its backing buffer is released.
func (endpoint *inputEndpoint) setBuffering(
	mode streamBufferMode,
	size int,
) error {
	if mode > streamBufferLine {
		return errInvalidStreamBufferMode
	}
	if endpoint == nil || endpoint.source.reader == nil {
		return ErrClosed
	}
	size, err := normalizedStreamBufferSize(size)
	if err != nil {
		return err
	}
	if endpoint.buffered != nil {
		count := endpoint.buffered.Buffered()
		if count != 0 {
			unread, _ := endpoint.buffered.Peek(count)
			endpoint.source.prependReplay(unread)
		}
		endpoint.buffered = nil
	}
	endpoint.mode = mode
	endpoint.bufferSize = size
	return nil
}

func (endpoint *inputEndpoint) Read(buffer []byte) (int, error) {
	reader, err := endpoint.reader()
	if err != nil {
		return 0, err
	}
	count, err := reader.Read(buffer)
	return count, endpoint.exposeError(err)
}

func (endpoint *inputEndpoint) ReadByte() (byte, error) {
	reader, err := endpoint.reader()
	if err != nil {
		return 0, err
	}
	value, err := reader.ReadByte()
	return value, endpoint.exposeError(err)
}

func (endpoint *inputEndpoint) UnreadByte() error {
	reader, err := endpoint.reader()
	if err != nil {
		return err
	}
	return endpoint.exposeError(reader.UnreadByte())
}

func (endpoint *inputEndpoint) ReadSlice(
	delimiter byte,
) ([]byte, error) {
	reader, err := endpoint.reader()
	if err != nil {
		return nil, err
	}
	text, err := reader.ReadSlice(delimiter)
	if endpoint.source.hasPendingContextFailure() &&
		len(text) != 0 {
		text = bytes.Clone(text)
	}
	return text, endpoint.exposeError(err)
}

func (endpoint *inputEndpoint) ReadString(
	delimiter byte,
) (string, error) {
	reader, err := endpoint.reader()
	if err != nil {
		return "", err
	}
	text, err := reader.ReadString(delimiter)
	return text, endpoint.exposeError(err)
}

func (endpoint *inputEndpoint) Peek(size int) ([]byte, error) {
	reader, err := endpoint.reader()
	if err != nil {
		return nil, err
	}
	text, err := reader.Peek(size)
	_, recorded := err.(*recordedReadFailure)
	if (endpoint.source.hasPendingContextFailure() ||
		recorded) &&
		len(text) != 0 {
		// exposeError resets the bufio.Reader to discard its copy of the
		// deferred error. Preserve Peek's borrowed result across that reset.
		text = bytes.Clone(text)
	}
	return text, endpoint.exposeError(err)
}

// unreadBytes reports all input fetched from the underlying stream but not
// consumed through this endpoint. Replay bytes sit outside bufio after a
// deferred error is taken, so cursor correction must include both stores.
func (endpoint *inputEndpoint) unreadBytes() int {
	if endpoint == nil {
		return 0
	}
	unread := len(endpoint.source.replay)
	if endpoint.buffered != nil {
		unread += endpoint.buffered.Buffered()
	}
	return unread
}

// remainingRegularBytes returns the logical bytes remaining in an ordinary
// file. The operating-system cursor may be ahead of Lua's cursor because
// bufio has read ahead, so buffered and replay bytes are added back.
//
// This is an allocation hint only. A file may grow or shrink after the query;
// the read loop still detects EOF, excess input, and appended bytes.
func (endpoint *inputEndpoint) remainingRegularBytes() (int64, bool) {
	if endpoint == nil || !endpoint.allowRegularSizeHint {
		return 0, false
	}
	file, ok := endpoint.source.reader.(*os.File)
	if !ok {
		return 0, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, false
	}
	unread := int64(endpoint.unreadBytes())
	if unread < 0 || position < unread {
		return 0, false
	}
	logicalPosition := position - unread
	remaining := info.Size() - logicalPosition
	if remaining < unread {
		// Bytes already fetched by bufio remain observable even if another
		// process truncates the file after they were read.
		remaining = unread
	}
	return remaining, true
}

// takeFailure transfers ownership of one deferred Reader error to one logical
// consumer. Any bytes bufio prefetched with the error are replayed before the
// underlying stream, while Reset removes bufio's private copy of the error.
func (endpoint *inputEndpoint) takeFailure() error {
	if endpoint == nil || endpoint.source.pending == nil {
		return nil
	}
	failure := endpoint.source.pending
	endpoint.source.pending = nil

	if endpoint.buffered != nil {
		count := endpoint.buffered.Buffered()
		if count != 0 {
			unread, _ := endpoint.buffered.Peek(count)
			endpoint.source.prependReplay(unread)
		}
		endpoint.buffered.Reset(&endpoint.source)
	}
	endpoint.source.pending = endpoint.source.deferred
	endpoint.source.deferred = nil
	return failure.cause
}

func (endpoint *inputEndpoint) exposeError(err error) error {
	if endpoint.source.hasPendingContextFailure() {
		return endpoint.takeFailure()
	}
	failure, ok := err.(*recordedReadFailure)
	if !ok {
		return err
	}
	if endpoint.source.pending == failure {
		return endpoint.takeFailure()
	}
	return failure.cause
}

func (endpoint *inputEndpoint) detach() {
	if endpoint == nil {
		return
	}
	endpoint.buffered = nil
	endpoint.source.reader = nil
	endpoint.source.pending = nil
	endpoint.source.deferred = nil
	endpoint.source.replay = nil
	endpoint.source.contextThread = nil
}

// outputEndpoint applies one buffering policy to every writer of a logical
// output stream. The bufio.Writer and backing byte slice are allocated only
// after buffered output is both selected and used.
type outputEndpoint struct {
	writer        io.Writer
	buffered      *bufio.Writer
	mode          streamBufferMode
	bufferSize    int
	numberScratch [32]byte
}

type streamFlusher interface {
	Flush() error
}

func newOutputEndpoint(writer io.Writer) outputEndpoint {
	return outputEndpoint{
		writer:     writer,
		bufferSize: defaultStreamBufferBytes,
	}
}

func (endpoint *outputEndpoint) setBuffering(
	mode streamBufferMode,
	size int,
) error {
	if mode > streamBufferLine {
		return errInvalidStreamBufferMode
	}
	if endpoint == nil || endpoint.writer == nil {
		return ErrClosed
	}
	size, err := normalizedStreamBufferSize(size)
	if err != nil {
		return err
	}
	if err := endpoint.Flush(); err != nil {
		return err
	}
	endpoint.buffered = nil
	endpoint.mode = mode
	endpoint.bufferSize = size
	return nil
}

func normalizedStreamBufferSize(size int) (int, error) {
	if size <= 0 {
		return defaultStreamBufferBytes, nil
	}
	if size > maximumStreamBufferBytes {
		return 0, errStreamBufferTooLarge
	}
	return size, nil
}

func (endpoint *outputEndpoint) buffer() (*bufio.Writer, error) {
	if endpoint == nil || endpoint.writer == nil {
		return nil, ErrClosed
	}
	if endpoint.buffered == nil {
		size := endpoint.bufferSize
		if size <= 0 {
			size = defaultStreamBufferBytes
		}
		endpoint.buffered = bufio.NewWriterSize(
			endpoint.writer,
			size,
		)
	}
	return endpoint.buffered, nil
}

func (endpoint *outputEndpoint) Write(buffer []byte) (int, error) {
	if endpoint == nil || endpoint.writer == nil {
		return 0, ErrClosed
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	switch endpoint.mode {
	case streamBufferNone:
		return endpoint.writer.Write(buffer)
	case streamBufferFull:
		writer, err := endpoint.buffer()
		if err != nil {
			return 0, err
		}
		return writer.Write(buffer)
	case streamBufferLine:
		return endpoint.writeLines(buffer)
	default:
		return 0, errInvalidStreamBufferMode
	}
}

func (endpoint *outputEndpoint) WriteString(text string) (int, error) {
	if endpoint == nil || endpoint.writer == nil {
		return 0, ErrClosed
	}
	if text == "" {
		return 0, nil
	}
	switch endpoint.mode {
	case streamBufferNone:
		return io.WriteString(endpoint.writer, text)
	case streamBufferFull:
		writer, err := endpoint.buffer()
		if err != nil {
			return 0, err
		}
		return writer.WriteString(text)
	case streamBufferLine:
		return endpoint.writeStringLines(text)
	default:
		return 0, errInvalidStreamBufferMode
	}
}

func (endpoint *outputEndpoint) writeLines(
	buffer []byte,
) (int, error) {
	writer, err := endpoint.buffer()
	if err != nil {
		return 0, err
	}
	written := 0
	for len(buffer) != 0 {
		end := bytes.IndexByte(buffer, '\n')
		if end < 0 {
			count, writeErr := writer.Write(buffer)
			return written + count, writeErr
		}
		end++
		count, writeErr := writer.Write(buffer[:end])
		written += count
		if writeErr != nil {
			return written, writeErr
		}
		if flushErr := writer.Flush(); flushErr != nil {
			return written, flushErr
		}
		buffer = buffer[end:]
	}
	return written, nil
}

func (endpoint *outputEndpoint) writeStringLines(
	text string,
) (int, error) {
	writer, err := endpoint.buffer()
	if err != nil {
		return 0, err
	}
	written := 0
	for text != "" {
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			count, writeErr := writer.WriteString(text)
			return written + count, writeErr
		}
		end++
		count, writeErr := writer.WriteString(text[:end])
		written += count
		if writeErr != nil {
			return written, writeErr
		}
		if flushErr := writer.Flush(); flushErr != nil {
			return written, flushErr
		}
		text = text[end:]
	}
	return written, nil
}

func (endpoint *outputEndpoint) Flush() error {
	if endpoint == nil || endpoint.writer == nil {
		return ErrClosed
	}
	var bufferErr error
	if endpoint.buffered != nil {
		bufferErr = endpoint.buffered.Flush()
	}
	var writerErr error
	if flusher, ok := endpoint.writer.(streamFlusher); ok {
		writerErr = flusher.Flush()
	}
	return joinStreamErrors(bufferErr, writerErr)
}

func (endpoint *outputEndpoint) detach() error {
	if endpoint == nil {
		return nil
	}
	var err error
	if endpoint.writer != nil {
		err = endpoint.Flush()
	}
	endpoint.buffered = nil
	endpoint.writer = nil
	return err
}

func (endpoint *outputEndpoint) discard() {
	if endpoint == nil {
		return
	}
	endpoint.buffered = nil
	endpoint.writer = nil
}

func joinStreamErrors(first, second error) error {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	default:
		return errors.Join(first, second)
	}
}

type readFailureRecorder struct {
	reader        io.Reader
	pending       *recordedReadFailure
	deferred      *recordedReadFailure
	replay        []byte
	contextThread *Thread
}

type recordedReadFailure struct {
	cause error
}

func (recorder *readFailureRecorder) prependReplay(text []byte) {
	if recorder == nil || len(text) == 0 {
		return
	}
	replay := make([]byte, len(text)+len(recorder.replay))
	copy(replay, text)
	copy(replay[len(text):], recorder.replay)
	recorder.replay = replay
}

func (failure *recordedReadFailure) Error() string {
	return failure.cause.Error()
}

func (failure *recordedReadFailure) Unwrap() error {
	return failure.cause
}

func (recorder *readFailureRecorder) hasPendingContextFailure() bool {
	if recorder == nil || recorder.pending == nil {
		return false
	}
	failure, ok := recorder.pending.cause.(*Error)
	return ok && failure.Category() == ContextError
}

func (recorder *readFailureRecorder) Read(
	buffer []byte,
) (int, error) {
	thread := recorder.contextThread
	if thread != nil {
		if failure := pollExecutionContext(thread); failure != nil {
			return recorder.recordReadFailure(0, failure)
		}
		if len(buffer) > ioReadContextPollBytes {
			buffer = buffer[:ioReadContextPollBytes]
		}
	}

	var count int
	var err error
	if len(recorder.replay) != 0 {
		count = copy(buffer, recorder.replay)
		recorder.replay = recorder.replay[count:]
		if len(recorder.replay) == 0 {
			recorder.replay = nil
		}
	} else {
		count, err = recorder.reader.Read(buffer)
	}

	if thread != nil {
		if failure := pollExecutionContext(thread); failure != nil {
			if err != nil && err != io.EOF &&
				recorder.deferred == nil {
				recorder.deferred = &recordedReadFailure{cause: err}
			}
			err = failure
		}
	}
	return recorder.recordReadFailure(count, err)
}

func (recorder *readFailureRecorder) recordReadFailure(
	count int,
	err error,
) (int, error) {
	if err != nil && err != io.EOF {
		failure := &recordedReadFailure{cause: err}
		if recorder.pending != nil && recorder.deferred == nil {
			recorder.deferred = recorder.pending
		}
		recorder.pending = failure
		err = failure
	}
	return count, err
}
