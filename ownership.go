package lua

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
	"weak"
)

// noCopy lets go vet reject copying canonical runtime objects after first use.
// It occupies no storage.
type noCopy struct{}

func (*noCopy) Lock() {}

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

func (rt *runtimeState) accept(value Value) error {
	if !value.Valid() {
		return ErrInvalidValue
	}
	if owner := value.owner(); owner != nil && owner != rt {
		return ErrForeignValue
	}
	return nil
}

// importAcceptedSlot brings validated API values into the compact runtime.
// Short strings become runtime-local cache entries; longer State-neutral strings
// retain their existing immutable backing and enter the State's swept
// attribution set. Internal compact seams never pay this boundary work.
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

// hostToken is the owning boundary representation for collected objects.
// Its object pointer leads to the compact runtime object; runtimeState's host
// directory weakly indexes the object to this token. Named public handle types
// use this exact layout, so publication does not need a second wrapper
// allocation.
//
// owner and object also keep the allocation pointer-rich and larger than the
// runtime's tiny pointer-free allocation batching exception for weak pointers.
type hostToken struct {
	noCopy noCopy
	owner  *runtimeState
	object unsafe.Pointer
	kind   Kind
}
