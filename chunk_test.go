package lua

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestDumpPrototypeUsesLua51NativeChunkHeader(t *testing.T) {
	prototype, err := Compile("@header.lua", `return 42`)
	if err != nil {
		t.Fatalf("compile prototype: %v", err)
	}

	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatalf("dump prototype: %v", err)
	}
	if len(dumped) < lua51ChunkHeaderSize {
		t.Fatalf("dump length = %d, want at least %d", len(dumped), lua51ChunkHeaderSize)
	}

	header := []byte(dumped[:lua51ChunkHeaderSize])
	want := []byte{
		0x1b, 'L', 'u', 'a',
		0x51,
		0,
		nativeEndianMarker(),
		lua51IntegerSize,
		byte(unsafe.Sizeof(uintptr(0))),
		lua51InstructionSize,
		lua51NumberSize,
		0,
	}
	if string(header) != string(want) {
		t.Fatalf("chunk header = % x, want % x", header, want)
	}
}

func TestDumpPrototypeIsDeterministicAndRejectsInvalidInput(t *testing.T) {
	prototype, err := Compile("@deterministic.lua", `
local prefix = "answer:"
return function(value)
	return prefix .. value, value == 42
end
`)
	if err != nil {
		t.Fatalf("compile prototype: %v", err)
	}

	first, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatalf("first dump: %v", err)
	}
	second, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatalf("second dump: %v", err)
	}
	if first != second {
		t.Fatal("dumping the same immutable prototype produced different bytes")
	}

	if _, err := dumpPrototype(nil); !errors.Is(err, ErrInvalidPrototype) {
		t.Fatalf("nil prototype error = %v, want ErrInvalidPrototype", err)
	}
	if _, err := dumpPrototype(&Prototype{}); !errors.Is(err, ErrInvalidPrototype) {
		t.Fatalf("unsealed prototype error = %v, want ErrInvalidPrototype", err)
	}
}

