"""Private foreground execution protocol. stdin carries payload then a lease.

Closing the lease cancels this command only. The shell keeps its usual stdin,
stdout, stderr, environment and exit status. Persistent services use Docker's
service API and do not belong to this process group.
"""
import base64
import ctypes
import json
import os
import select
import signal
import subprocess
import sys
import threading
import time

token = sys.argv[1]
payload = json.loads(sys.stdin.buffer.readline())
# Adopt orphaned grandchildren so cancelling a shell cannot accumulate zombies
# under a workspace PID 1 that does not reap them. This is process supervision,
# not a new permission or a sandbox boundary. Older runtimes may lack prctl.
try:
    ctypes.CDLL(None).prctl(36, 1, 0, 0, 0)  # PR_SET_CHILD_SUBREAPER
except (AttributeError, OSError):
    pass
env = os.environ.copy()
env["PRISM_EXECUTION_ID"] = token
child = subprocess.Popen([payload["shell"], "-c", payload["command"]],
                         stdin=subprocess.PIPE, env=env, start_new_session=True)

def feed():
    try:
        child.stdin.write(base64.b64decode(payload.get("input") or ""))
        child.stdin.close()
    except (BrokenPipeError, OSError):
        pass

threading.Thread(target=feed, daemon=True).start()

def live_group():
    # The leader remains unreaped until cleanup is confirmed: its PID cannot
    # be reused for another command while we signal/check this group.
    for entry in os.listdir("/proc"):
        if not entry.isdigit():
            continue
        try:
            with open("/proc/" + entry + "/stat") as f:
                fields = f.read().rsplit(")", 1)[1].split()
            if int(fields[2]) == child.pid and fields[0] not in ("Z", "X"):
                return True
        except FileNotFoundError:
            pass
    return False

def reap_cancelled():
    child.wait()
    deadline = time.monotonic() + 0.5
    while time.monotonic() < deadline:
        try:
            pid, _ = os.waitpid(-1, os.WNOHANG)
            if pid == 0:
                time.sleep(0.01)
        except ChildProcessError:
            break

while True:
    # Check the lease before completion, so a concurrent cancellation also
    # stops remaining group members if the shell just exited.
    ready, _, _ = select.select([sys.stdin.buffer], [], [], 0.05)
    if ready:
        sys.stdin.buffer.read(1)
        for sig, grace in ((signal.SIGTERM, 1.0), (signal.SIGKILL, 3.0)):
            try:
                os.killpg(child.pid, sig)
            except ProcessLookupError:
                pass
            deadline = time.monotonic() + grace
            while live_group() and time.monotonic() < deadline:
                time.sleep(0.025)
            if not live_group():
                reap_cancelled()
                sys.stderr.write("\x1ePRISM_EXEC_STOPPED:" + token + "\x1f"); sys.stderr.flush()
                sys.exit(130)
        # Fail closed: callers must not delete data on an unconfirmed stop.
        sys.stderr.write("\x1ePRISM_EXEC_UNCONFIRMED:" + token + "\x1f"); sys.stderr.flush()
        sys.exit(125)
    status = os.waitid(os.P_PID, child.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
    if status is not None:
        rc = child.wait()
        sys.stderr.write("\x1ePRISM_EXEC_FINISHED:" + token + "\x1f"); sys.stderr.flush()
        sys.exit(rc if rc >= 0 else 128 - rc)
