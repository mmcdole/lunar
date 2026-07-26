package lua

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
	"time"
	"unsafe"
)

// ErrClosed reports an operation that requires a live State.
var ErrClosed = errors.New("lua: state is closed")

// ErrRunning reports an operation that cannot run while Lua is executing.
var ErrRunning = errors.New("lua: state is executing")

// ErrForeignValue reports a reference value owned by another State.
var ErrForeignValue = errors.New("lua: value belongs to another state")

// ErrInvalidValue reports use of the zero Value.
var ErrInvalidValue = errors.New("lua: invalid value")

// ErrInvalidKey reports nil, NaN, or another invalid Lua table key.
var ErrInvalidKey = errors.New("lua: invalid table key")

// ErrInvalidNextKey reports a key that is not a valid continuation for table
// traversal.
var ErrInvalidNextKey = errors.New("lua: invalid key to next")

// ErrNegativeCapacity reports a negative collection capacity hint.
var ErrNegativeCapacity = errors.New("lua: capacity hint is negative")

// ErrCapacity reports a collection capacity hint too large for eager
// allocation. Tables may still grow beyond this size incrementally.
var ErrCapacity = errors.New("lua: capacity hint is too large")

// ErrReadOnlyUserData reports an attempt to replace the payload of userdata
// reserved for a runtime library's native resource.
var ErrReadOnlyUserData = errors.New(
	"lua: runtime-owned userdata payload is read-only",
)

// Options configures a State at construction.
//
// Options is copied by New. Mutating the caller's value after construction
// does not affect a live State.
type Options struct {
	// Stdin is the State's standard input stream. A nil interface selects
	// os.Stdin. Standard-input consumers share one logical cursor. Lua
	// libraries borrow the stream and never close it.
	Stdin io.Reader
	// Stdout is the State's standard output stream. A nil interface selects
	// os.Stdout. Standard-output consumers share one buffering endpoint. Lua
	// libraries borrow the stream and never close it.
	Stdout io.Writer
	// Stderr is the State's standard error stream. A nil interface selects
	// os.Stderr. Diagnostic consumers share one buffering endpoint. Lua
	// libraries borrow the stream and never close it.
	Stderr io.Writer
	// Location is the State's local timezone for operating-system library
	// calendar operations. Nil snapshots time.Local when New is called.
	// Later process-global timezone changes do not affect the State.
	Location *time.Location
	// Now supplies wall-clock time to Lua libraries. Nil selects time.Now.
	// The callback runs under the State's single-executor contract and must
	// not reenter that State. If shared by multiple States, it may be called
	// concurrently and must provide its own synchronization.
	Now func() time.Time
	// MaxValues limits values held by ordinary execution. Zero selects 65,536
	// values. Exceeding the limit raises an ordinary Lua error classified as
	// ResourceError. While an xpcall error handler runs, the runtime provides
	// bounded emergency capacity of max(64, MaxValues/8) additional values so
	// the handler can report an exhaustion failure.
	MaxValues int
	// MaxFrames limits ordinary nested Lua and native activations together.
	// Zero selects 20,000 activations. Exceeding the limit raises Lua 5.1's
	// ordinary "stack overflow" error, classified as ResourceError. An xpcall
	// error handler receives bounded emergency capacity of
	// max(8, MaxFrames/8) additional activations.
	MaxFrames int
	// MaxLoadBytes limits bytes consumed while loading one source or binary
	// chunk. Binary decoding independently applies the same bound to projected
	// retained storage. Zero selects 64 MiB. Exceeding either applicable bound
	// returns a ResourceError before the corresponding allocation.
	MaxLoadBytes int
}

type resourceLimits struct {
	values int
	frames int
}

const (
	defaultMaxValues = 64 << 10
	// Use Lua 5.1's configured LUAI_MAXCALLS as the compatibility default.
	defaultMaxFrames    = 20_000
	defaultMaxLoadBytes = 64 << 20
	maxTableHint        = 1 << 20
)

// noCopy lets go vet reject copying canonical runtime objects after first use.
// It occupies no storage.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

type objectHeader struct {
	noCopy noCopy
	owner  *runtimeState
}

// runtimeState is the lightweight ownership token shared by canonical
// objects. It owns only runtime-wide caches and close state, never the State's
// object roots. Retaining one object therefore does not pin an unrelated Lua
// graph.
type runtimeState struct {
	closed          atomic.Bool
	strings         stringPool
	nativeSequence  uint64
	nativeCallDepth uint16
}

