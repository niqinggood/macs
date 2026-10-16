"""Submit one MACS task, stream progress, and poll to durable completion."""

import os

from macs_agentos import Client


def main() -> None:
    client = Client(
        os.environ.get("MACS_BASE_URL", "http://127.0.0.1:8090"),
        bearer_token=os.environ["MACS_SERVICE_TOKEN"],
        act_as_user_id=os.environ.get("MACS_USER_ID", ""),
    )
    task = client.submit_task(
        {
            "role_id": os.environ.get("MACS_ROLE_ID", "default"),
            "message": "请分析这项业务请求并返回结果。",
        },
        idempotency_key=os.environ.get(
            "MACS_IDEMPOTENCY_KEY", "python-sdk-example-001"
        ),
    )
    print("accepted", task["task_id"])

    final = client.wait_task(
        task["task_id"],
        timeout=30 * 60,
        on_progress=lambda progress: print(
            "progress", progress.get("step_name"), progress.get("message")
        ),
    )
    print("finished", final["status"])
    for artifact in final.get("artifacts", []):
        print("artifact", artifact.get("download_url"))


if __name__ == "__main__":
    main()
