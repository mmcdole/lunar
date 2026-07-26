package lua

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIOReadLineMatrix(t *testing.T) {
	engine, _ := newTestIOReadEngine(
		&fragmentedIOReader{
			text:  "alpha\n\ncarriage\r\nnul\x00byte\nfinal",
			width: 1,
		},
		defaultIOReadLimit,
	)
	for index, want := range []string{
		"alpha",
		"",
		"carriage\r",
		"nul\x00byte",
		"final",
	} {
		value, present, err := engine.readLine()
		if err != nil {
			t.Fatalf("line %d: %v", index, err)
		}
		if !present {
			t.Fatalf("line %d was absent", index)
		}
		if got := ioReadSlotText(t, value); got != want {
			t.Fatalf("line %d = %q; want %q", index, got, want)
		}
	}
	if value, present, err := engine.readLine(); err != nil ||
		present || value != nilSlot {
		t.Fatalf(
			"terminal line = (%#v, %t, %v); want absent",
			value,
			present,
			err,
		)
	}
}

func TestIOReadByteCountAndZeroProbe(t *testing.T) {
	engine, _ := newTestIOReadEngine(
		strings.NewReader("abcdef"),
		defaultIOReadLimit,
	)

	value, present, err := engine.readBytes(3)
	assertIOReadText(t, value, present, err, "abc")

	value, present, err = engine.readBytes(0)
	assertIOReadText(t, value, present, err, "")

	value, present, err = engine.readBytes(3)
	assertIOReadText(t, value, present, err, "def")

	if value, present, err = engine.readBytes(0); err != nil ||
		present || value != nilSlot {
		t.Fatalf(
			"EOF probe = (%#v, %t, %v); want absent",
			value,
			present,
			err,
		)
	}

	short, _ := newTestIOReadEngine(
		strings.NewReader("xy"),
		defaultIOReadLimit,
	)
	value, present, err = short.readBytes(20)
	assertIOReadText(t, value, present, err, "xy")
	if _, present, err = short.readBytes(1); err != nil || present {
		t.Fatalf("read after short EOF = (%t, %v)", present, err)
	}
}

func TestIOReadAllAlwaysReturnsAString(t *testing.T) {
	for _, test := range []struct {
		name   string
		reader io.Reader
		want   string
	}{
		{
			name:   "empty",
			reader: strings.NewReader(""),
			want:   "",
		},
		{
			name: "fragmented binary",
			reader: &fragmentedIOReader{
				text:  "a\x00b\r\nc",
				width: 2,
			},
			want: "a\x00b\r\nc",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _ := newTestIOReadEngine(
				test.reader,
				defaultIOReadLimit,
			)
			value, present, err := engine.readAll()
			assertIOReadText(
				t,
				value,
				present,
				err,
				test.want,
			)
		})
	}
}

func TestIOReadNumberGrammarAndPrefixPreservation(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		present   bool
		want      float64
		remainder string
	}{
		{
			name:      "decimal",
			input:     " \t\r\n-12.5e+2tail",
			present:   true,
			want:      -1250,
			remainder: "tail",
		},
		{
			name:      "fraction",
			input:     ".5;",
			present:   true,
			want:      0.5,
			remainder: ";",
		},
		{
			name:      "trailing point",
			input:     "1.rest",
			present:   true,
			want:      1,
			remainder: "rest",
		},
		{
			name:      "hexadecimal",
			input:     "-0x10!",
			present:   true,
			want:      -16,
			remainder: "!",
		},
		{
			name:      "incomplete exponent",
			input:     "12e+rest",
			present:   true,
			want:      12,
			remainder: "e+rest",
		},
		{
			name:      "incomplete hexadecimal",
			input:     "0xZ",
			present:   true,
			want:      0,
			remainder: "xZ",
		},
		{
			name:      "sign without number",
			input:     "  +x",
			present:   false,
			remainder: "+x",
		},
		{
			name:      "point without number",
			input:     ".x",
			present:   false,
			remainder: ".x",
		},
		{
			name:      "binary non-number",
			input:     "\x001",
			present:   false,
			remainder: "\x001",
		},
		{
			name:      "only whitespace",
			input:     " \v\f",
			present:   false,
			remainder: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, _ := newTestIOReadEngine(
				&fragmentedIOReader{
					text:  test.input,
					width: 1,
				},
				defaultIOReadLimit,
			)
			value, present, err := engine.readNumber()
			if err != nil {
				t.Fatal(err)
			}
			if present != test.present {
				t.Fatalf(
					"present = %t; want %t",
					present,
					test.present,
				)
			}
			if present {
				got := math.Float64frombits(value.bits)
				if value.ref != nil || got != test.want {
					t.Fatalf(
						"number = (%#x, %v); want %v",
						value.ref,
						got,
						test.want,
					)
				}
			}
			remainder, remainderPresent, readErr :=
				engine.readAll()
			assertIOReadText(
				t,
				remainder,
				remainderPresent,
				readErr,
				test.remainder,
			)
		})
	}
}

