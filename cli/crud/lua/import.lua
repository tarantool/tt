-- This script implements logic on the router side for working with crud as part of the crud-import task.
-- For communication between the code on golang and lua, evals are initialized in the session storage,
-- which will be called from golang via tt connect.


local box  = require('box')
local crud = require('crud')
-- NOTE: crud issue #293, we don't need ddl module now, but in the future it may be.
-- local ddl  = require('ddl')
local json = require('json')
local decimal = require('decimal')
local ffi = require('ffi')

local crud_evals_init_complete = false
box.session.storage.batch_insert_res = nil
box.session.storage.batch_insert_err = nil
box.session.storage.null_val_interpretation = nil

-- Map string to boolean if possible, else return nil.
local toboolean = function(value)
    local map = {
        ['true']  = true,
        ['false'] = false,
        ['t']  = true,
        ['f']  = false
    }
    if map[value] ~= nil then
        return map[value]
    end

    return nil
end

-- Set crud.insert_many as crud stored procedure for import.
local init_eval_set_batch_insert_stored_procedure = function()
    box.session.storage.crudimport_batch_stored_procedure = crud.insert_many
end

-- Set crud.replace_many as crud stored procedure for import.
local init_eval_set_batch_replace_stored_procedure = function()
    box.session.storage.crudimport_batch_stored_procedure = crud.replace_many
end

-- Set one of available crud stored procedure for import.
local init_eval_set_stored_procedure = function()
    box.session.storage.crudimport_set_stored_procedure = function(name)
        if name == 'insert' then
            init_eval_set_batch_insert_stored_procedure()
            if type(box.session.storage.crudimport_batch_stored_procedure) ~= "function" then
                return "Function crud.insert_many in not found."
            end
            return true
        end
        if name == 'replace' then
            init_eval_set_batch_replace_stored_procedure()
            if type(box.session.storage.crudimport_batch_stored_procedure) ~= "function" then
                return "Function crud.replace_many in not found."
            end
            return true
        end

        return "Unknown crud stored procedure, check operation option."
    end
end

-- Set target space for import.
local init_eval_set_targetspace = function()
    box.session.storage.crudimport_set_targetspace = function(space)
        box.session.storage.crudimport_targetspace = space
    end
end

-- Check existance of target space for import.
local init_eval_check_targetspace_exist = function()
    box.session.storage.crudimport_check_targetspace_exist = function()
        local targetspace_format =
            crud.select(box.session.storage.crudimport_targetspace, nil, {first = 0})
        if targetspace_format ~= nil then
            box.session.storage.crudimport_targetspace_format = targetspace_format['metadata']
            return true
        end

        return false
    end
end

-- Sets value to be interpreted as NULL when importing.
local init_eval_set_null_interpretation = function()
    box.session.storage.crudimport_set_null_interpretation = function(null_val)
        if tostring(null_val) ~= nil then
            box.session.storage.null_val_interpretation = tostring(null_val)
            return true
        end

        return false
    end
end

-- Uploads the batch as serialized json.
-- Also sets null values. By default the value string('') in fields from the parser is perceived as null for import.
local init_eval_upload_batch_from_parser = function()
    box.session.storage.crudimport_upload_batch_from_parser = function(json_batch_bin)
        local try_decode_json_batch_to_table = function(jsonstr)
            box.session.storage.crudimport_bin_batch_from_parser = json_batch_bin
            box.session.storage.crudimport_batch_table_for_import = json.decode(jsonstr)
            for tuple_num, tuple in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
                if tuple['parserCtx']['parsedCsvRecord'] == nil then
                    -- Case of batch with tupleAmount < batchSize.
                    tuple['parserCtx']['parsedCsvRecord'] = {}
                end
                -- Logic of NULL value set.
                for key, val in pairs(tuple['parserCtx']['parsedCsvRecord']) do
                    if val == '' and box.session.storage.null_val_interpretation == nil then
                        box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][tuple_num]['parserCtx']['parsedCsvRecord'][key] = json.NULL
                    elseif val == box.session.storage.null_val_interpretation and
                            box.session.storage.null_val_interpretation ~= nil then
                                box.session.storage.crudimport_batch_table_for_import
                                    ['tuples'][tuple_num]['parserCtx']['parsedCsvRecord'][key] = json.NULL
                    end
                end
            end
        end
        local decode_err_handler = function (err)
            return err
        end
        xpcall(try_decode_json_batch_to_table(json_batch_bin), decode_err_handler)

        return true
    end
