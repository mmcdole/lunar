package lua

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixedWidthReader struct {
	text   string
	offset int
	width  int
	reads  int
}

func (reader *fixedWidthReader) Read(buffer []byte) (int, error) {
	reader.reads++
	if reader.offset == len(reader.text) {
		return 0, io.EOF
	}
	count := reader.width
	if count > len(buffer) {
		count = len(buffer)
	}
	if remaining := len(reader.text) - reader.offset; count > remaining {
		count = remaining
	}
	copy(buffer, reader.text[reader.offset:reader.offset+count])
	reader.offset += count
	return count, nil
}

type finalErrorReader struct {
	text     string
	failure  error
	returned bool
}

func (reader *finalErrorReader) Read(buffer []byte) (int, error) {
	if reader.returned {
		return 0, reader.failure
	}
	reader.returned = true
	return copy(buffer, reader.text), reader.failure
}

type emptyThenReader struct {
	empty int
	text  string
	done  bool
}

func (reader *emptyThenReader) Read(buffer []byte) (int, error) {
	if reader.empty != 0 {
		reader.empty--
		return 0, nil
	}
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	return copy(buffer, reader.text), nil
}

type closeProbeReader struct {
	*strings.Reader
	closed bool
}

func (reader *closeProbeReader) Close() error {
	reader.closed = true
	return nil
}

type cancelAfterEmptyReader struct {
	cancel context.CancelFunc
	reads  int
}

func (reader *cancelAfterEmptyReader) Read([]byte) (int, error) {
	reader.reads++
	reader.cancel()
	return 0, nil
}

type cancelOnFinalByteReader struct {
	text   string
	offset int
	cancel context.CancelFunc
}

func (reader *cancelOnFinalByteReader) Read(buffer []byte) (int, error) {
	if reader.offset == len(reader.text) {
		return 0, io.EOF
	}
	end := len(reader.text)
	if reader.offset == 0 && end-reader.offset > 1 {
		end--
	}
	if available := len(buffer); end-reader.offset > available {
		end = reader.offset + available
	}
	count := copy(buffer, reader.text[reader.offset:end])
	reader.offset += count
	if reader.offset == len(reader.text) {
		reader.cancel()
	}
	return count, nil
}

func TestStateLoadStreamsSourceAndBinaryChunks(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	sourceReader := &fixedWidthReader{
		text:  `return "source", 42`,
		width: 1,
	}
	source, err := state.Load("@fragmented.lua", sourceReader)
	if err != nil {
		t.Fatal(err)
	}
	if source.Prototype().SourceName() != "@fragmented.lua" {
		t.Fatalf(
			"source name = %q",
			source.Prototype().SourceName(),
		)
	}
	results, err := state.Call(source.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("source"), Number(42))
	if sourceReader.reads <= 2 {
		t.Fatalf("fragmented source used only %d reads", sourceReader.reads)
	}

	prototype, err := Compile("@binary-source.lua", `return marker`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("marker", Number(73)); err != nil {
		t.Fatal(err)
	}
	binaryReader := &fixedWidthReader{text: dumped, width: 1}
	binary, err := state.Load("@fragmented.luac", binaryReader)
	if err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(binary.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(73))
}

func TestStateLoadPreservesReaderResultsAndErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	t.Run("data with EOF", func(t *testing.T) {
		function, loadErr := state.Load(
			"@data-eof.lua",
			&finalErrorReader{
				text:    "return 11",
				failure: io.EOF,
			},
		)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		results, callErr := state.Call(function.Value())
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(t, results, Number(11))
	})

	t.Run("data with failure", func(t *testing.T) {
		sentinel := errors.New("reader failed after data")
		_, loadErr := state.Load(
			"@data-error.lua",
			&finalErrorReader{
				text:    "return 12",
				failure: sentinel,
			},
		)
		if loadErr != sentinel {
			t.Fatalf("error = %T %v, want original sentinel", loadErr, loadErr)
		}
	})

	t.Run("failure wins over early syntax return", func(t *testing.T) {
		sentinel := errors.New("reader failed with invalid source")
		_, loadErr := state.Load(
			"@early-error.lua",
			&finalErrorReader{
				text:    "local = trailing source never parsed",
				failure: sentinel,
			},
		)
		if loadErr != sentinel {
			t.Fatalf("error = %T %v, want original sentinel", loadErr, loadErr)
		}
	})

	t.Run("failure wins over binary trailing data", func(t *testing.T) {
		prototype, compileErr := Compile("@reader-binary.lua", `return 19`)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		dumped, dumpErr := dumpPrototype(prototype)
		if dumpErr != nil {
			t.Fatal(dumpErr)
		}
		sentinel := errors.New("reader failed after binary root")
		_, loadErr := state.Load(
			"@binary-error.luac",
			&finalErrorReader{
				text:    dumped + "trailing",
				failure: sentinel,
			},
		)
		if loadErr != sentinel {
			t.Fatalf("error = %T %v, want original sentinel", loadErr, loadErr)
		}
	})

	t.Run("wrapped EOF", func(t *testing.T) {
		wrapped := fmt.Errorf("reader context: %w", io.EOF)
		_, loadErr := state.Load(
			"@wrapped-eof.lua",
			&finalErrorReader{failure: wrapped},
		)
		if loadErr != wrapped {
			t.Fatalf("error = %T %v, want wrapped EOF", loadErr, loadErr)
		}
	})

	t.Run("transient empty reads", func(t *testing.T) {
		function, loadErr := state.Load(
			"@empty-reads.lua",
			&emptyThenReader{empty: 7, text: "return 13"},
		)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		results, callErr := state.Call(function.Value())
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(t, results, Number(13))
	})

	t.Run("no progress", func(t *testing.T) {
		_, loadErr := state.Load(
			"@no-progress.lua",
			&emptyThenReader{empty: maxConsecutiveEmptyRead},
		)
		if loadErr != io.ErrNoProgress {
			t.Fatalf("error = %T %v, want io.ErrNoProgress", loadErr, loadErr)
		}
	})

	t.Run("does not close", func(t *testing.T) {
		reader := &closeProbeReader{
			Reader: strings.NewReader("return 14"),
		}
		if _, loadErr := state.Load("@not-closed.lua", reader); loadErr != nil {
			t.Fatal(loadErr)
		}
		if reader.closed {
			t.Fatal("Load closed its caller-owned Reader")
		}
	})

	t.Run("nil reader", func(t *testing.T) {
		if _, loadErr := state.Load("@nil.lua", nil); !errors.Is(
			loadErr,
			ErrNilReader,
		) {
			t.Fatalf("nil Reader error = %v", loadErr)
		}
	})
}