func TestIOReadLimitsAreBoundedWithoutRejectingShortInput(t *testing.T) {
	engine, _ := newTestIOReadEngine(
		strings.NewReader("abc"),
		4,
	)
	value, present, err := engine.readBytes(math.MaxUint64)
	assertIOReadText(t, value, present, err, "abc")

	engine, _ = newTestIOReadEngine(
		strings.NewReader("abcde"),
		4,
	)
	if _, _, err := engine.readBytes(math.MaxUint64); !errors.Is(
		err,
		errIOReadTooLarge,
	) {
		t.Fatalf("oversized counted read error = %v", err)
	}
	value, present, err = engine.readBytes(1)
	assertIOReadText(t, value, present, err, "e")

	engine, _ = newTestIOReadEngine(
		strings.NewReader("abcde"),
		4,
	)
	if _, _, err := engine.readAll(); !errors.Is(
		err,
		errIOReadTooLarge,
	) {
		t.Fatalf("oversized all read error = %v", err)
	}
	value, present, err = engine.readBytes(1)
	assertIOReadText(t, value, present, err, "e")

	engine, _ = newTestIOReadEngine(
		strings.NewReader("abcde\n"),
		4,
	)
	if _, _, err := engine.readLine(); !errors.Is(
		err,
		errIOReadTooLarge,
	) {
		t.Fatalf("oversized line error = %v", err)
	}

	engine, _ = newTestIOReadEngine(
		strings.NewReader("1234x"),
		3,
	)
	if _, _, err := engine.readNumber(); !errors.Is(
		err,
		errIOReadTooLarge,
	) {
		t.Fatalf("oversized number error = %v", err)
	}
	value, present, err = engine.readAll()
	assertIOReadText(t, value, present, err, "4x")
}

func TestIOReadFailureIsReportedOnceAndRemainingInputSurvives(
	t *testing.T,
) {
	for _, test := range []struct {
		name string
		run  func(*ioReadEngine) error
		want string
	}{
		{
			name: "line",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readLine()
				return err
			},
			want: "kept\nlater\n",
		},
		{
			name: "count",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readBytes(2)
				return err
			},
			want: "d\nkept\nlater\n",
		},
		{
			name: "zero probe",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readBytes(0)
				return err
			},
			want: "bad\nkept\nlater\n",
		},
		{
			name: "number",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readNumber()
				return err
			},
			want: "bad\nkept\nlater\n",
		},
		{
			name: "all",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readAll()
				return err
			},
			want: "later\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sentinel := errors.New("transient read failure")
			endpoint := newInputEndpoint(&scriptedIOReader{
				steps: []scriptedIORead{
					{
						text: "bad\nkept\n",
						err:  sentinel,
					},
					{text: "later\n", err: io.EOF},
				},
			})
			pool := new(stringPool)
			engine := newIOReadEngine(&endpoint, pool)

			if err := test.run(&engine); err != sentinel {
				t.Fatalf(
					"first operation error = %v; want exact sentinel",
					err,
				)
			}
			if failure := endpoint.takeFailure(); failure != nil {
				t.Fatalf("failure remained pending: %v", failure)
			}

			value, present, err := engine.readAll()
			assertIOReadText(
				t,
				value,
				present,
				err,
				test.want,
			)
		})
	}
}

func TestIOReadWarmScalarPathsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	const longBlockBytes = 512

	t.Run("zero probe", func(t *testing.T) {
		engine, _ := newTestIOReadEngine(
			strings.NewReader("still present"),
			defaultIOReadLimit,
		)
		allocations := testing.AllocsPerRun(256, func() {
			value, present, err := engine.readBytes(0)
			if err != nil || !present ||
				ioReadSlotText(t, value) != "" {
				t.Fatalf("zero probe = (%#v, %t, %v)", value, present, err)
			}
		})
		if allocations != 0 {
			t.Fatalf("zero probe allocated %.2f times", allocations)
		}
	})

	t.Run("number", func(t *testing.T) {
		engine, _ := newTestIOReadEngine(
			strings.NewReader(strings.Repeat("12 ", 300)),
			defaultIOReadLimit,
		)
		allocations := testing.AllocsPerRun(256, func() {
			value, present, err := engine.readNumber()
			if err != nil || !present ||
				math.Float64frombits(value.bits) != 12 {
				t.Fatalf("number = (%#v, %t, %v)", value, present, err)
			}
		})
		if allocations != 0 {
			t.Fatalf("number read allocated %.2f times", allocations)
		}
	})

	t.Run("cached line", func(t *testing.T) {
		engine, _ := newTestIOReadEngine(
			strings.NewReader(strings.Repeat("x\n", 300)),
			defaultIOReadLimit,
		)
		allocations := testing.AllocsPerRun(256, func() {
			value, present, err := engine.readLine()
			if err != nil || !present ||
				ioReadSlotText(t, value) != "x" {
				t.Fatalf("line = (%#v, %t, %v)", value, present, err)
			}
		})
		if allocations != 0 {
			t.Fatalf("cached line allocated %.2f times", allocations)
		}
	})

	t.Run("cached all", func(t *testing.T) {
		reader := strings.NewReader("x")
		engine, endpoint := newTestIOReadEngine(
			reader,
			defaultIOReadLimit,
		)
		allocations := testing.AllocsPerRun(256, func() {
			reader.Reset("x")
			endpoint.resetReadAhead()
			value, present, err := engine.readAll()
			if err != nil || !present ||
				ioReadSlotText(t, value) != "x" {
				t.Fatalf("all = (%#v, %t, %v)", value, present, err)
			}
		})
		if allocations != 0 {
			t.Fatalf("cached all allocated %.2f times", allocations)
		}
	})

	for _, test := range []struct {
		name            string
		text            string
		count           uint64
		wantAllocations float64
	}{
		{
			name:            "one byte",
			text:            "x",
			count:           1,
			wantAllocations: 0,
		},
		{
			name:            "short cached block",
			text:            "sixteen-byte-val",
			count:           16,
			wantAllocations: 0,
		},
		{
			name:            "long uncached block",
			text:            strings.Repeat("x", longBlockBytes),
			count:           longBlockBytes,
			wantAllocations: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _ := newTestIOReadEngine(
				strings.NewReader(strings.Repeat(test.text, 300)),
				defaultIOReadLimit,
			)
			for warm := 0; warm < 2; warm++ {
				if _, present, err := engine.readBytes(
					test.count,
				); err != nil || !present {
					t.Fatalf(
						"warm fixed read = (present %t, %v)",
						present,
						err,
					)
				}
			}
			allocations := testing.AllocsPerRun(256, func() {
				value, present, err := engine.readBytes(test.count)
				if err != nil || !present ||
					ioReadSlotText(t, value) != test.text {
					t.Fatalf(
						"fixed read = (%#v, %t, %v)",
						value,
						present,
						err,
					)
				}
			})
			if allocations != test.wantAllocations {
				t.Fatalf(
					"fixed read allocated %.2f times; want %.2f",
					allocations,
					test.wantAllocations,
				)
			}
		})
	}
}

