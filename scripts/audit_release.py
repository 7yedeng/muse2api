"""Audit the exact Git upload index without printing matched private values."""
import argparse
from datetime import datetime, timezone
import hashlib
import ipaddress
import json
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess

ROOT_FILES = {".gitignore", ".dockerignore", ".env.example", "Dockerfile", "LICENSE", "NOTICE.md", "README.md", "PLAN.md", "CONTRIBUTING.md", "go.mod", "go.sum"}
ROOT_DIRS = {"cmd", "internal", "docs", "deploy", "skills", "browser-extension", "scripts", "tests", "LICENSES", ".github"}
PRIVATE_DIRS = {"env", "private", "runtime", "data", "state", "secrets", "client-state", "vendor", "bin", "dist", "release", ".cache", ".ssh", ".codex", ".claude", "__pycache__", ".venv", "backups"}
PRIVATE_NAMES = {"cookies.json", "session.json", "pairing.json", "identity.json", "id_rsa", "id_ed25519", "research.env"}
PRIVATE_SUFFIXES = {".enc", ".dpapi", ".pem", ".key", ".p12", ".log", ".db", ".sqlite", ".sqlite3", ".mp4", ".mp3", ".wav", ".exe", ".zip", ".7z", ".dmp"}
RULES = {
    "private-key": re.compile(r"-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----"),
    "provider-token": re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9_-]{24,}\b"),
    "jwt": re.compile(r"\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b"),
    "personal-windows-path": re.compile(r"\b[A-Za-z]:[\\/](?:Users|Projects|dev)[\\/]", re.I),
    "personal-unix-path": re.compile(r"/(?:Users|home)/[A-Za-z0-9_.-]+/"),
    "credential-literal": re.compile(r'''(?i)["']?(?:[A-Za-z0-9_]*(?:api|session)[_-]?key|password|hatch_sess|hatch_native_auth_device|hatch_vml|refresh_token|access_token)["']?\s*[=:]\s*["']([^"'\r\n]{20,})["']'''),
    "bearer-literal": re.compile(r"(?i)\bBearer\s+([A-Za-z0-9._~+/-]{20,}=*)"),
    "cookie-header-literal": re.compile(r'''(?i)\bhatch_(?:sess|native_auth_device|vml)\s*=\s*([^\s;"']{8,})'''),
}
EMAIL = re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b")
IPV4 = re.compile(r"(?<![\w.])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![\w.])")
ENV_SECRET = re.compile(r"^\s*(?:export\s+)?MUSE2API_(?:API_KEY|SESSION_KEY)\s*=\s*(.+)$")
YAML_SECRET = re.compile(r'''(?i)^\s*["']?(?:MUSE2API_API_KEY|MUSE2API_SESSION_KEY|api_key|session_key|hatch_sess|hatch_native_auth_device|hatch_vml|refresh_token|access_token)["']?\s*:\s*([A-Za-z0-9_~+/-]{16,}={0,2})(?=\s*(?:[,#}]|$))''')


def path_problem(relative: str) -> str | None:
    path = PurePosixPath(relative)
    if path.is_absolute() or ".." in path.parts or "\\" in relative:
        return "unsafe-path"
    if not path.parts or (len(path.parts) == 1 and relative not in ROOT_FILES) or (len(path.parts) > 1 and path.parts[0] not in ROOT_DIRS):
        return "not-in-source-allowlist"
    if any(part.lower() in PRIVATE_DIRS for part in path.parts[:-1]):
        return "private-directory"
    name = path.name.lower()
    if name in PRIVATE_NAMES or (name == ".env.example" and relative != ".env.example") or (name != ".env.example" and (name == ".env" or name.startswith(".env.") or name.endswith(".env"))):
        return "private-configuration"
    if path.suffix.lower() in PRIVATE_SUFFIXES:
        return "private-or-build-artifact"
    return None


def fake_literal(value: str) -> bool:
    lower = value.lower()
    return (lower in {"your_api_key", "your_key", "your_muse2api_api_key", "replace_with_your_own_key", "your-local-secret"}
            or re.fullmatch(r"\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*\}|env:[A-Za-z_][A-Za-z0-9_]*)", value) is not None)


