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

func TestCompileSourceBuildsMixedTableConstructor(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"return {1, 2; name = 3, [key] = value, 4,}",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var allocation instruction
	var list instruction
	records := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opNewTable:
			allocation = code
		case opSetTable:
			records++
		case opSetList:
			list = code
		}
	}
	if allocation.opcode() != opNewTable ||
		floatingByteToInt(allocation.b()) < 3 ||
		floatingByteToInt(allocation.c()) < 2 {
		t.Fatalf(
			"NEWTABLE = B:%d C:%d, want hints for 3 list and 2 record fields",
			allocation.b(),
			allocation.c(),
		)
	}
	if records != 2 {
		t.Fatalf("SETTABLE count = %d, want 2", records)
	}
	if list.opcode() != opSetList ||
		list.a() != allocation.a() ||
		list.b() != 3 ||
		list.c() != 1 {
		t.Fatalf(
			"SETLIST = A:%d B:%d C:%d, want table base, 3 values, block 1",
			list.a(),
			list.b(),
			list.c(),
		)
	}
}

func TestCompileSourceFlushesConstructorListsInBlocks(t *testing.T) {
	var source strings.Builder
	source.WriteString("return {")
	for index := 0; index < fieldsPerFlush+1; index++ {
		if index != 0 {
			source.WriteByte(',')
		}
		source.WriteString(strconv.Itoa(index))
	}
	source.WriteString(",name=1}")

	prototype, syntaxError := compileSource("@tables.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var allocation instruction
	var lists []instruction
	records := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opNewTable:
			allocation = code
		case opSetList:
			lists = append(lists, code)
		case opSetTable:
			records++
		}
	}
	if allocation.opcode() != opNewTable ||
		floatingByteToInt(allocation.b()) < fieldsPerFlush+1 ||
		floatingByteToInt(allocation.c()) < 1 {
		t.Fatalf("NEWTABLE array hint = %d", allocation.b())
	}
	if records != 1 {
		t.Fatalf("SETTABLE count = %d, want 1", records)
	}
	if len(lists) != 2 ||
		lists[0].b() != fieldsPerFlush ||
		lists[0].c() != 1 ||
		lists[1].b() != 1 ||
		lists[1].c() != 2 {
		t.Fatalf("SETLIST blocks = %#v", lists)
	}
	if prototype.RegisterCount() > fieldsPerFlush+1 {
		t.Fatalf(
			"constructor requires %d registers, want at most %d",
			prototype.RegisterCount(),
			fieldsPerFlush+1,
		)
	}
}

func TestCompileSourceExpandsOnlyFinalConstructorField(t *testing.T) {
	cases := []struct {
		source       string
		producer     opcode
		producerOpen func(instruction) bool
		listCount    int
		arrayHint    int
	}{
		{
			source:   "return {1, produce(),}",
			producer: opCall,
			producerOpen: func(code instruction) bool {
				return code.c() == 0
			},
			listCount: 0,
			arrayHint: 1,
		},
		{
			source:   "return {1, ...}",
			producer: opVararg,
			producerOpen: func(code instruction) bool {
				return code.b() == 0
			},
			listCount: 0,
			arrayHint: 1,
		},
		{
			source:   "return {produce(), 1}",
			producer: opCall,
			producerOpen: func(code instruction) bool {
				return code.c() == 2
			},
			listCount: 2,
			arrayHint: 2,
		},
		{
			source:   "return {1, (produce())}",
			producer: opCall,
			producerOpen: func(code instruction) bool {
				return code.c() == 2
			},
			listCount: 2,
			arrayHint: 2,
		},
		{
			source:   "return {flag and produce()}",
			producer: opCall,
			producerOpen: func(code instruction) bool {
				return code.c() == 2
			},
			listCount: 1,
			arrayHint: 1,
		},
	}

	for _, test := range cases {
		prototype, syntaxError := compileSource("@tables.lua", test.source)
		if syntaxError != nil {
			t.Fatalf("%s: %v", test.source, syntaxError)
		}

		producerPC := -1
		listPC := -1
		var producer instruction
		var list instruction
		var allocation instruction
		for pc, code := range prototype.code {
			switch code.opcode() {
			case test.producer:
				producerPC = pc
				producer = code
			case opSetList:
				listPC = pc
				list = code
			case opNewTable:
				allocation = code
			}
		}
		if producerPC < 0 ||
			!test.producerOpen(producer) ||
			listPC < 0 ||
			list.b() != test.listCount ||
			floatingByteToInt(allocation.b()) < test.arrayHint {
			t.Fatalf(
				"%s: producer %#v at %d, SETLIST %#v at %d, NEWTABLE B:%d",
				test.source,
				producer,
				producerPC,
				list,
				listPC,
				allocation.b(),
			)
		}
		if test.listCount == 0 && producerPC+1 != listPC {
			t.Fatalf(
				"%s: open producer at %d is not adjacent to SETLIST at %d",
				test.source,
				producerPC,
				listPC,
			)
		}
	}
}

