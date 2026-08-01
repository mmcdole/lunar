# Lunar Embedding

Lunar embeds Lua programs in Go applications while keeping the host boundary
explicit about ownership, execution, and policy.

## Language

**State**:
An isolated Lua runtime and the authority that owns its reference values.
_Avoid_: VM, interpreter instance

**Owning Value**:
A Lua value that may be retained by Go independently of the operation that
produced it.
_Avoid_: Stack value, borrowed value

**Frame**:
A borrowed view of one active call from Lua into Go.
_Avoid_: State, callback stack

**Lua Operation**:
An operation that follows ordinary Lua language semantics, including applicable
metamethods.
_Avoid_: Normal operation, non-raw operation

**Raw Operation**:
An operation that observes or changes stored Lua data without consulting
metamethods.
_Avoid_: Direct operation, unsafe operation

**Script Loader**:
The State configuration that controls how named Lua scripts are opened, or
denies script-file access entirely.
_Avoid_: Source policy, filesystem
