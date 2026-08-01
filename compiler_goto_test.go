package lua

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestGotoCanJumpForward(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
goto answer
ignored = 0
::answer::
return 42
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestGotoCanJumpBackward(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local count = 0
::again::
count = count + 1
if count < 3 then goto again end
return count
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(3))
}

func TestGotoCannotSeeLabelInNestedBlock(t *testing.T) {
	_, err := Compile("@goto.lua", `
do
	::inside::
end
goto inside
`)
	if err == nil {
		t.Fatal("nested label was visible outside its block")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError {
		t.Fatalf("error = %T %v, want syntax error", err, err)
	}
	if message := failure.Error(); !strings.Contains(message, "goto.lua:5") ||
		!strings.Contains(message, "no visible label 'inside'") {
		t.Fatalf("error = %q", message)
	}
}

func TestGotoCannotSeeLabelInSiblingBlock(t *testing.T) {
	_, err := Compile("@goto.lua", `
do
	goto sibling
end
do
	::sibling::
end
`)
	if err == nil {
		t.Fatal("sibling label was visible outside its block")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError ||
		!strings.Contains(failure.Error(), "goto.lua:3") ||
		!strings.Contains(failure.Error(), "no visible label 'sibling'") {
		t.Fatalf("error = %T %v, want undefined sibling label", err, err)
	}
}

func TestGotoUsesLaterLabelInItsOwnBlockBeforeOuterLabel(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local visits = 0
::target::
visits = visits + 1
do
	if visits > 1 then return "outer" end
	goto target
	::target::
	return "inner"
end
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("inner"))
}

func TestGotoCanResolveLaterLabelInOuterBlock(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
do
	goto outside
end
ignored = 1
::outside::
return 42
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestGotoCannotJumpIntoLocalScope(t *testing.T) {
	_, err := Compile("@goto.lua", `
goto target
local hidden = 1
::target::
return hidden
`)
	if err == nil {
		t.Fatal("goto entered the scope of a local")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError {
		t.Fatalf("error = %T %v, want syntax error", err, err)
	}
	if message := failure.Error(); !strings.Contains(message, "goto.lua:2") ||
		!strings.Contains(message, "<goto target>") ||
		!strings.Contains(message, "scope of local 'hidden'") {
		t.Fatalf("error = %q", message)
	}
}

func TestGotoRejectsDuplicateLabelsInOneBlock(t *testing.T) {
	_, err := Compile("@goto.lua", `
::duplicate::
::duplicate::
`)
	if err == nil {
		t.Fatal("duplicate label compiled")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError {
		t.Fatalf("error = %T %v, want syntax error", err, err)
	}
	if message := failure.Error(); !strings.Contains(message, "goto.lua:3") ||
		!strings.Contains(message, "label 'duplicate'") ||
		!strings.Contains(message, "already defined on line 2") {
		t.Fatalf("error = %q", message)
	}
}

func TestGotoCanJumpToLabelAtEndOfBlock(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
do
	goto done
	local hidden = 1
	::done::
end
return 42
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestGotoCanJumpToTrailingLabelChain(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
do
	goto first
	local hidden = 1
	::first::; ::second::;
end
return 42
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestGotoRecognizesEndLabelsAtBlockBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "before else",
			source: `
if true then
	goto done
	local hidden = 1
	::done::;
else
end
`,
		},
		{
			name: "before elseif",
			source: `
if true then
	goto done
	local hidden = 1
	::done::;
elseif false then
end
`,
		},
		{
			name: "before function end",
			source: `
local function run()
	goto done
	local hidden = 1
	::done::;
end
return run
`,
		},
		{
			name: "before source end",
			source: `
goto done
local hidden = 1
::done::;
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compile("@goto.lua", test.source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGotoClosesCapturedLocalsWhenLeavingScope(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local read
do
	local captured = 1
	read = function() return captured end
	goto done
end
::done::
local replacement = 2
return read(), replacement
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1), Number(2))
}

func TestGotoAccumulatesClosesAcrossNestedScopes(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local readOuter, readInner
do
	local outer = 1
	readOuter = function() return outer end
	do
		local inner = 2
		readInner = function() return inner end
		goto done
	end
end
::done::
local replacementOuter, replacementInner = 3, 4
return readOuter(), readInner(), replacementOuter, replacementInner
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(1),
		Number(2),
		Number(3),
		Number(4),
	)
}

func TestGotoRemainsAvailableAsAnIdentifier(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local goto = 41
goto = goto + 1
return goto
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestGotoDoesNotCloseCapturedLocalsInTheSameScope(t *testing.T) {
	const source = `
local x = 1
local f = function() return x end
::again::
x = x + 1
if x < 3 then goto again end
assert(f() == 3)
return f()
`
	prototype, err := Compile("@goto.lua", source)
	if err != nil {
		t.Fatal(err)
	}
	for pc, code := range prototype.code {
		if code.opcode() == opClose {
			t.Fatalf("same-scope goto emitted CLOSE at pc %d", pc)
		}
	}

	state, err := New(Options{Libraries: CoreLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", source)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(3))
}

func TestBackwardGotoClosesOnlyCapturedLocalSuffix(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local first, second, third
local iteration = 1
::again::
local value = iteration
if iteration == 1 then
	first = function() return value end
elseif iteration == 2 then
	second = function() return value end
else
	third = function() return value end
end
iteration = iteration + 1
if iteration <= 3 then goto again end
return first(), second(), third()
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1), Number(2), Number(3))
}

func TestBackwardGotoAccountsForCaptureDiscoveredLater(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local saved
local phase = 0
::outer::
local x = phase + 10
::check::
if phase == 1 then
	phase = 2
	goto outer
end
if phase == 2 then return saved(), x end
saved = function() return x end
phase = 1
goto check
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(10), Number(12))
}

func TestBackwardGotoKeepsCapturedPrefixOpen(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	results, err := state.DoString("@goto.lua", `
local x = 1
local read = function() return x end
local iteration = 0
::again::
local scratch = iteration
iteration = iteration + 1
x = x + 1
if iteration < 2 then goto again end
return read(), scratch
`)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(3), Number(1))
}

func TestGotoWorksWithStructuredControlFlow(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   float64
	}{
		{
			name: "conditional",
			source: `
local value = 0
if true then
	goto done
else
	value = 1
end
value = 2
::done::
return value
`,
			want: 0,
		},
		{
			name: "while",
			source: `
local value, sum = 0, 0
while value < 4 do
	value = value + 1
	if value % 2 == 0 then goto continue end
	sum = sum + value
	::continue::
end
return sum
`,
			want: 4,
		},
		{
			name: "numeric for",
			source: `
local sum = 0
for value = 1, 4 do
	if value % 2 == 0 then goto continue end
	sum = sum + value
	::continue::
end
return sum
`,
			want: 4,
		},
		{
			name: "generic for",
			source: `
local sum = 0
for _, value in ipairs({1, 2, 3, 4}) do
	if value % 2 == 0 then goto continue end
	sum = sum + value
	::continue::
end
return sum
`,
			want: 4,
		},
		{
			name: "repeat",
			source: `
local value = 0
repeat
	::again::
	value = value + 1
	if value < 3 then goto again end
until true
return value
`,
			want: 3,
		},
		{
			name: "nearby break",
			source: `
local value = 0
while true do
	value = value + 1
	if value == 2 then break end
	goto continue
	::continue::
end
return value
`,
			want: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{Libraries: CoreLibraries()})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()

			results, err := state.DoString("@goto.lua", test.source)
			if err != nil {
				t.Fatal(err)
			}
			assertTestValues(t, results, Number(test.want))
		})
	}
}

func TestGotoClosesCapturedLocalsAcrossLoopEdges(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "repeat exit",
			source: `
local read
repeat
	local captured = 7
	read = function() return captured end
	goto done
until true
::done::
local replacement = 9
return read(), replacement
`,
			want: []Value{Number(7), Number(9)},
		},
		{
			name: "numeric for continue",
			source: `
local reads = {}
for index = 1, 3 do
	local captured = index
	reads[index] = function() return captured end
	goto continue
	::continue::
end
return reads[1](), reads[2](), reads[3]()
`,
			want: []Value{Number(1), Number(2), Number(3)},
		},
		{
			name: "generic for continue",
			source: `
local reads = {}
for _, value in ipairs({1, 2, 3}) do
	local captured = value
	reads[value] = function() return captured end
	goto continue
	::continue::
end
return reads[1](), reads[2](), reads[3]()
`,
			want: []Value{Number(1), Number(2), Number(3)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{Libraries: CoreLibraries()})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()

			results, err := state.DoString("@goto.lua", test.source)
			if err != nil {
				t.Fatal(err)
			}
			assertTestValues(t, results, test.want...)
		})
	}
}

func TestLabelBeforeUntilRemainsInsideRepeatLocalScope(t *testing.T) {
	_, err := Compile("@goto.lua", `
repeat
	goto check
	local hidden = 1
	::check::
until true
`)
	if err == nil {
		t.Fatal("label before until was treated as outside repeat locals")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError {
		t.Fatalf("error = %T %v, want syntax error", err, err)
	}
	if message := failure.Error(); !strings.Contains(message, "goto.lua:3") ||
		!strings.Contains(message, "scope of local 'hidden'") {
		t.Fatalf("error = %q", message)
	}
}

func TestGotoReportsSyntaxErrorsAtOffendingLines(t *testing.T) {
	tests := []struct {
		name   string
		source string
		line   string
		want   string
	}{
		{
			name:   "undefined",
			source: "\ngoto missing\n",
			line:   "goto.lua:2",
			want:   "no visible label 'missing'",
		},
		{
			name:   "missing label name",
			source: "\n:: ::\n",
			line:   "goto.lua:2",
			want:   "expected <name> near ::",
		},
		{
			name:   "missing closing delimiter",
			source: "\n::label:\n",
			line:   "goto.lua:2",
			want:   "expected :: near ':'",
		},
		{
			name:   "goto without label name",
			source: "\ngoto 1\n",
			line:   "goto.lua:2",
			want:   "expected '=' near <number>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile("@goto.lua", test.source)
			if err == nil {
				t.Fatal("malformed goto source compiled")
			}
			var failure *Error
			if !errors.As(err, &failure) || failure.Category() != SyntaxError {
				t.Fatalf("error = %T %v, want syntax error", err, err)
			}
			if message := failure.Error(); !strings.Contains(message, test.line) ||
				!strings.Contains(message, test.want) {
				t.Fatalf(
					"error = %q, want %q and %q",
					message,
					test.line,
					test.want,
				)
			}
		})
	}
}

func TestGotoPreservesLua51BreakLastRule(t *testing.T) {
	_, err := Compile(
		"@goto.lua",
		"while true do break; ::after:: end",
	)
	if err == nil {
		t.Fatal("label after break compiled")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != SyntaxError ||
		!strings.Contains(failure.Error(), "break must be the last") {
		t.Fatalf("error = %T %v, want break-last syntax error", err, err)
	}
}

func TestGotoBytecodeRoundTripsThroughLua51ChunkFormat(t *testing.T) {
	prototype, err := Compile("@goto.lua", `
local read
do
	local captured = 7
	read = function() return captured end
	goto done
end
::done::
local replacement = 9
return read(), replacement
`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeChunkForTest("@goto.luac", dumped)
	if err != nil {
		t.Fatal(err)
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
	assertTestValues(t, results, Number(7), Number(9))
}

func TestGotoCompilationMatchesAcrossReaderRefills(t *testing.T) {
	const source = `
local goto = 0
::again::
goto = goto + 1
if goto < 3 then goto again end
return goto
`
	want, syntaxError := compileSource("@goto.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	wantDump, err := dumpPrototype(want)
	if err != nil {
		t.Fatal(err)
	}

	check := func(label string, pieces []string) {
		t.Helper()
		got, compileErr := compileInput(
			"@goto.lua",
			testChunkInput(pieces, nil, nil),
		)
		if compileErr != nil {
			t.Fatalf("%s: %v", label, compileErr)
		}
		gotDump, dumpErr := dumpPrototype(got)
		if dumpErr != nil {
			t.Fatalf("%s dump: %v", label, dumpErr)
		}
		if gotDump != wantDump {
			t.Fatalf("%s produced different prototype bytes", label)
		}
	}
	for split := 1; split < len(source); split++ {
		check(
			"split "+strconv.Itoa(split),
			[]string{source[:split], source[split:]},
		)
	}
	check("one-byte pieces", oneByteTestPieces(source))
}
