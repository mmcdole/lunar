package lua

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unsafe"
)

// The chunk layout follows Lua 5.1's ldump.c. See THIRD_PARTY_NOTICES.md for
// the reference implementation's license.

const (
	lua51ChunkHeaderSize  = 12
	lua51IntegerSize      = 4
	lua51InstructionSize  = 4
	lua51NumberSize       = 8
	maxChunkFunctionDepth = 200
	chunkFallbackSource   = "=?"

	// These deliberately overstate current 64-bit Go layouts. The base charge
	// covers the map header and its first allocation group; each string charge
	// covers luaString plus amortized map key, value, and growth metadata.
	// String payload bytes are charged separately.
	chunkStringTableBytes = 512
	chunkStringIndexBytes = 64
)

// dumpPrototype serializes prototype in Lua 5.1's native binary chunk format.
//
// Lua 5.1 chunks deliberately describe the host ABI in their header rather
// than defining a portable byte order or size_t width. The resulting string is
// therefore loadable by a Lua 5.1 interpreter built for the same architecture.
func dumpPrototype(prototype *Prototype) (string, error) {
	if prototype == nil || !prototype.sealed {
		return "", ErrInvalidPrototype
	}

	writer := chunkWriter{
		order:    nativeByteOrder(),
		wordSize: int(unsafe.Sizeof(uintptr(0))),
	}
	writer.writeHeader()
	writer.writeFunction(prototype, nil)
	if writer.err != nil {
		return "", writer.err
	}
	return writer.output.String(), nil
}

type chunkWriter struct {
	output   strings.Builder
	order    binary.ByteOrder
	wordSize int
	err      error
}

func (writer *chunkWriter) writeHeader() {
	header := lua51NativeHeader()
	writer.writeBytes(header[:])
}

