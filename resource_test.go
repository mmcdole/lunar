package lua

import (
	"errors"
	"io"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type nativeCleanupProbe struct {
	done    chan struct{}
	err     error
	order   *[]int
	id      int
	count   atomic.Int32
	release atomic.Uint32
}

func cleanNativeProbe(value any, release nativeRelease) error {
	probe := value.(*nativeCleanupProbe)
	probe.count.Add(1)
	probe.release.Store(uint32(release.reason))
	if probe.order != nil {
		*probe.order = append(*probe.order, probe.id)
	}
	if probe.done != nil {
		select {
		case probe.done <- struct{}{}:
		default:
		}
	}
	return probe.err
}

func TestManagedNativeResourceExplicitCloseAndSealedPayload(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	probe := &nativeCleanupProbe{}
	data, err := state.newManagedUserData(probe, cleanNativeProbe)
	if err != nil {
		t.Fatal(err)
	}
	handle := data.owningHandle()
	if handle.Data() != nil {
		t.Fatalf("managed userdata exposed payload %v", handle.Data())
	}
	if err := handle.SetData("replacement"); !errors.Is(
		err,
		ErrReadOnlyUserData,
	) {
		t.Fatalf("managed SetData = %v; want ErrReadOnlyUserData", err)
	}

	lease, open := acquireManagedResource(data)
	if !open || lease.value != probe {
		t.Fatalf("managed value = (%v, %v)", lease.value, open)
	}
	lease.release()

	first, err := closeManagedResource(data)
	if err != nil || !first {
		t.Fatalf("first close = (%v, %v)", first, err)
	}
	if probe.count.Load() != 1 {
		t.Fatalf("cleanup count = %d; want 1", probe.count.Load())
	}
	if reason := nativeReleaseReason(probe.release.Load()); reason !=
		nativeReleaseExplicit {
		t.Fatalf("explicit cleanup reason = %d", reason)
	}
	if _, open := acquireManagedResource(data); open {
		t.Fatal("closed resource remained available")
	}
	first, err = closeManagedResource(data)
	if err != nil || first {
		t.Fatalf("second close = (%v, %v)", first, err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if probe.count.Load() != 1 {
		t.Fatalf(
			"State.Close repeated explicit cleanup %d times",
			probe.count.Load(),
		)
	}
	if err := handle.SetData("replacement"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed-State SetData = %v; want ErrClosed", err)
	}
}

func TestManagedNativeResourceSemanticSweepClosesAndUnregisters(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	probe := &nativeCleanupProbe{done: make(chan struct{}, 1)}
	if err := allocateAbandonedNativeResource(state, probe); err != nil {
		t.Fatal(err)
	}

	state.collectUnreachable()
	if probe.count.Load() != 1 {
		t.Fatalf("semantic cleanup count = %d; want 1", probe.count.Load())
	}
	if reason := nativeReleaseReason(probe.release.Load()); reason !=
		nativeReleaseCollected {
		t.Fatalf("semantic cleanup reason = %d", reason)
	}
	if nativeResourceCount(state.resources) != 0 {
		t.Fatal("semantically collected resource remained registered")
	}
	runtime.KeepAlive(state)
}

func TestCloseFinalizerUsesDeterministicNativeResourcePolicy(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("close cleanup failed")
	probe := &nativeCleanupProbe{err: cleanupFailure}
	var resourceData *userDataObject
	var order []string

	observer := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "observer")
		if _, open := acquireManagedResource(resourceData); open {
			t.Fatal("later close finalizer observed an open resource")
		}
		return frame.Return()
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(observer),
		),
		nil,
	)

	resourceData, err = state.newManagedUserData(probe, cleanNativeProbe)
	if err != nil {
		t.Fatal(err)
	}
	releaser := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "release")
		data, present := frame.userDataObject(0)
		if !present || data != resourceData {
			t.Fatal("resource finalizer received the wrong userdata")
		}
		_, _ = releaseCollectedResource(state, data)
		return frame.Return()
	})
	resourceData.metatable = newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(releaser),
	)

	closeErr := state.Close()
	if !errors.Is(closeErr, cleanupFailure) {
		t.Fatalf(
			"State.Close error = %v; want cleanup failure",
			closeErr,
		)
	}
	if !reflect.DeepEqual(order, []string{"release", "observer"}) {
		t.Fatalf(
			"resource close finalizer order = %v; want [release observer]",
			order,
		)
	}
	if probe.count.Load() != 1 {
		t.Fatalf("resource cleanup count = %d; want 1", probe.count.Load())
	}
	if reason := nativeReleaseReason(probe.release.Load()); reason !=
		nativeReleaseStateClose {
		t.Fatalf("close-finalizer release reason = %d", reason)
	}
}

