package lua

import (
	"errors"
	"io"
	"math"
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
			if failure := endpoint.failure(); failure != nil {
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
