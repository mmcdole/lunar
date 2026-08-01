package lua

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
	"weak"
)

// ErrClosed reports an operation that requires a live State.
var ErrClosed = errors.New("lua: state is closed")

// ErrRunning reports an operation that requires an idle State while Lua is
// executing. A NativeFunc reenters Lua through its borrowed Frame instead.
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
	// Libraries selects the Lua standard libraries installed by New. Its zero
	// value installs none. CoreLibraries and FullLibraries provide common
	// profiles; a LibrarySet literal selects any other subset.
	Libraries LibrarySet
	// ScriptLoader controls file-backed script loading for State.LoadFile,
	// State.DoFile, Lua loadfile and dofile, and require. Its zero value
	// denies script-file access. Reader- and string-backed loading remain
	// available independently.
	ScriptLoader ScriptLoader
	// Stdin is the State's standard input stream. A nil interface selects
	// os.Stdin. Standard-input consumers share one logical cursor. Lua
	// libraries borrow the stream and never close it. Child processes
	// instead inherit the embedding process's actual os.Stdin unless
	// io.popen connects that descriptor to its returned pipe. Filename-less
	// loadfile and dofile may consume this stream only with HostLoader.
	Stdin io.Reader
	// Stdout is the State's standard output stream. A nil interface selects
	// os.Stdout. Standard-output consumers share one buffering endpoint. Lua
	// libraries borrow the stream and never close it. Child processes
	// instead inherit the embedding process's actual os.Stdout unless
	// io.popen connects that descriptor to its returned pipe.
	Stdout io.Writer
	// Stderr is the State's standard error stream. A nil interface selects
	// os.Stderr. Diagnostic consumers share one buffering endpoint. Lua
	// libraries borrow the stream and never close it. Child processes
	// instead inherit the embedding process's actual os.Stderr.
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
	// MaxHeapBytes limits the logical Lua heap, measured as HeapBytes
	// measures it: Lua objects and their owned storage, not process
	// memory. Opaque userdata payloads and Go allocator overhead are
	// outside the count, so actual process usage is higher. Zero leaves
	// the heap unlimited.
	//
	// The limit is enforced at execution safe points: crossing it schedules
	// a collection, and the runtime raises a LimitError only if the heap
	// is still over the limit once unreachable objects are gone. A single
	// allocation can therefore overshoot the limit before the runtime
	// observes it, so MaxHeapBytes bounds sustained retention rather than
	// peak allocation. Collection runs more often as retention approaches
	// the limit, so a State held near saturation trades throughput for
	// enforcement.
	//
	// While an xpcall error handler runs, the limit widens by
	// max(64 KiB, MaxHeapBytes/8) so the handler can allocate its report,
	// mirroring the emergency capacity MaxValues and MaxFrames grant.
	//
	// Every operation that runs the executor observes the limit, including
	// host-initiated Lua operations such as Call, SetGlobal, and Index. Raw
	// operations and explicit State.Collect and Frame.Collect do not, so a
	// host can still build and inspect a State that holds more than the
	// limit allows.
	MaxHeapBytes int
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

func (header *objectHeader) owningToken(
	kind Kind,
	object unsafe.Pointer,
) *hostToken {
	if header == nil ||
		header.owner == nil ||
		!kind.isReference() ||
		object == nil ||
		unsafe.Pointer(header) != object {
		panic("lua: invalid host-token publication")
	}
	return header.owner.hosts.publish(header, kind, object)
}

// hostDirectory canonicalizes public handles without adding a host-only word
// to every compact object. Both sides are weak: the directory neither turns a
// discarded handle into a root nor prevents an unreachable object from being
// reclaimed. State-local collection also uses these entries as its host-root
// set.
type hostDirectory struct {
	mutex   sync.Mutex
	entries map[weak.Pointer[objectHeader]]weak.Pointer[hostToken]
	keys    []weak.Pointer[objectHeader]
	cursor  int
}

