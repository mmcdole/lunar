package lua

// separateFinalizers classifies userdata in reverse creation order. A normal
// collection considers only dead userdata after the ordinary root graph has
// drained. Close considers every remaining userdata exactly once.
//
// Lua 5.1 marks each considered userdata finalized at this point. A current
// raw __gc value, callable or not, moves it into the persistent call-order
// queue. The handler itself is deliberately not captured: Lua looks it up
// again immediately before the call.
func (state *State) separateFinalizers(all bool) {
	ledger := &state.objects
	ledger.compactFinalizerQueue()
	for index := len(ledger.userData) - 1; index >= 0; index-- {
		data := ledger.userData[index]
		if data.flags&userDataFinalized != 0 ||
			!all && data.gcMark == objectMarked {
			continue
		}
		data.flags |= userDataFinalized
		if _, found := metatableEventSlot(data.metatable, metaGC); found {
			ledger.appendFinalizer(data)
		}
	}
}

func (state *State) runPendingFinalizers(
	frame *Frame,
	automaticThread *threadObject,
) *Error {
	if frame != nil && automaticThread != nil {
		panic("lua: ambiguous finalizer execution seam")
	}
	if automaticThread != nil &&
		(automaticThread.state != state ||
			automaticThread.owner != state.runtime) {
		panic("lua: finalizer thread belongs to another State")
	}
	control := &state.runtime.collection
	previousServicing := control.servicing
	control.setServicing(true)
	defer func() {
		control.setServicing(previousServicing)
	}()
	for {
		handler, argument, pending, invoke :=
			state.nextPendingFinalizer()
		if !pending {
			return nil
		}
		if !invoke {
			continue
		}
		arguments := [1]slot{argument}
		var failure *Error
		if frame != nil {
			failure = frame.callCompactNone(handler, arguments[:])
		} else if automaticThread != nil {
			failure = callAutomaticFinalizer(
				automaticThread,
				handler,
				argument,
			)
		} else {
			failure = state.callMainCompactNone(handler, arguments[:])
		}
		if failure != nil {
			return failure
		}
		// GCTM saves and restores the outer collection threshold around
		// every successful __gc call. Preserve allocation debt and pause
		// changes, but discard stop/restart requests made by that handler.
		control.restoreAfterFinalizer()
	}
}

// callAutomaticFinalizer re-enters the one executor on its current Thread.
// The temporary continuation is only a non-yielding and unwind guard:
// driveExecution stops before it can resume the record, and the checkpoint
// removes it after the call. Automatic collection is a cold synchronous seam,
// not a second execution path.
func callAutomaticFinalizer(
	thread *threadObject,
	handler slot,
	argument slot,
) (failure *Error) {
	checkpoint := captureThreadExecutionCheckpoint(thread)
	restored := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
	}()

	arguments := [1]slot{argument}
	_, failure = startStagedCall(
		thread,
		handler,
		callArguments{compact: arguments[:]},
		0,
	)
	if failure == nil {
		if len(thread.continuations) != checkpoint.continuationDepth {
			panic("lua: finalizer call created an unexpected continuation")
		}
		thread.pushExecutionContinuation(executionContinuation{
			frameDepth:  uint32(checkpoint.frameDepth),
			scratchBase: uint32(checkpoint.liveExtent),
			savedTop:    uint32(checkpoint.top),
			savedExtent: uint32(checkpoint.frameExtent),
			mode:        continuationFinalizerGuard,
		})

		result := driveExecution(thread, checkpoint.frameDepth)
		switch result.kind {
		case executionReturned:
			if len(thread.frames) != checkpoint.frameDepth ||
				len(thread.continuations) !=
					checkpoint.continuationDepth+1 ||
				thread.continuations[len(thread.continuations)-1].mode !=
					continuationFinalizerGuard {
				panic("lua: finalizer returned invalid execution state")
			}
			checkpoint.restore(thread, true)
			restored = true
			return nil
		case executionFailed:
			if result.err == nil {
				panic("lua: finalizer failed without an error")
			}
			failure = result.err
			snapshotExecutionFailure(
				thread,
				checkpoint.frameDepth,
				failure,
			)
		case executionYielded:
			panic("lua: finalizer escaped its non-yielding call guard")
		default:
			panic("lua: finalizer produced an invalid execution result")
		}
	}
	checkpoint.restore(thread, true)
	restored = true
	return failure
}

func (state *State) nextPendingFinalizer() (
	handler slot,
	argument slot,
	pending bool,
	invoke bool,
) {
	data, found := state.objects.takeFinalizer()
	if !found {
		return nilSlot, nilSlot, false, false
	}
	if data == nil ||
		data.owner != state.runtime ||
		data.flags&userDataFinalized == 0 {
		panic("lua: invalid dequeued finalizer")
	}
	handler, found = metatableEventSlot(data.metatable, metaGC)
	if !found {
		return nilSlot, nilSlot, true, false
	}
	return handler, slotFromUserDataObject(data), true, true
}

// finalizeForClose separates every remaining userdata once and invokes every
// queued handler while the State is still fully usable. Lua failures are
// ignored as required by lua_close. A panic from host code is remembered,
// cleanup continues deterministically, and Close re-panics after teardown.
func (state *State) finalizeForClose() (any, bool) {
	control := &state.runtime.collection
	previousServicing := control.servicing
	control.setServicing(true)
	defer func() {
		control.setServicing(previousServicing)
	}()

	state.separateFinalizers(true)
	var firstPanic any
	panicked := false
	for {
		handler, argument, pending, invoke :=
			state.nextPendingFinalizer()
		if !pending {
			return firstPanic, panicked
		}
		if !invoke {
			continue
		}
		func() {
			completed := false
			defer func() {
				if completed {
					return
				}
				current := recover()
				if !panicked {
					firstPanic = current
					panicked = true
				}
			}()
			arguments := [1]slot{argument}
			_ = state.callMainCompactNone(handler, arguments[:])
			completed = true
		}()
	}
}

func (ledger *objectLedger) appendFinalizer(data *userDataObject) {
	if data == nil {
		panic("lua: cannot queue a nil finalizer")
	}
	ledger.finalizers = appendObjectVector(ledger.finalizers, data)
}

func (ledger *objectLedger) compactFinalizerQueue() {
	if ledger.finalizerHead == 0 {
		return
	}
	remaining := len(ledger.finalizers) - ledger.finalizerHead
	if remaining != 0 {
		copy(
			ledger.finalizers[:remaining],
			ledger.finalizers[ledger.finalizerHead:],
		)
	}
	clear(ledger.finalizers[remaining:])
	ledger.finalizers = ledger.finalizers[:remaining]
	ledger.finalizerHead = 0
	if remaining == 0 &&
		cap(ledger.finalizers) > maximumRetainedCollectionWork {
		ledger.finalizers = nil
	}
}

func (ledger *objectLedger) takeFinalizer() (*userDataObject, bool) {
	if ledger.finalizerHead == len(ledger.finalizers) {
		ledger.compactFinalizerQueue()
		return nil, false
	}
	data := ledger.finalizers[ledger.finalizerHead]
	ledger.finalizers[ledger.finalizerHead] = nil
	ledger.finalizerHead++
	if ledger.finalizerHead == len(ledger.finalizers) {
		ledger.compactFinalizerQueue()
	}
	return data, true
}