func TestIOReadRejectsReaderWithoutProgress(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*ioReadEngine) error
	}{
		{
			name: "fixed",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readBytes(1 << 20)
				return err
			},
		},
		{
			name: "all",
			run: func(engine *ioReadEngine) error {
				_, _, err := engine.readAll()
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _ := newTestIOReadEngine(
				&emptyThenReader{empty: maxConsecutiveEmptyRead},
				defaultIOReadLimit,
			)
			if err := test.run(engine); !errors.Is(err, io.ErrNoProgress) {
				t.Fatalf("read error = %v; want io.ErrNoProgress", err)
			}
		})
	}
}

func TestCompactIOReadBufferBoundsRetainedCapacity(t *testing.T) {
	const logicalBytes = 1<<20 + 1
	oversized := make([]byte, logicalBytes, 2<<20)
	compacted := compactIOReadBuffer(oversized)
	if cap(compacted) != len(compacted) {
		t.Fatalf(
			"compacted capacity = %d for %d bytes",
			cap(compacted),
			len(compacted),
		)
	}

	nearExact := make([]byte, 1<<20, 1<<20+(1<<17))
	retained := compactIOReadBuffer(nearExact)
	if &retained[0] != &nearExact[0] {
		t.Fatal("near-exact read buffer was copied")
	}

	small := make([]byte, 513, 1024)
	retained = compactIOReadBuffer(small)
	if &retained[0] != &small[0] {
		t.Fatal("small read-buffer slack caused a copy")
	}
}

func TestIOReadSurfaceFormatsAndStoppingRules(t *testing.T) {
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader("line\n\nfinal"),
	})
	defer state.Close()

	results := runIOChunk(t, state, `
return io.read(),io.read("*line"),io.read("*l"),
	io.read("*l"),io.read("*all")
`)
	assertTestValues(
		t,
		results,
		state.String("line"),
		state.String(""),
		state.String("final"),
		Nil(),
		state.String(""),
	)

	path := filepath.Join(t.TempDir(), "formats")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	results = runIOChunk(t, state, `
local file=assert(io.open(`+luaTestQuote(path)+`,"r"))
local first,second,failed=file:read(1,2,10,"*z")
local left=file:read("*a")
file:close()
local numericStringOK,numericStringError=pcall(io.read,"1")
return first,second,failed,left,numericStringOK,numericStringError
`)
	assertTestValues(
		t,
		results[:5],
		state.String("a"),
		state.String("bc"),
		Nil(),
		state.String(""),
		Bool(false),
	)
	message, _ := results[5].AsString()
	if !strings.Contains(message, "invalid option") {
		t.Fatalf("numeric string format error = %q", message)
	}
}

func TestIOReadSurfaceNumberPrefixesAndBinaryLines(t *testing.T) {
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader(
			"  -12.5e+2Z 0x10!   x",
		),
	})
	defer state.Close()

	results := runIOChunk(t, state, `
local first=io.read("*n")
local marker1=io.read(1)
local second=io.read("*number")
local marker2=io.read(1)
local failed=io.read("*n")
local rest=io.read("*a")
return first,marker1,second,marker2,failed,rest
`)
	assertTestValues(
		t,
		results,
		Number(-1250),
		state.String("Z"),
		Number(16),
		state.String("!"),
		Nil(),
		state.String("x"),
	)

	binary := newStateWithIO(t, Options{
		Stdin: strings.NewReader("a\x00b\r\n"),
	})
	defer binary.Close()
	results = runIOChunk(t, binary, `return io.read("*l")`)
	assertTestValues(t, results, binary.String("a\x00b\r"))
}

