-- Do not set listen for now so connector won't be
-- able to send requests until everything is configured.

box.cfg{
    work_dir = os.getenv("TEST_TNT_WORK_DIR"),
}

box.once("init", function()
    box.schema.user.create('test', {password = 'password'})
    box.schema.user.grant('test', 'execute', 'universe')
end)

require("console").listen("unix/:./console.control")
-- Set listen only when every other thing is configured.
box.cfg{
    listen = os.getenv("TEST_TNT_LISTEN"),
}
