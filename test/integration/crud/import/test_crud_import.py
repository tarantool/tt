import os
import re
import shutil
import signal
import subprocess
import warnings
from time import sleep

import msgpack
import pytest
import tarantool
from utils import run_command_and_get_output


def tarantool_v1_no_testing():
    warnings.warn(UserWarning("tarantool v1: cannot test working (only for v2 and higher)."))
    warnings.warn(UserWarning("crud import implementation cannot work stably with tarantool v1."))


@pytest.fixture(autouse=True)
def run_before_and_after_tests():
    # Kill existance test instance if it already run from past integration test.
    pid_instance = subprocess.run(
        ["pgrep", "crud_import_test_instance_cfg.lua"],
        capture_output=True)
    if len(pid_instance.stdout) != 0:
        os.kill(int(pid_instance.stdout), signal.SIGKILL)
    # Run test.
    yield
    # Kill a test instance if it was not stopped due to a failed test.
    pid_instance = subprocess.run(
        ["pgrep", "crud_import_test_instance_cfg.lua"],
        capture_output=True)
    if len(pid_instance.stdout) != 0:
        os.kill(int(pid_instance.stdout), signal.SIGKILL)


@pytest.fixture()
def prepare_crud_module():
    # Check if there is a crud module, otherwise install it.
    cmd = ["tarantoolctl", "rocks", "show", "crud"]
    rc, output = run_command_and_get_output(cmd)
    if rc == 1 and re.search(r"Error: cannot find package crud", output):
        cmd = ["tarantoolctl", "rocks", "install", "crud"]
        rc, output = run_command_and_get_output(cmd)
        assert rc == 0
        cmd = ["tarantoolctl", "rocks", "show", "crud"]
        rc, output = run_command_and_get_output(cmd)
        assert rc == 0
        assert None is re.search(r"Error: cannot find package crud", output)


def test_crud_import_unset_uri(tt_cmd, tmpdir):
    # Testing with unset uri.
    cmd = [tt_cmd, "crud", "import"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"It is required to specify router URI.", output)


def test_crud_import_unset_input_file(tt_cmd, tmpdir):
    # Testing with unset input file.
    cmd = [tt_cmd, "crud", "import", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"It is required to specify input file.", output)


def test_crud_import_unset_space(tt_cmd, tmpdir):
    # Testing with unset space.
    cmd = [tt_cmd, "crud", "import", "_", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"It is required to specify target space.", output)