func (directory *hostDirectory) publish(
	header *objectHeader,
	kind Kind,
	object unsafe.Pointer,
) *hostToken {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()

	key := weak.Make(header)
	if reference, found := directory.entries[key]; found {
		if existing := reference.Value(); existing != nil {
			if existing.owner != header.owner ||
				existing.kind != kind ||
				existing.object != object {
				panic("lua: corrupt host-token directory")
			}
			return existing
		}
	}

	if directory.entries == nil {
		directory.initialize()
	}
	token := &hostToken{
		owner:  header.owner,
		object: object,
		kind:   kind,
	}
	if _, found := directory.entries[key]; !found {
		directory.keys = append(directory.keys, key)
	}
	directory.entries[key] = weak.Make(token)
	// Install before maintenance so the reverse scan can move away from the
	// growing tail and continue visiting older entries.
	directory.maintainLocked(1)
	return token
}

func (directory *hostDirectory) initialize() {
	directory.entries = make(
		map[weak.Pointer[objectHeader]]weak.Pointer[hostToken],
	)
}

func (directory *hostDirectory) prune() {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	directory.pruneLocked()
}

func (directory *hostDirectory) pruneLocked() {
	live := directory.keys[:0]
	for _, object := range directory.keys {
		token, found := directory.entries[object]
		if !found ||
			object.Value() == nil ||
			token.Value() == nil {
			delete(directory.entries, object)
			continue
		}
		live = append(live, object)
	}
	clear(directory.keys[len(live):])
	directory.keys = live
	directory.cursor = 0
	if len(directory.entries) == 0 {
		directory.entries = nil
		directory.keys = nil
	}
}

func (directory *hostDirectory) maintainLocked(limit int) {
	for limit > 0 && len(directory.keys) != 0 {
		// Scan backwards so new tail entries cannot keep the cursor ahead
		// of older entries. cursor is the exclusive upper bound of the
		// current reverse pass.
		if directory.cursor <= 0 ||
			directory.cursor > len(directory.keys) {
			directory.cursor = len(directory.keys)
		}
		directory.cursor--
		object := directory.keys[directory.cursor]
		token, found := directory.entries[object]
		if found &&
			object.Value() != nil &&
			token.Value() != nil {
			limit--
			continue
		}
		delete(directory.entries, object)
		last := len(directory.keys) - 1
		directory.keys[directory.cursor] = directory.keys[last]
		directory.keys[last] = weak.Pointer[objectHeader]{}
		directory.keys = directory.keys[:last]
		limit--
	}
	if len(directory.entries) == 0 {
		directory.entries = nil
		directory.keys = nil
		directory.cursor = 0
	}
}

// runtimeState is the lightweight ownership token shared by canonical
// objects. It owns only runtime-wide caches and close state, never the State's
// object roots. Retaining one object therefore does not pin an unrelated Lua
// graph.
type runtimeState struct {
	closed          atomic.Bool
	strings         stringPool
	hosts           hostDirectory
	collection      collectionControl
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
	noCopy           noCopy
	runtime          *runtimeState
	objects          objectLedger
	options          Options
	limits           resourceLimits
	streams          *standardStreams
	scriptLoader     scriptLoaderConfig
	location         *time.Location
	now              func() time.Time
	closeContext     *stateCloseContext
	active           *threadObject
	main             *threadObject
	registry         *tableObject
	packageSentinel  *userDataObject
	modulePreloads   *tableObject
	resources        *nativeResourceRegistry
	execution        executionControl
	ambient          context.Context
	ambientDone      <-chan struct{}
	operationBridges [luaOperationCount]*functionObject
	typeMetatables   [TableKind + 1]*tableObject
	userDataTypes    map[string]*userDataTypeRegistration
}