end

-- Allows to get the current state of the batch at router.
local init_eval_get_prepared_batch_table = function()
    box.session.storage.crudimport_get_prepared_batch_table = function ()
        return box.session.storage.crudimport_batch_table_for_import
    end
end

-- Performs permutations for the match option.
local init_eval_swap_according_to_header = function()
    box.session.storage.crud_import_swap_according_to_header = function ()
        -- Actions to prevent indexing of null values.
        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'] = {}
            -- Case for batch with tupels amount < batchSize.
            if box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'] == nil then
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'] = {}
                end
        end

        local function get_permutation_match(header_t, format_field_name)
            for key, val in pairs(header_t) do
                if val == format_field_name then
                    return key
                end
            end

            return json.NULL
        end

        local permutation_match_map = {}
        for format_field_num, format_field_val in pairs(box.session.storage.crudimport_targetspace_format) do
            permutation_match_map[format_field_num] = get_permutation_match(
                box.session.storage.crudimport_batch_table_for_import['header'],
                format_field_val['name']
            )
        end


        -- These steps are performed for all tuples in the batch:
        -- 1 step:
        --     candidateTule -- is a candidate tuple for import.
        --     create candidateTule = {NULL, ... , NULL}
        --     |candidateTule| = |targetSpace|
        -- 2 step:
        --     candidateTule[i] = parsedTuple[ F[i] ]
        --         (parsedTuple took from uploaded butch.)
        --     where F : spaceFieldName -> positionIndexInHeader (get_permutation_match() function implements this.)
        --         (If there is no such spaceFieldName in header, then F matches NULL.)
        -- 3 step:
        --     parsedTuple = candidateTule

        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            if box.session.storage.crudimport_batch_table_for_import
                    ['tuples'][parsed_tuple_num]['parserCtx']['parsedSuccess'] ~= true then
                        goto skip_tuple_mapping
            end

            local candidate_tule = {}
            for i = 1, #box.session.storage.crudimport_targetspace_format do
                candidate_tule[i] = json.NULL
            end
            for i = 1, #box.session.storage.crudimport_targetspace_format do
                if box.session.storage.crudimport_batch_table_for_import
                    ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][permutation_match_map[i]] ~= nil then
                    if permutation_match_map[i] ~= json.NULL then
                        candidate_tule[i] = box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][permutation_match_map[i]]
                    else
                        candidate_tule[i] = json.NULL
                    end
                end
            end

            box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'] = candidate_tule

            ::skip_tuple_mapping::
        end

        return true
    end
end