func TestCompileSourceDistinguishesNameFieldsFromNameValues(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"return {name == value, name = name}",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var allocation instruction
	lists := 0
	records := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opNewTable:
			allocation = code
		case opSetList:
			lists++
		case opSetTable:
			records++
		}
	}
	if lists != 1 ||
		records != 1 ||
		floatingByteToInt(allocation.b()) < 1 ||
		floatingByteToInt(allocation.c()) < 1 {
		t.Fatalf(
			"name fields = %d SETLIST, %d SETTABLE, NEWTABLE B:%d C:%d",
			lists,
			records,
			allocation.b(),
			allocation.c(),
		)
	}
}

func TestCompileSourceEnforcesConstructorRegisterLimit(t *testing.T) {
	var prefix strings.Builder
	prefix.WriteString("local ")
	for index := 0; index < maxActiveLocals; index++ {
		if index != 0 {
			prefix.WriteByte(',')
		}
		prefix.WriteByte('v')
		prefix.WriteString(strconv.Itoa(index))
	}
	prefix.WriteString("\nreturn {")

	constructor := func(fields int) string {
		var source strings.Builder
		source.Grow(prefix.Len() + fields*2 + 1)
		source.WriteString(prefix.String())
		for index := 0; index < fields; index++ {
			if index != 0 {
				source.WriteByte(',')
			}
			source.WriteByte('1')
		}
		source.WriteByte('}')
		return source.String()
	}

	prototype, syntaxError := compileSource(
		"@tables.lua",
		constructor(maxLuaRegisters-maxActiveLocals-1),
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if prototype.RegisterCount() != maxLuaRegisters {
		t.Fatalf(
			"constructor register count = %d, want %d",
			prototype.RegisterCount(),
			maxLuaRegisters,
		)
	}

	if _, syntaxError = compileSource(
		"@tables.lua",
		constructor(maxLuaRegisters-maxActiveLocals),
	); syntaxError == nil || syntaxError.Category() != SyntaxError {
		t.Fatalf("oversized constructor error = %v", syntaxError)
	}
}

func TestCompileSourceEvaluatesRecordKeyBeforeValue(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"return {[key()] = value()}",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var sequence []opcode
	for _, code := range prototype.code {
		switch code.opcode() {
		case opGetGlobal, opCall, opSetTable:
			sequence = append(sequence, code.opcode())
		}
	}
	want := []opcode{
		opGetGlobal,
		opCall,
		opGetGlobal,
		opCall,
		opSetTable,
	}
	if len(sequence) != len(want) {
		t.Fatalf("record evaluation sequence = %v, want %v", sequence, want)
	}
	for index := range want {
		if sequence[index] != want[index] {
			t.Fatalf("record evaluation sequence = %v, want %v", sequence, want)
		}
	}
}

func TestCompileSourcePreservesMixedConstructorEvaluationOrder(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"return {first(), [key()] = value(), second()}",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var sequence []opcode
	var calls []instruction
	finalCallPC := -1
	listPC := -1
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opGetGlobal, opCall, opSetTable, opSetList:
			sequence = append(sequence, code.opcode())
		}
		switch code.opcode() {
		case opCall:
			calls = append(calls, code)
			finalCallPC = pc
		case opSetList:
			listPC = pc
		}
	}
	want := []opcode{
		opGetGlobal,
		opCall,
		opGetGlobal,
		opCall,
		opGetGlobal,
		opCall,
		opSetTable,
		opGetGlobal,
		opCall,
		opSetList,
	}
	if len(sequence) != len(want) {
		t.Fatalf("mixed evaluation sequence = %v, want %v", sequence, want)
	}
	for index := range want {
		if sequence[index] != want[index] {
			t.Fatalf("mixed evaluation sequence = %v, want %v", sequence, want)
		}
	}
	if len(calls) != 4 ||
		calls[0].c() != 2 ||
		calls[1].c() != 2 ||
		calls[2].c() != 2 ||
		calls[3].c() != 0 ||
		finalCallPC+1 != listPC {
		t.Fatalf(
			"mixed calls = %#v, final CALL at %d, SETLIST at %d",
			calls,
			finalCallPC,
			listPC,
		)
	}
}