// State owns one Lua runtime, its main Thread, and the runtime-wide registry.
// Each Thread owns its Lua 5.1 global-environment pointer.
//
// A State has one active executor. Callers must serialize all operations on a
// State; no State method, coroutine Resume, or owned-object mutation may
// overlap another operation, including Close. Owning Values and object handles
// may be retained by other goroutines, but their operations remain subject to
// the same rule.
//
// A State must not be copied after first use. Retain and pass its pointer.
type State struct {
	noCopy          noCopy
	runtime         *runtimeState
	options         Options
	limits          resourceLimits
	streams         *standardStreams
	location        *time.Location
	now             func() time.Time
	active          *Thread
	main            *Thread
	registry        *Table
	packageSentinel *UserData
	resources       *nativeResourceRegistry
	execution       executionControl
	typeMetatables  [TableKind + 1]*Table
}

// New constructs an empty State.
func New(options Options) (*State, error) {
	if options.MaxValues < 0 ||
		options.MaxFrames < 0 ||
		options.MaxLoadBytes < 0 {
		return nil, ErrNegativeCapacity
	}
	if options.MaxValues == 0 {
		options.MaxValues = defaultMaxValues
	}
	if options.MaxFrames == 0 {
		options.MaxFrames = defaultMaxFrames
	}
	if options.MaxLoadBytes == 0 {
		options.MaxLoadBytes = defaultMaxLoadBytes
	}
	if options.Stdin == nil {
		options.Stdin = os.Stdin
	}
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	rt := &runtimeState{}
	state := &State{
		runtime: rt,
		options: options,
		streams: newStandardStreams(
			options.Stdin,
			options.Stdout,
			options.Stderr,
		),
		location: options.Location,
		now:      options.Now,
		limits: resourceLimits{
			values: options.MaxValues,
			frames: options.MaxFrames,
		},
	}
	globals := newTable(rt, 0, 0)
	state.main = &Thread{
		objectHeader: objectHeader{owner: rt},
		state:        state,
		globals:      globals,
		status:       ThreadReady,
		main:         true,
	}
	state.registry = newTable(rt, 0, 0)
	return state, nil
}

// Close releases runtime-owned resources and prevents further execution or
// mutation. Repeated serialized calls are idempotent. Close must not overlap
// another operation on this State, including another call to Close.
//
// Every still-open runtime-owned native resource is closed exactly once.
// Borrowed native handles are detached without closing their underlying
// resources. Close continues through all records and returns owned-resource
// cleanup failures joined together. Buffered standard output is flushed and
// any flush failures are included in the returned error. The State is closed
// even when that error is non-nil. Standard streams supplied through Options
// are borrowed and are never closed.
//
// Previously returned owning Values and canonical object handles remain safe
// to inspect after Close.
func (state *State) Close() error {
	if state == nil || state.runtime == nil {
		return nil
	}
	if state.active != nil || state.runtime.nativeCallDepth != 0 {
		return ErrRunning
	}
	if state.runtime.closed.Swap(true) {
		return nil
	}
	var streamErr error
	if state.streams != nil {
		streamErr = state.streams.release()
	}
	state.streams = nil
	state.location = nil
	state.now = nil
	var resourceErr error
	if state.resources != nil {
		resourceErr = state.resources.releaseAll()
		state.resources = nil
	}
	state.runtime.strings.close()
	if state.main != nil {
		state.main.closeUpvalues(0)
		state.main.values = nil
		state.main.frames = nil
		state.main.continuations = nil
		state.main.top = 0
		state.main.frameExtent = 0
		state.main.globals = nil
		state.main.status = ThreadClosed
	}
	state.registry = nil
	state.options.Stdin = nil
	state.options.Stdout = nil
	state.options.Stderr = nil
	state.options.Location = nil
	state.options.Now = nil
	state.typeMetatables = [TableKind + 1]*Table{}
	return errors.Join(streamErr, resourceErr)
}

// MainThread returns the canonical main Thread.
func (state *State) MainThread() *Thread {
	if state == nil {
		return nil
	}
	return state.main
}

// String returns an owning Lua string Value.
//
// Strings are immutable and State-neutral. A returned Value may be shared
// among States and remains safe after this State is closed. Calling String
// after Close returns is permitted and constructs an uncached Value. As with
// every State operation, String must not overlap Close on the same State.
func (state *State) String(text string) Value {
	if state == nil || state.runtime == nil {
		return Value{}
	}
	return stringValue(state.runtime.strings.make(text))
}

// NewTable constructs an empty canonical Table using optional capacity hints.
func (state *State) NewTable(arrayHint, recordHint int) (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if arrayHint < 0 || recordHint < 0 {
		return nil, ErrNegativeCapacity
	}
	if arrayHint > maxTableHint || recordHint > maxTableHint {
		return nil, ErrCapacity
	}
	return newTable(state.runtime, arrayHint, recordHint), nil
}

