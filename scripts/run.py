"""Load a local configuration literally, build if needed and run Muse2API."""
import argparse
import base64
import os
from pathlib import Path
import shutil
import subprocess

from config_utils import read_env

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=ROOT / ".env")
    parser.add_argument("--build-only", action="store_true")
    parser.add_argument("--check", action="store_true", help="Validate local configuration without logging in")
    args = parser.parse_args()
    environment = os.environ.copy()
    if not args.build_only:
        try:
            for key, value in read_env(args.config).items():
                environment.setdefault(key, value)
            if len(environment.get("MUSE2API_API_KEY", "")) < 32:
                raise ValueError("api_key_missing_or_short")
            if len(base64.b64decode(environment.get("MUSE2API_SESSION_KEY", ""), validate=True)) != 32:
                raise ValueError("session_key_invalid")
        except Exception as error:
            print("Configuration unavailable or invalid:", type(error).__name__)
            return 1
        if args.check:
            print("Configuration structure is valid. No login or inference was performed.")
            return 0
    binary = ROOT / "bin" / ("muse2api.exe" if os.name == "nt" else "muse2api")
    if args.build_only or not binary.is_file():
        go = shutil.which("go")
        if not go:
            print("Go is required to build the application.")
            return 1
        binary.parent.mkdir(exist_ok=True)
        result = subprocess.run([go, "build", "-trimpath", "-o", str(binary), "./cmd/muse2api"], cwd=ROOT, env=environment)
        if result.returncode:
            return result.returncode
    if args.build_only:
        print("Build completed. No account was accessed.")
        return 0
    return subprocess.run([str(binary), "serve"], cwd=ROOT, env=environment).returncode


if __name__ == "__main__":
    raise SystemExit(main())
