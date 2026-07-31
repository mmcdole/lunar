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
