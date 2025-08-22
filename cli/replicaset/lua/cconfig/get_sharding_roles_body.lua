local sharding = require('config'):get().sharding
if sharding == nil or sharding.roles == nil then
    error("sharding roles are not configured, please make sure managed cluster is sharded")
end
return sharding.roles
