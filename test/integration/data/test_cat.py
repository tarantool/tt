import datetime
import json
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

from utils import run_command_and_get_output, run_command_and_get_streams

CAT_FILES = Path(__file__).parent / "cat_file"

# A script that writes the journals of an instance into its working directory:
# a write-ahead log with a transaction of two rows, a snapshot, and a
# write-ahead log after it with every kind of data change and values of every
# extension type.
JOURNALS_SCRIPT = """
local decimal = require('decimal')
local uuid = require('uuid')
local datetime = require('datetime')
local varbinary = require('varbinary')
box.cfg{log_level = 2}
local s = box.schema.space.create('test', {id = 512})
s:create_index('pk')
s:insert{1, 'a'}
box.atomic(function()
    s:insert{2, 'b'}
    s:insert{3, 'c'}
end)
box.snapshot()
s:insert{4, decimal.new('12.340'), uuid.fromstr('b54147fd-538b-4d39-b3ad-17ba72762f48')}
s:insert{5, (datetime.parse('2024-11-14T14:02:36.027 Europe/Moscow'))}
s:insert{6, varbinary.new('\\0\\1'), 18446744073709551615ULL, -1.5, {x = {1, box.NULL}}}
s:update(1, {{'=', 2, 'x'}})
s:upsert({7, 'u'}, {{'=', 2, 'z'}})
s:delete(2)
box.atomic(function()
    s:replace{8, 'r'}
    s:replace{9, 'r'}
end)
os.exit(0)
"""


@pytest.fixture(scope="module")
def journals(tmp_path_factory) -> Path:
    """The journals JOURNALS_SCRIPT writes, made by the Tarantool in PATH."""
    if shutil.which("tarantool") is None:
        pytest.skip("tarantool is not found in PATH")

    work = tmp_path_factory.mktemp("journals")
    (work / "journals.lua").write_text(JOURNALS_SCRIPT)
    subprocess.run(["tarantool", "journals.lua"], cwd=work, check=True)
    (work / "journals.lua").unlink()

    return work


def cat_json(tt_cmd, *args, cwd=None):
    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "data", "cat", "-o", "json", *map(str, args)],
        cwd=cwd,
    )
    assert rc == 0, stderr
    return [json.loads(line) for line in stdout.splitlines()]


@pytest.mark.parametrize(
    "args, expected_error",
    [
        ([], "specify at least one .snap, .xlog or .vylog file or directory"),
        (["path-to-non-existent-file"], "no such file or directory"),
        (["test.xlog", "--timestamp=abcdef"], 'failed to parse a timestamp: parsing time "abcdef"'),
        (
            ["test.xlog", "--timestamp=2024-11-14T14:02:36.abc"],
            'failed to parse a timestamp: parsing time "2024-11-14T14:02:36.abc"',
        ),
        (["test.xlog", "--from=5", "--to=4"], "the lower bound is past the upper one"),
        (["test.xlog", "--to-vclock=1:4"], "--to-vclock: invalid vclock"),
        (["test.xlog", "--from=1", "--to-vclock={1: 4}"], "mutually exclusive"),
        (["test.xlog", "--space=nope"], "--space nope: unknown space"),
        (["test.xlog", "-o", "table"], 'unknown output format "table"'),
    ],
)
def test_cat_invalid(tt_cmd, tmp_path, args, expected_error):
    shutil.copy(CAT_FILES / "test.xlog", tmp_path)

    rc, output = run_command_and_get_output([tt_cmd, "data", "cat", *args], cwd=tmp_path)
    assert rc == 1
    assert expected_error in output


def test_cat_old_commands_are_gone(tt_cmd, tmp_path):
    for command in ("cat", "play"):
        rc, output = run_command_and_get_output([tt_cmd, command, "test.xlog"], cwd=tmp_path)
        assert rc == 1
        assert f"unknown command {command}" in output


def test_cat_yaml(tt_cmd, tmp_path):
    shutil.copy(CAT_FILES / "test.snap", tmp_path)

    rc, stdout, _ = run_command_and_get_streams(
        [tt_cmd, "data", "cat", "test.snap", "--space=320", "--space=_space"],
        cwd=tmp_path,
    )
    assert rc == 0
    documents = list(yaml.safe_load_all(stdout))
    assert documents
    # --space names system spaces too, by id and by the name the snapshot
    # gives it.
    assert {doc["BODY"]["space_id"] for doc in documents} == {280, 320}
    assert documents[0]["HEADER"]["type"] == "INSERT"


def test_cat_system_rows(tt_cmd, tmp_path):
    shutil.copy(CAT_FILES / "test.snap", tmp_path)

    # The snapshot has the rows of system spaces only.
    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "data", "cat", "test.snap"],
        cwd=tmp_path,
    )
    assert rc == 0
    assert stdout == ""
    assert "No rows match" in stderr

    records = cat_json(tt_cmd, "test.snap", "--show-system", cwd=tmp_path)
    assert all(r["BODY"]["space_id"] < 512 for r in records if "space_id" in r["BODY"])
    # The RAFT and RAFT_PROMOTE rows of the snapshot are decoded.
    assert records[-2]["HEADER"]["type"] == "RAFT"
    assert records[-2]["BODY"] == {"term": 1}
    assert records[-1]["HEADER"]["type"] == "RAFT_PROMOTE"
    assert records[-1]["BODY"] == {"replica_id": 0, "lsn": 0, "term": 0}


def test_cat_directories(tt_cmd, tmp_path):
    shutil.copytree(CAT_FILES, tmp_path / "files")

    rc, _, stderr = run_command_and_get_streams(
        [tt_cmd, "data", "cat", "files", "files/test.xlog", "--show-system"],
        cwd=tmp_path,
    )
    assert rc == 0
    assert f"Reading {tmp_path}/files/test.xlog" in stderr
    assert f"Reading {tmp_path}/files/timestamp.xlog" in stderr
    # Of the two snapshots, at one vclock, one is read.
    assert stderr.count("Skipping the snapshot") == 1


