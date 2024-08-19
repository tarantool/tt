-- greeter.lua --
return {
    validate = function() end,
    apply = function() require('log').info("Hi from the 'greeter' role!") end,
    stop = function() end,
}
