#!/usr/bin/env python3
"""Run a command against new, loopback-only disposable PostgreSQL and Redis."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import shutil
import shlex
import signal
import socket
import subprocess
import tempfile
import time


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--postgres-bin", default=os.environ.get("POSTGRES_BIN", ""))
    parser.add_argument("--output", help="Directory for service logs and command metadata")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("supply a command after --")
    binaries = {}
    for name in ("initdb", "pg_ctl", "postgres", "pg_dump", "pg_restore", "redis-server"):
        binary = str(pathlib.Path(args.postgres_bin) / name) if args.postgres_bin and name != "redis-server" else shutil.which(name)
        if not binary or not pathlib.Path(binary).is_file():
            parser.error(f"{name} is unavailable; install test services or set POSTGRES_BIN")
        binaries[name] = binary
    output = pathlib.Path(args.output or tempfile.mkdtemp(prefix="gateway-test-evidence-" )).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    pg_port, redis_port = port(), port()
    while redis_port == pg_port:
        redis_port = port()
    metadata = {"command": command, "postgres": subprocess.check_output([binaries["postgres"], "--version"], text=True).strip(), "redis": subprocess.check_output([binaries["redis-server"], "--version"], text=True).strip(), "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "isolation": "fresh cluster and Redis process; loopback only; no existing services"}
    root = pathlib.Path(__file__).resolve().parent.parent
    metadata["commit"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    metadata["working_tree"] = subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).splitlines()
    source = hashlib.sha256()
    files = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root).split(b"\0")
    for name in sorted(filter(None, files)):
        path = root / os.fsdecode(name)
        if path.is_file():
            source.update(name + b"\0" + path.read_bytes())
    metadata["source_sha256"] = source.hexdigest()
    metadata["machine"] = {"platform": platform.platform(), "logical_cpus": os.cpu_count()}
    print(f"Test evidence: {output}", flush=True)
    with tempfile.TemporaryDirectory(prefix="gateway-isolated-") as directory:
        root = pathlib.Path(directory)
        os.chmod(root, 0o700)
        pg_data = root / "postgres"
        pg_started, redis_process = False, None
        try:
            with (output / "initdb.log").open("w") as log:
                subprocess.run([binaries["initdb"], "-D", str(pg_data), "-U", "gateway_test", "--auth=trust", "--encoding=UTF8", "--no-locale"], stdout=log, stderr=subprocess.STDOUT, check=True)
            pg_options = shlex.join(["-h", "127.0.0.1", "-p", str(pg_port), "-k", str(root), "-c", "max_connections=150"])
            subprocess.run([binaries["pg_ctl"], "-D", str(pg_data), "-l", str(output / "postgres.log"), "-o", pg_options, "-w", "start"], check=True, stdout=subprocess.DEVNULL)
            pg_started = True
            metadata["postgres_pid"] = int((pg_data / "postmaster.pid").read_text().splitlines()[0])
            with (output / "redis.log").open("w") as log:
                redis_process = subprocess.Popen([binaries["redis-server"], "--bind", "127.0.0.1", "--port", str(redis_port), "--dir", str(root), "--save", "", "--appendonly", "no"], stdout=log, stderr=subprocess.STDOUT)
            metadata["redis_pid"] = redis_process.pid
            deadline = time.monotonic() + 10
            while True:
                if redis_process.poll() is not None:
                    raise RuntimeError("isolated Redis failed to start; inspect redis.log")
                try:
                    with socket.create_connection(("127.0.0.1", redis_port), timeout=0.2) as connection:
                        connection.sendall(b"*1\r\n$4\r\nPING\r\n")
                        if connection.recv(32).startswith(b"+PONG"):
                            break
                except OSError:
                    pass
                if time.monotonic() > deadline:
                    raise RuntimeError("isolated Redis readiness timed out")
                time.sleep(0.05)
            env = os.environ.copy()
            # Always replace inherited URLs: never run against an existing database.
            env["GATEWAY_TEST_DATABASE_URL"] = f"postgres://gateway_test@127.0.0.1:{pg_port}/postgres?sslmode=disable"
            env["GATEWAY_TEST_REDIS_URL"] = f"redis://127.0.0.1:{redis_port}/0"
            control = root / "services-control.json"
            control.write_text(json.dumps({"data_dir": str(pg_data), "log": str(output / "postgres.log"), "database_url": env["GATEWAY_TEST_DATABASE_URL"], "postgres_options": pg_options, "pg_ctl": binaries["pg_ctl"], "pg_dump": binaries["pg_dump"], "pg_restore": binaries["pg_restore"]}))
            os.chmod(control, 0o600)
            env["GATEWAY_TEST_NATIVE_SERVICES"] = str(control)
            env.setdefault("GATEWAY_PERF_OUTPUT", str(output))
            with (output / "command.log").open("wb") as log:
                # A separate group lets interruption clean up Go test binaries too.
                process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    for line in process.stdout:
                        log.write(line)
                        print(line.decode(errors="replace"), end="", flush=True)
                    code = process.wait()
                finally:
                    try:
                        os.killpg(process.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                    if process.poll() is None:
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            os.killpg(process.pid, signal.SIGKILL)
                            process.wait()
            metadata["exit_code"] = code
            return code
        except BaseException as error:
            metadata["error"] = str(error) or type(error).__name__
            raise
        finally:
            if redis_process is not None:
                redis_process.terminate()
                try:
                    redis_process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    redis_process.kill()
                    redis_process.wait()
            try:
                if pg_started:
                    subprocess.run([binaries["pg_ctl"], "-D", str(pg_data), "-m", "immediate", "-w", "stop"], stdout=subprocess.DEVNULL, check=True)
            except subprocess.CalledProcessError as error:
                metadata["cleanup_error"] = str(error)
                metadata["exit_code"] = 1
                raise
            finally:
                metadata["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                (output / "services.json").write_text(json.dumps(metadata, indent=2) + "\n")


if __name__ == "__main__":
    def interrupted(signum, frame):
        raise KeyboardInterrupt(f"interrupted by signal {signum}")
    signal.signal(signal.SIGTERM, interrupted)
    raise SystemExit(main())