def test_crud_import_simplest_progress(tt_cmd, tmpdir, prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "progress")
    shutil.copy(test_app_path + "/developers_incorrect.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_incorrect.csv",
        "developers", "--username=guest", "--header", "--match=header", "--on-error=skip",
        "--batch-size=1", "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[skip\]", output)
    assert re.search(r"total read:\s*4", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*3", output)
    assert re.search(r"parsed error:\s*1", output)
    assert re.search(r"import success:\s*2", output)
    assert re.search(r"import error:\s*2", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        assert re.search(r"7,700,Ned,Flanders,35", success_recs)
        assert re.search(r"10,1000,Homer,Simpson,40", success_recs)
        assert None is re.search(r"8,800,Bart\",Simpson,16", success_recs)
        assert None is re.search(r"7,900,Marge,Simpson,33", success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", error_recs)
        assert re.search(r"8,800,Bart\",Simpson,16", error_recs)
        assert re.search(r"7,900,Marge,Simpson,33", error_recs)
        assert None is re.search(r"7,700,Ned,Flanders,35", error_recs)
        assert None is re.search(r"10,1000,Homer,Simpson,40", error_recs)

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert re.search(r"line position: 3\nproblem record: 8,800,Bart\",Simpson,16", log_recs)
        assert re.search(r"parse error on line 3, column 11: bare \" in non-quoted", log_recs)
        assert re.search(r"line position: 4\nproblem record: 7,900,Marge,Simpson,33", log_recs)
        assert re.search(r"Duplicate key exists in unique index \"primary_index\"", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":5", progress_recs)
        assert re.search(r"\"retryPositions\":\[3,4\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        assert re.search(r"\[7, 700, 'Ned', 'Flanders', 35\]", developers_str)
        assert re.search(r"\[10, 1000, 'Homer', 'Simpson', 40\]", developers_str)
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch.
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Copy the .csv file and progress.json to the "run" directory.
    shutil.copy(test_app_path + "/developers_correct.csv", tmpdir)
    shutil.copy(test_app_path + "/progress.json", tmpdir)

    # Now run import with --progress file.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_correct.csv",
        "developers", "--username=guest", "--header", "--match=header", "--on-error=skip",
        "--batch-size=1", "--progress",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[skip\]", output)
    assert re.search(r"total read:\s*4", output)
    assert re.search(r"ignored \(--progress\):\s*2", output)
    assert re.search(r"parsed success:\s*4", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*2", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        assert re.search(r"8,800,Bart,Simpson,16", success_recs)
        assert re.search(r"9,900,Marge,Simpson,33", success_recs)
        assert None is re.search(r"7,700,Ned,Flanders,35", success_recs)
        assert None is re.search(r"10,1000,Homer,Simpson,40", success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", error_recs)
        assert None is re.search(r"8,800,Bart,Simpson,16", error_recs)
        assert None is re.search(r"7,700,Ned,Flanders,35", error_recs)
        assert None is re.search(r"9,900,Marge,Simpson,33", error_recs)
        assert None is re.search(r"10,1000,Homer,Simpson,40", error_recs)

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":4", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        assert re.search(r"\[7, 700, 'Ned', 'Flanders', 35\]", developers_str)
        assert re.search(r"\[8, 800, 'Bart', 'Simpson', 16\]", developers_str)
        assert re.search(r"\[9, 900, 'Marge', 'Simpson', 33\]", developers_str)
        assert re.search(r"\[10, 1000, 'Homer', 'Simpson', 40\]", developers_str)
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()


def test_crud_import_match_header(tt_cmd, tmpdir, prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "match")
    shutil.copy(test_app_path + "/developers_header.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_header.csv",
        "developers", "--username=guest", "--header", "--match=header", "--on-error=skip",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[skip\]", output)
    assert re.search(r"total read:\s*5", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*5", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*5", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"bucket_id,id,name,age,vacation", success_recs)
        assert re.search(r"100,1,Ned,35,false", success_recs)
        assert re.search(r"200,2,Bart,16,true", success_recs)
        assert re.search(r"300,3,Marge,33,false", success_recs)
        assert re.search(r"400,4,Homer,40,true", success_recs)
        assert re.search(r"500,5,Alan,50,", success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert re.search(r"bucket_id,id,name,age,vacation", error_recs)
        assert None is re.search(r"100,1,Ned,35,false", error_recs)
        assert None is re.search(r"200,2,Bart,16,true", error_recs)
        assert None is re.search(r"300,3,Marge,33,false", error_recs)
        assert None is re.search(r"400,4,Homer,40,true", error_recs)
        assert None is re.search(r"500,5,Alan,50,", error_recs)

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":6", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        assert re.search(r"\[1, 100, 'Ned', None, 35\]", developers_str)
        assert re.search(r"\[2, 200, 'Bart', None, 16\]", developers_str)
        assert re.search(r"\[3, 300, 'Marge', None, 33\]", developers_str)
        assert re.search(r"\[4, 400, 'Homer', None, 40\]", developers_str)
        assert re.search(r"\[5, 500, 'Alan', None, 50\]", developers_str)
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()


def test_crud_import_batch_sizing_correct_input(tt_cmd, tmpdir, prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "sizing")
    shutil.copy(test_app_path + "/developers_correct.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Batch size is 1 and input file has 30 correct records.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_correct.csv",
        "developers", "--username=guest", "--header", "--match=header", "--batch-size=1",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*30", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*30", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*30", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        field_val = 1
        for id in range(1, 31):
            field_val = id
            record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                      str(field_val) + ',' + str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":31", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        field_val = 1
        expected_template = str()
        for id in range(1, 31):
            field_val = id
            rec = [field_val, field_val, str(field_val), str(field_val), field_val]
            expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch (backet size was 1).
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Batch size is 7 and input file has 30 correct records.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_correct.csv",
        "developers", "--username=guest", "--header", "--match=header", "--batch-size=7",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*30", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*30", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*30", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        field_val = 1
        for id in range(1, 31):
            field_val = id
            record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                      str(field_val) + ',' + str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":31", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        field_val = 1
        expected_template = str()
        for id in range(1, 31):
            field_val = id
            rec = [field_val, field_val, str(field_val), str(field_val), field_val]
            expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch (backet size was 7).
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Batch size is 30 and input file has 30 correct records.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_correct.csv",
        "developers", "--username=guest", "--header", "--match=header", "--batch-size=30",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*30", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*30", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*30", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        field_val = 1
        for id in range(1, 31):
            field_val = id
            record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                      str(field_val) + ',' + str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":31", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        field_val = 1
        expected_template = str()
        for id in range(1, 31):
            field_val = id
            rec = [field_val, field_val, str(field_val), str(field_val), field_val]
            expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch (backet size was 30).
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Batch size is 100 and input file has 30 correct records.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_correct.csv",
        "developers", "--username=guest", "--header", "--match=header", "--batch-size=100",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*30", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*30", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*30", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        field_val = 1
        for id in range(1, 31):
            field_val = id
            record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                      str(field_val) + ',' + str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":31", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        field_val = 1
        expected_template = str()
        for id in range(1, 31):
            field_val = id
            rec = [field_val, field_val, str(field_val), str(field_val), field_val]
            expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()


def test_crud_import_batch_sizing_incorrect_input_with_progress(tt_cmd, tmpdir,
                                                                prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "sizing")
    shutil.copy(test_app_path + "/developers_incorrect.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Batch with 15 records: 20 first lines in input file.
    # One of 15 records has syntax problem, and 3 has problem on crud level.
    # Finally, must be 4 import error and 11 import success.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_incorrect.csv",
        "developers", "--username=guest", "--header", "--match=header", "--batch-size=15",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*15", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*14", output)
    assert re.search(r"parsed error:\s*1", output)
    assert re.search(r"import success:\s*11", output)
    assert re.search(r"import error:\s*4", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        success_id = [1, 2, 3, 4, 6, 7, 9, 11, 12, 14, 15]
        for field_val in success_id:
            if field_val != 3:
                record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                          str(field_val) + ',' + str(field_val))
            else:
                record = (str(field_val) + ',' + str(field_val) + ',' +
                          str('\"name\nwith line\nbreaks\"') + ',' + str(field_val) + ',' +
                          str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        error_expected = [
            "id,bucket_id,name,surname,age\n",
            "5,5,5,\"surname\nwith line\nbreaks\",\"non-numeric-value\"\n"
            "8,8\",8,8,8\n",
            "9,10,10,10,10\n",
            "13,13,,13,13\n",
        ]
        error_expected_str = str()
        for rec in error_expected:
            error_expected_str = error_expected_str + str(rec)
        assert error_recs == error_expected_str

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        log_expected = [
            (r"field 5 \(age\) type does not match one required by operation: "
             r"expected number, got string"),
            r"parse error on line 13, column 4: bare \" in non-quoted-field",
            r"Duplicate key exists in unique index \"primary_index\" in space \"developers\"",
            (r"with old tuple - \[9, 9, \"9\", \"9\", 9\] "
             r"and new tuple - \[9, 10, \"10\", \"10\", 10\]"),
            (r"field 3 \(name\) type does not match one required by operation: "
             r"expected string, got nil"),
        ]
        for rec in log_expected:
            assert re.search(rec, log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":false", progress_recs)
        assert re.search(r"\"lastPosition\":20", progress_recs)
        assert re.search(r"\"retryPositions\":\[8,13,15,18\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        success_id = [1, 2, 3, 4, 6, 7, 9, 11, 12, 14, 15]
        expected_template = str()
        for id in success_id:
            if id != 3:
                field_val = id
                rec = [field_val, field_val, str(field_val), str(field_val), field_val]
                expected_template += str(rec)
            else:
                field_val = id
                rec = [
                    field_val, field_val,
                    str('name\nwith line\nbreaks'), str(field_val), field_val,
                    ]
                expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch.
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Copy fixed .csv file to the "run" directory.
    # Progress file from prev launch already there.
    shutil.copy(test_app_path + "/developers_fixed.csv", tmpdir)

    # Now run import with --progress file.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers_fixed.csv",
        "developers", "--username=guest", "--header", "--match=header",
        "--batch-size=15", "--progress",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*30", output)
    assert re.search(r"ignored \(--progress\):\s*11", output)
    assert re.search(r"parsed success:\s*30", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*19", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        assert re.search(r"id,bucket_id,name,surname,age", success_recs)
        success_id = [5, 8, 10, 13, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30]
        for field_val in success_id:
            if field_val != 5:
                record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                          str(field_val) + ',' + str(field_val))
            else:
                record = (str(field_val) + ',' + str(field_val) + ',' + str(field_val) + ',' +
                          str('\"surname\nwith line\nbreaks\"') + ',' + str(field_val))
            assert re.search(record, success_recs)

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":35", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        expected_template = str()
        for id in range(1, 31):
            if id != 3 and id != 5:
                field_val = id
                rec = [field_val, field_val, str(field_val), str(field_val), field_val]
                expected_template += str(rec)
            if id == 3:
                field_val = id
                rec = [
                    field_val, field_val,
                    str('name\nwith line\nbreaks'), str(field_val), field_val,
                    ]
                expected_template += str(rec)
            if id == 5:
                field_val = id
                rec = [
                    field_val, field_val, str(field_val),
                    str('surname\nwith line\nbreaks'), field_val,
                ]
                expected_template += str(rec)
        assert expected_template == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()


def test_crud_import_null_interpretation(tt_cmd, tmpdir, prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "null")
    shutil.copy(test_app_path + "/developers.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Import with standard null interpretation.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers.csv",
        "developers", "--username=guest", "--header",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*18", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*18", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*18", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        with open(tmpdir + '/developers.csv', 'r') as input_file:
            input_recs = input_file.read()
            assert success_recs == input_recs

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":19", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        expected_template = [
            [1, 1, '1', None, 1], [2, 2, '2', 'NULL', 2],
            [3, 3, '3', 'NULL', 3], [4, 4, '4', None, 4, None],
            [5, 5, '5', 'NULL', 5, 'NULL'], [6, 6, '6', 'NULL', 6, 'NULL'],
            [7, 7, '7', None, 7], [8, 8, '8', 'null', 8],
            [9, 9, '9', 'null', 9], [10, 10, '10', None, 10, None],
            [11, 11, '11', 'null', 11, 'null'], [12, 12, '12', 'null', 12, 'null'],
            [13, 13, '13', None, 13], [14, 14, '14', 'nil', 14],
            [15, 15, '15', 'nil', 15], [16, 16, '16', None, 16, None],
            [17, 17, '17', 'nil', 17, 'nil'], [18, 18, '18', 'nil', 18, 'nil'],
        ]
        expected_template_str = str()
        expected_template_str = expected_template_str.join(str(dev_tuple)
                                                           for dev_tuple in expected_template)
        assert expected_template_str == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Remove success, error and log files from prev launch.
    os.remove(tmpdir + "/imported.csv")
    os.remove(tmpdir + "/notimported.csv")
    os.remove(tmpdir + "/logfile.log")

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "developers")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    # Import with null interpretation as string 'NULL'.
    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./developers.csv",
        "developers", "--username=guest", "--header", "--null=NULL",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*18", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*18", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*18", output)
    assert re.search(r"import error:\s*0", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        with open(tmpdir + '/developers.csv', 'r') as input_file:
            input_recs = input_file.read()
            assert success_recs == input_recs

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        assert error_recs == "id,bucket_id,name,surname,age\n"

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert None is re.search(r"problem record", log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":19", progress_recs)
        assert re.search(r"\"retryPositions\":\[\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        developers = connection.call('crud.select', "developers")
        developers_str = str()
        developers_str = developers_str.join(str(dev_tuple) for dev_tuple in developers[0]['rows'])
        expected_template = [
            [1, 1, '1', '', 1], [2, 2, '2', None, 2],
            [3, 3, '3', None, 3], [4, 4, '4', '', 4, ''],
            [5, 5, '5', None, 5, None], [6, 6, '6', None, 6, None],
            [7, 7, '7', '', 7], [8, 8, '8', 'null', 8],
            [9, 9, '9', 'null', 9], [10, 10, '10', '', 10, ''],
            [11, 11, '11', 'null', 11, 'null'], [12, 12, '12', 'null', 12, 'null'],
            [13, 13, '13', '', 13], [14, 14, '14', 'nil', 14],
            [15, 15, '15', 'nil', 15], [16, 16, '16', '', 16, ''],
            [17, 17, '17', 'nil', 17, 'nil'], [18, 18, '18', 'nil', 18, 'nil'],
        ]
        expected_template_str = str()
        expected_template_str = expected_template_str.join(str(dev_tuple)
                                                           for dev_tuple in expected_template)
        assert expected_template_str == developers_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()


def test_crud_import_types_cast(tt_cmd, tmpdir, prepare_crud_module):
    # Checking the availability of crud module.
    prepare_crud_module

    # Get tarantool major version.
    # For major version 1 will be no testing.
    tarantool_version = subprocess.run(["tarantool", "--version"],
                                       stdout=subprocess.PIPE, text=True)
    tarantool_version_major = tarantool_version.stdout[10]

    if tarantool_version_major == '1':
        tarantool_v1_no_testing()
        return

    # Run instance for test.
    instance = subprocess.Popen(
        [
            "tarantool",
            "./test/integration/crud/import/test_file/crud_import_test_instance_cfg.lua"
        ],
        stderr=subprocess.STDOUT
        )
    # The delay is needed so that the instance has time to start and configure itself.
    sleep(1)

    # Copy the .csv file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "types")
    shutil.copy(test_app_path + "/data.csv", tmpdir)

    # Clean target space before test.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        clean_res = connection.call('crud.truncate', "typetest")
        assert clean_res[0] is True
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    cmd = [
        tt_cmd, "crud", "import", "localhost:3301", "./data.csv",
        "typetest", "--username=guest", "--header",
        "--success=imported", "--error=notimported", "--log=logfile",
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1

    # Check import summary.
    assert re.search(r"Crud init complete:\s*\[true\]", output)
    assert re.search(r"Target space exist:\s*\[true\]", output)
    assert re.search(r"In case of error:\s*\[stop\]", output)
    assert re.search(r"total read:\s*37", output)
    assert re.search(r"ignored \(--progress\):\s*0", output)
    assert re.search(r"parsed success:\s*37", output)
    assert re.search(r"parsed error:\s*0", output)
    assert re.search(r"import success:\s*30", output)
    assert re.search(r"import error:\s*7", output)

    # Check import succsess file.
    with open(tmpdir + '/imported.csv', 'r') as succsess_file:
        success_recs = succsess_file.read()
        with open(tmpdir + '/data.csv', 'r') as input_file:
            input_recs = input_file.read()
            input_recs = input_recs.replace("16,16,,,3.1415,,,,\n", "")
            input_recs = input_recs.replace("22,22,,,,3.1415,,,\n", "")
            input_recs = input_recs.replace("23,23,,,,-123456789,,,\n", "")
            input_recs = input_recs.replace("35,35,,,,,,,1\n", "")
            input_recs = input_recs.replace("36,36,,,,,,,0\n", "")
            input_recs = input_recs.replace("37,37,,,,,,,TRUE\n", "")
            input_recs = input_recs.replace("38,38,,,,,,,FALSE\n", "")
            assert success_recs == input_recs

    # Check import error file.
    with open(tmpdir + '/notimported.csv', 'r') as error_file:
        error_recs = error_file.read()
        expected_template = [
            "id,bucket_id,string,number,integer,unsigned,double,decimal,boolean\n",
            "16,16,,,3.1415,,,,\n",
            "22,22,,,,3.1415,,,\n",
            "23,23,,,,-123456789,,,\n",
            "35,35,,,,,,,1\n",
            "36,36,,,,,,,0\n",
            "37,37,,,,,,,TRUE\n",
            "38,38,,,,,,,FALSE\n",
        ]
        expected_template_str = str()
        expected_template_str = expected_template_str.join(str(dev_tuple)
                                                           for dev_tuple in expected_template)
        assert expected_template_str == error_recs

    # Check import logs file.
    with open(tmpdir + '/logfile.log', 'r') as log_file:
        log_recs = log_file.read()
        assert re.search((r"field 5 \(integer\) type does not match one required by operation: "
                          r"expected integer, got double"), log_recs)
        assert re.search((r"field 6 \(unsigned\) type does not match one required by operation: "
                          r"expected unsigned, got double"), log_recs)
        assert re.search((r"field 6 \(unsigned\) type does not match one required by operation: "
                          r"expected unsigned, got integer"), log_recs)
        assert re.search((r"field 9 \(boolean\) type does not match one required by operation: "
                          r"expected boolean, got string"), log_recs)

    # Check import progress file.
    with open(tmpdir + '/progress.json', 'r') as progress_file:
        progress_recs = progress_file.read()
        assert re.search(r"\"endOfFileReached\":true", progress_recs)
        assert re.search(r"\"lastPosition\":43", progress_recs)
        assert re.search(r"\"retryPositions\":\[22,27,28,40,41,42,43\]", progress_recs)

    # Check imported data via crud.select on router.
    try:
        connection = tarantool.connect("localhost", 3301, user='guest')
        records = connection.call('crud.select', "typetest")
        records_str = str()
        records_str = records_str.join(str(dev_tuple) for dev_tuple in records[0]['rows'])
        expected_template = [
            # String testing.
            [1, 1, '123', None, None, None, None, None, None],
            [2, 2, ' B i l l ', None, None, None, None, None, None],
            [3, 3, '\nB\ni\nl\nl\n', None, None, None, None, None, None],
            [4, 4, '.,\t;人\'木"夕', None, None, None, None, None, None],
            # Number testing.
            [5, 5, None, 0, None, None, None, None, None],
            [6, 6, None, -1000000, None, None, None, None, None],
            [7, 7, None, -1000000, None, None, None, None, None],
            [8, 8, None, -2.7e+20, None, None, None, None, None],
            [9, 9, None, -2.7e+20, None, None, None, None, None],
            [10, 10, None, 123456789.12345679, None, None, None, None, None],
            # Integer testing.
            [11, 11, None, None, 0, None, None, None, None],
            [12, 12, None, None, 123456789, None, None, None, None],
            [13, 13, None, None, -123456789, None, None, None, None],
            [14, 14, None, None, -1000000, None, None, None, None],
            [15, 15, None, None, -1000000, None, None, None, None],
            # Unsigned testing.
            [17, 18, None, None, None, 0, None, None, None],
            [19, 19, None, None, None, 123456789, None, None, None],
            [20, 20, None, None, None, 1000000, None, None, None],
            [21, 21, None, None, None, 1000000, None, None, None],
            # Double testing.
            # TODO: write this test case later (crud issue #398, now this type is unstable).
            # Decimal testing.
            [24, 24, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\x00\x0c'), None],
            [25, 25, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\x00\x124Vx\x9c'), None],
            [26, 26, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\x00\x124Vx\x9d'), None],
            [27, 27, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\x00\x10\x00\x00\r'), None],
            [28, 28, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\x00\x10\x00\x00\r'), None],
            [29, 29, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\t\x01#Eg\x89\x124Vx\x9c'), None],
            [30, 30, None, None, None, None, None,
                msgpack.ExtType(code=1, data=b'\t\x01#Eg\x89\x124Vx\x9d'), None],
            # Boolean testing.
            [31, 31, None, None, None, None, None, None, True],
            [32, 32, None, None, None, None, None, None, False],
            [33, 33, None, None, None, None, None, None, True],
            [34, 34, None, None, None, None, None, None, False],
        ]
        expected_template_str = str()
        expected_template_str = expected_template_str.join(str(dev_tuple)
                                                           for dev_tuple in expected_template)
        assert expected_template_str == records_str
    except tarantool.NetworkError as instanceConnectionExc:
        assert instanceConnectionExc is None

    instance.kill()