func TestIOReadSurfaceNegativeCountRetainsFixedReadEOF(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	path := filepath.Join(t.TempDir(), "negative-count")
	if err := os.WriteFile(path, []byte("remaining"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := runIOChunk(t, state, `
local file=assert(io.open(`+luaTestQuote(path)+`))
local remaining=file:read(-1)
local exhausted=file:read(-1)
local all=file:read("*a")
file:close()
return remaining,exhausted,all
`)
	assertTestValues(
		t,
		results,
		state.String("remaining"),
		Nil(),
		state.String(""),
	)
}

func TestIOReadSurfaceSharesStandardInputWithLoadFile(t *testing.T) {
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader(
			"discarded\nreturn 42",
		),
	})
	defer state.Close()

	results := runIOChunk(t, state, `
local line=io.read("*l")
local loaded=assert(loadfile())
return line,loaded()
`)
	assertTestValues(
		t,
		results,
		state.String("discarded"),
		Number(42),
	)
}

func TestIOReadSurfaceErrorsAndClosedDiagnostics(t *testing.T) {
	sentinel := errors.New("input failed")
	state := newStateWithIO(t, Options{
		Stdin: &streamErrorReader{
			text: "partial",
			err:  sentinel,
		},
	})
	defer state.Close()

	results := runIOChunk(t, state, `
local first,message,code=io.read(1,1)
return first,message,code
`)
	assertTestValues(t, results[:1], Nil())
	message, _ := results[1].AsString()
	if message != sentinel.Error() {
		t.Fatalf("read failure = %q; want %q", message, sentinel)
	}
	assertTestValues(t, results[2:], Number(0))

	closed := newStateWithIO(t, Options{})
	defer closed.Close()
	path := filepath.Join(t.TempDir(), "closed")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	results = runIOChunk(t, closed, `
local file=assert(io.open(`+luaTestQuote(path)+`))
file:close()
local methodOK,methodError=pcall(file.read,file)
local defaultInput=io.input(`+luaTestQuote(path)+`)
defaultInput:close()
local defaultOK,defaultError=pcall(io.read)
local writeOnly=assert(io.open(`+luaTestQuote(path)+`,"w"))
local value,readError,code=writeOnly:read()
writeOnly:close()
return methodOK,methodError,defaultOK,defaultError,
	value,readError,code
`)
	assertTestValues(
		t,
		[]Value{results[0], results[2], results[4]},
		Bool(false),
		Bool(false),
		Nil(),
	)
	methodError, _ := results[1].AsString()
	if !strings.Contains(
		methodError,
		"attempt to use a closed file",
	) {
		t.Fatalf("closed method error = %q", methodError)
	}
	defaultError, _ := results[3].AsString()
	if !strings.Contains(
		defaultError,
		"standard input file is closed",
	) {
		t.Fatalf("closed default error = %q", defaultError)
	}
	if text, ok := results[5].AsString(); !ok || text == "" {
		t.Fatalf("write-only read error = %v", results[5])
	}
	if code, ok := results[6].AsNumber(); !ok || code == 0 {
		t.Fatalf("write-only read errno = %v", results[6])
	}
}

