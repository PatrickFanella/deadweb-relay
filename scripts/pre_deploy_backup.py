#!/usr/bin/env python3
"""Host-owned Deadweb rollback snapshot and isolated candidate restore check."""

import fcntl
import hashlib
import html
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import selectors
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import traceback
import uuid


CONTAINER = "deadweb-relay-relay-1"
REPOSITORY = "subculture-collective/deadweb-relay"
ROOT = Path.home() / ".local/state/fleet-deploy/deadweb-relay/backups"
LABEL = "tv.subcult.deadweb-restore"
LIMIT = 256 * 1024**2


def command(args, *, stdin=None, timeout=15):
    result = subprocess.run(args, stdin=stdin, capture_output=True, timeout=timeout)
    if result.returncode:
        with (ROOT / "last-command-failure.log").open("wb") as log:
            log.write(result.stdout + b"\n" + result.stderr)
        (ROOT / "last-command-failure.log").chmod(0o600)
        raise RuntimeError("Host command failed")
    return result.stdout


def inspect(name, kind="container"):
    return json.loads(command(["docker", kind, "inspect", name]))[0]


def save(path, value):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(value, sort_keys=True, indent=2) + "\n")
    temp.chmod(0o600)
    temp.replace(path)


def inventory(path, prefixes=None):
    """Reject unsafe archives; optionally prove original file prefixes survive."""
    result, seen, total = {}, set(), 0
    with tarfile.open(path, "r:") as archive:
        for member in archive:
            name = str(PurePosixPath(member.name))
            if (PurePosixPath(name).is_absolute() or ".." in PurePosixPath(name).parts
                    or name in seen or not (member.isdir() or member.isfile())):
                raise ValueError("Unsafe archive member")
            seen.add(name)
            if member.isdir():
                continue
            total += member.size
            if total > LIMIT or len(seen) > 10000:
                raise ValueError("Archive exceeds backup bound")
            expected = prefixes.get(name) if prefixes is not None else None
            if prefixes is not None and expected is None:
                continue
            size = expected["size"] if expected else member.size
            if member.size < size:
                raise ValueError("Original file was truncated")
            digest, remaining = hashlib.sha256(), size
            with archive.extractfile(member) as stream:
                while remaining:
                    block = stream.read(min(1024**2, remaining))
                    if not block:
                        raise ValueError("Incomplete archive payload")
                    digest.update(block)
                    remaining -= len(block)
            if prefixes is None and name.endswith((".jsonl", ".ndjson")):
                with archive.extractfile(member) as stream:
                    while line := stream.readline(2 * 1024**2 + 1):
                        if len(line) > 2 * 1024**2:
                            raise ValueError("Journal entry exceeds restore bound")
                        if line.strip():
                            json.loads(line)
            result[name] = {"size": size, "sha256": digest.hexdigest()}
    if prefixes is not None and result != prefixes:
        raise ValueError("Original payloads did not survive restore")
    return result


def copy_archive(container, path):
    """Bound the Docker archive stream without buffering user data in memory."""
    deadline, size = time.monotonic() + 20, 0
    with path.open("wb") as out, path.with_suffix(".stderr").open("wb") as errors:
        with subprocess.Popen(["docker", "cp", container + ":/app/data/.", "-"],
                              stdout=subprocess.PIPE, stderr=errors) as process:
            try:
                with selectors.DefaultSelector() as selector:
                    selector.register(process.stdout, selectors.EVENT_READ)
                    while True:
                        left = deadline - time.monotonic()
                        if left <= 0 or not selector.select(left):
                            raise TimeoutError("Archive stream timed out")
                        block = os.read(process.stdout.fileno(), 1024**2)
                        if not block:
                            break
                        size += len(block)
                        if size > LIMIT + 2 * 1024**2:
                            raise ValueError("Archive stream exceeds backup bound")
                        out.write(block)
                if process.wait(timeout=max(0.1, deadline - time.monotonic())):
                    raise RuntimeError("Docker archive failed")
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
    path.chmod(0o600)


def arm_watchdog(container_id, seconds=45):
    if command(["loginctl", "show-user", str(os.getuid()), "-p", "Linger", "--value"]).strip() != b"yes":
        raise RuntimeError("Persistent user manager is required for recovery")
    unit = "deadweb-backup-unpause-" + uuid.uuid4().hex
    command(["systemd-run", "--user", "--unit=" + unit, "--collect",
             "--on-active=" + str(seconds) + "s", "--timer-property=AccuracySec=1s",
             "--property=TimeoutStartSec=10s", "/usr/bin/docker", "unpause", container_id])
    command(["systemctl", "--user", "is-active", "--quiet", unit + ".timer"])
    return unit


