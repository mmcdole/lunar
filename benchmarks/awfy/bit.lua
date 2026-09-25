-- Pure-Lua 5.1 stand-in for the 'bit' module (bit32-style unsigned results).
-- Used identically for PUC Lua 5.1 and Lunar so both run the same code.
local floor = math.floor
local M = 4294967296

local function bitop(a, b, keep)
  a, b = a % M, b % M
  local result, place = 0, 1
  while a > 0 or b > 0 do
    local x, y = a % 2, b % 2
    if keep(x, y) then result = result + place end
    a, b, place = floor(a / 2), floor(b / 2), place * 2
  end
  return result
end

local function both(x, y) return x == 1 and y == 1 end
local function either(x, y) return x == 1 or y == 1 end
local function differ(x, y) return x ~= y end

return {
  band = function(a, b) return bitop(a, b, both) end,
  bor = function(a, b) return bitop(a, b, either) end,
  bxor = function(a, b) return bitop(a, b, differ) end,
  lshift = function(a, n) return (a * 2 ^ n) % M end,
  rshift = function(a, n) return floor((a % M) / 2 ^ n) end,
}