func TestIOReadContextOnWriteOnlyFileReturnsFailure(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	path := filepath.Join(t.TempDir(), "write-only")
	open := ioFunctionField(t, ioLibraryTable(t, state), "open")
	opened, err := state.Call(
		open.Value(),
		state.String(path),
		state.String("w"),
	)
	if err != nil {
		t.Fatal(err)
	}
	read := ioFunctionField(t, fileMetatable(t, state), "read")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, err := state.CallContext(
		ctx,
		read.Value(),
		opened[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if message, ok := results[1].AsString(); !ok || message == "" {
		t.Fatalf("write-only context read error = %v", results[1])
	}
	if code, ok := results[2].AsNumber(); !ok || code == 0 {
		t.Fatalf("write-only context read errno = %v", results[2])
	}
}

func TestIOLinesOwnershipAndEOF(t *testing.T) {
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader("standard\n"),
	})
	defer state.Close()
	path := filepath.Join(t.TempDir(), "lines")
	if err := os.WriteFile(path, []byte("first\nlast"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := runIOChunk(t, state, `
local iterator=io.lines(`+luaTestQuote(path)+`)
local first=iterator()
local last=iterator()
local eofCount=select("#",iterator())
local afterOK,afterError=pcall(iterator)

local standard=io.lines(nil)
local standardLine=standard()
local standardEOF=select("#",standard())
local standardAgain=select("#",standard())

local file=assert(io.open(`+luaTestQuote(path)+`))
local fileIterator=file:lines()
file:close()
local explicitOK,explicitError=pcall(fileIterator)
return first,last,eofCount,afterOK,afterError,
	standardLine,standardEOF,standardAgain,io.type(io.stdin),
	explicitOK,explicitError
`)
	assertTestValues(
		t,
		[]Value{
			results[0],
			results[1],
			results[2],
			results[3],
			results[5],
			results[6],
			results[7],
			results[8],
			results[9],
		},
		state.String("first"),
		state.String("last"),
		Number(0),
		Bool(false),
		state.String("standard"),
		Number(0),
		Number(0),
		state.String("file"),
		Bool(false),
	)
	for _, index := range []int{4, 10} {
		message, _ := results[index].AsString()
		if !strings.Contains(message, "file is already closed") {
			t.Fatalf("iterator closed error %d = %q", index, message)
		}
	}
}

func TestIOReadSurfaceWarmZeroProbeDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader("still present"),
	})
	defer state.Close()
	read := ioFunctionField(t, ioLibraryTable(t, state), "read")
	arguments := [...]Value{Number(0)}
	var destination [1]Value
	run := func() {
		count, err := state.CallInto(
			read.Value(),
			arguments[:],
			destination[:],
		)
		if err != nil || count != 1 {
			panic("warm io.read(0) failed")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1_000, run); allocations != 0 {
		t.Fatalf(
			"warm compact io.read(0) allocations = %.2f; want 0",
			allocations,
		)
	}
}

func TestIOReadSurfaceWarmFixedReadDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithIO(t, Options{
		Stdin: strings.NewReader(strings.Repeat("x", 1_100)),
	})
	defer state.Close()
	read := ioFunctionField(t, ioLibraryTable(t, state), "read")
	arguments := [...]Value{Number(1)}
	var destination [1]Value
	run := func() {
		count, err := state.CallInto(
			read.Value(),
			arguments[:],
			destination[:],
		)
		if err != nil || count != 1 {
			panic("warm io.read(1) failed")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1_000, run); allocations != 0 {
		t.Fatalf(
			"warm compact io.read(1) allocations = %.2f; want 0",
			allocations,
		)
	}
}

func TestIOReadSurfaceObservesActiveContext(t *testing.T) {
	for _, test := range []struct {
		name     string
		argument func(*State) Value
	}{
		{
			name: "all",
			argument: func(state *State) Value {
				return state.String("*a")
			},
		},
		{
			name: "line",
			argument: func(state *State) Value {
				return state.String("*l")
			},
		},
		{
			name: "number",
			argument: func(state *State) Value {
				return state.String("*n")
			},
		},
		{
			name: "zero probe",
			argument: func(*State) Value {
				return Number(0)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			reader := &cancelingIOReader{
				cancel:    cancel,
				remaining: 1 << 20,
			}
			state := newStateWithIO(t, Options{Stdin: reader})
			defer state.Close()
			if err := state.streams.stdin.setBuffering(
				streamBufferFull,
				4*ioReadContextPollBytes,
			); err != nil {
				t.Fatal(err)
			}
			read := ioFunctionField(
				t,
				ioLibraryTable(t, state),
				"read",
			)

			_, err := state.CallContext(
				ctx,
				read.Value(),
				test.argument(state),
			)
			var failure *Error
			if !errors.As(err, &failure) ||
				failure.Category() != ContextError ||
				!errors.Is(err, context.Canceled) {
				t.Fatalf("context read failure = %#v", err)
			}
			if reader.delivered > ioReadContextPollBytes {
				t.Fatalf(
					"context read fetched %d bytes before stopping",
					reader.delivered,
				)
			}
		})
	}
}

func TestIOReadContextWinsOverAnEmptyReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterEmptyReader{cancel: cancel}
	state := newStateWithIO(t, Options{Stdin: reader})
	defer state.Close()
	read := ioFunctionField(t, ioLibraryTable(t, state), "read")

	_, err := state.CallContext(
		ctx,
		read.Value(),
		state.String("*a"),
	)
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ContextError ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("empty-reader context failure = %#v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("empty reader was called %d times; want 1", reader.reads)
	}
}

func TestIOReadDefersASimultaneousSourceFailureAfterContext(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	sentinel := errors.New("source failed during cancellation")
	reader := &cancelingFailureReader{
		cancel:  cancel,
		failure: sentinel,
	}
	state := newStateWithIO(t, Options{Stdin: reader})
	defer state.Close()
	read := ioFunctionField(t, ioLibraryTable(t, state), "read")

	_, err := state.CallContext(
		ctx,
		read.Value(),
		state.String("*a"),
	)
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ContextError {
		t.Fatalf("simultaneous failure = %#v; want ContextError", err)
	}

	results, err := state.Call(read.Value(), state.String("*a"))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if message, _ := results[1].AsString(); message != sentinel.Error() {
		t.Fatalf("deferred source failure = %q", message)
	}

	results, err = state.Call(read.Value(), state.String("*a"))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String(""))
}

func TestIOReadNumberHasBoundedTokenStorage(t *testing.T) {
	reader := &repeatingDigitReader{remaining: 1 << 20}
	engine, _ := newTestIOReadEngine(
		reader,
		defaultIOReadLimit,
	)
	_, present, err := engine.readNumber()
	if present || !errors.Is(err, errIONumberTooLong) {
		t.Fatalf("long numeric read = (present %t, err %v)", present, err)
	}
	if reader.delivered >
		maximumIONumberBytes+defaultStreamBufferBytes {
		t.Fatalf(
			"numeric read fetched %d bytes for a %d-byte limit",
			reader.delivered,
			maximumIONumberBytes,
		)
	}

	state := newStateWithIO(t, Options{
		Stdin: &repeatingDigitReader{remaining: 1 << 20},
	})
	defer state.Close()
	read := ioFunctionField(t, ioLibraryTable(t, state), "read")
	_, callErr := state.Call(read.Value(), state.String("*n"))
	var failure *Error
	if !errors.As(callErr, &failure) ||
		failure.Category() != ResourceError {
		t.Fatalf("long numeric surface failure = %#v", callErr)
	}
}

