import os
import time
from datetime import datetime

import redis


def main():
    redis_host = os.environ.get("REDIS_HOST", "localhost")
    redis_port = int(os.environ.get("REDIS_PORT", 6379))

    r = redis.Redis(host=redis_host, port=redis_port, decode_responses=True)

    print(f"Connecting to Redis at {redis_host}:{redis_port}")

    script_dir = os.path.dirname(os.path.realpath(__file__))
    log_file_path = os.path.join(script_dir, "redis_monitor.log")
    print(f"Logging to {log_file_path}")

    try:
        with open(log_file_path, "a") as log_file:
            while True:
                try:
                    worker_ids = sorted(r.hkeys("cluster:lifetime_connections"))

                    if not worker_ids:
                        print("No workers found.")
                        time.sleep(5)
                        continue

                    print("\033c", end="")
                    print(
                        f"{'WORKER ID':<38} {'STATUS':<12} {'UPTIME':<10} {'HB':<5} {'CONN (A/L)':<12} {'ENDPOINT':<22} {'CMD'}"
                    )
                    print("-" * 130)

                    for worker_id in worker_ids:
                        worker_key = f"worker:{worker_id}"

                        worker_data = r.hgetall(worker_key)
                        if not worker_data:
                            continue

                        active_connections = (
                            r.hget("cluster:active_connections", worker_id) or 0
                        )
                        lifetime_connections = (
                            r.hget("cluster:lifetime_connections", worker_id) or 0
                        )

                        status = worker_data.get("status", "N/A")
                        endpoint = worker_data.get("endpoint", "N/A")

                        now = time.time()

                        started_at_str = worker_data.get("startedAt", "0")
                        uptime_seconds = (
                            (now - float(started_at_str) / 1000)
                            if started_at_str
                            else 0
                        )
                        uptime = f"{int(uptime_seconds)}s"

                        last_heartbeat_str = worker_data.get("lastHeartbeat", "0")
                        heartbeat_ago_seconds = (
                            (now - float(last_heartbeat_str) / 1000)
                            if last_heartbeat_str
                            else 0
                        )
                        heartbeat = f"{int(heartbeat_ago_seconds)}s"

                        command = r.get(f"worker:cmd:{worker_id}") or ""

                        print(
                            f"{worker_id:<38} {status:<12} {uptime:<10} {heartbeat:<5} {f'{active_connections}/{lifetime_connections}':<12} {endpoint:<22} {command}"
                        )

                        log_timestamp = datetime.now().isoformat()
                        log_line = (
                            f"{log_timestamp} worker_id={worker_id} status={status} uptime={uptime} "
                            f"heartbeat={heartbeat} active_connections={active_connections} "
                            f"lifetime_connections={lifetime_connections} endpoint={endpoint} "
                            f"command='{command}'\n"
                        )
                        log_file.write(log_line)

                    log_file.flush()
                    time.sleep(1)

                except redis.ConnectionError as e:
                    print(f"Connection error: {e}")
                    time.sleep(5)
                except Exception as e:
                    print(f"An error occurred: {e}")
                    time.sleep(5)

    except IOError as e:
        print(f"Could not open log file {log_file_path}: {e}")


if __name__ == "__main__":
    main()
