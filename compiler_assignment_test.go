package lua

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileSourceAssignsMultipleValuesRightToLeft(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local first, second = ...\n"+
			"first, second = second, first",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var moves []instruction
	for _, code := range prototype.code {
		if code.opcode() == opMove {
			moves = append(moves, code)
		}
	}
	if len(moves) != 3 ||
		moves[0].a() != 2 ||
		moves[0].b() != 1 ||
		moves[1].a() != 1 ||
		moves[1].b() != 0 ||
		moves[2].a() != 0 ||
		moves[2].b() != 2 {
		t.Fatalf("swap MOVE instructions = %#v", moves)
	}
	if registerCount(prototype) != 3 {
		t.Fatalf(
			"swap register count = %d, want 3",
			registerCount(prototype),
		)
	}
}

func TestCompileSourcePreservesIndexedKeyAcrossLocalAssignment(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local index, object = ...\n"+
			"object[index], index = left(), right()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var calls []instruction
	copyPC := -1
	firstCallPC := -1
	localStorePC := -1
	tableStorePC := -1
	var tableStore instruction
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opMove:
			if code.b() == 0 && code.a() >= 2 {
				copyPC = pc
			}
			if code.a() == 0 && code.b() >= 2 {
				localStorePC = pc
			}
		case opCall:
			if firstCallPC < 0 {
				firstCallPC = pc
			}
			calls = append(calls, code)
		case opSetTable:
			tableStorePC = pc
			tableStore = code
		}
	}
	if copyPC < 0 ||
		firstCallPC < 0 ||
		localStorePC < 0 ||
		tableStorePC < 0 ||
		copyPC >= firstCallPC {
		t.Fatal("assignment did not retain the old index")
	}
	if len(calls) != 2 ||
		calls[0].c() != 2 ||
		calls[1].c() != 2 {
		t.Fatalf("RHS CALL instructions = %#v", calls)
	}
	savedIndex := tableStore.b()
	if savedIndex == 0 ||
		tableStore.a() != 1 ||
		localStorePC >= tableStorePC {
		t.Fatalf(
			"conflict lowering = SETTABLE A:%d B:%d C:%d, local store %d, table store %d",
			tableStore.a(),
			tableStore.b(),
			tableStore.c(),
			localStorePC,
			tableStorePC,
		)
	}

	prototype, syntaxError = compileSource(
		"@assignment.lua",
		"local index, object = ...\n"+
			"index, object[index] = new_index(), value()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var safeStore instruction
	copies := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opMove:
			if code.b() == 0 && code.a() >= 2 {
				copies++
			}
		case opSetTable:
			safeStore = code
		}
	}
	if copies != 0 ||
		safeStore.opcode() != opSetTable ||
		safeStore.b() != 0 {
		t.Fatalf(
			"safe assignment emitted %d copies, SETTABLE B:%d",
			copies,
			safeStore.b(),
		)
	}
}

func TestCompileSourcePreservesIndexedTableAndKeyWithOneCopy(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local object = ...\n"+
			"object[object], object = 1, 2",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	copies := 0
	var set instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opMove:
			if code.b() == 0 && code.a() != 0 {
				copies++
			}
		case opSetTable:
			set = code
		}
	}
	if copies != 1 ||
		set.opcode() != opSetTable ||
		set.a() == 0 ||
		set.a() != set.b() {
		t.Fatalf(
			"table/key conflict = %d copies, SETTABLE A:%d B:%d C:%d",
			copies,
			set.a(),
			set.b(),
			set.c(),
		)
	}
}

func TestCompileSourceSharesConflictCopyAcrossIndexedTargets(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local index, first, second = ...\n"+
			"first[index], second[index], index = 1, 2, 3",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	copies := 0
	var stores []instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opMove:
			if code.b() == 0 && code.a() >= 3 {
				copies++
			}
		case opSetTable:
			stores = append(stores, code)
		}
	}
	if copies != 1 ||
		len(stores) != 2 ||
		stores[0].b() != stores[1].b() ||
		stores[0].b() == 0 {
		t.Fatalf(
			"shared conflict = %d copies, SETTABLE instructions %#v",
			copies,
			stores,
		)
	}
}