func TestCompileSourceSpillsConstructorKeyAndValueAboveTable(t *testing.T) {
	var source strings.Builder
	source.WriteString("local sink = ...\n")
	for number := 0; number < 256; number++ {
		source.WriteString("sink = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString("return {field = 999}")

	prototype, syntaxError := compileSource("@tables.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	fieldIndex := -1
	valueIndex := -1
	for index, constant := range prototype.constants {
		switch {
		case constant.kind() == StringKind &&
			(*luaString)(constant.ref).text == "field":
			fieldIndex = index
		case constant.kind() == NumberKind &&
			constant.bits == slotFromValue(Number(999)).bits:
			valueIndex = index
		}
	}
	if fieldIndex <= maxRegisterConstant ||
		valueIndex <= maxRegisterConstant {
		t.Fatalf(
			"constructor constant indexes = key %d, value %d",
			fieldIndex,
			valueIndex,
		)
	}

	tableRegister := -1
	keyRegister := -1
	valueRegister := -1
	var set instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opNewTable:
			tableRegister = code.a()
		case opLoadK:
			switch code.bx() {
			case fieldIndex:
				keyRegister = code.a()
			case valueIndex:
				valueRegister = code.a()
			}
		case opSetTable:
			set = code
		}
	}
	if tableRegister < 0 ||
		keyRegister <= tableRegister ||
		valueRegister <= keyRegister ||
		set.a() != tableRegister ||
		set.b() != keyRegister ||
		set.c() != valueRegister {
		t.Fatalf(
			"constructor spill = table R%d, key R%d, value R%d, SETTABLE A:%d B:%d C:%d",
			tableRegister,
			keyRegister,
			valueRegister,
			set.a(),
			set.b(),
			set.c(),
		)
	}
}

func TestCompileSourceEmitsExtendedConstructorBlock(t *testing.T) {
	const fieldCount = maxOperandC*fieldsPerFlush + 1
	var source strings.Builder
	source.Grow(fieldCount*2 + 9)
	source.WriteString("return {")
	for index := 0; index < fieldCount; index++ {
		if index != 0 {
			source.WriteByte(',')
		}
		source.WriteByte('1')
	}
	source.WriteByte('}')

	prototype, syntaxError := compileSource("@tables.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	wantBlock := maxOperandC + 1
	for pc, code := range prototype.code {
		if code.opcode() != opSetList || code.c() != 0 {
			continue
		}
		if pc+1 >= len(prototype.code) {
			t.Fatal("extended SETLIST has no data word")
		}
		if got := int(prototype.code[pc+1]); got != wantBlock {
			t.Fatalf(
				"extended SETLIST block = %d, want %d",
				got,
				wantBlock,
			)
		}
		return
	}
	t.Fatal("constructor did not emit an extended SETLIST")
}

func TestCompileSourcePassesConstructorAsCallArgument(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tables.lua",
		"consume {value = 1}\nobject:consume {2}",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	constructors := 0
	var calls []instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opNewTable:
			constructors++
		case opCall:
			calls = append(calls, code)
		}
	}
	if constructors != 2 ||
		len(calls) != 2 ||
		calls[0].b() != 2 ||
		calls[0].c() != 1 ||
		calls[1].b() != 3 ||
		calls[1].c() != 1 {
		t.Fatalf(
			"constructor calls = %d tables, calls %#v",
			constructors,
			calls,
		)
	}
}

func TestCompileSourceRejectsMalformedConstructor(t *testing.T) {
	for _, source := range []string{
		"return {,}",
		"return {;}",
		"return {name =}",
		"return {[key] value}",
		"return {[key] =}",
		"return {1 2}",
		"return {1,",
		"consume {",
	} {
		if _, syntaxError := compileSource(
			"@invalid.lua",
			source,
		); syntaxError == nil || syntaxError.Category() != SyntaxError {
			t.Fatalf("%q: syntax error = %v", source, syntaxError)
		}
	}
}