@pytest.mark.parametrize(
    "timestamp",
    [
        "1731592956.1182",
        "1731592956.8182",
        "2024-11-14T14:02:36.818+00:00",
        "2024-11-14T14:02:36.0180+00:00",
        "2024-11-14T14:02:35+00:00",
    ],
)
def test_cat_timestamp(tt_cmd, tmp_path, timestamp):
    shutil.copy(CAT_FILES / "timestamp.xlog", tmp_path)

    if timestamp[0].isdigit() and "T" not in timestamp:
        before = float(timestamp)
    else:
        before = datetime.datetime.fromisoformat(timestamp).timestamp()

    records = cat_json(tt_cmd, "timestamp.xlog", f"--timestamp={timestamp}", cwd=tmp_path)
    everything = cat_json(tt_cmd, "timestamp.xlog", cwd=tmp_path)

    assert all(record["HEADER"]["timestamp"] < before for record in records)
    assert len(records) == sum(1 for r in everything if r["HEADER"]["timestamp"] < before)


def test_cat_chain(tt_cmd, journals):
    # The snapshot, then the rows past it: the log before it is not read.
    records = cat_json(tt_cmd, journals)
    keys = [r["BODY"]["key" if "key" in r["BODY"] else "tuple"][0] for r in records]
    assert keys == [1, 2, 3, 4, 5, 6, 1, 7, 2, 8, 9]
    assert [r["HEADER"]["type"] for r in records[3:]] == [
        "INSERT",
        "INSERT",
        "INSERT",
        "UPDATE",
        "UPSERT",
        "DELETE",
        "REPLACE",
        "REPLACE",
    ]

    # Values with no JSON type are strings.
    assert records[3]["BODY"]["tuple"] == [4, "12.340", "b54147fd-538b-4d39-b3ad-17ba72762f48"]
    assert records[4]["BODY"]["tuple"] == [5, "2024-11-14T14:02:36.027+03:00"]
    assert records[5]["BODY"]["tuple"] == [6, "AAE=", 18446744073709551615, -1.5, {"x": [1, None]}]

    # The rows of a transaction of several carry its first LSN.
    first, last = records[-2]["HEADER"], records[-1]["HEADER"]
    assert first["tsn"] == first["lsn"] and last["tsn"] == first["lsn"]
    assert "commit" not in first and last["commit"] is True


def test_cat_lsn_bounds(tt_cmd, journals):
    xlogs = sorted(journals.glob("*.xlog"))
    everything = cat_json(tt_cmd, *xlogs)
    lsns = [record["HEADER"]["lsn"] for record in everything]

    # Both ends are included.
    low, high = lsns[4], lsns[6]
    records = cat_json(tt_cmd, *xlogs, f"--from={low}", f"--to={high}")
    assert [record["HEADER"]["lsn"] for record in records] == lsns[4:7]

    # A bound inside a transaction takes it whole, either end.
    tsn = everything[-1]["HEADER"]["tsn"]
    records = cat_json(tt_cmd, *xlogs, f"--from={tsn + 1}")
    assert [record["HEADER"]["lsn"] for record in records] == [tsn, tsn + 1]
    records = cat_json(tt_cmd, *xlogs, f"--to={tsn}", f"--from={tsn}")
    assert [record["HEADER"]["lsn"] for record in records] == [tsn, tsn + 1]

    records = cat_json(tt_cmd, *xlogs, f"--to-vclock={{1: {tsn}}}", f"--from-vclock={{1: {low}}}")
    assert [record["HEADER"]["lsn"] for record in records] == [n for n in lsns if n > low]


def test_cat_lua_replays(tt_cmd, journals, tmp_path):
    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "data", "cat", str(journals), "-o", "lua"],
    )
    assert rc == 0, stderr
    assert "box.space[512]:update({1}, {{'=', 2, 'x'}})" in stdout

    # The statements remake the data in a new instance.
    script = tmp_path / "replay.lua"
    script.write_text(
        "box.cfg{log_level = 2}\n"
        "box.schema.space.create('test', {id = 512}):create_index('pk')\n"
        + stdout
        + "local yaml = require('yaml')\n"
        + "io.write(yaml.encode(box.space.test:select()))\n"
        + "os.exit(0)\n",
    )
    replay = subprocess.run(
        ["tarantool", script.name],
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=True,
    )
    data = yaml.safe_load(replay.stdout)
    assert [row[0] for row in data] == [1, 3, 4, 5, 6, 7, 8, 9]
    assert data[0] == [1, "x"]
    assert data[1] == [3, "c"]
    assert data[5] == [7, "u"]


def test_cat_vylog(tt_cmd, tmp_path):
    if shutil.which("tarantool") is None:
        pytest.skip("tarantool is not found in PATH")

    (tmp_path / "vinyl.lua").write_text(
        "box.cfg{log_level = 2}\n"
        "local s = box.schema.space.create('kv', {engine = 'vinyl'})\n"
        "s:create_index('pk')\n"
        "s:replace{1}\n"
        "box.snapshot()\n"
        "os.exit(0)\n",
    )
    subprocess.run(["tarantool", "vinyl.lua"], cwd=tmp_path, check=True)
    vylog = sorted(tmp_path.glob("*.vylog"))[-1]

    records = cat_json(tt_cmd, vylog)
    types = [record["BODY"]["type"] for record in records]
    assert "create_lsm" in types
    assert any(record["BODY"].get("space_id") == 512 for record in records)
