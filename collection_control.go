package lua

// Host-side collector control.
//
// These mirror the options Lua's collectgarbage exposes, so a host can
// apply the same policy without going through a Lua chunk. Collect and
// HeapBytes cover the "collect" and "count" options.
//
// Every method requires an idle State. A NativeFunc controls the collector
// through the Frame forms while Lua is executing.

// StopGC suspends automatic collection. Explicit Collect still runs, and
// so does a collection requested by Lua's collectgarbage.
//
// Retention is unbounded while the collector is stopped. A State with
// Options.MaxHeapBytes still measures its heap at execution safe points,
// so stopping the collector can surface the limit that automatic
// collection would otherwise have avoided.
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

// GCRunning reports whether automatic collection is enabled.
func (state *State) GCRunning() (bool, error) {
	if err := state.checkOpen(); err != nil {
		return false, err
	}
	return !state.runtime.collection.stopped, nil
}

// GCPause reports the collector's pause, as a percentage of live size.
func (state *State) GCPause() (int, error) {
	if err := state.checkOpen(); err != nil {
		return 0, err
	}
	return state.runtime.collection.pause, nil
}

// SetGCPause sets the collector's pause and returns the previous value.
//
// The pause is a percentage of the live heap measured at the end of a
// cycle: the collector waits until allocation since then reaches
// pause-minus-100 percent of that size. Values at or below 100 collect as
// eagerly as the runtime's minimum debt allows.
func (state *State) SetGCPause(pause int) (int, error) {
	if err := state.checkIdle(); err != nil {
		return 0, err
	}
	control := &state.runtime.collection
	previous := control.pause
	control.pause = pause
	return previous, nil
}

// GCStepMultiplier reports the collector's step multiplier.
func (state *State) GCStepMultiplier() (int, error) {
	if err := state.checkOpen(); err != nil {
		return 0, err
	}
	return state.runtime.collection.stepMultiplier, nil
}

// SetGCStepMultiplier sets the collector's step multiplier and returns the
// previous value.
//
// Lunar's collector is synchronous: a cycle runs to completion at a safe
// point, so there is no host StepGC — Collect is a complete step. The
// multiplier is recorded for compatibility with Lua's collectgarbage and
// does not change how much work a collection performs.
func (state *State) SetGCStepMultiplier(multiplier int) (int, error) {
	if err := state.checkIdle(); err != nil {
		return 0, err
	}
	control := &state.runtime.collection
	previous := control.stepMultiplier
	control.stepMultiplier = multiplier
	return previous, nil
}

// StopGC suspends automatic collection from a native callback.
func (frame Frame) StopGC() {
	frame.activation()
	frame.thread.owner.collection.setStopped(true)
}

// RestartGC resumes automatic collection from a native callback and
// requests a cycle.
func (frame Frame) RestartGC() {
	frame.activation()
	frame.thread.owner.collection.setStopped(false)
	frame.thread.owner.collection.requestCycle()
}

// GCRunning reports whether automatic collection is enabled.
func (frame Frame) GCRunning() bool {
	frame.activation()
	return !frame.thread.owner.collection.stopped
}

// The Frame forms cover suspending collection around a latency-sensitive
// section from inside a callback. Retuning pause or step multiplier is a
// policy decision that belongs to an idle host, so those exist only on
// State; a callback that must retune defers it to when the call returns.

// checkIdle reports whether the State is open and not executing. Collector
// policy changes from a running State would race the executor's own
// scheduling, so those go through the Frame forms.
func (state *State) checkIdle() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if state.active != nil {
		return ErrRunning
	}
	return nil
}
