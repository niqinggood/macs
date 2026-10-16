"""Small diagnostic wrapper for the bundled Agent Reach skill runtime."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys


def main() -> int:
    executable = shutil.which("agent-reach")
    if not executable:
        print(json.dumps({"installed": False, "channels": {}}, ensure_ascii=False))
        return 0
    completed = subprocess.run(
        [executable, "doctor", "--json"],
        capture_output=True,
        timeout=45,
        check=False,
    )
    stdout = completed.stdout.decode("utf-8", errors="replace")
    stderr = completed.stderr.decode("utf-8", errors="replace")
    if completed.returncode != 0:
        print(
            json.dumps(
                {
                    "installed": True,
                    "error": (stderr or stdout).strip(),
                },
                ensure_ascii=False,
            )
        )
        return completed.returncode
    try:
        channels = json.loads(stdout)
    except json.JSONDecodeError as exc:
        print(json.dumps({"installed": True, "error": str(exc)}, ensure_ascii=False))
        return 2
    print(json.dumps({"installed": True, "channels": channels}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
