package lua

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileSourceIndexesAndUpdatesTables(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"local object, key = root, dynamic\n"+
			"object[key] = input\n"+
			"object.name = object[key]\n"+
			"return object.name",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	gets := 0
	sets := 0
	moves := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opGetTable:
			gets++
		case opSetTable:
			sets++
		case opMove:
			moves++
		}
	}
	if gets != 2 || sets != 2 {
		t.Fatalf(
			"table operations = %d GETTABLE, %d SETTABLE; want 2 and 2",
			gets,
			sets,
		)
	}
	if moves != 0 {
		t.Fatalf("indexed operations emitted %d avoidable MOVE instructions", moves)
	}
}

func TestCompileSourceReusesIndexedTemporaryAcrossChain(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@chain.lua",
		"return root[first][second].value",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var gets []instruction
	for _, code := range prototype.code {
		if code.opcode() == opGetTable {
			gets = append(gets, code)
		}
	}
	if len(gets) != 3 {
		t.Fatalf("GETTABLE count = %d, want 3", len(gets))
	}
	if prototype.RegisterCount() > 3 {
		t.Fatalf(
			"indexed chain requires %d registers, want at most 3",
			prototype.RegisterCount(),
		)
	}
	for index := 1; index < len(gets); index++ {
		if gets[index].b() != gets[index-1].a() {
			t.Fatalf(
				"GETTABLE %d reads R%d, prior result is R%d",
				index,
				gets[index].b(),
				gets[index-1].a(),
			)
		}
	}
}

func TestCompileSourceKeepsFinalIndexAsAssignmentTarget(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"root[first][second] = value",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var operations []opcode
	for _, code := range prototype.code {
		switch code.opcode() {
		case opGetGlobal, opGetTable, opSetTable:
			operations = append(operations, code.opcode())
		}
	}
	want := []opcode{
		opGetGlobal,
		opGetGlobal,
		opGetTable,
		opGetGlobal,
		opGetGlobal,
		opSetTable,
	}
	if len(operations) != len(want) {
		t.Fatalf("table operation sequence = %v, want %v", operations, want)
	}
	for index := range want {
		if operations[index] != want[index] {
			t.Fatalf("table operation sequence = %v, want %v", operations, want)
		}
	}
}

func TestCompileSourceAssignsThroughParenthesizedPrefix(t *testing.T) {
	for _, source := range []string{
		"(root)[key] = value",
		"(root).field = value",
		"((root))[key] = value",
		"(root[key]).field = value",
	} {
		prototype, syntaxError := compileSource("@assignment.lua", source)
		if syntaxError != nil {
			t.Fatalf("%q: %v", source, syntaxError)
		}
		sets := 0
		for _, code := range prototype.code {
			if code.opcode() == opSetTable {
				sets++
			}
		}
		if sets != 1 {
			t.Fatalf("%q: SETTABLE count = %d, want 1", source, sets)
		}
	}
}

func TestCompileSourceSpillsLargeFieldKeyWithoutLosingTable(t *testing.T) {
	var source strings.Builder
	source.WriteString("local object, sink = ...\n")
	for number := 0; number < 256; number++ {
		source.WriteString("sink = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString("return object.field")

	prototype, syntaxError := compileSource("@field.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	fieldIndex := -1
	for index, constant := range prototype.constants {
		if constant.kind() == StringKind &&
			(*luaString)(constant.ref).text == "field" {
			fieldIndex = index
			break
		}
	}
	if fieldIndex <= maxRegisterConstant {
		t.Fatalf(
			"field constant index = %d, want above RK limit %d",
			fieldIndex,
			maxRegisterConstant,
		)
	}

	keyRegister := -1
	var get instruction
	for _, code := range prototype.code {
		if code.opcode() == opLoadK && code.bx() == fieldIndex {
			keyRegister = code.a()
		}
		if code.opcode() == opGetTable {
			get = code
		}
	}
	if keyRegister < 0 ||
		get.opcode() != opGetTable ||
		get.b() != 0 ||
		get.c() != keyRegister ||
		get.a() != keyRegister {
		t.Fatalf(
			"field spill/get = key R%d, GETTABLE A:%d B:%d C:%d",
			keyRegister,
			get.a(),
			get.b(),
			get.c(),
		)
	}
}

func TestCompileSourceRejectsMalformedIndex(t *testing.T) {
	for _, source := range []string{
		"return value[]",
		"return value[key",
		"value.field",
		"value[1] + 2 = 3",
		"(value) = 1",
		"(value[key]) = 1",
		"(value.field) = 1",
	} {
		if _, syntaxError := compileSource(
			"@invalid.lua",
			source,
		); syntaxError == nil || syntaxError.Category() != SyntaxError {
			t.Fatalf("%q: syntax error = %v", source, syntaxError)
		}
	}
}