func TestRecursiveCollectionDuringCloseUsesShutdownResourcePolicy(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("recursive close cleanup failed")
	probe := &nativeCleanupProbe{err: cleanupFailure}
	if _, err := state.newManagedUserData(probe, cleanNativeProbe); err != nil {
		t.Fatal(err)
	}
	collector := newFinalizerFunction(state, func(frame Frame) Outcome {
		if _, failure := frame.collectAndFinalize(); failure != nil {
			return frame.Reraise(failure)
		}
		return frame.Return()
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(collector),
		),
		nil,
	)

	closeErr := state.Close()
	if !errors.Is(closeErr, cleanupFailure) {
		t.Fatalf(
			"recursive State.Close error = %v; want cleanup failure",
			closeErr,
		)
	}
	if probe.count.Load() != 1 {
		t.Fatalf(
			"recursive close cleanup count = %d; want 1",
			probe.count.Load(),
		)
	}
	if reason := nativeReleaseReason(probe.release.Load()); reason !=
		nativeReleaseStateClose {
		t.Fatalf("recursive close release reason = %d", reason)
	}
}

func allocateAbandonedNativeResource(
	state *State,
	probe *nativeCleanupProbe,
) error {
	_, err := state.newManagedUserData(probe, cleanNativeProbe)
	return err
}

func TestCompactUserDataKeepsNativeResourceAlive(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	probe := &nativeCleanupProbe{done: make(chan struct{}, 1)}
	data, err := state.newManagedUserData(probe, cleanNativeProbe)
	if err != nil {
		t.Fatal(err)
	}
	key := stringSlot(state.runtime.strings.make("compact resource"))
	state.registry.rawSetSlot(key, slotFromUserDataObject(data))
	data = nil

	for range 8 {
		runtime.GC()
		runtime.Gosched()
		select {
		case <-probe.done:
			t.Fatal("resource finalized while compact Lua storage retained it")
		default:
		}
	}
	if entries, keys, _ := hostDirectoryCounts(
		&state.runtime.hosts,
	); entries != 0 || keys != 0 {
		t.Fatalf(
			"compact resource created host metadata: entries=%d keys=%d",
			entries,
			keys,
		)
	}

	state.registry.rawSetSlot(key, nilSlot)
	state.collectUnreachable()
	waitForNativeCleanup(t, probe.done)
}

func waitForNativeCleanup(t *testing.T, done <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-done:
			return
		case <-deadline.C:
			t.Fatal("native resource was not finalized")
		case <-ticker.C:
		}
	}
}

func nativeResourceCount(registry *nativeResourceRegistry) int {
	if registry == nil {
		return 0
	}
	registry.mu.Lock()
	count := registry.count
	registry.mu.Unlock()
	return count
}

func TestManagedNativeResourceLeaseKeepsFinalizerTokenAlive(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	probe := &nativeCleanupProbe{done: make(chan struct{}, 1)}
	lease, err := acquireAbandonedNativeResource(state, probe)
	if err != nil {
		t.Fatal(err)
	}

	for range 8 {
		runtime.GC()
		select {
		case <-probe.done:
			t.Fatal("resource finalized while a native lease was live")
		default:
		}
	}
	runtime.KeepAlive(lease.token)
	lease.release()
}

func acquireAbandonedNativeResource(
	state *State,
	probe *nativeCleanupProbe,
) (nativeResourceLease, error) {
	data, err := state.newManagedUserData(probe, cleanNativeProbe)
	if err != nil {
		return nativeResourceLease{}, err
	}
	lease, open := acquireManagedResource(data)
	if !open {
		return nativeResourceLease{}, errors.New("resource was born closed")
	}
	return lease, nil
}