func TestDumpPrototypeLoadsAndExecutesInLua51(t *testing.T) {
	executable := lua51ChunkTestExecutable(t)
	prototype, err := Compile("@native-chunk.lua", `
local offset = 7
local function accumulate(...)
	local values = {...}
	local total = 0
	for index = 1, #values do
		total = total + values[index]
	end
	return function(value)
		return value + total + offset, "ok\0tail"
	end
end

local result, label = accumulate(1, 2, 3)(10)
io.write(result, "|", string.byte(label, 3), "|", #label)
`)
	if err != nil {
		t.Fatalf("compile prototype: %v", err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatalf("dump prototype: %v", err)
	}

	chunkPath := filepath.Join(t.TempDir(), "native.luac")
	if err := os.WriteFile(chunkPath, []byte(dumped), 0o600); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	output, runErr := exec.Command(executable, chunkPath).CombinedOutput()
	if runErr != nil {
		t.Fatalf(
			"Lua 5.1 rejected dumped chunk: %v\n%s",
			runErr,
			output,
		)
	}
	if got, want := string(output), "23|0|7"; got != want {
		t.Fatalf("dumped chunk output = %q, want %q", got, want)
	}
}

func TestDecodeBinaryChunkRoundTripsAndExecutes(t *testing.T) {
	compiled, err := Compile("@roundtrip.lua", `
local function first()
	return "shared\0value"
end
local function second()
	return "shared\0value"
end
return first(), second(), -0.0
`)
	if err != nil {
		t.Fatalf("compile prototype: %v", err)
	}
	dumped, err := dumpPrototype(compiled)
	if err != nil {
		t.Fatalf("dump prototype: %v", err)
	}
	decoded, err := decodeChunkForTest("@roundtrip.luac", dumped)
	if err != nil {
		t.Fatalf("decode prototype: %v", err)
	}
	if !decoded.sealed {
		t.Fatal("decoded Prototype is not sealed")
	}
	if len(decoded.children) != 2 ||
		len(decoded.children[0].constants) != 1 ||
		len(decoded.children[1].constants) != 1 {
		t.Fatal("decoded nested constants have an unexpected shape")
	}
	firstString := (*luaString)(decoded.children[0].constants[0].ref)
	secondString := (*luaString)(decoded.children[1].constants[0].ref)
	if firstString != secondString {
		t.Fatal("equal strings in separate functions were not interned together")
	}

	redumped, err := dumpPrototype(decoded)
	if err != nil {
		t.Fatalf("redump prototype: %v", err)
	}
	if redumped != dumped {
		t.Fatal("decode followed by dump changed the binary chunk")
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadPrototype(decoded)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("shared\000value"),
		state.String("shared\000value"),
		Number(math.Copysign(0, -1)),
	)
}

func TestDecodeBinaryChunkAcceptsPUC51Output(t *testing.T) {
	lua := lua51ChunkTestExecutable(t)
	luac := filepath.Join(filepath.Dir(lua), "luac")
	if _, err := os.Stat(luac); err != nil {
		t.Skipf("Lua 5.1 compiler is unavailable: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "puc.lua")
	if err := os.WriteFile(
		sourcePath,
		[]byte(`local value = 19
return value * 2, "puc\0chunk"
`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, stripped := range []bool{false, true} {
		name := "debug"
		if stripped {
			name = "stripped"
		}
		t.Run(name, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), name+".luac")
			arguments := []string{"-o", outputPath, sourcePath}
			if stripped {
				arguments = append([]string{"-s"}, arguments...)
			}
			if output, runErr := exec.Command(
				luac,
				arguments...,
			).CombinedOutput(); runErr != nil {
				t.Fatalf("compile PUC chunk: %v\n%s", runErr, output)
			}
			encoded, readErr := os.ReadFile(outputPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			prototype, decodeErr := decodeChunkForTest(
				"@"+outputPath,
				string(encoded),
			)
			if decodeErr != nil {
				t.Fatalf("decode PUC chunk: %v", decodeErr)
			}
			assertDecodedChunkResults(
				t,
				prototype,
				[]Value{Number(38), stringValue(newLuaString("puc\000chunk"))},
			)
			if stripped && prototype.SourceName() != "=?" {
				t.Fatalf(
					"stripped source name = %q; want =?",
					prototype.SourceName(),
				)
			}
		})
	}

	t.Run("all opcodes", func(t *testing.T) {
		sourcePath := filepath.Join(t.TempDir(), "opcodes.lua")
		if err := os.WriteFile(
			sourcePath,
			[]byte(pucOpcodeCoverageSource),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		for _, stripped := range []bool{false, true} {
			name := "debug"
			if stripped {
				name = "stripped"
			}
			t.Run(name, func(t *testing.T) {
				outputPath := filepath.Join(t.TempDir(), name+".luac")
				arguments := []string{"-o", outputPath, sourcePath}
				if stripped {
					arguments = append([]string{"-s"}, arguments...)
				}
				if output, runErr := exec.Command(
					luac,
					arguments...,
				).CombinedOutput(); runErr != nil {
					t.Fatalf(
						"compile PUC opcode chunk: %v\n%s",
						runErr,
						output,
					)
				}
				encoded, readErr := os.ReadFile(outputPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				prototype, decodeErr := decodeChunkForTest(
					"@"+outputPath,
					string(encoded),
				)
				if decodeErr != nil {
					t.Fatalf(
						"decode PUC opcode chunk: %v",
						decodeErr,
					)
				}
				seen := make(map[opcode]bool, opCount)
				recordTestPrototypeOpcodes(prototype, seen)
				for operation := opcode(0); operation < opCount; operation++ {
					if !seen[operation] {
						t.Errorf(
							"PUC coverage chunk omitted %s",
							operation,
						)
					}
				}
			})
		}
	})

	dumped, err := exec.Command(
		lua,
		"-e",
		`io.write(string.dump(function(value) return value + 5 end))`,
	).Output()
	if err != nil {
		t.Fatalf("produce PUC string.dump: %v", err)
	}
	prototype, err := decodeChunkForTest(string(dumped), string(dumped))
	if err != nil {
		t.Fatalf("decode PUC string.dump: %v", err)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value(), Number(7))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(12))
}

const pucOpcodeCoverageSource = `
local outer = 3

local function iterator(limit, value)
	value = value + 1
	if value <= limit then
		return value, value * 2
	end
end

local function tail(value)
	return value
end

local function exercise(...)
	local a, b, c
	a = 2
	b = 3
	c = nil
	local values = {a, b, ..., label = "x"}
	values[1] = values[1] + values[2] - 1
	values[2] = values[1] * 2 / 2 % 5
	values.pow = values[2] ^ 2
	values.neg = -values.pow
	values.len = #values
	values.text = values.label .. tostring(values[1])
	local comparison = a < b
	local negated = not c
	local choice = c or a
	values.comparison = comparison
	values.negated = negated
	values.choice = choice
	if not c and (a < b or a == b) and b >= a then
		values.ok = true
	else
		values.ok = false
	end
	for index = 1, 3 do
		values[index] = index
	end
	for key, value in iterator, 2, 0 do
		values[key] = value
	end
	do
		local captured = outer
		values.closure = function()
			captured = captured + 1
			return captured
		end
	end
	function values:method(value)
		return self[1] + value
	end
	values.called = values:method(1)
	coverage_global = values
	return tail(values)
end

return exercise(4, 5, 6)
`

func recordTestPrototypeOpcodes(
	prototype *Prototype,
	seen map[opcode]bool,
) {
	for _, code := range prototype.code {
		operation := code.opcode()
		if operation < opCount {
			seen[operation] = true
		}
	}
	for _, child := range prototype.children {
		recordTestPrototypeOpcodes(child, seen)
	}
}

func TestDecodeBinaryChunkRejectsHeadersAndEveryTruncation(t *testing.T) {
	prototype, err := Compile("@malformed.lua", `return "payload"`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("empty chunk name", func(t *testing.T) {
		_, decodeErr := decodeChunkForTest("", dumped[:1])
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			": unexpected end in precompiled chunk",
		)
	})

	for index := 0; index < lua51ChunkHeaderSize; index++ {
		mutated := []byte(dumped)
		mutated[index] ^= 0xff
		_, decodeErr := decodeChunkForTest(
			"@header.luac",
			string(mutated),
		)
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			"header.luac: bad header in precompiled chunk",
		)
	}

	for length := 0; length < len(dumped); length++ {
		_, decodeErr := decodeChunkForTest(
			"@truncated.luac",
			dumped[:length],
		)
		if decodeErr == nil {
			t.Fatalf("decoder accepted %d-byte truncated chunk", length)
		}
		var luaErr *Error
		if !errors.As(decodeErr, &luaErr) ||
			luaErr.Category() != SyntaxError {
			t.Fatalf(
				"truncation %d error = %#v; want SyntaxError",
				length,
				decodeErr,
			)
		}
	}
}

func TestDecodeBinaryChunkBoundsCountsAndValidatesStringsAndTags(
	t *testing.T,
) {
	prototype, err := Compile("@mutation.lua", `return "payload"`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	layout := inspectTestChunkRoot(t, dumped)
	order := nativeByteOrder()

	t.Run("initial decoder storage", func(t *testing.T) {
		limit := int(unsafe.Sizeof(compileUnit{})) +
			chunkStringTableBytes +
			chunkStringIndexBytes +
			len(chunkFallbackSource) -
			1
		control, failure := newLoadControl(nil, limit)
		if failure != nil {
			t.Fatal(failure)
		}
		input := newStringChunkInput(dumped, &control)
		_, decodeErr := decodeBinaryChunk(
			"@initial-storage.luac",
			input,
			&control,
		)
		var luaErr *Error
		if !errors.As(decodeErr, &luaErr) ||
			luaErr.Category() != ResourceError {
			t.Fatalf(
				"initial storage error = %#v; want ResourceError",
				decodeErr,
			)
		}
		if input.position != 0 {
			t.Fatalf(
				"decoder consumed %d bytes before reserving initial storage",
				input.position,
			)
		}
	})

	t.Run("projected storage boundary", func(t *testing.T) {
		control, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		if _, decodeErr := decodeBinaryChunk(
			"@storage.luac",
			newStringChunkInput(dumped, &control),
			&control,
		); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if control.storageBytes <= uint64(len(dumped)) {
			t.Fatalf(
				"projected storage %d does not isolate the %d-byte input",
				control.storageBytes,
				len(dumped),
			)
		}

		exact, failure := newLoadControl(
			nil,
			int(control.storageBytes),
		)
		if failure != nil {
			t.Fatal(failure)
		}
		if _, decodeErr := decodeBinaryChunk(
			"@storage.luac",
			newStringChunkInput(dumped, &exact),
			&exact,
		); decodeErr != nil {
			t.Fatalf("decode at exact storage limit: %v", decodeErr)
		}

		short, failure := newLoadControl(
			nil,
			int(control.storageBytes)-1,
		)
		if failure != nil {
			t.Fatal(failure)
		}
		_, decodeErr := decodeBinaryChunk(
			"@storage.luac",
			newStringChunkInput(dumped, &short),
			&short,
		)
		var luaErr *Error
		if !errors.As(decodeErr, &luaErr) ||
			luaErr.Category() != ResourceError {
			t.Fatalf(
				"short storage error = %#v; want ResourceError",
				decodeErr,
			)
		}
	})

	t.Run("negative count", func(t *testing.T) {
		mutated := []byte(dumped)
		order.PutUint32(mutated[layout.codeCount:], ^uint32(0))
		_, decodeErr := decodeChunkForTest(
			"@negative.luac",
			string(mutated),
		)
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			"negative.luac: bad integer in precompiled chunk",
		)
	})

	t.Run("bounded huge count", func(t *testing.T) {
		mutated := []byte(dumped)
		order.PutUint32(mutated[layout.codeCount:], uint32(math.MaxInt32))
		control, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		_, decodeErr := decodeBinaryChunk(
			"@huge.luac",
			newStringChunkInput(string(mutated), &control),
			&control,
		)
		var luaErr *Error
		if !errors.As(decodeErr, &luaErr) ||
			luaErr.Category() != ResourceError {
			t.Fatalf("huge count error = %#v; want ResourceError", decodeErr)
		}
	})

	t.Run("unknown constant", func(t *testing.T) {
		mutated := []byte(dumped)
		mutated[layout.firstConstantTag] = 0xff
		_, decodeErr := decodeChunkForTest(
			"@tag.luac",
			string(mutated),
		)
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			"tag.luac: bad constant in precompiled chunk",
		)
	})

	t.Run("null string constant", func(t *testing.T) {
		mutated := []byte(dumped)
		putTestSize(mutated[layout.firstStringSize:], 0)
		_, decodeErr := decodeChunkForTest(
			"@null-string.luac",
			string(mutated),
		)
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			"null-string.luac: bad constant in precompiled chunk",
		)
	})

	t.Run("missing string terminator", func(t *testing.T) {
		mutated := []byte(dumped)
		mutated[layout.sourceTerminator] = 'x'
		_, decodeErr := decodeChunkForTest(
			"@string.luac",
			string(mutated),
		)
		assertChunkError(
			t,
			decodeErr,
			SyntaxError,
			"string.luac: bad string in precompiled chunk",
		)
	})
}

func TestChunkDecoderDoesNotChargeRepeatedStrings(t *testing.T) {
	const text = "repeated"
	writer := chunkWriter{
		order:    nativeByteOrder(),
		wordSize: int(unsafe.Sizeof(uintptr(0))),
	}
	value := newLuaString(text)
	writer.writeString(value)
	writer.writeString(value)
	if writer.err != nil {
		t.Fatal(writer.err)
	}

	encoded := writer.output.String()
	for _, fragmented := range []bool{false, true} {
		name := "contiguous"
		if fragmented {
			name = "fragmented"
		}
		t.Run(name, func(t *testing.T) {
			control, failure := newLoadControl(nil, 1<<20)
			if failure != nil {
				t.Fatal(failure)
			}
			var input *chunkInput
			if fragmented {
				offset := 0
				input = newRefillableChunkInput(
					func() (string, error) {
						if offset == len(encoded) {
							return "", nil
						}
						piece := encoded[offset : offset+1]
						offset++
						return piece, nil
					},
					&control,
				)
			} else {
				input = newStringChunkInput(encoded, &control)
			}
			decoder := chunkDecoder{
				sourceName: "@strings.luac",
				input:      input,
				control:    &control,
				unit:       newCompileUnit(chunkFallbackSource),
				order:      nativeByteOrder(),
				wordSize:   int(unsafe.Sizeof(uintptr(0))),
			}
			first, err := decoder.readString()
			if err != nil {
				t.Fatalf("read first string: %v", err)
			}
			used := control.storageBytes
			second, err := decoder.readString()
			if err != nil {
				t.Fatalf("read repeated string: %v", err)
			}
			if first != second {
				t.Fatal("repeated strings did not share canonical identity")
			}
			if control.storageBytes != used {
				t.Fatalf(
					"repeated string storage = %d; want unchanged %d",
					control.storageBytes,
					used,
				)
			}
		})
	}
}

func TestDecodeBinaryChunkBoundsDepthAndDoesNotConsumeTrailingInput(
	t *testing.T,
) {
	if _, err := decodeChunkForTest(
		"@depth-200.luac",
		nestedTestChunk(200),
	); err != nil {
		t.Fatalf("decode depth 200: %v", err)
	}
	if _, err := decodeChunkForTest(
		"@depth-201.luac",
		nestedTestChunk(201),
	); err == nil {
		t.Fatal("decoder accepted depth 201")
	} else {
		assertChunkError(
			t,
			err,
			SyntaxError,
			"depth-201.luac: code too deep in precompiled chunk",
		)
	}

	prototype, err := Compile("@trailing.lua", `return 41`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	control, failure := newLoadControl(nil, len(dumped)*16)
	if failure != nil {
		t.Fatal(failure)
	}
	input := newStringChunkInput(dumped+"ignored trailing bytes", &control)
	decoded, err := decodeBinaryChunk("@trailing.luac", input, &control)
	if err != nil {
		t.Fatalf("decode with trailing input: %v", err)
	}
	if input.position != uint64(len(dumped)) {
		t.Fatalf(
			"decoder consumed %d bytes; root ends at %d",
			input.position,
			len(dumped),
		)
	}
	assertDecodedChunkResults(t, decoded, []Value{Number(41)})

	piece := 0
	refills := 0
	control, failure = newLoadControl(nil, len(dumped)*16)
	if failure != nil {
		t.Fatal(failure)
	}
	fragmented := newRefillableChunkInput(func() (string, error) {
		refills++
		if piece == len(dumped) {
			return "", errors.New("decoder requested trailing input")
		}
		next := dumped[piece : piece+1]
		piece++
		return next, nil
	}, &control)
	decoded, err = decodeBinaryChunk(
		"@fragmented.luac",
		fragmented,
		&control,
	)
	if err != nil {
		t.Fatalf("decode one-byte pieces: %v", err)
	}
	if piece != len(dumped) || refills != len(dumped) {
		t.Fatalf(
			"fragmented reader consumed %d bytes in %d calls; want %d",
			piece,
			refills,
			len(dumped),
		)
	}
	assertDecodedChunkResults(t, decoded, []Value{Number(41)})
}

func TestDecodeBinaryChunkPropagatesCancellationAndRefillErrors(t *testing.T) {
	prototype, err := Compile("@control.lua", `return 41`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pre-cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, failure := newLoadControl(ctx, 1<<20)
		if failure == nil || failure.Category() != ContextError {
			t.Fatalf("pre-cancelled control = %#v; want ContextError", failure)
		}
	})

	t.Run("mid-decode", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		control, failure := newLoadControl(ctx, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		offset := 0
		input := newRefillableChunkInput(func() (string, error) {
			if offset == len(dumped)/2 {
				cancel()
			}
			if offset == len(dumped) {
				return "", nil
			}
			piece := dumped[offset : offset+1]
			offset++
			return piece, nil
		}, &control)
		_, decodeErr := decodeBinaryChunk(
			"@cancelled.luac",
			input,
			&control,
		)
		var luaErr *Error
		if !errors.As(decodeErr, &luaErr) ||
			luaErr.Category() != ContextError {
			t.Fatalf("mid-decode error = %#v; want ContextError", decodeErr)
		}
	})

	t.Run("refill failure", func(t *testing.T) {
		refillErr := errors.New("reader failed")
		control, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		supplied := false
		input := newRefillableChunkInput(func() (string, error) {
			if !supplied {
				supplied = true
				return dumped[:lua51ChunkHeaderSize], nil
			}
			return "", refillErr
		}, &control)
		_, decodeErr := decodeBinaryChunk(
			"@reader.luac",
			input,
			&control,
		)
		if !errors.Is(decodeErr, refillErr) {
			t.Fatalf(
				"refill error = %#v; want original failure",
				decodeErr,
			)
		}
	})

	t.Run("shared control invariant", func(t *testing.T) {
		first, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		second, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("mismatched controls did not panic")
			}
		}()
		_, _ = decodeBinaryChunk(
			"@mismatch.luac",
			newStringChunkInput(dumped, &first),
			&second,
		)
	})
}

