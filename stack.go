package lua

import "fmt"

// Where returns the source position at an activation level relative to this
// native call, formatted the way Lua positions runtime errors:
// "chunk.lua:12: ", including the trailing space.
//
// Level 0 is the native call itself and level 1 is the activation that
// called it, so Where(1) attributes a failure to the call site the way the
// runtime would. Where returns "" when the requested level has no Lua
// source position, which covers native activations, eliminated tail calls,
// and levels past the bottom of the stack.
//
// Throw* methods do not position the messages they are given. Host code that
// composes its own message and wants runtime-identical attribution prefixes
// it with Where.
func (frame Frame) Where(level int) string {
	frame.activation()
	return threadWhere(frame.thread, level)
}

func threadWhere(thread *threadObject, level int) string {
	record, found := thread.debugActivation(level)
	if !found || record.isTail() {
		return ""
	}
	prototype := record.prototype()
	if prototype == nil {
		return ""
	}
	line := record.currentLine()
	if line <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d: ", sourceID(prototype.SourceName()), line)
}
