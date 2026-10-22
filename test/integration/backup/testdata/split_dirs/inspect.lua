-- Reports what the restored instance came up with, and exits. It is run as
-- the configuration's app.file (passed in TT_APP_FILE), so box.cfg has
-- already been applied from the cluster config under test.
print('TT_TEST_RESULT ' .. require('json').encode({
    uuid = box.info.uuid,
    lsn = box.info.vclock[1],
    memtx_rows = box.space.backup_test:len(),
    vinyl_rows = box.space.backup_test_vinyl:len(),
}))
os.exit(0)
