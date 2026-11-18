"""Generate a private configuration without printing credentials."""
import argparse
import base64
import os
from pathlib import Path
import secrets

from config_utils import private_path

ROOT = Path(__file__).resolve().parent.parent


def initialize(output: Path, data_dir: Path) -> None:
    output = output.absolute()
    data_dir = data_dir.absolute()
    if output.exists() or output.is_symlink():
        raise FileExistsError("configuration_exists_no_overwrite")
    if not output.parent.is_dir():
        raise ValueError("configuration_parent_directory_missing")
    data_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    private_path(data_dir, directory=True)
    descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        # Apply the private ACL before any secret is written, also on Windows.
        private_path(output)
        values = {
            "MUSE2API_API_KEY": secrets.token_urlsafe(48),
            "MUSE2API_SESSION_KEY": base64.b64encode(secrets.token_bytes(32)).decode("ascii"),
            "MUSE2API_DATA_DIR": str(data_dir),
            "MUSE2API_LISTEN": "127.0.0.1:18688",
            "MUSE2API_ADMIN_LISTEN": "127.0.0.1:18689",
            "MUSE2API_PROXY_URL": "",
            "MUSE2API_GENERATE_TIMEOUT": "30m",
        }
        text = "# Private runtime configuration. Never commit or share this file.\n"
        text += "\n".join(key + "=" + value for key, value in values.items()) + "\n"
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as stream:
            descriptor = -1
            stream.write(text)
    finally:
        if descriptor >= 0:
            os.close(descriptor)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / ".env")
    parser.add_argument("--data-dir", type=Path, default=ROOT / "runtime")
    args = parser.parse_args()
    try:
        initialize(args.output, args.data_dir)
    except Exception as error:
        print("Configuration was not initialized:", type(error).__name__)
        return 1
    print("Private configuration created. Credentials were not printed. Keep the file outside source releases.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
