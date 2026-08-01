package lua

type pendingGoto struct {
	name       *internedText
	closePC    int
	jump       jumpList
	line       uint32
	localCount int
}

type definedLabel struct {
	name       *internedText
	pc         int
	line       uint32
	localCount int
}

func (function *functionState) emitGoto(
	name *internedText,
	line uint32,
) *Error {
	block := function.blocks[len(function.blocks)-1]
	pending := pendingGoto{
		name:       name,
		closePC:    function.emitAsBx(opJump, 0, 0, line),
		jump:       function.emitJump(line),
		line:       line,
		localCount: len(function.locals),
	}
	for index := len(function.labels) - 1; index >= block.firstLabel; index-- {
		label := function.labels[index]
		if label.name == name {
			return function.resolveGoto(pending, label)
		}
	}
	function.pendingGotos = append(function.pendingGotos, pending)
	return nil
}

func (function *functionState) defineLabel(
	name *internedText,
	line uint32,
	localCount int,
) *Error {
	block := function.blocks[len(function.blocks)-1]
	for index := block.firstLabel; index < len(function.labels); index++ {
		previous := function.labels[index]
		if previous.name == name {
			return newSourceSyntaxError(
				function.unit.sourceName.text,
				line,
				"label '%s' already defined on line %d",
				name.text,
				previous.line,
			)
		}
	}
	function.labels = append(function.labels, definedLabel{
		name:       name,
		pc:         function.currentPC(),
		line:       line,
		localCount: localCount,
	})
	label := function.labels[len(function.labels)-1]
	for index := block.firstGoto; index < len(function.pendingGotos); {
		pending := function.pendingGotos[index]
		if pending.name != name {
			index++
			continue
		}
		if syntaxError := function.resolveGoto(pending, label); syntaxError != nil {
			return syntaxError
		}
		function.pendingGotos = append(
			function.pendingGotos[:index],
			function.pendingGotos[index+1:]...,
		)
	}
	return nil
}

func (function *functionState) resolveGoto(
	pending pendingGoto,
	label definedLabel,
) *Error {
	if pending.localCount < label.localCount {
		local := function.locals[pending.localCount]
		return newSourceSyntaxError(
			function.unit.sourceName.text,
			pending.line,
			"<goto %s> jumps into the scope of local '%s'",
			pending.name.text,
			local.name.text,
		)
	}
	function.patchGotoClose(&pending, label.localCount)
	return function.patchJumps(pending.jump, label.pc)
}

// patchGotoClose turns the no-op jump before a goto into CLOSE when resolving
// the goto proves that it exits local scopes. The threshold is the first
// exited local, so a capture discovered later in the parse is still closed
// without touching a captured prefix that remains in scope. Keeping the slot
// as JMP +0 at an equal local watermark avoids closing live cells prematurely.
func (function *functionState) patchGotoClose(
	pending *pendingGoto,
	targetLocalCount int,
) {
	if pending == nil ||
		targetLocalCount < 0 ||
		targetLocalCount > pending.localCount ||
		pending.localCount > len(function.locals) {
		panic("lua: invalid goto local range")
	}
	closeBase := noRegister
	code := function.builder.code[pending.closePC]
	switch code.opcode() {
	case opJump:
		if code.a() != 0 || code.sbx() != 0 {
			panic("lua: malformed goto close placeholder")
		}
	case opClose:
		closeBase = code.a()
	default:
		panic("lua: goto lost its close placeholder")
	}
	if targetLocalCount < pending.localCount {
		firstExited := function.locals[targetLocalCount].register
		if firstExited < closeBase {
			closeBase = firstExited
		}
	}
	if closeBase != noRegister {
		function.builder.code[pending.closePC] = makeABC(
			opClose,
			closeBase,
			0,
			0,
		)
	}
}

func (function *functionState) moveGotosOut(blockIndex int) {
	block := function.blocks[blockIndex]
	parent := function.blocks[blockIndex-1]
	for gotoIndex := block.firstGoto; gotoIndex < len(function.pendingGotos); {
		pending := function.pendingGotos[gotoIndex]
		labelIndex := block.firstLabel - 1
		for ; labelIndex >= parent.firstLabel; labelIndex-- {
			if function.labels[labelIndex].name == pending.name {
				break
			}
		}
		if labelIndex < parent.firstLabel {
			function.patchGotoClose(
				&function.pendingGotos[gotoIndex],
				block.localBase,
			)
			function.pendingGotos[gotoIndex].localCount = block.localBase
			gotoIndex++
			continue
		}
		label := function.labels[labelIndex]
		if syntaxError := function.resolveGoto(
			pending,
			label,
		); syntaxError != nil && function.gotoError == nil {
			function.gotoError = syntaxError
		}
		function.pendingGotos = append(
			function.pendingGotos[:gotoIndex],
			function.pendingGotos[gotoIndex+1:]...,
		)
	}
}

func (parser *sourceParser) parseGoto() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	name, syntaxError := parser.expect(tokenName)
	if syntaxError != nil {
		return syntaxError
	}
	return parser.function.emitGoto(parser.unit.internToken(name), line)
}

func (parser *sourceParser) parseLabel() *Error {
	type parsedLabel struct {
		name *internedText
		line uint32
	}
	var labels []parsedLabel
	for {
		line := parser.current.line
		if syntaxError := parser.advance(); syntaxError != nil {
			return syntaxError
		}
		name, syntaxError := parser.expect(tokenName)
		if syntaxError != nil {
			return syntaxError
		}
		if _, syntaxError = parser.expect(tokenDoubleColon); syntaxError != nil {
			return syntaxError
		}
		labels = append(labels, parsedLabel{
			name: parser.unit.internToken(name),
			line: line,
		})
		for parser.current.kind == ';' {
			if syntaxError = parser.advance(); syntaxError != nil {
				return syntaxError
			}
		}
		if parser.current.kind != tokenDoubleColon {
			break
		}
	}

	localCount := len(parser.function.locals)
	if isEndLabelFollower(parser.current.kind) {
		block := parser.function.blocks[len(parser.function.blocks)-1]
		localCount = block.localBase
	}
	for _, label := range labels {
		if syntaxError := parser.function.defineLabel(
			label.name,
			label.line,
			localCount,
		); syntaxError != nil {
			return syntaxError
		}
	}
	return nil
}

func isEndLabelFollower(kind tokenKind) bool {
	switch kind {
	case tokenElse, tokenElseIf, tokenEnd, tokenEOF:
		return true
	default:
		return false
	}
}
