"""A stand-in for TCM, the Tarantool Cluster Manager, which is Tarantool
Enterprise only.

It takes the config the way TCM does, -c=<file> (or -c <file>), records what
it was started with into tcm-shim.json in its working directory - the
arguments, the TCM_ variables of its environment, the config and the working
directory - and answers HTTP on http.host:http.port of the config, as TCM does
once it is up, with 401 to every request, as the pages of TCM answer without
a session. It logs a line to its standard output, which tt writes to tt.log,
and stops on SIGTERM.

The test writes it as the tcm of a scope with a shebang of its own Python,
which has PyYAML.
"""

import http.server
import json
import os
import signal
import sys

import yaml

RECORD = "tcm-shim.json"


def config_path(args):
    for i, arg in enumerate(args):
        if arg.startswith("-c="):
            return arg[len("-c=") :]
        if arg == "-c" and i + 1 < len(args):
            return args[i + 1]
    sys.exit("tcm shim: no -c=<config>")


def write_record(record):
    # Whole, through a temporary file renamed over it: the test reads it
    # while the shim may be writing it.
    tmp = RECORD + ".tmp"
    with open(tmp, "w") as f:
        json.dump(record, f)
    os.rename(tmp, RECORD)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(401)
        self.end_headers()
        self.wfile.write(b"unauthorized\n")

    def log_message(self, format, *args):
        pass


def main():
    path = config_path(sys.argv[1:])
    with open(path) as f:
        config = yaml.safe_load(f)

    write_record(
        {
            "args": sys.argv[1:],
            "env": {k: v for k, v in os.environ.items() if k.startswith("TCM_")},
            "config": config,
            "config_mode": oct(os.stat(path).st_mode & 0o777),
            "cwd": os.getcwd(),
            "pid": os.getpid(),
        },
    )

    http_cfg = config.get("http", {})
    host = str(http_cfg.get("host", "127.0.0.1"))
    port = int(http_cfg.get("port", 8080))

    server = http.server.HTTPServer((host, port), Handler)

    def stop(_signum, _frame):
        print("INF tcm shim: stopping", flush=True)
        sys.exit(0)

    signal.signal(signal.SIGTERM, stop)

    print(f"INF tcm shim: listening on {host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
