package lua

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStandardInputCursorIsLazyAndSharedWithLoaders(t *testing.T) {
	tests := []struct {
		name   string
		caller string
	}{
		{
			name: "loadfile",
			caller: `
local loaded = assert(loadfile())
return loaded()
`,
		},
		{
			name:   "dofile",
			caller: `return dofile()`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{
				ScriptLoader: HostLoader(),
				Stdin: strings.NewReader(
					"consumed by io\nreturn 42",
				),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			if state.streams.stdin.buffered != nil {
				t.Fatal("New eagerly allocated the stdin buffer")
			}

			line, err := state.streams.stdin.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line != "consumed by io\n" {
				t.Fatalf("first stdin line = %q", line)
			}
			if state.streams.stdin.buffered == nil {
				t.Fatal("first stdin read did not install a buffer")
			}

			if err := state.OpenBase(); err != nil {
				t.Fatal(err)
			}
			chunk := mustLoadString(
				t,
				state,
				"@shared-stdin.lua",
				test.caller,
			)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			assertTestValues(t, results, Number(42))
		})
	}
}

func TestStandardStreamBuffersAreNotAllocatedByNew(t *testing.T) {
	state, err := New(Options{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.streams == nil {
		t.Fatal("New omitted standard stream endpoints")
	}
	if state.streams.stdin.buffered != nil ||
		state.streams.stdout.buffered != nil ||
		state.streams.stderr.buffered != nil {
		t.Fatal("New eagerly allocated a standard stream buffer")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputEndpointBufferingModes(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		var output bytes.Buffer
		endpoint := newOutputEndpoint(&output)
		count, err := endpoint.WriteString("plain")
		if err != nil || count != len("plain") {
			t.Fatalf("WriteString = (%d, %v)", count, err)
		}
		if output.String() != "plain" {
			t.Fatalf("unbuffered output = %q", output.String())
		}
		if endpoint.buffered != nil {
			t.Fatal("unbuffered output allocated a buffer")
		}
	})

	t.Run("full", func(t *testing.T) {
		var output bytes.Buffer
		endpoint := newOutputEndpoint(&output)
		if err := endpoint.setBuffering(
			streamBufferFull,
			16,
		); err != nil {
			t.Fatal(err)
		}
		if endpoint.buffered != nil {
			t.Fatal("selecting full buffering allocated eagerly")
		}
		if _, err := endpoint.WriteString("held"); err != nil {
			t.Fatal(err)
		}
		if output.Len() != 0 {
			t.Fatalf("full buffer wrote early: %q", output.String())
		}
		if endpoint.buffered == nil {
			t.Fatal("first buffered write did not allocate")
		}
		if err := endpoint.Flush(); err != nil {
			t.Fatal(err)
		}
		if output.String() != "held" {
			t.Fatalf("flushed output = %q", output.String())
		}
	})

	t.Run("line", func(t *testing.T) {
		var output bytes.Buffer
		endpoint := newOutputEndpoint(&output)
		if err := endpoint.setBuffering(
			streamBufferLine,
			32,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := endpoint.WriteString("first"); err != nil {
			t.Fatal(err)
		}
		if output.Len() != 0 {
			t.Fatalf("line buffer wrote without newline: %q", output.String())
		}
		if _, err := endpoint.Write([]byte(" line\nsecond\nheld")); err != nil {
			t.Fatal(err)
		}
		if output.String() != "first line\nsecond\n" {
			t.Fatalf("line-buffered output = %q", output.String())
		}
		if err := endpoint.Flush(); err != nil {
			t.Fatal(err)
		}
		if output.String() != "first line\nsecond\nheld" {
			t.Fatalf("final line-buffered output = %q", output.String())
		}
	})
}

type streamErrorReader struct {
	text string
	err  error
	done bool
}

func (reader *streamErrorReader) Read(buffer []byte) (int, error) {
	if reader.done {
		return 0, reader.err
	}
	reader.done = true
	count := copy(buffer, reader.text)
	return count, reader.err
}

type streamErrorWriter struct {
	err error
}

func (writer *streamErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type flushingStreamWriter struct {
	bytes.Buffer
	err     error
	flushes int
}

func (writer *flushingStreamWriter) Flush() error {
	writer.flushes++
	return writer.err
}

func TestStreamEndpointsPreserveUnderlyingErrorIdentity(t *testing.T) {
	sentinel := errors.New("stream failed")

	input := newInputEndpoint(&streamErrorReader{
		text: "partial",
		err:  sentinel,
	})
	text, err := input.ReadString('\n')
	if text != "partial" || err != sentinel {
		t.Fatalf("input result = (%q, %v); want exact sentinel", text, err)
	}
	if failure := input.takeFailure(); failure != nil {
		t.Fatalf("delivered input failure remained pending: %v", failure)
	}

	control, failure := newLoadControl(nil, 1<<20)
	if failure != nil {
		t.Fatal(failure)
	}
	loadInput := newInputEndpoint(&streamErrorReader{
		text: "local = trailing",
		err:  sentinel,
	})
	_, err = loadFileEndpointPrototype(
		"@broken.lua",
		"broken.lua",
		&loadInput,
		&control,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("loader error = %v; want wrapped sentinel", err)
	}

	for _, mode := range []streamBufferMode{
		streamBufferNone,
		streamBufferFull,
		streamBufferLine,
	} {
		writer := &streamErrorWriter{err: sentinel}
		output := newOutputEndpoint(writer)
		if err := output.setBuffering(mode, 16); err != nil {
			t.Fatal(err)
		}
		if mode == streamBufferLine {
			_, err = output.WriteString("line\n")
		} else {
			_, err = output.WriteString("text")
			if err == nil {
				err = output.Flush()
			}
		}
		if err != sentinel {
			t.Fatalf(
				"%v buffering error = %v; want exact sentinel",
				mode,
				err,
			)
		}
	}
}

type stagedStreamReader struct {
	steps []struct {
		text string
		err  error
	}
}

func (reader *stagedStreamReader) Read(buffer []byte) (int, error) {
	if len(reader.steps) == 0 {
		return 0, io.EOF
	}
	step := reader.steps[0]
	reader.steps = reader.steps[1:]
	return copy(buffer, step.text), step.err
}

func TestDeliveredInputFailureDoesNotPoisonLaterConsumer(t *testing.T) {
	sentinel := errors.New("transient input failure")
	endpoint := newInputEndpoint(&stagedStreamReader{
		steps: []struct {
			text string
			err  error
		}{
			{text: "discarded", err: sentinel},
			{text: "return 42", err: io.EOF},
		},
	})
	text, err := endpoint.ReadString('\n')
	if text != "discarded" || err != sentinel {
		t.Fatalf("first read = (%q, %v)", text, err)
	}
	if failure := endpoint.takeFailure(); failure != nil {
		t.Fatalf("delivered failure remained pending: %v", failure)
	}

	control, failure := newLoadControl(nil, 1<<20)
	if failure != nil {
		t.Fatal(failure)
	}
	prototype, err := loadFileEndpointPrototype(
		"=stdin",
		"stdin",
		&endpoint,
		&control,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prototype == nil {
		t.Fatal("later loader returned a nil prototype")
	}
}

func TestLoaderConsumesDeferredInputFailureOnce(t *testing.T) {
	sentinel := errors.New("deferred input failure")
	endpoint := newInputEndpoint(&stagedStreamReader{
		steps: []struct {
			text string
			err  error
		}{
			{text: "local = trailing", err: sentinel},
			{text: "return 42", err: io.EOF},
		},
	})
	control, failure := newLoadControl(nil, 1<<20)
	if failure != nil {
		t.Fatal(failure)
	}
	_, err := loadFileEndpointPrototype(
		"=stdin",
		"stdin",
		&endpoint,
		&control,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("first loader error = %v; want deferred failure", err)
	}
	if failure := endpoint.takeFailure(); failure != nil {
		t.Fatalf(
			"loader left its reported failure pending: %v",
			failure,
		)
	}

	control, failure = newLoadControl(nil, 1<<20)
	if failure != nil {
		t.Fatal(failure)
	}
	prototype, err := loadFileEndpointPrototype(
		"=stdin",
		"stdin",
		&endpoint,
		&control,
	)
	if err != nil {
		t.Fatalf("later loader inherited the old failure: %v", err)
	}
	if prototype == nil {
		t.Fatal("later loader returned a nil prototype")
	}
}

func TestTakingInputFailurePreservesPrefetchedBytes(t *testing.T) {
	sentinel := errors.New("deferred input failure")
	endpoint := newInputEndpoint(&stagedStreamReader{
		steps: []struct {
			text string
			err  error
		}{
			{text: "prefetched", err: sentinel},
			{text: " tail", err: io.EOF},
		},
	})
	first, err := endpoint.Peek(1)
	if err != nil || string(first) != "p" {
		t.Fatalf("Peek = (%q, %v)", first, err)
	}
	if got := endpoint.unreadBytes(); got != len("prefetched") {
		t.Fatalf("unread bytes = %d; want %d", got, len("prefetched"))
	}

	if err := endpoint.takeFailure(); err != sentinel {
		t.Fatalf("takeFailure = %v; want exact sentinel", err)
	}
	if failure := endpoint.takeFailure(); failure != nil {
		t.Fatalf("taken failure remained pending: %v", failure)
	}
	if got := endpoint.unreadBytes(); got != len("prefetched") {
		t.Fatalf(
			"replayed unread bytes = %d; want %d",
			got,
			len("prefetched"),
		)
	}
	if err := endpoint.takeFailure(); err != nil {
		t.Fatalf("failure was delivered twice: %v", err)
	}

	text, err := io.ReadAll(&endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "prefetched tail" {
		t.Fatalf("replayed input = %q", text)
	}
	if got := endpoint.unreadBytes(); got != 0 {
		t.Fatalf("unread bytes after replay = %d; want 0", got)
	}
}

func TestExposingInputFailurePreservesPeekedBytes(t *testing.T) {
	sentinel := errors.New("peek input failure")
	endpoint := newInputEndpoint(&stagedStreamReader{
		steps: []struct {
			text string
			err  error
		}{
			{text: "peeked", err: sentinel},
			{text: " remainder", err: io.EOF},
		},
	})
	text, err := endpoint.Peek(len("peeked") + 1)
	if string(text) != "peeked" || err != sentinel {
		t.Fatalf("Peek = (%q, %v); want bytes and exact sentinel", text, err)
	}
	if failure := endpoint.takeFailure(); failure != nil {
		t.Fatalf("exposed failure remained pending: %v", failure)
	}
	if got := endpoint.unreadBytes(); got != len("peeked") {
		t.Fatalf("unread bytes = %d; want %d", got, len("peeked"))
	}

	remaining, err := io.ReadAll(&endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "peeked remainder" {
		t.Fatalf("input after Peek = %q", remaining)
	}
}

func TestPrintSharesTheBufferedStandardOutputEndpoint(t *testing.T) {
	var output bytes.Buffer
	state := newStateWithBase(t, Options{Stdout: &output})
	defer state.Close()
	if err := state.streams.stdout.setBuffering(
		streamBufferFull,
		32,
	); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(
		t,
		state,
		"@buffered-print.lua",
		`print("shared", "output")`,
	)
	if _, err := state.Call(chunk.Value()); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("print bypassed stdout buffering: %q", output.String())
	}
	if err := state.streams.stdout.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "shared\toutput\n" {
		t.Fatalf("print output = %q", output.String())
	}
}

func TestOutputEndpointFlushesTheBorrowedWriter(t *testing.T) {
	sentinel := errors.New("underlying flush failed")
	writer := &flushingStreamWriter{err: sentinel}
	endpoint := newOutputEndpoint(writer)
	if _, err := endpoint.WriteString("direct"); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.Flush(); err != sentinel {
		t.Fatalf("unbuffered Flush = %v; want exact sentinel", err)
	}
	if writer.flushes != 1 {
		t.Fatalf("underlying flush count = %d; want 1", writer.flushes)
	}

	writer.err = nil
	if err := endpoint.setBuffering(streamBufferFull, 32); err != nil {
		t.Fatal(err)
	}
	before := writer.flushes
	if _, err := endpoint.WriteString(" buffered"); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.Flush(); err != nil {
		t.Fatal(err)
	}
	if writer.String() != "direct buffered" {
		t.Fatalf("flushed writer = %q", writer.String())
	}
	if writer.flushes != before+1 {
		t.Fatalf(
			"buffered underlying flush count = %d; want %d",
			writer.flushes,
			before+1,
		)
	}
}

type borrowedEndpointProbe struct {
	bytes.Buffer
	closes atomic.Int32
}

func (probe *borrowedEndpointProbe) Close() error {
	probe.closes.Add(1)
	return nil
}

func TestStateCloseFlushesButNeverClosesBorrowedStreams(t *testing.T) {
	output := &borrowedEndpointProbe{}
	state, err := New(Options{
		Stdin:  strings.NewReader(""),
		Stdout: output,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.streams.stdout.setBuffering(
		streamBufferFull,
		32,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.streams.stdout.WriteString("flushed"); err != nil {
		t.Fatal(err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "flushed" {
		t.Fatalf("State.Close output = %q", output.String())
	}
	if output.closes.Load() != 0 {
		t.Fatalf("borrowed output closed %d times", output.closes.Load())
	}
}

func TestStateCloseReportsBufferedStreamFailure(t *testing.T) {
	sentinel := errors.New("flush failed")
	state, err := New(Options{
		Stdout: &streamErrorWriter{err: sentinel},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.streams.stdout.setBuffering(
		streamBufferFull,
		32,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.streams.stdout.WriteString("pending"); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("State.Close error = %v; want wrapped sentinel", err)
	}
}
