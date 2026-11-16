---@meta
-- The module luatest as EmmyLua is to see it, written by tt emmylua with
-- the configuration it gives EmmyLua, in place of luatest/init.lua of the
-- rock, which the configuration leaves out.
--
-- luatest/init.lua makes the module setmetatable({}, {__index =
-- require('luatest.assertions')}) and defines before_suite and after_suite
-- at run time: EmmyLua 0.30 does not follow that __index, and no release
-- sees those hooks, so every t.assert_equals(...) of a test was an undefined
-- field. Here the module is the assertions module itself, read from the
-- rock as it is, with the fields luatest/init.lua sets on it, those of
-- luatest 1.x, so the assertions are always those of the luatest
-- installed. luatest is MIT-licensed; this file describes its interface
-- only.

--- A group of tests (luatest.group): a test is a function at a key that
--- starts with test, the hooks run around them. It is a table, so the
--- tests and what they keep in the group, a server say, are any fields; the
--- group a callback gets is left untyped for the same reason.
---@class luatest.Group: table
---@field name string the name of the group
local Group = {}

--- Add a callback to run once before all the tests of the group.
---@param fn fun(cg)
---@overload fun(params: table, fn: fun(cg))
function Group.before_all(fn) end

--- Add a callback to run once after all the tests of the group.
---@param fn fun(cg)
---@overload fun(params: table, fn: fun(cg))
function Group.after_all(fn) end

--- Add a callback to run before each test of the group.
---@param fn fun(cg)
---@overload fun(params: table, fn: fun(cg))
function Group.before_each(fn) end

--- Add a callback to run after each test of the group.
---@param fn fun(cg)
---@overload fun(params: table, fn: fun(cg))
function Group.after_each(fn) end

--- Add a callback to run before the test named test_name.
---@param test_name string
---@param fn fun(cg)
---@overload fun(test_name: string, params: table, fn: fun(cg))
function Group.before_test(test_name, fn) end

--- Add a callback to run after the test named test_name.
---@param test_name string
---@param fn fun(cg)
---@overload fun(test_name: string, params: table, fn: fun(cg))
function Group.after_test(test_name, fn) end

local luatest = require('luatest.assertions')

luatest.Process = require('luatest.process')
luatest.VERSION = require('luatest.VERSION')
luatest.replica_proxy = require('luatest.replica_proxy')
luatest.tarantool = require('luatest.tarantool')
luatest.helpers = require('luatest.helpers')
luatest.Server = require('luatest.server')
luatest.ReplicaSet = require('luatest.replica_set')
luatest.log = require('luatest.log')
luatest.justrun = require('luatest.justrun')
luatest.cbuilder = require('luatest.cbuilder')
luatest.cluster = require('luatest.cluster')
luatest.hooks = require('luatest.hooks')

---@type table<string, luatest.Group>
luatest.groups = {}

--- Add a callback to run once before all the tests.
---@param fn fun()
---@overload fun(params: table, fn: fun())
function luatest.before_suite(fn) end

--- Add a callback to run once after all the tests.
---@param fn fun()
---@overload fun(params: table, fn: fun())
function luatest.after_suite(fn) end

--- Create a group of tests, named after the file by default, and with
--- params one group for each set of parameters.
---@param name? string
---@param params? table[]
---@return luatest.Group
function luatest.group(name, params) end

--- Update the default options of the runner and return them.
---@param options? table
---@return table
function luatest.configure(options) end

---@deprecated use luatest.configure
---@param options? table
---@return table
function luatest.defaults(options) end

return luatest