// NewUserData constructs canonical userdata holding payload. Its initial
// environment is the currently executing Function's environment, or the main
// Thread's global environment outside a callback.
func (state *State) NewUserData(payload any) (*UserData, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	return &UserData{
		objectHeader: objectHeader{owner: state.runtime},
		payload:      payload,
		environment:  state.constructionEnvironment(),
	}, nil
}

// Registry returns the private Lua registry table.
//
// The returned table is canonical. It is not an execution stack or a set of
// pseudo-indexed Go registers.
func (state *State) Registry() (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	return state.registry, nil
}

func (state *State) currentThread() *Thread {
	if state.active != nil {
		return state.active
	}
	return state.main
}

func (state *State) globalEnvironment() *Table {
	return state.currentThread().globals
}

func (state *State) constructionEnvironment() *Table {
	thread := state.currentThread()
	if state.active != nil && len(thread.frames) != 0 {
		return thread.frames[len(thread.frames)-1].function.environment
	}
	return thread.globals
}

// Global returns a raw value from the current global environment.
//
// During a native callback, current means the executing Thread. Otherwise it
// means the main Thread.
func (state *State) Global(name string) (Value, error) {
	if err := state.checkOpen(); err != nil {
		return Value{}, err
	}
	return state.globalEnvironment().RawGetString(name), nil
}

// SetGlobal performs a raw assignment in the current global environment.
//
// During a native callback, current means the executing Thread. Otherwise it
// means the main Thread.
func (state *State) SetGlobal(name string, value Value) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	return state.globalEnvironment().RawSetString(name, value)
}

// RawEqual applies Lua raw equality without invoking metamethods.
func (state *State) RawEqual(left, right Value) (bool, error) {
	if err := state.checkOpen(); err != nil {
		return false, err
	}
	if err := state.runtime.accept(left); err != nil {
		return false, err
	}
	if err := state.runtime.accept(right); err != nil {
		return false, err
	}
	return rawEqual(left, right), nil
}

// Metatable returns value's metatable without invoking Lua. A nil result means
// no metatable is installed.
func (state *State) Metatable(value Value) (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if err := state.runtime.accept(value); err != nil {
		return nil, err
	}
	switch value.Kind() {
	case TableKind:
		table, _ := value.Table()
		return table.metatable, nil
	case UserDataKind:
		data, _ := value.UserData()
		return data.metatable, nil
	default:
		return state.typeMetatables[value.Kind()], nil
	}
}

// SetMetatable replaces value's metatable without invoking Lua. Passing nil
// removes the metatable.
func (state *State) SetMetatable(value Value, metatable *Table) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if err := state.runtime.accept(value); err != nil {
		return err
	}
	if metatable != nil {
		if metatable.owner == nil {
			return ErrInvalidValue
		}
		if metatable.owner != state.runtime {
			return ErrForeignValue
		}
	}
	switch value.Kind() {
	case TableKind:
		table, _ := value.Table()
		table.metatable = metatable
	case UserDataKind:
		data, _ := value.UserData()
		data.metatable = metatable
	default:
		state.typeMetatables[value.Kind()] = metatable
	}
	return nil
}

// FunctionEnvironment returns function's Lua 5.1 environment.
func (state *State) FunctionEnvironment(function *Function) (*Table, error) {
	if err := state.checkFunction(function); err != nil {
		return nil, err
	}
	return function.environment, nil
}

// SetFunctionEnvironment replaces function's Lua 5.1 environment.
func (state *State) SetFunctionEnvironment(function *Function, environment *Table) error {
	if err := state.checkFunction(function); err != nil {
		return err
	}
	if environment == nil || environment.owner == nil {
		return ErrInvalidValue
	}
	if environment.owner != state.runtime {
		return ErrForeignValue
	}
	function.environment = environment
	return nil
}

// ThreadEnvironment returns thread's Lua 5.1 global environment.
func (state *State) ThreadEnvironment(thread *Thread) (*Table, error) {
	if err := state.checkThread(thread); err != nil {
		return nil, err
	}
	return thread.globals, nil
}

// SetThreadEnvironment replaces thread's Lua 5.1 global environment.
func (state *State) SetThreadEnvironment(
	thread *Thread,
	environment *Table,
) error {
	if err := state.checkThread(thread); err != nil {
		return err
	}
	if environment == nil || environment.owner == nil {
		return ErrInvalidValue
	}
	if environment.owner != state.runtime {
		return ErrForeignValue
	}
	thread.globals = environment
	return nil
}

// UserDataEnvironment returns data's Lua 5.1 environment. A nil result means
// no environment is installed.
func (state *State) UserDataEnvironment(data *UserData) (*Table, error) {
	if err := state.checkUserData(data); err != nil {
		return nil, err
	}
	return data.environment, nil
}

