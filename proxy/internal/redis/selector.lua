-- Selects the least-loaded available worker atomically.
-- Uses HGETALL for bulk active/lifetime fetch.
-- Filters where status == "available", active < 5, lifetime < 50, and lastHeartbeat is recent.
-- Increments active and lifetime by 1 for the selected worker.
-- Returns the selected worker UUID or nil if none available.

local max_concurrent_sessions = 5
local max_lifetime_sessions = 50

local active_hash = 'cluster:active_connections'
local lifetime_hash = 'cluster:lifetime_connections'

local time = redis.call('TIME')
local now_ms = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
local threshold = now_ms - (60 * 1000)

-- Bulk fetch all active and lifetime data
local active_data = redis.call('HGETALL', active_hash)
local lifetime_data = redis.call('HGETALL', lifetime_hash)

-- Convert to maps (UUID -> count)
local active_map = {}
local lifetime_map = {}
for i = 1, #active_data, 2 do
    local uuid = active_data[i]
    active_map[uuid] = tonumber(active_data[i+1] or 0)
end
for i = 1, #lifetime_data, 2 do
    local uuid = lifetime_data[i]
    lifetime_map[uuid] = tonumber(lifetime_data[i+1] or 0)
end

local candidates = {}
for uuid, active in pairs(active_map) do
    local worker_key = 'worker:' .. uuid
    local status = redis.call('HGET', worker_key, 'status')
    local lastHeartbeat_str = redis.call('HGET', worker_key, 'lastHeartbeat')
    
    local lifetime = lifetime_map[uuid] or 0

    local is_recent = false
    if lastHeartbeat_str then
        local heartbeat_unix = tonumber(lastHeartbeat_str)
        if heartbeat_unix and heartbeat_unix >= threshold then
            is_recent = true
        end
    end
    
    if status == 'available' and active < max_concurrent_sessions and lifetime < max_lifetime_sessions and is_recent then
        table.insert(candidates, {uuid = uuid, active = active, worker_key = worker_key})
    end
end

if #candidates == 0 then
    return nil
end

-- Sort candidates by active connections (ascending)
table.sort(candidates, function(a, b) return a.active < b.active end)

local selected = candidates[1]
local selected_uuid = selected.uuid
local selected_worker_key = selected.worker_key

-- Increment counters
redis.call('HINCRBY', active_hash, selected_uuid, 1)
redis.call('HINCRBY', lifetime_hash, selected_uuid, 1)

return selected_uuid