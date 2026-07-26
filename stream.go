package lua

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

const defaultStreamBufferBytes = 8 << 10

type streamBufferMode uint8

const (
	streamBufferNone streamBufferMode = iota
	streamBufferFull
	streamBufferLine
)

var errInvalidStreamBufferMode = errors.New(
	"lua: invalid stream buffering mode",
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
// use. readFailureRecorder preserves a non-EOF Reader error even when bufio
// has prefetched bytes and has not exposed that error to its caller yet.
type inputEndpoint struct {
	source   readFailureRecorder
	buffered *bufio.Reader
}

func newInputEndpoint(reader io.Reader) inputEndpoint {
	return inputEndpoint{
		source: readFailureRecorder{reader: reader},
	}
}

func (endpoint *inputEndpoint) reader() (*bufio.Reader, error) {
	if endpoint == nil || endpoint.source.reader == nil {
		return nil, ErrClosed
	}
	if endpoint.buffered == nil {
		endpoint.buffered = bufio.NewReaderSize(
			&endpoint.source,
			defaultStreamBufferBytes,
		)
	}
	return endpoint.buffered, nil
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
	if _, recorded := err.(*recordedReadFailure); recorded &&
		len(text) != 0 {
		// exposeError resets the bufio.Reader to discard its copy of the
		// deferred error. Preserve Peek's borrowed result across that reset.
		text = bytes.Clone(text)
	}
	return text, endpoint.exposeError(err)
}

func (endpoint *inputEndpoint) failure() error {
	if endpoint == nil {
		return nil
	}
	return endpoint.source.pendingFailure()
}

// unreadBytes reports all input fetched from the underlying stream but not
// consumed through this endpoint. Replay bytes sit outside bufio after a
// deferred error is taken, so cursor correction must include both stores.
func (endpoint *inputEndpoint) unreadBytes() int {
	if endpoint == nil {
		return 0
	}
	unread := endpoint.source.replayBytes()
	if endpoint.buffered != nil {
		unread += endpoint.buffered.Buffered()
	}
	return unread
}

// takeFailure transfers ownership of a deferred Reader error to one logical
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
	return failure.cause
}

func (endpoint *inputEndpoint) exposeError(err error) error {
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
	endpoint.source.replay = nil
}

// outputEndpoint applies one buffering policy to every writer of a logical
// output stream. The bufio.Writer and backing byte slice are allocated only
// after buffered output is both selected and used.
type outputEndpoint struct {
	writer     io.Writer
	buffered   *bufio.Writer
	mode       streamBufferMode
	bufferSize int
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
	if err := endpoint.Flush(); err != nil {
		return err
	}
	if size <= 0 {
		size = defaultStreamBufferBytes
	}
	endpoint.buffered = nil
	endpoint.mode = mode
	endpoint.bufferSize = size
	return nil
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
	reader  io.Reader
	pending *recordedReadFailure
	replay  []byte
}

type recordedReadFailure struct {
	cause error
}

func (recorder *readFailureRecorder) pendingFailure() error {
	if recorder == nil || recorder.pending == nil {
		return nil
	}
	return recorder.pending.cause
}

func (recorder *readFailureRecorder) replayBytes() int {
	if recorder == nil {
		return 0
	}
	return len(recorder.replay)
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

func (recorder *readFailureRecorder) Read(
	buffer []byte,
) (int, error) {
	if len(recorder.replay) != 0 {
		count := copy(buffer, recorder.replay)
		recorder.replay = recorder.replay[count:]
		if len(recorder.replay) == 0 {
			recorder.replay = nil
		}
		return count, nil
	}
	count, err := recorder.reader.Read(buffer)
	if err != nil && err != io.EOF {
		failure := &recordedReadFailure{cause: err}
		recorder.pending = failure
		err = failure
	}
	return count, err
}