// New constructs a State and installs the selected standard libraries.
func New(options Options) (*State, error) {
	if options.MaxValues < 0 ||
		options.MaxFrames < 0 ||
		options.MaxLoadBytes < 0 ||
		options.MaxHeapBytes < 0 {
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
	libraries, err := normalizeLibrarySet(options.Libraries)
	if err != nil {
		return nil, err
	}
	options.Libraries = append(LibrarySet(nil), options.Libraries...)
	loader, err := normalizeScriptLoader(options.ScriptLoader)
	if err != nil {
		return nil, err
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

	rt := &runtimeState{
		collection: defaultCollectionControl(),
	}
	rt.collection.heapLimit = uint64(options.MaxHeapBytes)
	rt.strings.owner = rt
	state := &State{
		runtime: rt,
		options: options,
		streams: newStandardStreams(
			options.Stdin,
			options.Stdout,
			options.Stderr,
		),
		scriptLoader: loader,
		location:     options.Location,
		now:          options.Now,
		limits: resourceLimits{
			values: options.MaxValues,
			frames: options.MaxFrames,
		},
	}
	globals := newTable(state, 0, 0)
	state.main = &threadObject{
		state:   state,
		globals: globals,
		status:  ThreadReady,
		flags:   threadFlagMain,
	}
	state.registerThread(state.main)
	state.registry = newTable(state, 0, 0)
	if err := state.openLibraries(libraries); err != nil {
		return nil, errors.Join(err, state.Close())
	}
	return state, nil
}

// Close releases runtime-owned resources and prevents further execution or
// mutation. Repeated serialized calls are idempotent. Close must not overlap
// another operation on this State, including another call to Close.
//
// Every still-open runtime-owned native resource is closed exactly once.
// Borrowed native handles are detached without closing their underlying
// resources. Before native teardown, Close first drains previously pending
// userdata __gc handlers, then runs newly eligible handlers in reverse
// creation order, including handlers on reachable userdata. Lua errors from
// those handlers are ignored. A panic from a native handler is remembered
// while later handlers and native cleanup continue, then propagated after the
// State has closed.
//
// Close continues through every native-resource record and returns cleanup
// failures joined together, including failures produced by a resource's
// close-time __gc handler. Buffered standard output is flushed and any flush
// failures are included in the returned error. The State is closed even when
// that error is non-nil. Standard streams supplied through Options are
// borrowed and are never closed.
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
	if state.runtime.closed.Load() {
		state.runtime.hosts.prune()
		return nil
	}
	closeContext := stateCloseContext{}
	state.closeContext = &closeContext
	panicValue, panicked := state.finalizeForClose()
	state.closeContext = nil
	state.runtime.closed.Store(true)
	var streamErr error
	if state.streams != nil {
		streamErr = state.streams.release()
	}
	state.streams = nil
	state.scriptLoader = scriptLoaderConfig{}
	state.location = nil
	state.now = nil
	var resourceErr error
	if state.resources != nil {
		resourceErr = state.resources.releaseAll()
		state.resources = nil
	}
	state.runtime.strings.close()
	state.runtime.collection.release()
	state.detachObjectsForClose()
	state.registry = nil
	state.packageSentinel = nil
	state.modulePreloads = nil
	state.operationBridges =
		[luaOperationCount]*functionObject{}
	state.options.Stdin = nil
	state.options.Stdout = nil
	state.options.Stderr = nil
	state.options.Libraries = nil
	state.options.ScriptLoader = ScriptLoader{}
	state.options.Location = nil
	state.options.Now = nil
	state.ambient = nil
	state.ambientDone = nil
	state.typeMetatables = [TableKind + 1]*tableObject{}
	state.userDataTypes = nil
	state.runtime.hosts.prune()
	closeErr := errors.Join(
		errors.Join(closeContext.failures...),
		streamErr,
		resourceErr,
	)
	if panicked {
		panic(panicValue)
	}
	return closeErr
}

// MainThread returns the canonical main Thread.
func (state *State) MainThread() *Thread {
	if state == nil || state.main == nil {
		return nil
	}
	return state.main.owningHandle()
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
	var value stringRef
	if len(text) <= shortStringLimit {
		value = state.runtime.strings.make(text)
	} else {
		value = newStateNeutralLongString(text)
	}
	return stringValue(value)
}

// NewTable constructs an empty canonical Table.
func (state *State) NewTable() (*Table, error) {
	return state.NewTableWithCapacity(0, 0)
}

// NewTableWithCapacity constructs an empty canonical Table using capacity
// hints for its array and record parts.
func (state *State) NewTableWithCapacity(
	arrayHint, recordHint int,
) (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if arrayHint < 0 || recordHint < 0 {
		return nil, ErrNegativeCapacity
	}
	if arrayHint > maxTableHint || recordHint > maxTableHint {
		return nil, ErrCapacity
	}
	return newTable(state, arrayHint, recordHint).owningHandle(), nil
}

// NewUserData constructs canonical userdata holding payload. Its initial
// environment is the currently executing Function's environment, or the main
// Thread's global environment outside a callback.
func (state *State) NewUserData(payload any) (*UserData, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	data := newUserDataObject(
		state,
		payload,
		state.constructionEnvironment(),
		nil,
	)
	return data.owningHandle(), nil
}

// Registry returns the private Lua registry table.
//
// The returned table is canonical. It is not an execution stack or a set of
// pseudo-indexed Go registers.
func (state *State) Registry() (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	return state.registry.owningHandle(), nil
}

func (state *State) currentThread() *threadObject {
	if state.active != nil {
		return state.active
	}
	return state.main
}

func (state *State) globalEnvironment() *tableObject {
	return state.currentThread().globals
}

func (state *State) constructionEnvironment() *tableObject {
	thread := state.currentThread()
	if state.active != nil && len(thread.frames) != 0 {
		return thread.frames[len(thread.frames)-1].function.environment
	}
	return thread.globals
}

// RawGlobal returns a raw value from the current global environment.
//
// During a native callback, current means the executing Thread. Otherwise it
// means the main Thread.
func (state *State) RawGlobal(name string) (Value, error) {
	if err := state.checkOpen(); err != nil {
		return Value{}, err
	}
	return state.globalEnvironment().rawGetStringValue(name), nil
}

// RawSetGlobal performs a raw assignment in the current global environment.
//
// During a native callback, current means the executing Thread. Otherwise it
// means the main Thread.
func (state *State) RawSetGlobal(name string, value Value) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	return state.globalEnvironment().rawSetStringValue(name, value)
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
	var metatable *tableObject
	switch value.Kind() {
	case TableKind:
		metatable = tableObjectFromSlot(slotFromValue(value)).metatable
	case UserDataKind:
		metatable = userDataObjectFromSlot(slotFromValue(value)).metatable
	default:
		metatable = state.typeMetatables[value.Kind()]
	}
	return metatable.owningHandle(), nil
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
	var compactMetatable *tableObject
	if metatable != nil {
		var err error
		compactMetatable, err = state.acceptTable(metatable)
		if err != nil {
			return err
		}
	}
	switch value.Kind() {
	case TableKind:
		tableObjectFromSlot(slotFromValue(value)).metatable = compactMetatable
	case UserDataKind:
		userDataObjectFromSlot(slotFromValue(value)).metatable = compactMetatable
	default:
		state.typeMetatables[value.Kind()] = compactMetatable
	}
	runtime.KeepAlive(metatable)
	return nil
}

