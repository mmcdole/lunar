local cbor = require "cbor"

local data_path = assert(LUNAR_CBOR_DATA_PATH, "LUNAR_CBOR_DATA_PATH is required")
local log = LUNAR_CBOR_LOG or function() end

LUNAR_CBOR_GRAPH = nil

local function graph_counts(graph)
    local area_count = 0
    for _ in pairs(graph.areas) do
        area_count = area_count + 1
    end

    local room_count = 0
    for _ in pairs(graph.rooms) do
        room_count = room_count + 1
    end

    local exit_count = 0
    for _, exit_list in pairs(graph.exits) do
        exit_count = exit_count + #exit_list
    end
    return area_count, room_count, exit_count
end

function loadBenchmarkGraph()
    local file, open_error = io.open(data_path, "rb")
    if not file then
        log("cannot open CBOR graph for reading: " .. tostring(open_error))
        return false
    end

    local encoded = file:read("*a")
    file:close()
    if not encoded or #encoded == 0 then
        log("CBOR graph is empty")
        return false
    end

    local success, graph = pcall(cbor.decode, encoded)
    if not success or type(graph) ~= "table" then
        log("cannot decode CBOR graph: " .. tostring(graph))
        return false
    end
    if type(graph.areas) ~= "table" or type(graph.rooms) ~= "table" or type(graph.exits) ~= "table" then
        log("decoded CBOR graph is missing required tables")
        return false
    end

    LUNAR_CBOR_GRAPH = graph
    local areas, rooms, exits = graph_counts(graph)
    log(string.format("loaded %d areas, %d rooms, and %d exits", areas, rooms, exits))
    return true
end

function saveBenchmarkGraph()
    local graph = LUNAR_CBOR_GRAPH
    if type(graph) ~= "table" then
        log("no CBOR graph is loaded")
        return false
    end

    graph.timestamp = os.time()
    local success, encoded = pcall(cbor.encode, graph)
    if not success or type(encoded) ~= "string" then
        log("cannot encode CBOR graph: " .. tostring(encoded))
        return false
    end

    local file, open_error = io.open(data_path, "wb")
    if not file then
        log("cannot open CBOR graph for writing: " .. tostring(open_error))
        return false
    end
    local wrote, write_error = file:write(encoded)
    local closed, close_error = file:close()
    if not wrote or not closed then
        log("cannot write CBOR graph: " .. tostring(write_error or close_error))
        return false
    end

    local areas, rooms, exits = graph_counts(graph)
    log(string.format("saved %d areas, %d rooms, and %d exits (%d bytes)", areas, rooms, exits, #encoded))
    return true
end