type testChunkRootLayout struct {
	codeCount        int
	firstConstantTag int
	firstStringSize  int
	sourceTerminator int
}

func inspectTestChunkRoot(
	t *testing.T,
	encoded string,
) testChunkRootLayout {
	t.Helper()
	bytes := []byte(encoded)
	order := nativeByteOrder()
	wordSize := int(unsafe.Sizeof(uintptr(0)))
	offset := lua51ChunkHeaderSize
	sourceSize := testSize(bytes[offset:])
	offset += wordSize
	if sourceSize == 0 || sourceSize > uint64(len(bytes)-offset) {
		t.Fatal("test chunk has an invalid root source")
	}
	sourceTerminator := offset + int(sourceSize) - 1
	offset += int(sourceSize)
	offset += 8 // line-defined and last-line integers.
	offset += 4 // upvalues, parameters, vararg flags, and registers.
	codeCount := offset
	instructions := int(order.Uint32(bytes[offset:]))
	offset += 4 + instructions*lua51InstructionSize
	constants := int(order.Uint32(bytes[offset:]))
	offset += 4
	if constants == 0 {
		t.Fatal("test chunk has no constants")
	}
	firstConstantTag := offset
	if bytes[offset] != 4 {
		t.Fatalf("first test constant tag = %d; want string", bytes[offset])
	}
	offset++
	firstStringSize := offset
	return testChunkRootLayout{
		codeCount:        codeCount,
		firstConstantTag: firstConstantTag,
		firstStringSize:  firstStringSize,
		sourceTerminator: sourceTerminator,
	}
}