// SetUserDataEnvironment replaces data's Lua 5.1 environment. Passing nil
// removes it.
func (state *State) SetUserDataEnvironment(data *UserData, environment *Table) error {
	if err := state.checkUserData(data); err != nil {
		return err
	}
	if environment != nil {
		if environment.owner == nil {
			return ErrInvalidValue
		}
		if environment.owner != state.runtime {
			return ErrForeignValue
		}
	}
	data.environment = environment
	return nil
}

func (state *State) checkOpen() error {
	if state == nil || state.runtime == nil || state.runtime.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (state *State) checkFunction(function *Function) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if function == nil || function.owner == nil {
		return ErrInvalidValue
	}
	if function.owner != state.runtime {
		return ErrForeignValue
	}
	return nil
}

func (state *State) checkThread(thread *Thread) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if thread == nil || thread.owner == nil {
		return ErrInvalidValue
	}
	if thread.owner != state.runtime {
		return ErrForeignValue
	}
	return nil
}

func (state *State) checkUserData(data *UserData) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if data == nil || data.owner == nil {
		return ErrInvalidValue
	}
	if data.owner != state.runtime {
		return ErrForeignValue
	}
	return nil
}

func (rt *runtimeState) accept(value Value) error {
	if !value.Valid() {
		return ErrInvalidValue
	}
	if owner := value.owner(); owner != nil && owner != rt {
		return ErrForeignValue
	}
	return nil
}

// ThreadStatus describes a Thread's execution state.
type ThreadStatus uint8

const (
	// ThreadReady identifies a Thread that has not started or is idle.
	ThreadReady ThreadStatus = iota
	// ThreadRunning identifies the currently executing Thread.
	ThreadRunning
	// ThreadNormal identifies a coroutine waiting for a coroutine it resumed.
	ThreadNormal
	// ThreadSuspended identifies a coroutine stopped at yield.
	ThreadSuspended
	// ThreadDead identifies a coroutine that returned or failed.
	ThreadDead
	// ThreadClosed identifies a Thread whose State has closed.
	ThreadClosed
)

// Thread is the canonical object for a Lua thread.
//
// The main thread and coroutines use the same representation. Execution
// registers and activation records remain private. Resume operations must be
// serialized with every other operation on the owning State.
//
// A Thread must not be copied after first use. Retain and pass its pointer.
type Thread struct {
	objectHeader
	state             *State
	globals           *Table
	values            []slot
	frames            []activation
	continuations     []executionContinuation
	openUpvalues      *upvalue
	top               int
	frameExtent       int
	activeNativeToken uint64
	nativeCallDepth   uint16
	errorHandlerDepth uint16
	contextBudget     uint16
	status            ThreadStatus
	main              bool
}

// Value returns the owning Lua value for thread.
func (thread *Thread) Value() Value {
	if thread == nil || thread.owner == nil {
		return Value{}
	}
	return objectValue(ThreadKind, unsafe.Pointer(thread))
}

// State returns the State that owns thread.
func (thread *Thread) State() *State {
	if thread == nil {
		return nil
	}
	return thread.state
}

// Status returns thread's current status.
func (thread *Thread) Status() ThreadStatus {
	if thread == nil || thread.owner == nil || thread.owner.closed.Load() {
		return ThreadClosed
	}
	return thread.status
}

// IsMain reports whether thread is its State's main thread.
func (thread *Thread) IsMain() bool {
	return thread != nil && thread.main
}

// UserData is a canonical Lua userdata object holding a Go value.
//
// The payload is opaque to Lua unless native functions expose operations on
// it. Metatable and environment changes are controlled by State operations.
//
// UserData must not be copied after first use. Retain and pass its pointer.
type UserData struct {
	objectHeader
	payload     any
	metatable   *Table
	environment *Table
	resource    *nativeResourceToken
}

// Value returns the owning Lua value for userdata.
func (data *UserData) Value() Value {
	if data == nil || data.owner == nil {
		return Value{}
	}
	return objectValue(UserDataKind, unsafe.Pointer(data))
}

// Data returns the Go payload. Reading the payload remains safe after the
// owning State closes. Userdata reserved for a runtime library has no public
// payload and returns nil.
func (data *UserData) Data() any {
	if data == nil {
		return nil
	}
	return data.payload
}

// SetData replaces the Go payload. Runtime-owned userdata returns
// ErrReadOnlyUserData.
func (data *UserData) SetData(payload any) error {
	if data == nil || data.owner == nil || data.owner.closed.Load() {
		return ErrClosed
	}
	if data.resource != nil {
		return ErrReadOnlyUserData
	}
	data.payload = payload
	return nil
}