func lua51NativeHeader() [lua51ChunkHeaderSize]byte {
	return [lua51ChunkHeaderSize]byte{
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
}

func (writer *chunkWriter) writeFunction(
	prototype *Prototype,
	parentSource *luaString,
) {
	if writer.err != nil {
		return
	}
	if prototype == nil || !prototype.sealed {
		writer.fail(ErrInvalidPrototype)
		return
	}

	if prototype.sourceName == parentSource {
		writer.writeString(nil)
	} else {
		writer.writeString(prototype.sourceName)
	}
	writer.writeInt64(int64(prototype.lineDefined), "defined line")
	writer.writeInt64(int64(prototype.lastLine), "last defined line")
	writer.writeByte(prototype.upvalues)
	writer.writeByte(prototype.parameters)
	writer.writeByte(prototype.varargFlags)
	writer.writeByte(prototype.registers)

	writer.writeCount(len(prototype.code), "instruction")
	for _, code := range prototype.code {
		writer.writeUint32(uint32(code))
	}

	writer.writeCount(len(prototype.constants), "constant")
	for _, constant := range prototype.constants {
		writer.writeConstant(constant)
	}

	writer.writeCount(len(prototype.children), "child prototype")
	for _, child := range prototype.children {
		writer.writeFunction(child, prototype.sourceName)
	}

	writer.writeDebug(prototype.debug)
}

func (writer *chunkWriter) writeConstant(value slot) {
	switch value.kind() {
	case NilKind:
		writer.writeByte(0)
	case BoolKind:
		writer.writeByte(1)
		if value.ref == trueMarkerPointer {
			writer.writeByte(1)
		} else {
			writer.writeByte(0)
		}
	case NumberKind:
		writer.writeByte(3)
		writer.writeUint64(value.bits)
	case StringKind:
		writer.writeByte(4)
		writer.writeStringText(stringSlotText(value))
	default:
		writer.fail(fmt.Errorf(
			"lua: cannot dump %s prototype constant",
			value.kind(),
		))
	}
}

func (writer *chunkWriter) writeDebug(debug *prototypeDebug) {
	if debug == nil {
		writer.writeInt32(0)
		writer.writeInt32(0)
		writer.writeInt32(0)
		return
	}

	writer.writeCount(len(debug.lines), "debug line")
	for _, line := range debug.lines {
		writer.writeInt64(int64(line), "debug line")
	}

	writer.writeCount(len(debug.locals), "debug local")
	for _, local := range debug.locals {
		writer.writeString(local.name)
		writer.writeInt64(int64(local.startPC), "local start pc")
		writer.writeInt64(int64(local.endPC), "local end pc")
	}

	writer.writeCount(len(debug.upvalues), "debug upvalue")
	for _, name := range debug.upvalues {
		writer.writeString(name)
	}
}

func (writer *chunkWriter) writeString(value *luaString) {
	if value == nil {
		writer.writeSize(0)
		return
	}
	writer.writeStringText(value.text)
}

func (writer *chunkWriter) writeStringText(text string) {
	writer.writeSize(uint64(len(text)) + 1)
	writer.writeText(text)
	writer.writeByte(0)
}

func (writer *chunkWriter) writeCount(count int, description string) {
	if count < 0 {
		writer.fail(fmt.Errorf("lua: negative %s count", description))
		return
	}
	writer.writeInt64(int64(count), description+" count")
}

func (writer *chunkWriter) writeInt64(value int64, description string) {
	if value < 0 || value > math.MaxInt32 {
		writer.fail(fmt.Errorf(
			"lua: %s %d does not fit a Lua 5.1 chunk integer",
			description,
			value,
		))
		return
	}
	writer.writeInt32(int32(value))
}

func (writer *chunkWriter) writeInt32(value int32) {
	writer.writeUint32(uint32(value))
}

func (writer *chunkWriter) writeSize(value uint64) {
	switch writer.wordSize {
	case 4:
		if value > math.MaxUint32 {
			writer.fail(errors.New(
				"lua: string does not fit the host chunk size_t",
			))
			return
		}
		writer.writeUint32(uint32(value))
	case 8:
		writer.writeUint64(value)
	default:
		writer.fail(fmt.Errorf(
			"lua: unsupported native size_t width %d",
			writer.wordSize,
		))
	}
}

func (writer *chunkWriter) writeUint32(value uint32) {
	if writer.err != nil {
		return
	}
	var encoded [4]byte
	writer.order.PutUint32(encoded[:], value)
	writer.writeBytes(encoded[:])
}

func (writer *chunkWriter) writeUint64(value uint64) {
	if writer.err != nil {
		return
	}
	var encoded [8]byte
	writer.order.PutUint64(encoded[:], value)
	writer.writeBytes(encoded[:])
}

func (writer *chunkWriter) writeByte(value byte) {
	if writer.err == nil {
		writer.output.WriteByte(value)
	}
}

func (writer *chunkWriter) writeBytes(value []byte) {
	if writer.err == nil {
		_, _ = writer.output.Write(value)
	}
}

func (writer *chunkWriter) writeText(value string) {
	if writer.err == nil {
		_, _ = writer.output.WriteString(value)
	}
}

func (writer *chunkWriter) fail(err error) {
	if writer.err == nil {
		writer.err = err
	}
}

// decodeBinaryChunk reads one native-ABI Lua 5.1 binary chunk.
//
// input remains positioned immediately after the root function. Lua 5.1 does
// not require end-of-input after a binary chunk, so trailing bytes are neither
// read nor rejected.
func decodeBinaryChunk(
	sourceName string,
	input *chunkInput,
	control *loadControl,
) (*Prototype, error) {
	if input == nil || control == nil {
		panic("lua: binary decoder requires input and load control")
	}
	if input.control != control {
		panic("lua: binary decoder and input use different load controls")
	}
	if failure := control.check(); failure != nil {
		return nil, failure
	}
	initialStorage := uint64(unsafe.Sizeof(compileUnit{})) +
		chunkStringTableBytes +
		chunkStringIndexBytes +
		uint64(len(chunkFallbackSource))
	if failure := control.reserve(initialStorage); failure != nil {
		return nil, failure
	}
	decoder := chunkDecoder{
		sourceName: sourceName,
		input:      input,
		control:    control,
		unit:       newCompileUnit(chunkFallbackSource),
		order:      nativeByteOrder(),
		wordSize:   int(unsafe.Sizeof(uintptr(0))),
	}
	if err := decoder.readHeader(); err != nil {
		return nil, err
	}
	prototype, err := decoder.readFunction(decoder.unit.sourceName, 1)
	if err != nil {
		return nil, err
	}
	if failure := control.check(); failure != nil {
		return nil, failure
	}
	return prototype, nil
}

type chunkDecoder struct {
	sourceName string
	input      *chunkInput
	control    *loadControl
	unit       *compileUnit
	order      binary.ByteOrder
	wordSize   int
	work       uint16
}

func (decoder *chunkDecoder) readHeader() error {
	var actual [lua51ChunkHeaderSize]byte
	if err := decoder.readFull(actual[:]); err != nil {
		return err
	}
	if actual != lua51NativeHeader() {
		return decoder.syntaxError("bad header")
	}
	return decoder.check()
}

func (decoder *chunkDecoder) readFunction(
	parentSource *luaString,
	depth int,
) (*Prototype, error) {
	if depth > maxChunkFunctionDepth {
		return nil, decoder.syntaxError("code too deep")
	}
	if err := decoder.check(); err != nil {
		return nil, err
	}
	if err := decoder.reserve(uint64(unsafe.Sizeof(Prototype{}))); err != nil {
		return nil, err
	}

	source, err := decoder.readString()
	if err != nil {
		return nil, err
	}
	if source == nil {
		source = parentSource
	}
	lineDefined, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	lastLine, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	upvalues, err := decoder.readByte()
	if err != nil {
		return nil, err
	}
	parameters, err := decoder.readByte()
	if err != nil {
		return nil, err
	}
	varargFlags, err := decoder.readByte()
	if err != nil {
		return nil, err
	}
	registers, err := decoder.readByte()
	if err != nil {
		return nil, err
	}

	code, err := decoder.readCode()
	if err != nil {
		return nil, err
	}
	constants, err := decoder.readConstants()
	if err != nil {
		return nil, err
	}
	children, err := decoder.readChildren(source, depth)
	if err != nil {
		return nil, err
	}
	debug, err := decoder.readDebug(len(code), int(upvalues))
	if err != nil {
		return nil, err
	}

	builder := &prototypeBuilder{
		sourceName:   source,
		lineDefined:  lineDefined,
		lastLine:     lastLine,
		parameters:   int(parameters),
		registers:    int(registers),
		upvalues:     int(upvalues),
		varargFlags:  int(varargFlags),
		code:         code,
		constants:    constants,
		children:     children,
		debug:        debug,
		adoptVectors: true,
	}
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		return nil, decoder.syntaxError("bad code")
	}
	return prototype, nil
}

