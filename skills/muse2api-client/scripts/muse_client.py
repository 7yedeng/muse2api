#!/usr/bin/env python3
"""Standard-library client for a caller's own Muse2API endpoint and key."""
from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import hashlib
from http.client import HTTPException
import json
import mimetypes
import os
from pathlib import Path
import re
import stat
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener
import uuid

MAX_RESPONSE = 64 * 1024 * 1024
MAX_RECEIPT = 1024 * 1024
MAX_IMAGE_BYTES = 10 * 1024 * 1024
MAX_IMAGES = 4
MAX_TOTAL_IMAGE_BYTES = 24 * 1024 * 1024
MAX_REQUEST_BYTES = 32 * 1024 * 1024
SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")
RECEIPT_FILE = re.compile(r"^[a-f0-9]{64}\.json$")
MEDIA_STATES = {"queued", "in_progress", "completed", "failed", "unknown"}


class ClientError(Exception):
    def __init__(self, code: str, status: int | None = None, resource_id: str | None = None):
        self.code, self.status, self.resource_id = code, status, resource_id
        super().__init__(code)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def save_json(path: Path, value: dict) -> None:
    temporary = path.with_name(path.name + ".tmp-" + uuid.uuid4().hex)
    with temporary.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def observed_at() -> str:
    return datetime.now(timezone.utc).isoformat()


def unsafe_link(info: os.stat_result) -> bool:
    return stat.S_ISLNK(info.st_mode) or bool(
        getattr(info, "st_file_attributes", 0) & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0))


def safe_directory_chain(path: Path, allow_missing: bool = False) -> bool:
    for candidate in (path, *path.parents):
        try:
            info = candidate.lstat()
        except FileNotFoundError:
            if allow_missing:
                continue
            return False
        if unsafe_link(info) or not stat.S_ISDIR(info.st_mode):
            return False
    return True


def publish_complete(partial: Path, output: Path) -> None:
    """Publish complete bytes atomically without replacing a caller's file."""
    try:
        os.link(partial, output)
    except FileExistsError:
        raise ClientError("output_already_exists_partial_preserved") from None
    except OSError:
        raise ClientError("atomic_publish_unavailable_partial_preserved") from None
    partial.unlink()


def image_extension(content: bytes) -> str:
    if content.startswith(b"\x89PNG\r\n\x1a\n"):
        return ".png"
    if content.startswith(b"\xff\xd8\xff"):
        return ".jpg"
    if content.startswith((b"GIF87a", b"GIF89a")):
        return ".gif"
    if content[:4] == b"RIFF" and content[8:12] == b"WEBP":
        return ".webp"
    raise ClientError("image_format_unrecognized_use_original_resource")


def _image_data_url(path: str) -> tuple[str, int]:
    source = Path(path)
    if not source.is_file() or source.stat().st_size > MAX_IMAGE_BYTES:
        raise ClientError("reference_image_missing_or_too_large")
    kind = mimetypes.guess_type(source.name)[0]
    if kind not in {"image/png", "image/jpeg", "image/webp", "image/gif"}:
        raise ClientError("unsupported_reference_image_type")
    with source.open("rb") as stream:
        content = stream.read(MAX_IMAGE_BYTES + 1)
    if not content or len(content) > MAX_IMAGE_BYTES:
        raise ClientError("reference_image_missing_or_too_large")
    return "data:" + kind + ";base64," + base64.b64encode(content).decode("ascii"), len(content)


def image_data_url(path: str) -> str:
    return _image_data_url(path)[0]


def image_data_urls(paths: list[str]) -> list[str]:
    if len(paths) > MAX_IMAGES:
        raise ClientError("too_many_reference_images")
    output, total = [], 0
    for path in paths:
        encoded, size = _image_data_url(path)
        total += size
        if total > MAX_TOTAL_IMAGE_BYTES:
            raise ClientError("reference_images_total_too_large")
        output.append(encoded)
    return output


def encode_json_body(body: dict) -> bytes:
    data = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(data) > MAX_REQUEST_BYTES:
        raise ClientError("request_json_too_large")
    return data


