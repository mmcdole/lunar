package lua

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
)

type nativeReleaseReason uint8

const (
	nativeReleaseExplicit nativeReleaseReason = iota
	nativeReleaseStateClose
	nativeReleaseCollected
)

// nativeRelease describes why one native resource is being released. The
// context is present only during an explicit operation and is never retained.
type nativeRelease struct {
	reason  nativeReleaseReason
	context context.Context
}

// nativeResourceCleanup releases one runtime-owned native resource. It must
// not enter Lua or retain the runtime UserData that owns its finalizer token.
type nativeResourceCleanup func(any, nativeRelease) error

// nativeResourceRegistry lets State.Close release every live native resource
// without making the State itself part of a retained UserData's object graph.
// Its mutex only coordinates Go finalizers with serialized State operations.
type nativeResourceRegistry struct {
	mu     sync.Mutex
	head   *nativeResource
	count  int
	closed bool
}

type nativeResource struct {
	registry *nativeResourceRegistry
	previous *nativeResource
	next     *nativeResource
	value    any
	cleanup  nativeResourceCleanup
	owned    bool
	once     sync.Once
	closed   atomic.Bool
	err      error
}

// nativeResourceToken is reachable only through its owning UserData. The
// registry deliberately retains the resource record, not this token, so Go
// can reclaim an unreachable UserData and run native cleanup.
type nativeResourceToken struct {
	resource *nativeResource
	class    *nativeResourceClass
}

// nativeResourceClass is an unforgeable runtime-library object class. A
// library combines this identity with its own current metatable check, so
// assigning a library metatable to unrelated userdata cannot impersonate a
// native handle.
type nativeResourceClass struct {
	marker byte
}

// nativeResourceLease keeps the finalizer token reachable for the complete
// native operation. Callers must release it after using value.
type nativeResourceLease struct {
	value any
	token *nativeResourceToken
	owned bool
}

var errBorrowedNativeResource = errors.New(
	"lua: borrowed native resource cannot be closed",
)

func (registry *nativeResourceRegistry) register(
	value any,
	cleanup nativeResourceCleanup,
	owned bool,
) (*nativeResourceToken, error) {
	if cleanup == nil {
		panic("lua: nil native resource cleanup")
	}
	resource := &nativeResource{
		registry: registry,
		value:    value,
		cleanup:  cleanup,
		owned:    owned,
	}

	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil, ErrClosed
	}
	resource.next = registry.head
	if registry.head != nil {
		registry.head.previous = resource
	}
	registry.head = resource
	registry.count++
	registry.mu.Unlock()

	token := &nativeResourceToken{resource: resource}
	runtime.SetFinalizer(token, finalizeNativeResource)
	return token, nil
}

func (registry *nativeResourceRegistry) remove(resource *nativeResource) {
	registry.mu.Lock()
	if resource.previous == nil {
		if registry.head == resource {
			registry.head = resource.next
		}
	} else {
		resource.previous.next = resource.next
	}
	if resource.next != nil {
		resource.next.previous = resource.previous
	}
	if registry.count != 0 {
		registry.count--
	}
	resource.previous = nil
	resource.next = nil
	registry.mu.Unlock()
}

func (registry *nativeResourceRegistry) releaseAll() error {
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil
	}
	registry.closed = true
	resources := make([]*nativeResource, 0, registry.count)
	for resource := registry.head; resource != nil; resource = resource.next {
		resources = append(resources, resource)
	}
	registry.mu.Unlock()

	failures := make([]error, 0, len(resources))
	for _, resource := range resources {
		if _, err := resource.release(nativeRelease{
			reason: nativeReleaseStateClose,
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (resource *nativeResource) current() (any, bool) {
	if resource == nil || resource.closed.Load() {
		return nil, false
	}
	return resource.value, true
}

func (resource *nativeResource) release(
	release nativeRelease,
) (first bool, err error) {
	if resource == nil {
		return false, nil
	}
	resource.once.Do(func() {
		first = true
		resource.closed.Store(true)
		value := resource.value
		cleanup := resource.cleanup
		defer func() {
			resource.value = nil
			resource.cleanup = nil
			registry := resource.registry
			resource.registry = nil
			if registry != nil {
				registry.remove(resource)
			}
		}()
		resource.err = cleanup(value, release)
	})
	return first, resource.err
}

func finalizeNativeResource(token *nativeResourceToken) {
	// Cleanup functions are private runtime code, but a finalizer must never
	// let an implementation panic terminate the host process.
	defer func() {
		_ = recover()
	}()
	if token != nil {
		_, _ = token.resource.release(nativeRelease{
			reason: nativeReleaseCollected,
		})
	}
}

func (state *State) newManagedUserData(
	value any,
	cleanup nativeResourceCleanup,
) (*UserData, error) {
	return state.newRuntimeUserData(value, cleanup, true)
}

func (state *State) newBorrowedUserData(
	value any,
) (*UserData, error) {
	return state.newRuntimeUserData(
		value,
		releaseBorrowedNativeResource,
		false,
	)
}

func (state *State) newRuntimeUserData(
	value any,
	cleanup nativeResourceCleanup,
	owned bool,
) (*UserData, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	registry := state.resources
	if registry == nil {
		registry = &nativeResourceRegistry{}
		state.resources = registry
	}
	token, err := registry.register(value, cleanup, owned)
	if err != nil {
		return nil, err
	}
	return &UserData{
		objectHeader: objectHeader{owner: state.runtime},
		environment:  state.constructionEnvironment(),
		resource:     token,
	}, nil
}

func releaseBorrowedNativeResource(any, nativeRelease) error {
	return nil
}

func acquireManagedResource(
	data *UserData,
) (nativeResourceLease, bool) {
	if data == nil || data.resource == nil {
		return nativeResourceLease{}, false
	}
	value, open := data.resource.resource.current()
	if !open {
		return nativeResourceLease{}, false
	}
	return nativeResourceLease{
		value: value,
		token: data.resource,
		owned: data.resource.resource.owned,
	}, true
}

func classifyManagedUserData(
	data *UserData,
	class *nativeResourceClass,
	metatable *Table,
) {
	if data == nil || data.resource == nil ||
		class == nil || metatable == nil {
		panic("lua: invalid managed userdata classification")
	}
	data.resource.class = class
	data.metatable = metatable
}

func isManagedUserDataClass(
	data *UserData,
	class *nativeResourceClass,
) bool {
	return data != nil &&
		data.resource != nil &&
		class != nil &&
		data.resource.class == class
}

func (lease nativeResourceLease) release() {
	runtime.KeepAlive(lease.token)
}

func closeManagedResource(data *UserData) (first bool, err error) {
	return closeManagedResourceContext(data, nil)
}

func closeManagedResourceContext(
	data *UserData,
	ctx context.Context,
) (first bool, err error) {
	if data == nil || data.resource == nil {
		return false, nil
	}
	token := data.resource
	if !token.resource.owned {
		return false, errBorrowedNativeResource
	}
	runtime.SetFinalizer(token, nil)
	first, err = token.resource.release(nativeRelease{
		reason:  nativeReleaseExplicit,
		context: ctx,
	})
	runtime.KeepAlive(data)
	return first, err
}

func collectManagedResource(
	data *UserData,
) (first bool, err error) {
	if data == nil || data.resource == nil {
		return false, nil
	}
	token := data.resource
	runtime.SetFinalizer(token, nil)
	first, err = token.resource.release(nativeRelease{
		reason: nativeReleaseCollected,
	})
	runtime.KeepAlive(data)
	return first, err
}
