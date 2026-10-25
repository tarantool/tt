-- Create something to generate xlogs.
box.schema.space.create('customers', {if_not_exists = true})
