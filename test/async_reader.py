import io
import queue
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Optional

"""
The `async_reader` module provides the `AsyncProcessReader` class for asynchronously reading
the output of processes launched using `subprocess.Popen`.
The class allows non-blocking reading of lines from `stdout` and `stderr` of an external process
in separate threads, placing them in queues for subsequent processing.
This is useful for integration testing, logging, and monitoring the output of
long-running or interactive processes.

Features:
- Asynchronous reading of `stdout` and `stderr` in real time without blocking the main thread.
- Waiting for a specific string (token) to appear in the output with a timeout.
- Safe termination and forced killing of the process.
- Retrieving accumulated output at any point in time.
- Flexible working directory handling and support for `pathlib.Path` paths.


Application:
The module is intended for use in integration tests with the following scenario:
we expect that the running process will output the expected data to `stdout`, and
errors will be logged to `stderr`.

1. Create an instance of `AsyncProcessReader`, passing the command to run and the working directory.
2. Use the `stdout_wait_for` method to wait for a specific pattern to appear in the `stdout` stream.
3. Once you get it, you can compare the output with the expected result.
4. It is recommended to process the data in `stderr` a little differently,
only checking for the presence of the necessary patterns.
This will reduce dependence on the appearance of other logs in this stream.
"""

_StreamQueue = queue.Queue[Optional[str]]


class AsyncProcessReader:
    """
    Asynchronous reader that reads output lines from a `subprocess` streams and
    places them on a queue to handle. Used for asynchronous reading of `stdout` and `stderr`.
    """

    __q_stdout: _StreamQueue
    __q_stderr: _StreamQueue
    __process: subprocess.Popen
    __stdout_thread: threading.Thread
    __stderr_thread: threading.Thread
    __stop_event: threading.Event

    def __init__(self, cmd: list[str | Path], work_dir: Path):
        self.cmd = cmd
        self.__q_stdout = queue.Queue()
        self.__q_stderr = queue.Queue()
        self.__stop_event = threading.Event()

        self.__process = subprocess.Popen(
            cmd,
            cwd=work_dir,
            stderr=subprocess.PIPE,
            stdout=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        self.__stdout_thread = self.__run_reader(self.__process.stdout, self.__q_stdout)
        self.__stderr_thread = self.__run_reader(self.__process.stderr, self.__q_stderr)

    def __run_reader(self, stream, output_queue: _StreamQueue) -> threading.Thread:
        assert stream is not None, "Process stream is None"

        thread = threading.Thread(
            target=self.__stream_reader_thread_target,
            args=(stream, output_queue),
        )
        thread.start()
        return thread

    @property
    def returncode(self) -> Optional[int]:
        return self.__process.returncode

    @property
    def stderr(self) -> list[str]:
        return self.__read_stream(self.__q_stderr)

    @property
    def stdout(self) -> list[str]:
        return self.__read_stream(self.__q_stdout)

    def __read_stream(self, q: _StreamQueue) -> list[str]:
        """
        Reads all lines from the queue until it is empty.
        Returns a list of lines.
        """
        lines = []
        while not q.empty():
            line = q.get_nowait()
            if line is None:
                break
            lines.append(line)
        return lines

    def stdout_wait_for(self, expected: str, timeout: float = 5.0) -> tuple[list[str], bool]:
        """
        Expects a token in stdout, collecting all lines.
        Returns a list of lines and a flag if a token was found.
        """
        return self.__wait_for(self.__q_stdout, expected, timeout)

    def stderr_wait_for(self, expected: str, timeout: float = 5.0) -> tuple[list[str], bool]:
        """
        Expects a token in stderr, collecting all lines.
        Returns a list of lines and a flag if a token was found.
        """
        return self.__wait_for(self.__q_stderr, expected, timeout)

    def send_signal(self, sig: int) -> None:
        if not self._isExited():
            self.__process.send_signal(sig)

    def pStop(self) -> None:
        """Stop process gracefully."""
        if not self._isExited():
            self.__stop_event.set()
            self.__stdout_thread.join(timeout=5.0)
            self.__stderr_thread.join(timeout=5.0)

    def pKill(self) -> None:
        """Force terminates the process."""
        if not self._isExited():
            try:
                self.__process.kill()
                self.pWait()
            except ProcessLookupError:
                pass

    def pWait(self, timeout: float = 5.0) -> Optional[int]:
        """
        Waits for the process to finish, with an optional timeout.
        Returns the return code of the process or None if it is still running.
        """
        try:
            return self.__process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            return None

    def _isExited(self) -> bool:
        """
        Checks if the process has exited.
        Returns True if the process has exited, False otherwise.
        """
        return self.__process.poll() is not None

    def __wait_for(
        self,
        q: _StreamQueue,
        expected: str,
        timeout: float = 10.0,
    ) -> tuple[list[str], bool]:
        stop_time = time.monotonic() + timeout
        wait_close = False
        FINAL_TIMEOUT = 1.0  # After process exit, tiny wait to collect remaining lines.

        marker_found = False
        lines: list[str] = []

        while time.monotonic() < stop_time:
            try:
                line = q.get(timeout=0.1)
                if line is None:
                    break

                lines.append(line)
                if expected in line:
                    marker_found = True
                    break

            except queue.Empty:
                if self._isExited() and q.empty():
                    if not wait_close:
                        wait_close = True
                        next_timeout = time.monotonic() + FINAL_TIMEOUT
                        if next_timeout < stop_time:
                            stop_time = next_timeout
                continue

        return lines, marker_found

    def __stream_reader_thread_target(
        self,
        stream: io.TextIOWrapper,
        output_queue: _StreamQueue,
    ) -> None:
        """
        Reads strings from `stream` and puts them into `output_queue`.
        Ends when `stop_event` is set or the stream is closed.
        """
        try:
            for line in iter(stream.readline, ""):
                if self.__stop_event.is_set() and stream.closed:
                    break
                output_queue.put(line)
                if self.__stop_event.is_set() and output_queue.empty():
                    break

        except ValueError:
            # May occur if stream is closed while readline is in progress.
            pass

        except Exception as e:
            print(f"Stream reader error: {type(e).__name__}: {e}", file=sys.stderr)

        finally:
            output_queue.put(None)  # Signal that reading is complete.
