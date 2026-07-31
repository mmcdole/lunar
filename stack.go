package lua

import "fmt"

// Where returns the source position of the activation level levels below
// this native call, formatted the way Lua positions runtime errors:
// "chunk.lua:12: ", including the trailing space.
//
// Level 0 is the native call itself and level 1 is the activation that
// called it, so Where(1) attributes a failure to the call site the way the
// runtime would. Where returns "" when the requested level has no Lua
// source position, which covers native activations, eliminated tail calls,
// and levels past the bottom of the stack.
//
// Raise* and Throw* do not position the messages they are given. Host code
// that composes its own message and wants runtime-identical attribution
// prefixes it with Where; ArgError and ArgTypeError leave that choice to
// the caller for the same reason.
func (frame Frame) Where(level int) string {
	frame.activation()
	return threadWhere(frame.thread, level)
}

// Stack reports the activation level levels below this native call, using
// the same numbering as Where. It reports ok=false past the bottom of the
// stack, which lets a caller walk levels until the stack is exhausted.
func (frame Frame) Stack(level int) (TraceFrame, bool) {
	frame.activation()
	return threadStack(frame.thread, level)
}

// Traceback returns the executing call stack, innermost activation first,
// in the same form Error.Traceback reports. The result is owned by the
// caller and remains readable after the State closes.
func (frame Frame) Traceback() []TraceFrame {
	frame.activation()
	return appendExecutionTraceback(nil, frame.thread, 0)
}

// Traceback returns the call stack of the State's executing thread,
// innermost activation first, in the same form Error.Traceback reports.
//
// It is meaningful while the State is executing, such as from an interrupt
// installed with SetInterrupt. A State that is not executing has no
// activations and reports an empty traceback.
func (state *State) Traceback() ([]TraceFrame, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	return appendExecutionTraceback(nil, state.currentThread(), 0), nil
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

func threadStack(thread *threadObject, level int) (TraceFrame, bool) {
	record, found := thread.debugActivation(level)
	if !found {
		return TraceFrame{}, false
	}
	if record.isTail() {
		return TraceFrame{Source: "=(tail call)"}, true
	}
	prototype := record.prototype()
	if prototype == nil {
		return TraceFrame{
			Source:   "=[Go]",
			Function: "native function",
		}, true
	}
	entry := TraceFrame{
		Source:    prototype.SourceName(),
		TailCalls: record.frame.tailCalls,
	}
	if line := record.currentLine(); line > 0 {
		entry.Line = line
	}
	if _, name, named := record.functionName(thread); named {
		entry.Function = name
	}
	return entry, true
}
