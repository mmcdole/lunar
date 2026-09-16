package lua

// Host-side collector control.
//
// A host that must keep a latency-sensitive section free of collection
// suspends the collector around it. Everything else about collection policy
// is either construction configuration (Options.MaxHeapBytes) or Lua's own
// collectgarbage. Both methods require an idle State.

// StopGC suspends automatic collection. Explicit Collect still runs, and
// so does a collection requested by Lua's collectgarbage.
//
// Uncollected objects remain retained while the collector is stopped. A
// State with Options.MaxHeapBytes still measures its heap at scheduled
// execution safe points without collecting or running finalizers, so
// stopping the collector can surface the limit that automatic collection
// would otherwise have avoided.
func (state *State) StopGC() error {
	if err := state.checkIdle(); err != nil {
		return err
	}
	state.runtime.collection.setStopped(true)
	return nil
}

// RestartGC resumes automatic collection and requests a cycle, which the
// runtime services at the next execution safe point.
func (state *State) RestartGC() error {
	if err := state.checkIdle(); err != nil {
		return err
	}
	state.runtime.collection.setStopped(false)
	state.runtime.collection.requestCycle()
	return nil
}

// checkIdle reports whether the State is open and not executing. Collector
// policy changes from a running State would race the executor's own
// scheduling.
func (state *State) checkIdle() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if state.active != nil {
		return ErrRunning
	}
	return nil
}

const (
	defaultCollectionPause                = 200
	defaultCollectionStepMultiplier       = 200
	minimumAutomaticCollectionDebt        = 256 << 10
	minimumAttributedStringCompactionPeak = 256
)

// collectionControl is scheduling policy, kept separate from the object
// ledger and its transient mark/sweep state. A stopped collector still
// accepts explicit collection and services scheduled heap-limit checks.
type collectionControl struct {
	pause          int
	stepMultiplier int
	debt           uint64
	budget         uint64
	baseline       uint64
	stopped        bool
	requested      bool
	servicing      bool
	runnable       bool

	// heapLimit is Options.MaxHeapBytes; zero leaves the heap unlimited.
	// baseline plus debt is the running estimate charge compares against,
	// which overstates the heap by whatever has died since the last
	// collection. Crossing it schedules a cycle or, while stopped, a heap
	// measurement. The safe point enforces the measured baseline.
	heapLimit uint64

	// attributedStrings records long string backing admitted and charged to
	// this State. A completed semantic scan removes entries not retained by
	// the Lua graph, making this a swept attribution set rather than a
	// permanent string store.
	attributedStrings         map[stringRef]struct{}
	attributedStringHighWater int
}

func defaultCollectionControl() collectionControl {
	return collectionControl{
		pause:          defaultCollectionPause,
		stepMultiplier: defaultCollectionStepMultiplier,
		budget:         minimumAutomaticCollectionDebt,
	}
}

// charge records retained Lua-heap growth. It is called only at allocation
// and capacity-growth seams, never for in-capacity replacement or deletion.
func (control *collectionControl) charge(bytes uint64) {
	if control == nil || bytes == 0 {
		return
	}
	if bytes > ^uint64(0)-control.debt {
		control.debt = ^uint64(0)
	} else {
		control.debt += bytes
	}
	if control.budget == 0 {
		control.budget = minimumAutomaticCollectionDebt
	}
	if control.requested {
		return
	}
	if control.debt >= control.budget {
		control.requestCycle()
		return
	}
	// A limited State schedules collection ahead of the ordinary debt
	// budget so it measures its heap promptly instead of waiting for the
	// heap to double. An unlimited one pays one compare against zero.
	if control.heapLimit == 0 {
		return
	}
	// A measured heap already over the limit has an enforcement raise
	// pending; schedule it without waiting for debt.
	if control.baseline > control.heapLimit {
		control.requestCycle()
		return
	}
	// Under the limit, the estimate overstates the heap by whatever has
	// died since the last cycle, so crossing it does not mean retention
	// crossed it. Requiring the collector's minimum debt before another
	// limit-triggered cycle keeps a State parked just under its limit
	// from running a full collection every few kilobytes of churn.
	if control.debt >= minimumAutomaticCollectionDebt &&
		control.baseline+control.debt > control.heapLimit {
		control.requestCycle()
	}
}

func (control *collectionControl) refreshRunnable() {
	control.runnable =
		control.requested &&
			!control.servicing &&
			(!control.stopped || control.heapLimit != 0)
}

func (control *collectionControl) requestCycle() {
	control.requested = true
	control.refreshRunnable()
}

func (control *collectionControl) setStopped(stopped bool) {
	control.stopped = stopped
	control.refreshRunnable()
}

func (control *collectionControl) setServicing(servicing bool) {
	control.servicing = servicing
	control.refreshRunnable()
}

func (control *collectionControl) attributeString(value stringRef) {
	bytes := stringRefRetainedBytes(value)
	if bytes == 0 {
		return
	}
	if control.attributedStrings == nil {
		control.attributedStrings = make(map[stringRef]struct{})
	}
	if _, found := control.attributedStrings[value]; found {
		return
	}
	control.attributedStrings[value] = struct{}{}
	if size := len(control.attributedStrings); size >
		control.attributedStringHighWater {
		control.attributedStringHighWater = size
	}
	control.charge(bytes)
}