func TestStateLoadHonorsLimitsAndContexts(t *testing.T) {
	const source = "return 21"
	exact, err := New(Options{MaxLoadBytes: len(source)})
	if err != nil {
		t.Fatal(err)
	}
	defer exact.Close()
	if _, err := exact.Load(
		"@exact.lua",
		&fixedWidthReader{text: source, width: 1},
	); err != nil {
		t.Fatalf("exact-limit load: %v", err)
	}

	limited, err := New(Options{MaxLoadBytes: len(source) - 1})
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	_, loadErr := limited.Load(
		"@limited.lua",
		&fixedWidthReader{text: source, width: 1},
	)
	var failure *Error
	if !errors.As(loadErr, &failure) ||
		failure.Category() != ResourceError {
		t.Fatalf("limited error = %#v; want ResourceError", loadErr)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &fixedWidthReader{text: source, width: 1}
	_, loadErr = state.LoadContext(ctx, "@cancelled.lua", reader)
	if !errors.As(loadErr, &failure) ||
		failure.Category() != ContextError ||
		reader.reads != 0 {
		t.Fatalf(
			"cancelled load = (%#v, %d reads); want ContextError before reading",
			loadErr,
			reader.reads,
		)
	}
	if _, loadErr = state.LoadContext(
		nil,
		"@nil-context.lua",
		strings.NewReader(source),
	); !errors.Is(loadErr, ErrNilContext) {
		t.Fatalf("nil context error = %v", loadErr)
	}
	if _, loadErr = state.LoadStringContext(
		ctx,
		"@cancelled-string.lua",
		source,
	); !errors.As(loadErr, &failure) ||
		failure.Category() != ContextError {
		t.Fatalf("cancelled string error = %#v", loadErr)
	}

	emptyContext, cancelEmpty := context.WithCancel(context.Background())
	emptyReader := &cancelAfterEmptyReader{cancel: cancelEmpty}
	_, loadErr = state.LoadContext(
		emptyContext,
		"@cancelled-empty.lua",
		emptyReader,
	)
	if !errors.As(loadErr, &failure) ||
		failure.Category() != ContextError ||
		emptyReader.reads != 1 {
		t.Fatalf(
			"empty-read cancellation = (%#v, %d reads); want ContextError after one read",
			loadErr,
			emptyReader.reads,
		)
	}

	prototype, compileErr := Compile("@cancelled-binary-source.lua", `return 22`)
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	dumped, dumpErr := dumpPrototype(prototype)
	if dumpErr != nil {
		t.Fatal(dumpErr)
	}
	binaryContext, cancelBinary := context.WithCancel(context.Background())
	binaryReader := &cancelOnFinalByteReader{
		text:   dumped,
		cancel: cancelBinary,
	}
	_, loadErr = state.LoadContext(
		binaryContext,
		"@cancelled-binary.luac",
		binaryReader,
	)
	if !errors.As(loadErr, &failure) ||
		failure.Category() != ContextError {
		t.Fatalf("final binary cancellation = %#v; want ContextError", loadErr)
	}
}

func TestStateLoadFileHandlesShebangTextAndBinary(t *testing.T) {
	directory := t.TempDir()
	textPath := filepath.Join(directory, "script.lua")
	text := "#!/usr/bin/env lua\nreturn 31"
	if err := os.WriteFile(textPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := New(Options{
		Source:       OSSource(),
		MaxLoadBytes: len(text),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadFile(textPath)
	if err != nil {
		t.Fatal(err)
	}
	if function.Prototype().SourceName() != "@"+textPath {
		t.Fatalf(
			"file source name = %q",
			function.Prototype().SourceName(),
		)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(31))

	syntaxPath := filepath.Join(directory, "syntax.lua")
	if err := os.WriteFile(
		syntaxPath,
		[]byte("# ignored\nlocal ="),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, syntaxErr := state.LoadFile(syntaxPath)
	if syntaxErr == nil || !strings.Contains(syntaxErr.Error(), ":2:") {
		t.Fatalf("shebang syntax error = %v; want original line 2", syntaxErr)
	}

	prototype, err := Compile("@dumped.lua", `return 32`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(directory, "script.luac")
	binaryFile := append([]byte("#!lua\n"), []byte(dumped)...)
	if err := os.WriteFile(binaryPath, binaryFile, 0o600); err != nil {
		t.Fatal(err)
	}
	binaryState, err := New(Options{
		Source:       OSSource(),
		MaxLoadBytes: len(binaryFile) * 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer binaryState.Close()
	binary, err := binaryState.LoadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	results, err = binaryState.Call(binary.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(32))
}

func TestStateLoadFileReportsOpenReadAndLimitFailures(t *testing.T) {
	state, err := New(Options{Source: OSSource()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	missing := filepath.Join(t.TempDir(), "missing.lua")
	_, openErr := state.LoadFile(missing)
	var fileErr *fileLoadError
	if !errors.As(openErr, &fileErr) ||
		fileErr.operation != "open" ||
		!strings.Contains(openErr.Error(), "cannot open "+missing) {
		t.Fatalf("open error = %#v", openErr)
	}

	sentinel := errors.New("file reader failed")
	control, failure := newLoadControl(nil, 1<<20)
	if failure != nil {
		t.Fatal(failure)
	}
	_, readErr := loadFileReaderPrototype(
		"@broken.lua",
		"broken.lua",
		&finalErrorReader{
			text:    "local = trailing",
			failure: sentinel,
		},
		&control,
	)
	if !errors.Is(readErr, sentinel) ||
		!strings.Contains(readErr.Error(), "cannot read broken.lua") {
		t.Fatalf("read error = %#v", readErr)
	}

	path := filepath.Join(t.TempDir(), "limited.lua")
	text := "#!/bin/lua\nreturn 1"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	limited, err := New(Options{
		Source:       OSSource(),
		MaxLoadBytes: len(text) - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	_, limitErr := limited.LoadFile(path)
	var luaErr *Error
	if !errors.As(limitErr, &luaErr) ||
		luaErr.Category() != ResourceError {
		t.Fatalf("file limit error = %#v; want ResourceError", limitErr)
	}

	tiny, err := New(Options{
		Source:       OSSource(),
		MaxLoadBytes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tiny.Close()
	_, limitErr = tiny.LoadFile(path)
	var wrappedFileErr *fileLoadError
	if !errors.As(limitErr, &luaErr) ||
		luaErr.Category() != ResourceError ||
		errors.As(limitErr, &wrappedFileErr) {
		t.Fatalf(
			"shebang limit error = %#v; want direct ResourceError",
			limitErr,
		)
	}
}

func BenchmarkStateLoad(b *testing.B) {
	const source = `
local function accumulate(limit, ...)
	local total = 0
	local values = {...}
	for index = 1, limit do
		local value = values[index] or index
		total = total + value * value
	end
	return {
		total = total,
		label = "result:" .. total,
		values = values,
	}
end
return accumulate(6, 2, 3, 5)
`
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	b.Run("string", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(source)))
		for range b.N {
			function, loadErr := state.LoadString(
				"@benchmark.lua",
				source,
			)
			if loadErr != nil {
				b.Fatal(loadErr)
			}
			benchmarkLoadedFunction = function
		}
	})
	b.Run("reader", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(source)))
		for range b.N {
			function, loadErr := state.Load(
				"@benchmark.lua",
				strings.NewReader(source),
			)
			if loadErr != nil {
				b.Fatal(loadErr)
			}
			benchmarkLoadedFunction = function
		}
	})
}

var benchmarkLoadedFunction *Function
