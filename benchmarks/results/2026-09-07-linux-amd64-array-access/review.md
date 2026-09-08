Reviewed clean shared-lookup revision `a98a15738f29dbf4684e7ee51213ab8f811673df`
against main `5fc51e449a6661340056544e995aae4fdf89dcb2` and the unsigned VM-only
variant `c013ee6fc609324345cc92358dbd435e3358b5c3`. No correctness blocker found.

The shared variant moves the read shortcut from the executor into
[table.rawSlot](sources/shared/table.go.txt#L649). This extends it to public generic
RawGet, Lua rawget, Frame.Index and delegated table-valued __index lookups.
The existing integer- and constant-string-specific readers are unchanged.
PUC 5.1 similarly dispatches numeric keys through its general
[luaH_get](sources/puc51/ltable.c#L469) into
[luaH_getnum](sources/puc51/ltable.c#L435).

- For positive integral keys within the logical array length, the returned
  `(slot, found)` is exactly the result of the original rawIntSlot path. A nil
  hole returns false. It must not search the record store after an array hole;
  the original lookup follows the same rule and array growth maintains one
  storage location per integer key. Capacity beyond logical length is excluded.
- The unsigned bound accepts only indices 1 through the current array length.
  The following float roundtrip rejects fractional and exceptional values,
  including any implementation-dependent float-to-int conversion result.
  Negative zero, zero, sparse/out-of-range integers, NaN, infinities and string
  keys retain the original normalization/fallback behavior.
- [rawGetValue](sources/shared/table.go.txt#L231) still validates its receiver and accepts
  the host key before the shortcut. Foreign/invalid owning values are rejected
  before conversion, and returned references still become owning Values at the
  same boundary. Raw reads of preserved tables after State.Close remain reads
  of the same stored data; the shortcut performs no mutation or admission.
- Lua rawget remains raw. Executor reads and
  [Frame.indexSlots](sources/shared/native_call.go.txt#L244) continue handling a missing
  field through the existing __index path. False and other non-nil values are
  present. Numeric lookups in table-valued delegation chains now use the same
  shortcut, with no change to limits, callbacks or error construction.
- The VM write shortcut is unchanged from the reviewed unsigned variant. It
  accepts only existing non-nil array fields and uses replaceResolvedSlot, so
  nil deletion, occupancy, reference writes, signed-zero representations and
  metamethod-cache invalidation retain their established behavior. Missing
  fields still enter __newindex handling. SELF operand-publication order is
  unchanged because lookup still follows operand capture.

The existing new executor tests now exercise the shared reader too. General raw
scalar/invalid/foreign-key tests, rawget tests and metamethod delegation tests
cover the wider callers. A compact public RawGet test combining an array hole
with a metatable and a retained reference result would add direct boundary
coverage, but the unchanged boundary checks and identical slot result make it
a suggestion rather than a blocker.

Performance remains an empirical question. Moving the check broadens its useful
callers, but also adds work to their non-array lookups. It may change inlining
or instruction layout in the VM. Constant-string specialized paths and
RawGetInt do not gain from this move. The earlier callback regressions cannot
be considered resolved by source inspection; the full program and embedding
gate must judge this binary independently.

No runtime edits, builds or measurements were performed as part of this review.
CBOR worker build and module validation are separate, root-authorized steps.