def cleanup(record):
    # Identifiers are recorded before creation; only this run's labelled fixtures
    # may be removed. The production container is only ever unpaused by exact ID.
    if record.get("watchdog"):
        result = subprocess.run(["docker", "inspect", record["source_container"]], capture_output=True)
        if result.returncode == 0 and json.loads(result.stdout)[0]["State"]["Paused"]:
            command(["docker", "unpause", record["source_container"]])
        subprocess.run(["systemctl", "--user", "stop", record["watchdog"] + ".timer"], capture_output=True)
    for name, kind, args in [(record["restore_container"], "container", ["docker", "rm", "-f"]),
                             (record["restore_volume"], "volume", ["docker", "volume", "rm"])]:
        result = subprocess.run(["docker", kind, "inspect", name], capture_output=True)
        if result.returncode:
            continue
        item = json.loads(result.stdout)[0]
        labels = item.get("Labels", {}) if kind == "volume" else item["Config"].get("Labels", {})
        if (labels or {}).get(LABEL) != record["id"]:
            raise RuntimeError("Fixture ownership differs")
        command(args + [item["Id"] if kind == "container" else name])


def recover():
    for path in ROOT.glob("*/receipt.json"):
        record = json.loads(path.read_text())
        if record.get("status") in ("complete", "recovered"):
            continue
        if not re.fullmatch(r"[0-9a-f]{32}", record.get("id", "")):
            raise RuntimeError("Unrecognized backup receipt")
        cleanup(record)
        record["status"] = "recovered"
        save(path, record)


def http(container, path):
    return command(["docker", "exec", container, "wget", "-q", "-T", "3", "-O", "-",
                    "--header=Accept: text/html", "http://127.0.0.1:8080" + path], timeout=5)


