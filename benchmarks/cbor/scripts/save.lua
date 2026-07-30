local fixture = assert(os.getenv("LUNAR_CBOR_FIXTURE"))

package.path = fixture .. "/?.lua;" .. package.path
LUNAR_CBOR_DATA_PATH = fixture .. "/data/graph.cbor"
LUNAR_CBOR_LOG = function() end

dofile(fixture .. "/workload.lua")
assert(loadBenchmarkGraph())
collectgarbage("collect")
collectgarbage("collect")

local started = os.clock()
assert(saveBenchmarkGraph())
local elapsed = os.clock() - started

io.write(string.format(
    "CBOR_LUA_SAVE cpu_seconds=%.6f heap_kib=%.3f\n",
    elapsed,
    collectgarbage("count")
))
