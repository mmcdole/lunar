package lua

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unsafe"
)

// The chunk layout follows Lua 5.1's ldump.c. See THIRD_PARTY_NOTICES.md for
// the reference implementation's license.

const (
	lua51ChunkHeaderSize = 12
	lua51IntegerSize     = 4
	lua51InstructionSize = 4
	lua51NumberSize      = 8
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
	writer.writeBytes([]byte{
		0x1b, 'L', 'u', 'a',
		0x51,
		0,
		nativeEndianMarker(),
		lua51IntegerSize,
		byte(writer.wordSize),
		lua51InstructionSize,
		lua51NumberSize,
		0,
	})
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
		writer.writeString((*luaString)(value.ref))
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
	writer.writeSize(uint64(len(value.text)) + 1)
	writer.writeText(value.text)
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