func (decoder *chunkDecoder) readCode() ([]instruction, error) {
	count, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if err := decoder.reserveVector(
		count,
		uint64(unsafe.Sizeof(instruction(0)))+
			uint64(unsafe.Sizeof(prototypeWordRole(0))),
	); err != nil {
		return nil, err
	}
	code := make([]instruction, count)
	for index := range code {
		value, readErr := decoder.readUint32()
		if readErr != nil {
			return nil, readErr
		}
		code[index] = instruction(value)
		if checkErr := decoder.step(); checkErr != nil {
			return nil, checkErr
		}
	}
	return code, nil
}

func (decoder *chunkDecoder) readConstants() ([]slot, error) {
	count, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if count > maxOperandBx+1 {
		return nil, decoder.syntaxError("bad code")
	}
	if err := decoder.reserveVector(
		count,
		uint64(unsafe.Sizeof(slot{})),
	); err != nil {
		return nil, err
	}
	constants := make([]slot, count)
	for index := range constants {
		value, readErr := decoder.readConstant()
		if readErr != nil {
			return nil, readErr
		}
		constants[index] = value
		if checkErr := decoder.step(); checkErr != nil {
			return nil, checkErr
		}
	}
	return constants, nil
}

func (decoder *chunkDecoder) readConstant() (slot, error) {
	tag, err := decoder.readByte()
	if err != nil {
		return slot{}, err
	}
	switch tag {
	case 0:
		return nilSlot, nil
	case 1:
		value, readErr := decoder.readByte()
		if readErr != nil {
			return slot{}, readErr
		}
		if value == 0 {
			return falseSlot, nil
		}
		return trueSlot, nil
	case 3:
		bits, readErr := decoder.readUint64()
		if readErr != nil {
			return slot{}, readErr
		}
		return slot{bits: bits}, nil
	case 4:
		value, readErr := decoder.readString()
		if readErr != nil {
			return slot{}, readErr
		}
		if value == nil {
			return slot{}, decoder.syntaxError("bad constant")
		}
		return prototypeStringSlot(value), nil
	default:
		return slot{}, decoder.syntaxError("bad constant")
	}
}