func TestCompileSourcePreservesConflictBeforeLaterTarget(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local index, object = ...\n"+
			"object[index], index, mutate().field = 1, 2, 3",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	copyPC := -1
	targetCallPC := -1
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opMove:
			if code.b() == 0 && code.a() >= 2 {
				copyPC = pc
			}
		case opCall:
			if targetCallPC < 0 {
				targetCallPC = pc
			}
		}
	}
	if copyPC < 0 ||
		targetCallPC < 0 ||
		copyPC >= targetCallPC {
		t.Fatalf(
			"conflict copy at %d, later target call at %d",
			copyPC,
			targetCallPC,
		)
	}
}

func TestCompileSourceAdjustsMultipleAssignmentResults(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local first, second, third = ...\n"+
			"first, second, third = produce()\n"+
			"first = 1, discarded()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var calls []instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			calls = append(calls, code)
		}
	}
	if len(calls) != 2 ||
		calls[0].c() != 4 ||
		calls[1].c() != 1 {
		t.Fatalf("assignment CALL instructions = %#v", calls)
	}

	prototype, syntaxError = compileSource(
		"@assignment.lua",
		"local first, second, third = ...\n"+
			"first, second, third = 1",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var fill instruction
	for _, code := range prototype.code {
		if code.opcode() == opLoadNil {
			fill = code
		}
	}
	if fill.opcode() != opLoadNil ||
		fill.a() != 4 ||
		fill.b() != 5 {
		t.Fatalf(
			"missing-value LOADNIL = A:%d B:%d, want A:4 B:5",
			fill.a(),
			fill.b(),
		)
	}

	prototype, syntaxError = compileSource(
		"@assignment.lua",
		"local first, second = ...\n"+
			"first, second = 1, produce()\n"+
			"first, second = 1, ...",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var exactCall instruction
	var variable instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			exactCall = code
		case opVararg:
			variable = code
		}
	}
	if exactCall.opcode() != opCall ||
		exactCall.c() != 2 ||
		variable.opcode() != opVararg ||
		variable.b() != 2 {
		t.Fatalf(
			"exact CALL/VARARG = C:%d B:%d, want 2 and 2",
			exactCall.c(),
			variable.b(),
		)
	}
}

func TestCompileSourceAdjustsAssignmentVarargs(t *testing.T) {
	cases := []struct {
		source string
		count  int
		width  int
	}{
		{source: "first = 1, ...", count: 0},
		{source: "first, second = 1, ...", count: 1, width: 2},
		{source: "first, second, third = 1, ...", count: 1, width: 3},
		{source: "first, second = 1, (...)", count: 1, width: 2},
	}
	for _, test := range cases {
		prototype, syntaxError := compileSource(
			"@assignment.lua",
			test.source,
		)
		if syntaxError != nil {
			t.Fatalf("%s: %v", test.source, syntaxError)
		}
		count := 0
		var variable instruction
		for _, code := range prototype.code {
			if code.opcode() == opVararg {
				count++
				variable = code
			}
		}
		if count != test.count ||
			(count != 0 && variable.b() != test.width) {
			t.Fatalf(
				"%s: VARARG count %d, B:%d; want %d and %d",
				test.source,
				count,
				variable.b(),
				test.count,
				test.width,
			)
		}
	}
}

func TestCompileSourceStoresDuplicateTargetsRightToLeft(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"global, global = 1, 2",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var stores []instruction
	for _, code := range prototype.code {
		if code.opcode() == opSetGlobal {
			stores = append(stores, code)
		}
	}
	if len(stores) != 2 ||
		stores[0].bx() != stores[1].bx() ||
		stores[0].a() <= stores[1].a() {
		t.Fatalf("duplicate SETGLOBAL instructions = %#v", stores)
	}
}

