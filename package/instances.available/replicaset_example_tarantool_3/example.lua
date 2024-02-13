box.once("schema", function()
    box.schema.space.create("example")
    box.space.example:create_index("primary")
end)