-- Tries to convert fields in tuples to the space format.
-- Initially, all fields come as strings to router.
-- If the conversion fails, then finally will be an attempt to insert a string.
local init_eval_cast_tuples_to_scapce_format = function()
    box.session.storage.crudimport_cast_tuples_to_scapce_format = function ()
        -- Actions to prevent indexing of null values.
        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'] = {}
            -- Case for batch with tupels amount < batchSize.
            if box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'] == nil then
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'] = {}
                end
        end

        for parsed_tuple_num, parsed_tuple_val in
                pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            for parsedTupleFIeldNum, parsedTupleFIeldVal in pairs(parsed_tuple_val['parserCtx']['parsedCsvRecord']) do
                if box.session.storage.crudimport_batch_table_for_import
                    ['tuples'][parsed_tuple_num]['parserCtx']['parsedSuccess'] == true then
                        box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'][parsedTupleFIeldNum]
                                = parsedTupleFIeldVal
                end
            end
        end

        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            if box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['parserCtx']['parsedSuccess'] ~= true then
                    goto skip_tuple
                end
                for format_record_number, format_record in pairs(box.session.storage.crudimport_targetspace_format) do
                    -- Try cast to number if field's format require number.
                    if (
                        format_record['type'] == 'number' or
                        format_record['type'] == 'unsigned' or
                        format_record['type'] == 'integer'
                    )
                      and
                        type(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                                == 'string' then
                        -- TODO: write separate function for th-sep.
                        local assump_number = box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number]
                        assump_number = assump_number:gsub(' ', '')
                        assump_number = assump_number:gsub('`', '')
                        if tonumber(assump_number) ~= nil then
                            box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'][format_record_number]
                                    = tonumber(assump_number)
                        end
                    end
                    -- Try cast to double if field's format require double.
                    -- NOTE: crud issue #398, now this type is unstable.
                    if (
                        format_record['type'] == 'double'
                    )
                      and
                        type(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                                == 'string' then
                        -- TODO: write separate function for th-sep.
                        local assump_number = tostring(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                        assump_number = assump_number:gsub(' ', '')
                        assump_number = assump_number:gsub('`', '')
                        if pcall(ffi.cast, 'double', tonumber(assump_number)) ~= false then
                            box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'][format_record_number] =
                                                                            ffi.cast('double', tonumber(assump_number))
                        end
                    end
                    -- Try cast to decimal if field's format require double.
                    if (
                        format_record['type'] == 'decimal'
                    )
                      and
                        type(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                                == 'string' then
                        -- TODO: write separate function for th-sep.
                        local assump_number = tostring(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                        assump_number = assump_number:gsub(' ', '')
                        assump_number = assump_number:gsub('`', '')
                        if pcall(decimal.new, assump_number) ~= false then
                            box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'][format_record_number] =
                                                                                        decimal.new(assump_number)
                        end
                    end
                    -- Try cast to boolean if field's format require boolean.
                    if (
                        format_record['type'] == 'boolean'
                    )
                      and
                        toboolean(box.session.storage.crudimport_batch_table_for_import
                            ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number]) ~= nil
                      then
                        box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple'][format_record_number] =
                            toboolean(box.session.storage.crudimport_batch_table_for_import
                                ['tuples'][parsed_tuple_num]['parserCtx']['parsedCsvRecord'][format_record_number])
                    end
                end
            ::skip_tuple::
        end

        return true
    end
end

-- Imports a prepared batch using a crud stored procedure.
local init_eval_import_prepared_batch = function()
    box.session.storage.crudimport_import_prepared_batch = function ()
        local tupels_for_import = {}
        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            if box.session.storage.crudimport_batch_table_for_import
                ['tuples'][parsed_tuple_num]['parserCtx']['parsedSuccess'] == true then
                    table.insert(
                    tupels_for_import,
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple']
                    )
            end
        end
        -- NOTE: it require pcall now, see issue #300.
        local crud_ok
        crud_ok, box.session.storage.batch_insert_res, box.session.storage.batch_insert_err = pcall(
            box.session.storage.crudimport_batch_stored_procedure,
                box.session.storage.crudimport_targetspace,
                tupels_for_import
        )
        if not crud_ok then
            box.session.storage.batch_insert_err = {}
            for tuple_num = 1, box.session.storage.crudimport_batch_table_for_import['tuplesAmount'] do
                box.session.storage.batch_insert_err[tuple_num] = {}
                box.session.storage.batch_insert_err[tuple_num]['str'] = box.session.storage.batch_insert_res
                box.session.storage.batch_insert_err[tuple_num]['tuple'] = tupels_for_import[tuple_num]
            end
            box.session.storage.batch_insert_res = nil
        end
        -- NOTE: use it after done crud issue #300.
        -- box.session.storage.batch_insert_res, box.session.storage.batch_insert_err =
        --     box.session.storage.crudimport_batch_stored_procedure(
        --         box.session.storage.crudimport_targetspace,
        --         tupels_for_import
        -- )
    end
end


-- Due to the current design of crud batching, the equal tuples are indistinguishable
-- in the variables RES/ERR and uploaded tuples at router.
-- It is a temporary solution with this function,
-- that using a matching search in RES/ERR variables and uploaded tuples.
-- As the matches are found, the values from the RES/ERR are crossed out.
-- As a result, we won't lose any tuple, but error messages, for exaple, can be mistakenly swapped between tuples!
local init_eval_is_tuples_equal = function()
    box.session.storage.crud_import_is_tuples_equal = function (tuple1, tuple2)
        if tuple1 == nil or tuple2 == nil then
            return false
        end
        if #tuple1 ~= #tuple2 then
            return false
        end
        for tuple_index, _ in pairs(tuple1) do
            if tuple1[tuple_index] ~= tuple2[tuple_index] then
                return false
            end
        end
        return true
    end
end

-- Allows to get the batch with updated context after import.
local init_eval_get_batch_final_ctx = function()
    box.session.storage.crud_import_get_batch_final_ctx = function ()
        -- Case of empty tables.
        if box.session.storage.batch_insert_res == nil then
            box.session.storage.batch_insert_res = {}
            box.session.storage.batch_insert_res['rows'] = {}
        end
        if box.session.storage.batch_insert_err == nil then
            box.session.storage.batch_insert_err = {}
        end

        local batch_final_ctx = box.session.storage.crudimport_batch_table_for_import
        for parsed_tuple_num, _ in pairs(box.session.storage.crudimport_batch_table_for_import['tuples']) do
            for imported_tuple_num, imported_tuple_val in pairs(box.session.storage.batch_insert_res['rows']) do
                if box.session.storage.crud_import_is_tuples_equal(
                    imported_tuple_val,
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple']
                    ) and
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['crudCtx']['imported'] == false
                    then
                        batch_final_ctx['tuples'][parsed_tuple_num]['crudCtx']['imported'] = true
                        -- Try to resist ambiguity, but a swap is still possible (at least we don't lose record).
                        box.session.storage.batch_insert_res['rows'][imported_tuple_num] = nil
                        goto resulted_tuple_found
                end
            end
            for error_tuple_num, error_tuple_val in pairs(box.session.storage.batch_insert_err) do
                if box.session.storage.crud_import_is_tuples_equal(
                    error_tuple_val['tuple'],
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['crudCtx']['castedTuple']
                    ) and
                    box.session.storage.crudimport_batch_table_for_import
                        ['tuples'][parsed_tuple_num]['crudCtx']['imported'] == false
                    then
                        batch_final_ctx['tuples'][parsed_tuple_num]['crudCtx']['err'] = error_tuple_val['str']
                        -- Try to resist ambiguity, but a swap is still possible (at least we don't lose record).
                        box.session.storage.batch_insert_res['rows'][error_tuple_num] = nil
                        goto resulted_tuple_found
                end
            end
            ::resulted_tuple_found::
        end

        return batch_final_ctx
    end
end

-- Allows to fill the session storage with evals functions.
local init_session_storage = function()
    init_eval_set_stored_procedure()
    init_eval_set_targetspace()
    init_eval_check_targetspace_exist()
    init_eval_set_null_interpretation()
    init_eval_upload_batch_from_parser()
    init_eval_get_prepared_batch_table()
    init_eval_swap_according_to_header()
    init_eval_cast_tuples_to_scapce_format()
    init_eval_import_prepared_batch()
    init_eval_is_tuples_equal()
    init_eval_get_batch_final_ctx()
    crud_evals_init_complete = true
end

init_session_storage()
return crud_evals_init_complete
