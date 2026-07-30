local fixture = assert(os.getenv("LUNAR_CBOR_FIXTURE"))

package.path = fixture .. "/?.lua;" .. package.path
LUNAR_CBOR_DATA_PATH = fixture .. "/data/graph.cbor"
LUNAR_CBOR_LOG = function() end

local function heap_kib()
    collectgarbage("collect")
    collectgarbage("collect")
    return collectgarbage("count")
end

dofile(fixture .. "/workload.lua")
local before = heap_kib()
local started = os.clock()
assert(loadBenchmarkGraph())
local elapsed = os.clock() - started
local after = heap_kib()

io.write(string.format(
    "CBOR_LUA_LOAD cpu_seconds=%.6f heap_kib=%.3f graph_delta_kib=%.3f\n",
    elapsed,
    after,
    after - before
))