func testSize(encoded []byte) uint64 {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return uint64(nativeByteOrder().Uint32(encoded))
	}
	return nativeByteOrder().Uint64(encoded)
}

func putTestSize(destination []byte, value uint64) {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		nativeByteOrder().PutUint32(destination, uint32(value))
		return
	}
	nativeByteOrder().PutUint64(destination, value)
}

func nestedTestChunk(depth int) string {
	writer := chunkWriter{
		order:    nativeByteOrder(),
		wordSize: int(unsafe.Sizeof(uintptr(0))),
	}
	writer.writeHeader()
	writeNestedTestFunction(&writer, depth, true)
	if writer.err != nil {
		panic(writer.err)
	}
	return writer.output.String()
}

func writeNestedTestFunction(
	writer *chunkWriter,
	depth int,
	root bool,
) {
	if root {
		writer.writeString(newLuaString("@depth.lua"))
	} else {
		writer.writeString(nil)
	}
	writer.writeInt32(0)
	writer.writeInt32(0)
	writer.writeByte(0)
	writer.writeByte(0)
	writer.writeByte(varargIsVararg)
	writer.writeByte(2)
	writer.writeInt32(1)
	writer.writeUint32(uint32(makeABC(opReturn, 0, 1, 0)))
	writer.writeInt32(0)
	if depth > 1 {
		writer.writeInt32(1)
		writeNestedTestFunction(writer, depth-1, false)
	} else {
		writer.writeInt32(0)
	}
	writer.writeInt32(0)
	writer.writeInt32(0)
	writer.writeInt32(0)
}

