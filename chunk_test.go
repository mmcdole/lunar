package lua

import (
	"errors"
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