func TestCompileSourceAttributesAssignmentStoresToFinalValue(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"first, second =\n"+
			"  produce(),\n"+
			"  final()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	stores := 0
	for pc, code := range prototype.code {
		if code.opcode() == opSetGlobal {
			stores++
			if prototype.lineAt(pc) != 3 {
				t.Fatalf(
					"SETGLOBAL at %d has line %d, want 3",
					pc,
					prototype.lineAt(pc),
				)
			}
		}
	}
	if stores != 2 {
		t.Fatalf("SETGLOBAL count = %d, want 2", stores)
	}

	prototype, syntaxError = compileSource(
		"@assignment.lua",
		"first, second =\n  value",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	stores = 0
	fills := 0
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLoadNil, opSetGlobal:
			if code.opcode() == opLoadNil {
				fills++
			} else {
				stores++
			}
			if prototype.lineAt(pc) != 2 {
				t.Fatalf(
					"%s at %d has line %d, want 2",
					code.opcode(),
					pc,
					prototype.lineAt(pc),
				)
			}
		}
	}
	if stores != 2 || fills != 1 {
		t.Fatalf(
			"assignment emitted %d SETGLOBAL and %d LOADNIL, want 2 and 1",
			stores,
			fills,
		)
	}
}

func TestCompileSourceEvaluatesAllAssignmentTargetsBeforeValues(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"factory()[key()], other()[other_key()] = left(), right()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var sequence []opcode
	var stores []instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall, opSetTable:
			sequence = append(sequence, code.opcode())
		}
		if code.opcode() == opSetTable {
			stores = append(stores, code)
		}
	}
	if len(sequence) != 8 {
		t.Fatalf("assignment sequence = %v", sequence)
	}
	for index := 0; index < 6; index++ {
		if sequence[index] != opCall {
			t.Fatalf("assignment sequence = %v", sequence)
		}
	}
	if sequence[6] != opSetTable ||
		sequence[7] != opSetTable ||
		len(stores) != 2 ||
		stores[0].a() <= stores[1].a() {
		t.Fatalf(
			"assignment stores = sequence %v, instructions %#v",
			sequence,
			stores,
		)
	}
}

func TestCompileSourceRejectsMalformedMultipleAssignment(t *testing.T) {
	for _, source := range []string{
		"first, = 1",
		"first,, second = 1, 2",
		"first, call() = 1, 2",
		"first + second, third = 1, 2",
		"(first), second = 1, 2",
		"first, second",
		"first, second =",
	} {
		if _, syntaxError := compileSource(
			"@invalid.lua",
			source,
		); syntaxError == nil || syntaxError.Category() != SyntaxError {
			t.Fatalf("%q: syntax error = %v", source, syntaxError)
		}
	}
}

func TestCompileSourceEnforcesAssignmentTargetLimit(t *testing.T) {
	assignment := func(count int) string {
		var source strings.Builder
		for index := 0; index < count; index++ {
			if index != 0 {
				source.WriteByte(',')
			}
			source.WriteByte('v')
			source.WriteString(strconv.Itoa(index))
		}
		source.WriteString(" = produce()")
		return source.String()
	}

	prototype, syntaxError := compileSource(
		"@assignment.lua",
		assignment(maxLuaRegisters),
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if registerCount(prototype) != maxLuaRegisters {
		t.Fatalf(
			"assignment register count = %d, want %d",
			registerCount(prototype),
			maxLuaRegisters,
		)
	}
	var call instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			call = code
		}
	}
	if call.opcode() != opCall ||
		call.c() != maxLuaRegisters+1 {
		t.Fatalf(
			"maximum assignment CALL C = %d, want %d",
			call.c(),
			maxLuaRegisters+1,
		)
	}

	if _, syntaxError = compileSource(
		"@assignment.lua",
		assignment(maxLuaRegisters+1),
	); syntaxError == nil || syntaxError.Category() != SyntaxError {
		t.Fatalf("oversized assignment error = %v", syntaxError)
	}
}
