import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import http.server
import json
import re
import socket
import sys

from twr_python_sdk import tarantool as box
from wit_world import exports


class Router:
    """Maps a method and a path pattern, /notes/<id:int>, to a handler."""

    def __init__(self):
        self.routes = []

    def route(self, method, pattern):
        def part(match):
            name, kind = match.group(1), match.group(2)
            return f'(?P<{name}>[0-9]+)' if kind == 'int' else f'(?P<{name}>[^/]+)'

        regex = re.compile('^' + re.sub(r'<(\w+)(?::(\w+))?>', part, pattern) + '$')

        def register(handler):
            self.routes.append((method, regex, pattern, handler))
            return handler

        return register

    def dispatch(self, method, path, body):
        known_path = False
        for route_method, regex, pattern, handler in self.routes:
            match = regex.match(path)
            if match is None:
                continue
            known_path = True
            if route_method == method:
                params = {
                    name: int(value) if f'<{name}:int>' in pattern else value
                    for name, value in match.groupdict().items()
                }
                return handler(body, **params)
        if known_path:
            return 405, {'error': f'{method} is not allowed on {path}'}
        return 404, {'error': f'no route for {path}'}


router = Router()


def note(row):
    return {'id': row[0], 'text': row[1]}


@router.route('GET', '/notes')
def list_notes(body):
    return 200, [note(row) for row in box.space['notes'].select(limit=100)]


@router.route('GET', '/notes/<id:int>')
def get_note(body, id):
    row = box.space['notes'].get(id)
    if row is None:
        return 404, {'error': f'no note {id}'}
    return 200, note(row)


@router.route('PUT', '/notes/<id:int>')
def put_note(body, id):
    text = json.loads(body)['text']
    return 200, note(box.space['notes'].replace((id, text)))


@router.route('DELETE', '/notes/<id:int>')
def delete_note(body, id):
    if box.space['notes'].delete(id) is None:
        return 404, {'error': f'no note {id}'}
    return 204, None


class Handler(http.server.BaseHTTPRequestHandler):
    """Parses one request with the standard library and answers with JSON."""

    def handle_one_request(self):
        self.close_connection = True
        super().handle_one_request()

    def serve(self):
        length = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(length)
        try:
            status, payload = router.dispatch(self.command, self.path.split('?')[0], body)
        except (ValueError, KeyError, TypeError) as error:
            status, payload = 400, {'error': f'bad request: {error!r}'}
        data = b'' if payload is None else json.dumps(payload).encode()
        self.send_response(status)
        if data:
            self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = serve

    def log_message(self, format, *args):
        box.log.write(box.LogLevel.INFO, 'notes: ' + format % args)


class Run(exports.Run):
    """The server: wasm.run runs it on a thread of its own."""

    def run(self) -> None:
        host, port = sys.argv[0], int(sys.argv[1])
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
            server.bind((host, port))
            server.listen()
            # A short accept timeout lets wasm.cancel stop the loop.
            server.settimeout(1)
            box.log.write(box.LogLevel.INFO, f'notes: listening on {host}:{port}')
            while True:
                try:
                    connection, address = server.accept()
                except TimeoutError:
                    continue
                with connection:
                    connection.settimeout(5)
                    try:
                        Handler(connection, address, server)
                    except Exception as error:
                        box.log.write(box.LogLevel.WARN, f'notes: {address}: {error!r}')