class Client:
    def __init__(self, base_url: str, key: str, state_dir: Path, timeout: float = 180):
        parsed = urlsplit(base_url.rstrip("/"))
        if (parsed.scheme not in {"http", "https"} or not parsed.hostname
                or parsed.username or parsed.password or parsed.query or parsed.fragment
                or ".." in parsed.path.split("/")):
            raise ClientError("invalid_base_url")
        if parsed.scheme == "http" and parsed.hostname not in {"localhost", "127.0.0.1", "::1"}:
            raise ClientError("remote_endpoint_requires_https")
        if not key or "\n" in key or "\r" in key:
            raise ClientError("api_key_not_configured")
        self.base = base_url.rstrip("/")
        if not self.base.endswith("/v1"):
            self.base += "/v1"
        self.key, self.state, self.timeout = key, Path(os.path.abspath(state_dir)), timeout
        handlers = [NoRedirect()]
        if parsed.hostname in {"localhost", "127.0.0.1", "::1"}:
            handlers.append(ProxyHandler({}))
        self.opener = build_opener(*handlers)

    def open(self, method: str, path: str, body: dict | None = None, request_id: str | None = None):
        if not path.startswith("/") or ".." in path.split("/"):
            raise ClientError("invalid_api_path")
        headers = {"Authorization": "Bearer " + self.key, "Accept": "application/json"}
        data = None
        if body is not None:
            data = encode_json_body(body)
            headers["Content-Type"] = "application/json"
        if request_id:
            headers.update({"Idempotency-Key": request_id, "X-Request-ID": request_id})
        request = Request(self.base + path, data=data, headers=headers, method=method)
        try:
            return self.opener.open(request, timeout=self.timeout)
        except HTTPError as exc:
            resource = None
            try:
                error_body = json.loads(exc.read(64 * 1024))
                candidate = error_body.get("id") if isinstance(error_body, dict) else None
                if isinstance(candidate, str) and SAFE_ID.fullmatch(candidate) and candidate.startswith(("image-", "video-")):
                    resource = candidate
            except (ValueError, OSError, HTTPException):
                pass
            raise ClientError("http_error", exc.code, resource) from None
        except (URLError, TimeoutError, OSError):
            raise ClientError("transport_error") from None

    def call(self, method: str, path: str, body: dict | None = None, request_id: str | None = None) -> dict:
        with self.open(method, path, body, request_id) as response:
            if response.status not in {200, 201, 202} or response.headers.get("Content-Range"):
                raise ClientError("unexpected_or_partial_json_response", response.status)
            try:
                raw = response.read(MAX_RESPONSE + 1)
            except (HTTPException, TimeoutError, OSError):
                raise ClientError("incomplete_json_response") from None
        if len(raw) > MAX_RESPONSE:
            raise ClientError("response_too_large")
        try:
            result = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            raise ClientError("invalid_json_response") from None
        if not isinstance(result, dict):
            raise ClientError("invalid_json_response")
        return result

    def receipt_path(self, request_id: str) -> Path:
        if not SAFE_ID.fullmatch(request_id):
            raise ClientError("invalid_request_id")
        digest = hashlib.sha256((self.base + "\n" + request_id).encode()).hexdigest()
        return self.state / "receipts" / (digest + ".json")

    def _receipt_directory(self, create: bool = False) -> Path | None:
        folder = self.state / "receipts"
        if not safe_directory_chain(folder, allow_missing=create):
            if create:
                raise ClientError("unsafe_receipt_storage")
            return None
        if create:
            folder.mkdir(parents=True, exist_ok=True)
            if not safe_directory_chain(folder):
                raise ClientError("unsafe_receipt_storage")
            os.chmod(folder, 0o700)
        return folder

    def _read_receipt_file(self, path: Path) -> tuple[dict, tuple] | None:
        if path.parent != self.state / "receipts" or not RECEIPT_FILE.fullmatch(path.name):
            return None
        try:
            info = path.lstat()
            if (unsafe_link(info) or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                    or info.st_size > MAX_RECEIPT):
                return None
            flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
            descriptor = os.open(path, flags)
            with os.fdopen(descriptor, "r", encoding="utf-8") as stream:
                opened = os.fstat(stream.fileno())
                if (not stat.S_ISREG(opened.st_mode) or unsafe_link(opened)
                        or opened.st_ino != info.st_ino or opened.st_dev != info.st_dev):
                    return None
                raw = stream.read(MAX_RECEIPT + 1)
            if len(raw) > MAX_RECEIPT:
                return None
            value = json.loads(raw)
            if not isinstance(value, dict):
                return None
            return value, (info.st_dev, info.st_ino, info.st_mtime_ns, info.st_size)
        except (OSError, ValueError, UnicodeDecodeError):
            return None

    def read_receipt(self, request_id: str) -> dict:
        if self._receipt_directory() is None:
            raise ClientError("local_receipt_not_found_or_unsafe")
        path = self.receipt_path(request_id)
        loaded = self._read_receipt_file(path)
        if loaded is None:
            raise ClientError("local_receipt_not_found_or_unsafe")
        receipt, _ = loaded
        if receipt.get("base_url") != self.base or receipt.get("request_id") != request_id:
            raise ClientError("local_receipt_identity_mismatch")
        return receipt

    def _update_receipts(self, resource: str, upstream_status: str | None = None,
                         delivery: dict | None = None) -> int:
        """Update existing matching records only; never discover outside this state directory."""
        folder = self._receipt_directory()
        if folder is None:
            return 0
        updated = 0
        with os.scandir(folder) as entries:
            paths = [folder / entry.name for entry in entries
                     if RECEIPT_FILE.fullmatch(entry.name) and entry.is_file(follow_symlinks=False)]
        for path in paths:
            loaded = self._read_receipt_file(path)
            if loaded is None:
                continue
            receipt, identity = loaded
            request_id = receipt.get("request_id")
            if (receipt.get("base_url") != self.base or receipt.get("resource_id") != resource
                    or not isinstance(request_id, str) or not SAFE_ID.fullmatch(request_id)
                    or self.receipt_path(request_id) != path):
                continue
            # Capture legacy evidence once, before writing an observation. A
            # later GET status must never become an alleged POST response.
            if receipt.get("submission_evidence_captured") is not True:
                already_observed = bool(receipt.get("upstream_observed_at") or receipt.get("last_observed_at"))
                receipt.setdefault("submission_state", "unknown" if already_observed else receipt.get("state", "unknown"))
                receipt.setdefault("submission_status", None if already_observed else receipt.get("status"))
                receipt.setdefault("submission_error", receipt.get("error"))
                receipt.setdefault("submission_http_status", receipt.get("http_status"))
                receipt["submission_evidence_captured"] = True
            receipt.pop("error", None)
            receipt.pop("http_status", None)
            now = observed_at()
            if upstream_status is not None:
                receipt.update(upstream_status=upstream_status, upstream_observed_at=now,
                               status=upstream_status)
                if not receipt.get("delivery_verified"):
                    receipt["state"] = upstream_status
                if upstream_status == "completed":
                    receipt.setdefault("completion_observed_at", now)
            if delivery is not None:
                evidence = dict(delivery, verified_at=now)
                receipt.setdefault("delivery", evidence)
                prior_downloads = receipt.get("download_count", 0)
                if not isinstance(prior_downloads, int) or prior_downloads < 0:
                    prior_downloads = 0
                receipt.update(last_delivery=evidence, delivery_verified=True, state="completed", status="completed",
                               output_path=delivery["output_path"], bytes=delivery["bytes"], sha256=delivery["sha256"],
                               download_count=prior_downloads + 1)
            receipt["last_observed_at"] = now
            if not safe_directory_chain(folder):
                raise ClientError("unsafe_receipt_storage")
            try:
                current = path.lstat()
            except FileNotFoundError:
                continue
            if (unsafe_link(current) or not stat.S_ISREG(current.st_mode)
                    or (current.st_dev, current.st_ino, current.st_mtime_ns, current.st_size) != identity):
                continue
            save_json(path, receipt)
            updated += 1
        return updated

    def create(self, kind: str, prompt: str, request_id: str, references: list[str] | None = None, edit: bool = False) -> dict:
        if kind not in {"image", "video"} or not prompt.strip():
            raise ClientError("invalid_creation_input")
        if edit and (kind != "image" or not references):
            raise ClientError("image_edit_requires_reference")
        path = self.receipt_path(request_id)
        body = {"model": "muse-" + kind, "prompt": prompt, "n": 1}
        if references:
            body["reference_images"] = image_data_urls(references)
        if kind == "image":
            body["response_format"] = "b64_json"
        encode_json_body(body)  # Reject the full Base64-expanded request before the creation checkpoint.
        self._receipt_directory(create=True)
        receipt = {"request_id": request_id, "base_url": self.base, "kind": kind,
                   "state": "submission_pending_or_unknown", "submission_state": "submission_pending_or_unknown",
                   "submission_status": None, "submission_error": None, "submission_http_status": None,
                   "submission_evidence_captured": False,
                   "automatic_resubmit": False,
                   "created_at": int(time.time()), "body_sha256": hashlib.sha256(
                       json.dumps(body, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()}
        try:
            with path.open("x", encoding="utf-8") as stream:
                json.dump(receipt, stream, ensure_ascii=False, indent=2)
                stream.write("\n")
                stream.flush()
                os.fsync(stream.fileno())
            os.chmod(path, 0o600)
        except FileExistsError:
            raise ClientError("creation_previously_attempted_read_original_receipt") from None
        try:
            endpoint = "/images/edits" if edit else "/images/generations" if kind == "image" else "/videos"
            result = self.call("POST", endpoint, body, request_id)
        except ClientError as exc:
            receipt.update(state="delivery_unknown_or_rejected", error=exc.code, http_status=exc.status,
                           submission_state="delivery_unknown_or_rejected", submission_error=exc.code,
                           submission_http_status=exc.status, submission_evidence_captured=True)
            if exc.resource_id:
                receipt["resource_id"] = exc.resource_id
            save_json(path, receipt)
            raise
        resource = result.get("id")
        if not isinstance(resource, str) or not SAFE_ID.fullmatch(resource):
            receipt.update(state="response_received_without_resource_id", error="invalid_resource_id",
                           submission_state="response_received_without_resource_id", submission_error="invalid_resource_id",
                           submission_evidence_captured=True)
            save_json(path, receipt)
            raise ClientError("response_received_without_resource_id")
        receipt.update(state="response_received", submission_state="response_received",
                       resource_id=resource, status=result.get("status"), submission_status=result.get("status"),
                       submission_evidence_captured=True)
        save_json(path, receipt)  # Bind the resource before writing image bytes.
        if kind == "image":
            images = result.get("data")
            encoded = images[0].get("b64_json") if isinstance(images, list) and images and isinstance(images[0], dict) else None
            if not isinstance(encoded, str):
                raise ClientError("image_response_missing_base64_use_original_resource")
            try:
                content = base64.b64decode(encoded, validate=True)
            except ValueError:
                raise ClientError("invalid_image_base64_use_original_resource") from None
            if not content or len(content) > MAX_RESPONSE:
                raise ClientError("invalid_image_size_use_original_resource")
            output = self.state / "outputs" / (resource + image_extension(content))
            output.parent.mkdir(parents=True, exist_ok=True)
            partial = output.with_name(output.name + ".part-" + uuid.uuid4().hex)
            with partial.open("xb") as stream:
                stream.write(content)
            publish_complete(partial, output)
            self._update_receipts(resource, delivery={"output_path": str(output.resolve()), "bytes": len(content),
                                                     "sha256": hashlib.sha256(content).hexdigest(), "source": "inline_base64"})
            receipt = self.read_receipt(request_id)
        return {key: value for key, value in receipt.items() if key != "body_sha256"}

    def status(self, resource: str) -> dict:
        if not SAFE_ID.fullmatch(resource) or not resource.startswith(("video-", "image-")):
            raise ClientError("invalid_media_id")
        result = self.call("GET", ("/videos/" if resource.startswith("video-") else "/images/") + resource)
        if result.get("id") != resource:
            raise ClientError("resource_identity_mismatch")
        state = result.get("status")
        if not isinstance(state, str) or state not in MEDIA_STATES:
            raise ClientError("unrecognized_media_status")
        result["local_receipt_updates"] = self._update_receipts(resource, upstream_status=state)
        return result

    def wait(self, resource: str, timeout: float = 600, interval: float = 5) -> dict:
        deadline = time.monotonic() + timeout
        while True:
            result = self.status(resource)
            state = result.get("status")
            if state == "completed":
                return result
            if state in {"unknown", "failed"}:
                raise ClientError("original_task_" + state + "_do_not_resubmit")
            if state not in {"queued", "in_progress"}:
                raise ClientError("unrecognized_media_status")
            if time.monotonic() >= deadline:
                raise ClientError("waiting_interrupted_keep_original_id")
            time.sleep(min(interval, max(0, deadline - time.monotonic())))

    def download(self, resource: str, output: Path) -> dict:
        if not SAFE_ID.fullmatch(resource) or not resource.startswith(("video-", "image-")):
            raise ClientError("invalid_media_id")
        output = Path(output)
        if output.exists():
            raise ClientError("output_already_exists")
        output.parent.mkdir(parents=True, exist_ok=True)
        partial = output.with_name(output.name + ".part-" + uuid.uuid4().hex)
        path = ("/videos/" if resource.startswith("video-") else "/images/") + resource + "/content"
        digest, size = hashlib.sha256(), 0
        with partial.open("xb") as stream:
            with self.open("GET", path) as response:
                if response.status != 200 or response.headers.get("Content-Range"):
                    raise ClientError("download_requires_complete_http_200", response.status)
                raw_length = response.headers.get("Content-Length")
                try:
                    expected = int(raw_length) if raw_length is not None else None
                except ValueError:
                    raise ClientError("invalid_content_length") from None
                while True:
                    try:
                        chunk = response.read(1024 * 1024)
                    except (HTTPException, TimeoutError, OSError):
                        raise ClientError("incomplete_download") from None
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > MAX_RESPONSE:
                        raise ClientError("download_too_large")
                    digest.update(chunk)
                    stream.write(chunk)
                if size == 0 or expected is not None and size != expected:
                    raise ClientError("incomplete_download")
        publish_complete(partial, output)
        result = {"resource_id": resource, "output_path": str(output.resolve()), "bytes": size, "sha256": digest.hexdigest()}
        result["local_receipt_updates"] = self._update_receipts(
            resource, delivery={**{key: result[key] for key in ("output_path", "bytes", "sha256")},
                                "source": "complete_http_200"})
        return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", type=Path, default=Path.home() / ".muse2api-client")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("models")
    chat = sub.add_parser("chat")
    chat.add_argument("prompt")
    chat.add_argument("--image", action="append", default=[])
    for name in ("image", "edit", "video"):
        create = sub.add_parser(name)
        create.add_argument("prompt")
        create.add_argument("--request-id", required=True)
        create.add_argument("--reference", action="append", default=[])
    for name in ("status", "wait", "download"):
        item = sub.add_parser(name)
        item.add_argument("resource_id")
        if name == "wait":
            item.add_argument("--timeout", type=float, default=600)
            item.add_argument("--interval", type=float, default=5)
        if name == "download":
            item.add_argument("--output", type=Path, required=True)
    receipt = sub.add_parser("receipt")
    receipt.add_argument("request_id")
    args = parser.parse_args(argv)
    client = Client(os.environ.get("MUSE2API_BASE_URL", "http://127.0.0.1:18688/v1"),
                    os.environ.get("MUSE2API_API_KEY", ""), args.state_dir)
    if args.command == "models":
        result = client.call("GET", "/models")
    elif args.command == "chat":
        content: str | list = args.prompt
        if args.image:
            content = [{"type": "text", "text": args.prompt}] + [
                {"type": "image_url", "image_url": {"url": encoded}} for encoded in image_data_urls(args.image)]
        response = client.call("POST", "/chat/completions", {"model": "muse-default", "messages": [{"role": "user", "content": content}], "stream": False})
        choices = response.get("choices", [])
        result = {"id": response.get("id"), "text": choices[0].get("message", {}).get("content") if choices else None}
    elif args.command in {"image", "edit", "video"}:
        result = client.create("video" if args.command == "video" else "image", args.prompt,
                               args.request_id, args.reference, edit=args.command == "edit")
    elif args.command == "status":
        result = client.status(args.resource_id)
    elif args.command == "wait":
        if args.timeout <= 0 or args.interval <= 0:
            raise ClientError("invalid_wait_interval")
        result = client.wait(args.resource_id, args.timeout, args.interval)
    elif args.command == "download":
        result = client.download(args.resource_id, args.output)
    else:
        result = client.read_receipt(args.request_id)
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ClientError as exc:
        print(json.dumps({"error": exc.code, "http_status": exc.status, "resource_id": exc.resource_id, "automatic_resubmit": False}))
        raise SystemExit(1)
    except (OSError, ValueError):
        print('{"error":"local_client_io_error","automatic_resubmit":false}')
        raise SystemExit(1)
