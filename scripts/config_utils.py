"""Literal .env parsing and local permission helpers; never evaluate shell code."""
from pathlib import Path
import csv
import io
import os
import subprocess


def private_path(path: Path, directory: bool = False) -> None:
    if path.is_symlink() or getattr(path, "is_junction", lambda: False)():
        raise ValueError("private_path_must_not_be_symlink")
    if os.name != "nt":
        path.chmod(0o700 if directory else 0o600)
        return
    identity = subprocess.run(["whoami", "/user", "/fo", "csv", "/nh"], capture_output=True, check=True)
    rows = list(csv.reader(io.StringIO(identity.stdout.decode(errors="replace"))))
    sid = next((cell.strip() for row in rows for cell in row if cell.strip().startswith("S-1-")), None)
    if not sid:
        raise RuntimeError("current_user_sid_unavailable")
    suffix = "(OI)(CI)F" if directory else "F"
    subprocess.run(["icacls", str(path), "/inheritance:r", "/grant:r", f"*{sid}:({suffix})" if not directory else f"*{sid}:(OI)(CI)F",
                    f"*S-1-5-18:({suffix})" if not directory else "*S-1-5-18:(OI)(CI)F"],
                   capture_output=True, check=True)


def read_env(path: Path) -> dict[str, str]:
    result = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8-sig").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.partition("=")
        if not sep or not key.startswith("MUSE2API_") or not key.replace("_", "").isalnum():
            raise ValueError(f"invalid_config_line_{number}")
        if key in result:
            raise ValueError(f"duplicate_config_key_line_{number}")
        if value.startswith(("'", '"')):
            if len(value) < 2 or value[-1] != value[0]:
                raise ValueError(f"unclosed_config_quote_line_{number}")
            value = value[1:-1]
        if "\0" in value or "\r" in value or "\n" in value:
            raise ValueError(f"invalid_config_value_line_{number}")
        result[key] = value
    return result