func decodeChunkForTest(
	sourceName string,
	encoded string,
) (*Prototype, error) {
	limit := len(encoded) * 32
	if limit < 1<<20 {
		limit = 1 << 20
	}
	control, failure := newLoadControl(nil, limit)
	if failure != nil {
		return nil, failure
	}
	return decodeBinaryChunk(
		sourceName,
		newStringChunkInput(encoded, &control),
		&control,
	)
}

func assertDecodedChunkResults(
	t *testing.T,
	prototype *Prototype,
	want []Value,
) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, want...)
}

func assertChunkError(
	t *testing.T,
	err error,
	category ErrorCategory,
	message string,
) {
	t.Helper()
	var luaErr *Error
	if !errors.As(err, &luaErr) ||
		luaErr.Category() != category ||
		luaErr.Error() != message {
		t.Fatalf(
			"chunk error = %#v; want %v %q",
			err,
			category,
			message,
		)
	}
}

func FuzzDecodeBinaryChunk(f *testing.F) {
	prototype, err := Compile("@fuzz-seed.lua", `return "seed", 42`)
	if err != nil {
		f.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(dumped))
	f.Add([]byte{})
	f.Add([]byte{0x1b, 'L', 'u', 'a'})
	f.Add([]byte("not a binary chunk"))

	f.Fuzz(func(t *testing.T, encoded []byte) {
		control, failure := newLoadControl(nil, 1<<20)
		if failure != nil {
			t.Fatal(failure)
		}
		prototype, decodeErr := decodeBinaryChunk(
			"@fuzz.luac",
			newStringChunkInput(string(encoded), &control),
			&control,
		)
		if decodeErr == nil && (prototype == nil || !prototype.sealed) {
			t.Fatal("decoder returned an unsealed Prototype")
		}
	})
}

func lua51ChunkTestExecutable(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("BADGER_LUA51")); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			t.Fatalf("BADGER_LUA51=%q is unavailable: %v", configured, err)
		}
		return configured
	}

	const localLua51 = "/private/tmp/lua-5.1.5/src/lua"
	if _, err := os.Stat(localLua51); err == nil {
		return localLua51
	}
	t.Skip("Lua 5.1 interpreter is not available")
	return ""
}