func (control *collectionControl) sweepAttributedStrings(
	ledger *objectLedger,
) {
	if control == nil || len(control.attributedStrings) == 0 {
		return
	}
	if size := len(control.attributedStrings); size >
		control.attributedStringHighWater {
		control.attributedStringHighWater = size
	}
	for value := range control.attributedStrings {
		if ledger.retainsString(value) {
			continue
		}
		delete(control.attributedStrings, value)
	}
	if len(control.attributedStrings) == 0 {
		control.attributedStrings = nil
		control.attributedStringHighWater = 0
		return
	}
	if control.attributedStringHighWater <
		minimumAttributedStringCompactionPeak ||
		len(control.attributedStrings) >=
			control.attributedStringHighWater/4 {
		return
	}
	compacted := make(
		map[stringRef]struct{},
		len(control.attributedStrings),
	)
	for value := range control.attributedStrings {
		compacted[value] = struct{}{}
	}
	control.attributedStrings = compacted
	control.attributedStringHighWater = len(compacted)
}

func (control *collectionControl) release() {
	if control == nil {
		return
	}
	*control = collectionControl{}
}

func (control *collectionControl) chargeCapacityGrowth(
	previous int,
	current int,
	elementBytes uint64,
) {
	if current <= previous || elementBytes == 0 {
		return
	}
	count := uint64(current - previous)
	if count > ^uint64(0)/elementBytes {
		control.charge(^uint64(0))
		return
	}
	control.charge(count * elementBytes)
}

func (control *collectionControl) resetDebt(liveBytes uint64) {
	control.debt = 0
	control.baseline = liveBytes
	control.budget = automaticCollectionBudget(
		liveBytes,
		control.pause,
	)
	control.requested = false
	control.refreshRunnable()
}

func (control *collectionControl) restoreAfterFinalizer() {
	control.stopped = false
	control.budget = automaticCollectionBudget(
		control.baseline,
		control.pause,
	)
	control.requested = control.debt >= control.budget
	control.refreshRunnable()
}

func automaticCollectionBudget(liveBytes uint64, pause int) uint64 {
	if pause <= 100 || liveBytes == 0 {
		return minimumAutomaticCollectionDebt
	}
	scale := uint64(pause - 100)
	if liveBytes > ^uint64(0)/scale {
		return ^uint64(0)
	}
	budget := liveBytes * scale / 100
	if budget < minimumAutomaticCollectionDebt {
		return minimumAutomaticCollectionDebt
	}
	return budget
}

// serviceAutomaticCollection runs only after the executor has published a
// complete root entry, operation, or call result. A stopped collector only
// measures the heap to enforce its limit. Allocation paths merely charge
// debt. Finalizers reuse the active Thread and executor; automatic
// re-entry is suppressed while they run, while an explicit nested collection
// remains legal.
func serviceAutomaticCollection(thread *threadObject) (failure *Error) {
	if thread == nil ||
		thread.owner == nil ||
		!thread.owner.collection.runnable {
		return nil
	}
	return runAutomaticCollection(thread)
}

// runAutomaticCollection is the cold half of the safe-point check. Keeping
// the one-branch wrapper small lets it inline at call and return seams.
func runAutomaticCollection(thread *threadObject) (failure *Error) {
	if thread.state == nil ||
		thread.owner == nil ||
		!thread.owner.collection.runnable {
		panic("lua: invalid automatic collection request")
	}
	state := thread.state
	if state.active != thread ||
		thread.status != ThreadRunning ||
		state.objects.phase != collectionIdle {
		panic("lua: automatic collection outside an execution safe point")
	}

	control := &thread.owner.collection
	control.setServicing(true)
	defer func() {
		control.setServicing(false)
	}()

	if !control.stopped {
		state.collectUnreachable()
	}
	state.resetCollectionDebt()
	// Enforce the measured heap rather than the allocation estimate. While
	// stopped, uncollected objects remain in that measurement.
	if limit := thread.effectiveHeapLimit(); limit != 0 &&
		control.baseline > limit {
		return newHeapLimitError()
	}
	if control.stopped {
		return nil
	}
	failure = state.runPendingFinalizers(nil, thread)
	return failure
}

// effectiveHeapLimit is the configured heap limit, widened while an xpcall
// error handler runs so the handler can allocate its report. This mirrors
// the bounded emergency capacity MaxValues and MaxFrames grant handlers:
// without it, a handler that builds even a format string over the limit
// dies, and Lua reports "error in error handling" instead of the failure.
func (thread *threadObject) effectiveHeapLimit() uint64 {
	limit := thread.owner.collection.heapLimit
	if limit == 0 || thread.errorHandlerDepth == 0 {
		return limit
	}
	reserve := limit / 8
	if reserve < protectedHeapReserve {
		reserve = protectedHeapReserve
	}
	if limit > ^uint64(0)-reserve {
		return ^uint64(0)
	}
	return limit + reserve
}