func TestStateCloseReleasesEveryManagedNativeResource(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("close failed")
	var order []int
	probes := []*nativeCleanupProbe{
		{id: 0, order: &order},
		{id: 1, order: &order, err: sentinel},
		{id: 2, order: &order},
	}
	allData := make([]*userDataObject, 0, len(probes))
	var retained *userDataObject
	for index, probe := range probes {
		data, createErr := state.newManagedUserData(
			probe,
			cleanNativeProbe,
		)
		if createErr != nil {
			t.Fatal(createErr)
		}
		allData = append(allData, data)
		if index == 1 {
			retained = data
		}
	}

	if err := state.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("State.Close error = %v; want cleanup failure", err)
	}
	runtime.KeepAlive(allData)
	if len(order) != 3 ||
		order[0] != 2 ||
		order[1] != 1 ||
		order[2] != 0 {
		t.Fatalf("State.Close order = %v; want [2 1 0]", order)
	}
	for index, probe := range probes {
		if probe.count.Load() != 1 {
			t.Fatalf(
				"resource %d cleanup count = %d; want 1",
				index,
				probe.count.Load(),
			)
		}
		if reason := nativeReleaseReason(probe.release.Load()); reason !=
			nativeReleaseStateClose {
			t.Fatalf("resource %d cleanup reason = %d", index, reason)
		}
	}
	if state.resources != nil {
		t.Fatal("State.Close retained the native resource registry")
	}
	if err := state.Close(); err != nil {
		t.Fatalf("repeated State.Close = %v", err)
	}
	first, err := closeManagedResource(retained)
	if first || !errors.Is(err, sentinel) {
		t.Fatalf("retained close = (%v, %v)", first, err)
	}
}

type borrowedStreamProbe struct {
	closes atomic.Int32
}

func (*borrowedStreamProbe) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (*borrowedStreamProbe) Write(text []byte) (int, error) {
	return len(text), nil
}

func (stream *borrowedStreamProbe) Close() error {
	stream.closes.Add(1)
	return nil
}

func TestStateCloseNeverClosesBorrowedStandardStreams(t *testing.T) {
	stream := &borrowedStreamProbe{}
	state, err := New(Options{
		Stdin:  stream,
		Stdout: stream,
		Stderr: stream,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.newBorrowedUserData(stream)
	if err != nil {
		t.Fatal(err)
	}
	handle := data.owningHandle()
	if err := handle.SetData("replacement"); !errors.Is(
		err,
		ErrReadOnlyUserData,
	) {
		t.Fatalf("borrowed SetData = %v; want ErrReadOnlyUserData", err)
	}
	lease, open := acquireManagedResource(data)
	if !open || lease.value != stream || lease.owned {
		t.Fatalf(
			"borrowed lease = (%v, %v, owned=%v)",
			lease.value,
			open,
			lease.owned,
		)
	}
	lease.release()
	if first, closeErr := closeManagedResource(data); first ||
		!errors.Is(closeErr, errBorrowedNativeResource) {
		t.Fatalf("borrowed close = (%v, %v)", first, closeErr)
	}
	if _, open := acquireManagedResource(data); !open {
		t.Fatal("failed borrowed close changed the handle")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if stream.closes.Load() != 0 {
		t.Fatalf(
			"borrowed stream closed %d times",
			stream.closes.Load(),
		)
	}
	if _, open := acquireManagedResource(data); open {
		t.Fatal("State.Close retained a borrowed runtime handle")
	}
}

func TestRetainedManagedUserDataDoesNotPinClosedStateRoots(t *testing.T) {
	collected := make(chan struct{}, 1)
	retained := closeStateWithManagedResource(collected)

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(retained)
			return
		case <-deadline.C:
			t.Fatal("managed userdata pinned an unrelated closed-State root")
		case <-ticker.C:
		}
	}
}

func closeStateWithManagedResource(
	collected chan<- struct{},
) *UserData {
	state, err := New(Options{})
	if err != nil {
		panic(err)
	}
	unrelated, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		panic(err)
	}
	runtime.SetFinalizer(unrelated, func(*Table) {
		collected <- struct{}{}
	})
	if err := state.SetRawGlobal("unrelated", unrelated.Value()); err != nil {
		panic(err)
	}
	object, err := state.newManagedUserData(
		&nativeCleanupProbe{},
		cleanNativeProbe,
	)
	if err != nil {
		panic(err)
	}
	retained := object.owningHandle()
	if err := state.SetUserDataEnvironment(retained, nil); err != nil {
		panic(err)
	}
	if err := state.Close(); err != nil {
		panic(err)
	}
	return retained
}