// FunctionEnvironment returns function's Lua 5.1 environment.
func (state *State) FunctionEnvironment(function *Function) (*Table, error) {
	object, err := state.acceptFunction(function)
	if err != nil {
		return nil, err
	}
	environment := object.environment.owningHandle()
	runtime.KeepAlive(function)
	return environment, nil
}

// SetFunctionEnvironment replaces function's Lua 5.1 environment.
func (state *State) SetFunctionEnvironment(function *Function, environment *Table) error {
	object, err := state.acceptFunction(function)
	if err != nil {
		return err
	}
	compactEnvironment, err := state.acceptTable(environment)
	if err != nil {
		return err
	}
	object.environment = compactEnvironment
	runtime.KeepAlive(function)
	runtime.KeepAlive(environment)
	return nil
}

func (state *State) checkOpen() error {
	if state == nil || state.runtime == nil || state.runtime.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (state *State) acceptTable(table *Table) (*tableObject, error) {
	token := table.token()
	if token == nil ||
		token.owner == nil ||
		token.kind != TableKind ||
		token.object == nil {
		return nil, ErrInvalidValue
	}
	if token.owner != state.runtime {
		return nil, ErrForeignValue
	}
	object := (*tableObject)(token.object)
	runtime.KeepAlive(table)
	return object, nil
}

func (state *State) acceptFunction(
	function *Function,
) (*functionObject, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	token := function.token()
	if token == nil ||
		token.owner == nil ||
		token.kind != FunctionKind ||
		token.object == nil {
		return nil, ErrInvalidValue
	}
	if token.owner != state.runtime {
		return nil, ErrForeignValue
	}
	object := (*functionObject)(token.object)
	runtime.KeepAlive(function)
	return object, nil
}

func (state *State) acceptThread(
	thread *Thread,
) (*threadObject, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	token := thread.token()
	if token == nil ||
		token.owner == nil ||
		token.kind != ThreadKind ||
		token.object == nil {
		return nil, ErrInvalidValue
	}
	if token.owner != state.runtime {
		return nil, ErrForeignValue
	}
	object := (*threadObject)(token.object)
	runtime.KeepAlive(thread)
	return object, nil
}

func (state *State) checkUserData(data *UserData) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	token := data.token()
	if token == nil ||
		token.owner == nil ||
		token.kind != UserDataKind ||
		token.object == nil {
		return ErrInvalidValue
	}
	if token.owner != state.runtime {
		return ErrForeignValue
	}
	runtime.KeepAlive(data)
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

// importValue crosses the owning Go API into the compact runtime. Short
// strings become runtime-local cache entries; longer State-neutral strings
// retain their existing immutable backing and enter the State's swept
// attribution set. Internal compact seams never pay this boundary work.
func (rt *runtimeState) importValue(
	value Value,
) (compact slot, err error) {
	if value.ref != numberMarkerPointer {
		return rt.importNonNumberValue(value)
	}
	compact.bits = value.bits
	return
}

func (rt *runtimeState) importNonNumberValue(value Value) (slot, error) {
	if err := rt.accept(value); err != nil {
		return nilSlot, err
	}
	return rt.importAcceptedSlot(slotFromValue(value)), nil
}

func (rt *runtimeState) importAcceptedSlot(compact slot) slot {
	if !compact.isString() {
		return compact
	}
	return rt.importAcceptedString(compact)
}

func (rt *runtimeState) importAcceptedString(compact slot) slot {
	length := stringSlotLen(compact)
	if length <= 1 {
		return compact
	}
	if length <= shortStringLimit {
		return stringSlot(rt.strings.makeKnownHash(
			stringSlotText(compact),
			stringSlotHash(compact),
		))
	}
	rt.collection.attributeString(stringRef{
		ref:  compact.ref,
		bits: compact.bits,
	})
	return compact
}

func (rt *runtimeState) acceptSlot(value slot) error {
	if value.kind() == InvalidKind {
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

// Thread is an opaque owning handle for a Lua thread.
//
// Repeated publication of the same live Lua thread returns the same handle
// pointer. Execution slots retain the compact thread directly and do not pass
// through this handle. Resume operations must be serialized with every other
// operation on the owning State.
//
// A Thread must not be copied after first use. Retain and pass its pointer.
type Thread hostToken

type threadObject struct {
	objectHeader
	state             *State
	globals           *tableObject
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
	flags             threadFlags
}

// Value returns the owning Lua value for thread.
func (thread *Thread) Value() Value {
	token := thread.token()
	if token == nil ||
		token.owner == nil ||
		token.object == nil ||
		token.kind != ThreadKind {
		return Value{}
	}
	value := Value{ref: unsafe.Pointer(token), bits: uint64(ThreadKind)}
	runtime.KeepAlive(thread)
	return value
}

func (thread *Thread) token() *hostToken {
	return (*hostToken)(thread)
}

func (thread *Thread) runtimeObject() *threadObject {
	token := thread.token()
	if token == nil ||
		token.kind != ThreadKind ||
		token.object == nil {
		return nil
	}
	return (*threadObject)(token.object)
}

func (thread *threadObject) owningHandle() *Thread {
	if thread == nil {
		return nil
	}
	token := thread.objectHeader.owningToken(
		ThreadKind,
		unsafe.Pointer(thread),
	)
	return (*Thread)(token)
}

func (thread *threadObject) owningValue() Value {
	handle := thread.owningHandle()
	return handle.Value()
}

func slotFromThreadObject(thread *threadObject) slot {
	if thread == nil || thread.owner == nil {
		panic("lua: invalid canonical thread")
	}
	return objectSlot(ThreadKind, unsafe.Pointer(thread))
}

func threadObjectFromSlot(value slot) *threadObject {
	if !value.isThread() {
		panic("lua: slot is not a thread")
	}
	return (*threadObject)(value.ref)
}

func threadHandleFromSlot(value slot) *Thread {
	return threadObjectFromSlot(value).owningHandle()
}

// State returns the State that owns thread.
func (thread *Thread) State() *State {
	object := thread.runtimeObject()
	if object == nil {
		return nil
	}
	state := object.state
	runtime.KeepAlive(thread)
	return state
}

// Status returns thread's current status.
func (thread *Thread) Status() ThreadStatus {
	object := thread.runtimeObject()
	if object == nil {
		return ThreadClosed
	}
	status := object.currentStatus()
	runtime.KeepAlive(thread)
	return status
}

func (thread *threadObject) currentStatus() ThreadStatus {
	if thread == nil ||
		thread.owner == nil ||
		thread.owner.closed.Load() {
		return ThreadClosed
	}
	return thread.status
}

// IsMain reports whether thread is its State's main thread.
func (thread *Thread) IsMain() bool {
	object := thread.runtimeObject()
	if object == nil {
		return false
	}
	main := object.isMain()
	runtime.KeepAlive(thread)
	return main
}

// UserData is an opaque owning handle for a Lua userdata object holding a Go
// value.
//
// The payload is opaque to Lua unless native functions expose operations on
// it. Metatable and environment changes are controlled by State operations.
// Repeated publication of the same live Lua object returns the same handle
// pointer. Execution slots retain the compact object directly and do not pass
// through this handle.
//
// UserData must not be copied after first use. Retain and pass its pointer.
type UserData hostToken

type userDataObject struct {
	objectHeader
	payload     any
	metatable   *tableObject
	environment *tableObject
	resource    *nativeResourceToken
	gcMark      objectMark
	flags       userDataFlags
}

func newUserDataObject(
	state *State,
	payload any,
	environment *tableObject,
	resource *nativeResourceToken,
) *userDataObject {
	if state == nil ||
		state.runtime == nil ||
		environment != nil && environment.owner != state.runtime {
		panic("lua: invalid userdata construction")
	}
	data := &userDataObject{
		payload:     payload,
		environment: environment,
		resource:    resource,
	}
	state.registerUserData(data)
	return data
}

// Value returns the owning Lua value for userdata.
func (data *UserData) Value() Value {
	token := data.token()
	if token == nil ||
		token.owner == nil ||
		token.object == nil ||
		token.kind != UserDataKind {
		return Value{}
	}
	value := Value{ref: unsafe.Pointer(token), bits: uint64(UserDataKind)}
	runtime.KeepAlive(data)
	return value
}

// Data returns the Go payload. Reading the payload remains safe after the
// owning State closes. Userdata reserved for a runtime library has no public
// payload and returns nil.
func (data *UserData) Data() any {
	object := data.runtimeObject()
	if object == nil {
		return nil
	}
	payload := object.payload
	runtime.KeepAlive(data)
	return payload
}

// SetData replaces the Go payload. Runtime-owned userdata returns
// ErrReadOnlyUserData.
func (data *UserData) SetData(payload any) error {
	token := data.token()
	if token == nil ||
		token.owner == nil ||
		token.owner.closed.Load() ||
		token.object == nil ||
		token.kind != UserDataKind {
		return ErrClosed
	}
	object := (*userDataObject)(token.object)
	if object.resource != nil {
		return ErrReadOnlyUserData
	}
	object.payload = payload
	runtime.KeepAlive(data)
	return nil
}

func (data *UserData) token() *hostToken {
	return (*hostToken)(data)
}

func (data *UserData) runtimeObject() *userDataObject {
	token := data.token()
	if token == nil ||
		token.kind != UserDataKind ||
		token.object == nil {
		return nil
	}
	return (*userDataObject)(token.object)
}

func (data *userDataObject) owningHandle() *UserData {
	if data == nil {
		return nil
	}
	token := data.objectHeader.owningToken(
		UserDataKind,
		unsafe.Pointer(data),
	)
	return (*UserData)(token)
}

func (data *userDataObject) owningValue() Value {
	handle := data.owningHandle()
	return handle.Value()
}

func userDataObjectFromSlot(value slot) *userDataObject {
	if !value.isUserData() {
		panic("lua: slot is not userdata")
	}
	return (*userDataObject)(value.ref)
}

func userDataHandleFromSlot(value slot) *UserData {
	return userDataObjectFromSlot(value).owningHandle()
}
