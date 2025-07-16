import signal
import sys

# cSpell:words signum

def handler(signum, frame):
    print("sigusr1", flush=True)

def int_handler(signum, frame):
    print("interrupted", flush=True)
    sys.exit(10)

signal.signal(signal.SIGUSR1, handler)
signal.signal(signal.SIGINT, int_handler)
signal.signal(signal.SIGTERM, int_handler)

print("started", flush=True)
while True:
    signal.pause()