def inspect_text(text: str, private_patterns: list[str]) -> list[tuple[int, str]]:
    found = []
    for number, line in enumerate(text.splitlines(), 1):
        assigned = ENV_SECRET.match(line)
        if assigned and not fake_literal(assigned.group(1).strip().strip("\"'")):
            found.append((number, "nonempty-secret-environment-assignment"))
        bare = YAML_SECRET.match(line)
        if bare and not fake_literal(bare.group(1)):
            found.append((number, "bare-secret-configuration-value"))
        for name, rule in RULES.items():
            for match in rule.finditer(line):
                if not (name in {"credential-literal", "bearer-literal", "cookie-header-literal"} and fake_literal(match.group(1))):
                    found.append((number, name))
        if any(value and value.casefold() in line.casefold() for value in private_patterns):
            found.append((number, "private-identifier"))
        for address in EMAIL.findall(line):
            host = address.rsplit("@", 1)[1].lower()
            if host not in {"example.com", "example.org", "example.net", "users.noreply.github.com"} and not host.endswith((".example", ".example.com", ".example.org", ".example.net")):
                found.append((number, "non-example-email"))
        for raw in IPV4.findall(line):
            try:
                address = ipaddress.ip_address(raw)
            except ValueError:
                continue
            if str(address) in {"0.0.0.0", "127.0.0.1"} or address in ipaddress.ip_network("192.0.2.0/24") or address in ipaddress.ip_network("198.51.100.0/24") or address in ipaddress.ip_network("203.0.113.0/24"):
                continue
            found.append((number, "non-example-ip"))
    return sorted(set(found))


def git_index(root: Path) -> list[tuple[str, str, bytes]]:
    output = subprocess.run(["git", "ls-files", "--stage", "-z"], cwd=root, capture_output=True, check=True).stdout
    items = []
    for raw in output.split(b"\0"):
        if not raw:
            continue
        metadata, relative = raw.split(b"\t", 1)
        mode, object_id, stage = metadata.decode("ascii").split()
        if stage != "0":
            raise ValueError("unmerged_index")
        path = relative.decode("utf-8")
        # Read the actual staged blob, not a possibly different working file.
        content = b"" if path_problem(path) or mode not in {"100644", "100755"} else subprocess.run(
            ["git", "cat-file", "blob", object_id], cwd=root, capture_output=True, check=True).stdout
        items.append((path, mode, content))
    return items


def inspect_index(root: Path, private_patterns: list[str]) -> dict:
    violations, manifest = [], []
    for relative, mode, content in git_index(root):
        problem = path_problem(relative)
        if mode not in {"100644", "100755"}:
            problem = "symlink-submodule-or-non-file"
        candidate = root / relative
        if not problem:
            for parent in [candidate, *candidate.parents]:
                if parent == root.parent:
                    break
                info = parent.lstat()
                if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400):
                    problem = "reparse-or-symlink"
                    break
        if problem:
            violations.append({"file": relative, "line": 0, "rule": problem})
            continue
        try:
            text = content.decode("utf-8")
            if b"\0" in content:
                raise UnicodeError()
        except UnicodeError:
            violations.append({"file": relative, "line": 0, "rule": "non-text-source"})
            continue
        violations.extend({"file": relative, "line": line, "rule": rule} for line, rule in inspect_text(text, private_patterns))
        manifest.append({"path": relative, "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest()})
    return {"observed_at": datetime.now(timezone.utc).isoformat(), "scope": "exact_git_index",
            "passed": bool(manifest) and not violations, "file_count": len(manifest),
            "violations": violations, "files": manifest}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--private-pattern-file", type=Path, help="Private local deny-list outside the repository; values are never printed")
    parser.add_argument("--manifest", type=Path, help="Write a review manifest outside the repository")
    args = parser.parse_args()
    root = args.root.resolve()
    for path in (args.private_pattern_file, args.manifest):
        if path and path.resolve().is_relative_to(root):
            parser.error("private audit inputs and outputs must be outside the release repository")
    patterns = args.private_pattern_file.read_text(encoding="utf-8").splitlines() if args.private_pattern_file else []
    try:
        report = inspect_index(root, patterns)
    except Exception as error:
        print(json.dumps({"passed": False, "error": type(error).__name__, "scope": "exact_git_index"}))
        return 1
    if args.manifest:
        args.manifest.parent.mkdir(parents=True, exist_ok=True)
        args.manifest.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(json.dumps({key: value for key, value in report.items() if key != "files"}, ensure_ascii=False))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