def backup(qualify_current=False):
    recover()
    original = inspect(CONTAINER)
    if not original["State"]["Running"] or original["State"]["Paused"]:
        raise RuntimeError("Source is not available for a bounded snapshot")
    if [(m["Source"], m["Destination"], m["RW"]) for m in original["Mounts"]] != [("/opt/deadweb-relay/data", "/app/data", True)]:
        raise RuntimeError("Persistent mount differs")
    if command(["docker", "exec", original["Id"], "id", "-u"]).strip() != b"1000":
        raise RuntimeError("Serving UID differs")
    if shutil.disk_usage(ROOT).free < 2 * 1024**3:
        raise RuntimeError("Insufficient backup headroom")
    sizes = command(["docker", "exec", original["Id"], "sh", "-c",
                     "find /app/data -type f -exec stat -c %s {} \\;"])
    if sum(int(v) for v in sizes.split()) > LIMIT:
        raise RuntimeError("Data exceeds qualified snapshot size")
    if qualify_current:
        image = original["Image"]
    else:
        revision = command(["git", "ls-remote", "--exit-code", "https://git.subcult.tv/" + REPOSITORY + ".git", "refs/heads/main"]).decode().split()[0]
        if not re.fullmatch(r"[0-9a-f]{40}", revision):
            raise RuntimeError("Invalid current main")
        candidate = inspect("fleet-deploy/deadweb-relay/relay:" + revision, "image")
        labels = candidate["Config"].get("Labels") or {}
        if labels.get("org.opencontainers.image.revision") != revision or labels.get("org.opencontainers.image.source") != "https://git.subcult.tv/" + REPOSITORY:
            raise RuntimeError("Candidate identity differs")
        image = candidate["Id"]
    reference_hash = hashlib.sha256(http(original["Id"], "/reference/http-relay.txt")).hexdigest()
    token = uuid.uuid4().hex
    folder = ROOT / (time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + "-" + token)
    folder.mkdir(mode=0o700)
    receipt = folder / "receipt.json"
    record = {"id": token, "status": "prepared", "source_container": original["Id"],
              "restore_container": "deadweb-restore-" + token, "restore_volume": "deadweb-restore-" + token,
              "image": image, "mode": "live-qualification" if qualify_current else "candidate"}
    save(receipt, record)
    try:
        record["watchdog"] = arm_watchdog(original["Id"])
        save(receipt, record)
        command(["docker", "pause", original["Id"]])
        try:
            if inspect(CONTAINER)["Id"] != original["Id"]:
                raise RuntimeError("Source changed before snapshot")
            copy_archive(original["Id"], folder / "data.tar")
        finally:
            command(["docker", "unpause", original["Id"]])
        subprocess.run(["systemctl", "--user", "stop", record["watchdog"] + ".timer"], capture_output=True)
        record.pop("watchdog")
        save(receipt, record)
        if inspect(CONTAINER)["Id"] != original["Id"]:
            raise RuntimeError("Source changed during snapshot")
        payloads = inventory(folder / "data.tar")
        save(folder / "manifest.json", payloads)
        public = dict(v.split("=", 1) for v in original["Config"].get("Env", []))
        with tempfile.TemporaryDirectory(prefix="restore-", dir=folder) as scratch:
            scratch = Path(scratch)
            env = {"ADDR": ":8080", "DATA_DIR": "/app/data", "SITE_NAME": public.get("SITE_NAME", "Patrick's WWW Page"),
                   "BASE_URL": public.get("BASE_URL", "https://www2.onnwee.me"),
                   "ADMIN_TOKEN": secrets.token_hex(32), "OBSERVATORY_HMAC_KEY": secrets.token_hex(32)}
            env_path = scratch / "fixture.env"
            env_path.write_text("".join(k + "=" + v + "\n" for k, v in env.items()))
            env_path.chmod(0o600)
            command(["docker", "volume", "create", "--label", LABEL + "=" + token, record["restore_volume"]])
            command(["docker", "create", "--name", record["restore_container"], "--label", LABEL + "=" + token,
                     "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
                     "--pids-limit", "128", "--tmpfs", "/tmp:rw,mode=1777", "--env-file", str(env_path),
                     "--mount", "type=volume,src=" + record["restore_volume"] + ",dst=/app/data", image])
            with (folder / "data.tar").open("rb") as archive:
                command(["docker", "cp", "-a", "-", record["restore_container"] + ":/app/data"], stdin=archive)
            copy_archive(record["restore_container"], scratch / "before.tar")
            if inventory(scratch / "before.tar") != payloads:
                raise RuntimeError("Restored bytes differ")
            command(["docker", "start", record["restore_container"]])
            for attempt in range(10):
                try:
                    if http(record["restore_container"], "/healthz").strip() == b"ok":
                        break
                except (RuntimeError, subprocess.TimeoutExpired):
                    pass
                time.sleep(0.2)
            else:
                raise RuntimeError("Isolated restore is not healthy")
            if command(["docker", "exec", record["restore_container"], "id", "-u"]).strip() != b"1000":
                raise RuntimeError("Candidate UID differs")
            if env["SITE_NAME"] not in html.unescape(http(record["restore_container"], "/").decode()):
                raise RuntimeError("Configured site identity differs")
            if hashlib.sha256(http(record["restore_container"], "/reference/http-relay.txt")).hexdigest() != reference_hash:
                raise RuntimeError("Authored reference differs")
            copy_archive(record["restore_container"], scratch / "after.tar")
            inventory(scratch / "after.tar", payloads)
        record.update(status="verified", files=len(payloads), payload_bytes=sum(v["size"] for v in payloads.values()),
                      archive_sha256=hashlib.sha256((folder / "data.tar").read_bytes()).hexdigest())
        save(receipt, record)
    finally:
        cleanup(record)
    record["status"] = "complete"
    save(receipt, record)
    return {"backup": str(folder / "data.tar"), "receipt": str(receipt), "files": record["files"],
            "archive_sha256": record["archive_sha256"], "isolated_restore": "passed", "mode": record["mode"]}


def main():
    os.umask(0o077)
    os.environ.setdefault("XDG_RUNTIME_DIR", "/run/user/" + str(os.getuid()))
    ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    if sys.argv[1:] not in ([], ["--qualify-current"], ["--recover"]):
        raise SystemExit("Unsupported backup mode")
    with (ROOT / "backup.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if sys.argv[1:] == ["--recover"]:
            recover()
            print(json.dumps({"recovery": "complete"}))
        else:
            print(json.dumps(backup(sys.argv[1:] == ["--qualify-current"])))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
        with (ROOT / "last-failure.log").open("w") as log:
            traceback.print_exc(file=log)
        (ROOT / "last-failure.log").chmod(0o600)
        print("Backup/restore qualification failed; inspect private host receipts", file=sys.stderr)
        raise SystemExit(1)