func TestIOReadValidatesBeforeSwitchingTheFileToRead(t *testing.T) {
	writer := &flushingStreamWriter{}
	output := newOutputEndpoint(writer)
	if err := output.setBuffering(streamBufferFull, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := output.WriteString("pending"); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("flush failed")
	writer.err = sentinel
	flushes := writer.flushes

	input := newInputEndpoint(strings.NewReader("line\n"))
	state := newStateWithIO(t, Options{})
	defer state.Close()
	data := newIOOperationFile(
		t,
		state,
		&fileHandle{input: &input, output: &output},
	)
	read := ioOperationFunction(t, state, fileRead)
	_, err := state.Call(
		read.Value(),
		data.Value(),
		state.String("*z"),
	)
	if err == nil || errors.Is(err, sentinel) {
		t.Fatalf("invalid format failure = %v", err)
	}
	if writer.flushes != flushes || writer.Len() != 0 {
		t.Fatalf(
			"invalid format flushed pending output: flushes=%d text=%q",
			writer.flushes,
			writer.String(),
		)
	}

	results, err := state.Call(
		read.Value(),
		data.Value(),
		state.String("*l"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results[:1], Nil())
	if message, _ := results[1].AsString(); message != sentinel.Error() {
		t.Fatalf("valid read transition failure = %q", message)
	}
}

func BenchmarkIOReadFixed(b *testing.B) {
	for _, test := range []struct {
		name  string
		count uint64
	}{
		{name: "1", count: 1},
		{name: "16", count: 16},
		{name: "512", count: 512},
	} {
		b.Run(test.name, func(b *testing.B) {
			engine, _ := newTestIOReadEngine(
				repeatingIOByteReader('x'),
				defaultIOReadLimit,
			)
			b.ReportAllocs()
			b.SetBytes(int64(test.count))
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, present, err := engine.readBytes(
					test.count,
				); err != nil || !present {
					b.Fatalf(
						"fixed read = (present %t, %v)",
						present,
						err,
					)
				}
			}
		})
	}
}

func newTestIOReadEngine(
	reader io.Reader,
	limit int,
) (*ioReadEngine, *inputEndpoint) {
	endpoint := newInputEndpoint(reader)
	engine := newIOReadEngine(&endpoint, new(stringPool))
	engine.limit = limit
	return &engine, &endpoint
}

func assertIOReadText(
	t *testing.T,
	value slot,
	present bool,
	err error,
	want string,
) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatalf("read was absent; want %q", want)
	}
	if got := ioReadSlotText(t, value); got != want {
		t.Fatalf("read = %q; want %q", got, want)
	}
}

func ioReadSlotText(t *testing.T, value slot) string {
	t.Helper()
	if value.kind() != StringKind {
		t.Fatalf("slot kind = %s; want string", value.kind())
	}
	return (*luaString)(value.ref).text
}

type fragmentedIOReader struct {
	text  string
	width int
}

func (reader *fragmentedIOReader) Read(destination []byte) (int, error) {
	if reader.text == "" {
		return 0, io.EOF
	}
	width := reader.width
	if width <= 0 || width > len(destination) {
		width = len(destination)
	}
	if width > len(reader.text) {
		width = len(reader.text)
	}
	count := copy(destination[:width], reader.text[:width])
	reader.text = reader.text[count:]
	return count, nil
}

type scriptedIORead struct {
	text string
	err  error
}

type scriptedIOReader struct {
	steps []scriptedIORead
}

type cancelingIOReader struct {
	cancel    context.CancelFunc
	remaining int
	delivered int
}

type cancelingFailureReader struct {
	cancel   context.CancelFunc
	failure  error
	returned bool
}

func (reader *cancelingFailureReader) Read(
	destination []byte,
) (int, error) {
	if reader.returned {
		return 0, io.EOF
	}
	reader.returned = true
	reader.cancel()
	destination[0] = 'x'
	return 1, reader.failure
}

func (reader *cancelingIOReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if reader.delivered == 0 {
		reader.cancel()
	}
	count := len(destination)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		destination[index] = 'x'
	}
	reader.remaining -= count
	reader.delivered += count
	return count, nil
}

type repeatingDigitReader struct {
	remaining int
	delivered int
}

type repeatingIOByteReader byte

func (value repeatingIOByteReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = byte(value)
	}
	return len(destination), nil
}

func (reader *repeatingDigitReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := len(destination)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		destination[index] = '9'
	}
	reader.remaining -= count
	reader.delivered += count
	return count, nil
}

func (reader *scriptedIOReader) Read(
	destination []byte,
) (int, error) {
	if len(reader.steps) == 0 {
		return 0, io.EOF
	}
	step := &reader.steps[0]
	count := copy(destination, step.text)
	step.text = step.text[count:]
	if step.text != "" {
		return count, nil
	}
	err := step.err
	reader.steps = reader.steps[1:]
	return count, err
}