func (decoder *chunkDecoder) readChildren(
	parentSource *luaString,
	depth int,
) ([]*Prototype, error) {
	count, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if count > maxOperandBx+1 {
		return nil, decoder.syntaxError("bad code")
	}
	if err := decoder.reserveVector(
		count,
		uint64(unsafe.Sizeof((*Prototype)(nil))),
	); err != nil {
		return nil, err
	}
	children := make([]*Prototype, count)
	for index := range children {
		child, readErr := decoder.readFunction(parentSource, depth+1)
		if readErr != nil {
			return nil, readErr
		}
		children[index] = child
	}
	return children, nil
}

func (decoder *chunkDecoder) readDebug(
	codeCount int,
	upvalueCount int,
) (*prototypeDebugBuilder, error) {
	lineCount, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if lineCount != 0 && lineCount != codeCount {
		return nil, decoder.syntaxError("bad code")
	}
	if err := decoder.reserveVector(
		lineCount,
		uint64(unsafe.Sizeof(int(0)))+
			uint64(unsafe.Sizeof(uint32(0))),
	); err != nil {
		return nil, err
	}
	lines := make([]int, lineCount)
	for index := range lines {
		line, readErr := decoder.readInteger()
		if readErr != nil {
			return nil, readErr
		}
		lines[index] = line
		if checkErr := decoder.step(); checkErr != nil {
			return nil, checkErr
		}
	}

	localCount, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if err := decoder.reserveVector(
		localCount,
		uint64(unsafe.Sizeof(prototypeLocalBuilder{}))+
			uint64(unsafe.Sizeof(localInfo{})),
	); err != nil {
		return nil, err
	}
	locals := make([]prototypeLocalBuilder, localCount)
	for index := range locals {
		name, readErr := decoder.readString()
		if readErr != nil {
			return nil, readErr
		}
		if name == nil {
			return nil, decoder.syntaxError("bad code")
		}
		startPC, readErr := decoder.readInteger()
		if readErr != nil {
			return nil, readErr
		}
		endPC, readErr := decoder.readInteger()
		if readErr != nil {
			return nil, readErr
		}
		locals[index] = prototypeLocalBuilder{
			name:    name,
			startPC: startPC,
			endPC:   endPC,
		}
		if checkErr := decoder.step(); checkErr != nil {
			return nil, checkErr
		}
	}

	upvalueNameCount, err := decoder.readInteger()
	if err != nil {
		return nil, err
	}
	if upvalueNameCount > upvalueCount {
		return nil, decoder.syntaxError("bad code")
	}
	if err := decoder.reserveVector(
		upvalueNameCount,
		2*uint64(unsafe.Sizeof((*luaString)(nil))),
	); err != nil {
		return nil, err
	}
	upvalues := make([]*luaString, upvalueNameCount)
	for index := range upvalues {
		name, readErr := decoder.readString()
		if readErr != nil {
			return nil, readErr
		}
		if name == nil {
			return nil, decoder.syntaxError("bad code")
		}
		upvalues[index] = name
		if checkErr := decoder.step(); checkErr != nil {
			return nil, checkErr
		}
	}

	if len(lines) == 0 && len(locals) == 0 && len(upvalues) == 0 {
		return nil, nil
	}
	if err := decoder.reserve(
		uint64(unsafe.Sizeof(prototypeDebugBuilder{})) +
			uint64(unsafe.Sizeof(prototypeDebug{})),
	); err != nil {
		return nil, err
	}
	return &prototypeDebugBuilder{
		lines:    lines,
		locals:   locals,
		upvalues: upvalues,
	}, nil
}

func (decoder *chunkDecoder) readString() (*luaString, error) {
	size, err := decoder.readSize()
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	maxInt := uint64(^uint(0) >> 1)
	if size > maxInt {
		return nil, decoder.syntaxError("bad string")
	}
	encoded, owned, err := decoder.input.readSpan(int(size))
	if err != nil {
		return nil, decoder.inputError(err)
	}
	if len(encoded) != int(size) || encoded[len(encoded)-1] != 0 {
		return nil, decoder.syntaxError("bad string")
	}
	text := encoded[:len(encoded)-1]
	if existing := decoder.unit.strings[text]; existing != nil {
		if owned {
			decoder.control.release(size)
		}
		return existing, nil
	}
	if !owned {
		if err := decoder.reserve(size); err != nil {
			return nil, err
		}
	}
	if err := decoder.reserve(chunkStringIndexBytes); err != nil {
		return nil, err
	}
	if owned {
		return decoder.unit.internOwned(text), nil
	}
	return decoder.unit.internBorrowed(text), nil
}

func (decoder *chunkDecoder) readInteger() (int, error) {
	value, err := decoder.readUint32()
	if err != nil {
		return 0, err
	}
	signed := int32(value)
	if signed < 0 {
		return 0, decoder.syntaxError("bad integer")
	}
	return int(signed), nil
}

func (decoder *chunkDecoder) readSize() (uint64, error) {
	switch decoder.wordSize {
	case 4:
		value, err := decoder.readUint32()
		return uint64(value), err
	case 8:
		return decoder.readUint64()
	default:
		panic("lua: unsupported native chunk word size")
	}
}

func (decoder *chunkDecoder) readByte() (byte, error) {
	value, err := decoder.input.readByte()
	if err != nil {
		return 0, decoder.inputError(err)
	}
	return value, nil
}

func (decoder *chunkDecoder) readUint32() (uint32, error) {
	var encoded [4]byte
	if err := decoder.readFull(encoded[:]); err != nil {
		return 0, err
	}
	return decoder.order.Uint32(encoded[:]), nil
}

func (decoder *chunkDecoder) readUint64() (uint64, error) {
	var encoded [8]byte
	if err := decoder.readFull(encoded[:]); err != nil {
		return 0, err
	}
	return decoder.order.Uint64(encoded[:]), nil
}

func (decoder *chunkDecoder) readFull(destination []byte) error {
	if err := decoder.input.readFull(destination); err != nil {
		return decoder.inputError(err)
	}
	return nil
}

func (decoder *chunkDecoder) inputError(err error) error {
	if refill, ok := err.(*chunkRefillFailure); ok {
		return refill.cause
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return decoder.syntaxError("unexpected end")
	}
	return err
}

func (decoder *chunkDecoder) reserveVector(
	count int,
	elementBytes uint64,
) error {
	if count < 0 {
		return decoder.syntaxError("bad integer")
	}
	if elementBytes != 0 &&
		uint64(count) > ^uint64(0)/elementBytes {
		return decoder.syntaxError("bad integer")
	}
	return decoder.reserve(uint64(count) * elementBytes)
}

func (decoder *chunkDecoder) reserve(size uint64) error {
	if failure := decoder.control.reserve(size); failure != nil {
		return failure
	}
	return nil
}

func (decoder *chunkDecoder) step() error {
	decoder.work++
	if decoder.work != contextPollInterval {
		return nil
	}
	decoder.work = 0
	return decoder.check()
}

func (decoder *chunkDecoder) check() error {
	if failure := decoder.control.check(); failure != nil {
		return failure
	}
	return nil
}

func (decoder *chunkDecoder) syntaxError(reason string) *Error {
	name := decoder.sourceName
	if name != "" {
		switch name[0] {
		case '@', '=':
			name = name[1:]
		case 0x1b:
			name = "binary string"
		}
	}
	if end := strings.IndexByte(name, 0); end >= 0 {
		name = name[:end]
	}
	message := fmt.Sprintf("%s: %s in precompiled chunk", name, reason)
	return &Error{
		value:       errorStringValue(message),
		description: message,
		category:    SyntaxError,
	}
}

func nativeByteOrder() binary.ByteOrder {
	if nativeEndianMarker() == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func nativeEndianMarker() byte {
	value := uint16(1)
	return *(*byte)(unsafe.Pointer(&value))
}
