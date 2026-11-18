from contextlib import contextmanager
import base64
import io
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
from unittest import mock
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from muse_client import Client, ClientError, image_data_url, image_data_urls


@contextmanager
def server(callback):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            callback(self)
        def do_POST(self):
            callback(self)
        def log_message(self, *args):
            pass
    instance = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=instance.serve_forever, daemon=True)
    thread.start()
    try:
        yield "http://127.0.0.1:" + str(instance.server_port)
    finally:
        instance.shutdown()
        instance.server_close()
        thread.join()


def respond(handler, status, body=b"", headers=None):
    handler.send_response(status)
    for key, value in (headers or {}).items():
        handler.send_header(key, value)
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


def seed_receipt(client, request_id, resource_id, state="delivery_unknown_or_rejected"):
    path = client.receipt_path(request_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"request_id": request_id, "base_url": client.base,
                                "resource_id": resource_id, "kind": "image", "state": state,
                                "error": "http_error", "http_status": 502,
                                "automatic_resubmit": False}), encoding="utf-8")
    return path


class ClientTests(unittest.TestCase):
    def test_unknown_creation_is_not_replayed_even_after_key_rotation(self):
        calls = []
        def handle(h):
            calls.append(h.command)
            h.rfile.read(int(h.headers.get("Content-Length", 0)))
            respond(h, 502, b'{}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            first = Client(base, "test-key-one", Path(folder))
            with self.assertRaises(ClientError):
                first.create("video", "a test", "test-001")
            second = Client(base, "test-key-two", Path(folder))
            with self.assertRaisesRegex(ClientError, "creation_previously_attempted"):
                second.create("video", "a test", "test-001")
            self.assertEqual(calls, ["POST"])
            receipt = json.loads(first.receipt_path("test-001").read_text())
            self.assertEqual(receipt["state"], "delivery_unknown_or_rejected")
            self.assertNotIn("test-key-one", first.receipt_path("test-001").read_text())

    def test_creation_error_preserves_returned_resource_for_readonly_recovery(self):
        calls = []
        def handle(h):
            calls.append(h.command)
            if h.command == "POST":
                h.rfile.read(int(h.headers["Content-Length"]))
                respond(h, 502, b'{"id":"image-existing","error":{"delivery_unknown":true}}')
            else:
                self.assertEqual(h.path, "/v1/images/image-existing")
                respond(h, 200, b'{"id":"image-existing","status":"unknown"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder))
            with self.assertRaises(ClientError):
                client.create("image", "a test", "image-unknown")
            receipt = json.loads(client.receipt_path("image-unknown").read_text())
            self.assertEqual(receipt["resource_id"], "image-existing")
            self.assertEqual(client.status(receipt["resource_id"])["status"], "unknown")
            self.assertEqual(calls, ["POST", "GET"])

    def test_recovery_updates_current_receipt_and_preserves_submission_failure(self):
        calls = []
        content = b"recovered-complete-media"
        def handle(h):
            calls.append(h.command)
            if h.command == "POST":
                h.rfile.read(int(h.headers["Content-Length"]))
                respond(h, 502, b'{"id":"image-original","error":{"delivery_unknown":true}}')
            elif h.path.endswith("/content"):
                respond(h, 200, content)
            else:
                respond(h, 200, b'{"id":"image-original","status":"completed"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder) / "state")
            with self.assertRaises(ClientError):
                client.create("image", "one request", "original-001")
            self.assertEqual(client.wait("image-original", 1, 0.01)["local_receipt_updates"], 1)
            observed = client.read_receipt("original-001")
            self.assertEqual(observed["upstream_status"], "completed")
            self.assertTrue(observed["upstream_observed_at"])
            self.assertNotIn("http_status", observed)
            downloaded = client.download("image-original", Path(folder) / "result.bin")
            receipt = client.read_receipt("original-001")
            self.assertEqual(receipt["state"], "completed")
            self.assertEqual(receipt["submission_state"], "delivery_unknown_or_rejected")
            self.assertEqual(receipt["submission_error"], "http_error")
            self.assertEqual(receipt["submission_http_status"], 502)
            self.assertIsNone(receipt["submission_status"])
            self.assertTrue(receipt["submission_evidence_captured"])
            self.assertTrue(receipt["delivery_verified"])
            self.assertEqual(receipt["delivery"]["sha256"], downloaded["sha256"])
            self.assertEqual(receipt["last_delivery"]["bytes"], len(content))
            self.assertEqual(Path(receipt["output_path"]).read_bytes(), content)
            with self.assertRaisesRegex(ClientError, "creation_previously_attempted"):
                client.create("image", "one request", "original-001")
            self.assertEqual(calls, ["POST", "GET", "GET"])

    def test_legacy_receipt_captures_missing_submission_status_once(self):
        calls = []
        def handle(h):
            calls.append(h.command)
            if h.path.endswith("/content"):
                respond(h, 200, b"complete-legacy-media")
            else:
                respond(h, 200, b'{"id":"image-legacy","status":"completed"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder) / "state")
            seed_receipt(client, "legacy-001", "image-legacy")
            client.wait("image-legacy", 1, 0.01)
            observed = client.read_receipt("legacy-001")
            self.assertEqual(observed["status"], "completed")
            self.assertIsNone(observed["submission_status"])
            self.assertTrue(observed["submission_evidence_captured"])
            client.download("image-legacy", Path(folder) / "result.bin")
            receipt = client.read_receipt("legacy-001")
            self.assertEqual(receipt["state"], "completed")
            self.assertIsNone(receipt["submission_status"])
            self.assertEqual(receipt["submission_state"], "delivery_unknown_or_rejected")
            self.assertEqual(receipt["submission_error"], "http_error")
            self.assertEqual(receipt["submission_http_status"], 502)
            self.assertTrue(receipt["delivery_verified"])
            with self.assertRaisesRegex(ClientError, "creation_previously_attempted"):
                client.create("image", "same task", "legacy-001")
            self.assertEqual(calls, ["GET", "GET"])

    def test_previously_observed_legacy_receipt_does_not_infer_post_status(self):
        with tempfile.TemporaryDirectory() as folder:
            client = Client("http://localhost:18688", "test-key", Path(folder) / "state")
            path = seed_receipt(client, "legacy-observed-001", "image-legacy")
            receipt = json.loads(path.read_text())
            receipt.update(state="completed", status="completed", upstream_observed_at="previous-observation",
                           submission_state="delivery_unknown_or_rejected", submission_error="http_error",
                           submission_http_status=502)
            receipt.pop("error")
            receipt.pop("http_status")
            path.write_text(json.dumps(receipt), encoding="utf-8")
            client._update_receipts("image-legacy", upstream_status="completed")
            captured = client.read_receipt("legacy-observed-001")
            self.assertIsNone(captured["submission_status"])
            self.assertEqual(captured["submission_http_status"], 502)
            self.assertTrue(captured["submission_evidence_captured"])

    def test_reference_image_limits_reject_before_checkpoint_or_network(self):
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "oversized.png"
            with source.open("wb") as stream:
                stream.truncate(10 * 1024 * 1024 + 1)
            state = Path(folder) / "state"
            client = Client("http://localhost:18688", "test-key", state)
            with mock.patch.object(client, "call") as called:
                with self.assertRaisesRegex(ClientError, "reference_image_missing_or_too_large"):
                    client.create("image", "a test", "too-large-001", [str(source)])
                with self.assertRaisesRegex(ClientError, "too_many_reference_images"):
                    client.create("video", "a test", "too-many-001", ["unused.png"] * 5)
                with mock.patch("muse_client._image_data_url", return_value=("data:image/png;base64,eA==", 9 * 1024 * 1024)):
                    with self.assertRaisesRegex(ClientError, "reference_images_total_too_large"):
                        client.create("image", "a test", "total-too-large-001", ["one.png", "two.png", "three.png"])
                called.assert_not_called()
            self.assertFalse(state.exists())

    def test_reference_image_boundaries_allow_ten_mib_and_four_twenty_four_mib_total(self):
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "boundary.png"
            with source.open("wb") as stream:
                stream.truncate(10 * 1024 * 1024)
            self.assertTrue(image_data_url(str(source)).startswith("data:image/png;base64,"))
        with mock.patch("muse_client._image_data_url", return_value=("data:image/png;base64,eA==", 6 * 1024 * 1024)):
            self.assertEqual(len(image_data_urls(["one.png", "two.png", "three.png", "four.png"])), 4)

    def test_expanded_json_limit_rejects_before_checkpoint_or_network(self):
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "reference.png"
            source.write_bytes(b"\x89PNG\r\n\x1a\n" + b"x" * 92)
            state = Path(folder) / "state"
            client = Client("http://localhost:18688", "test-key", state)
            prompt = "纸" * 20
            body = {"model": "muse-image", "prompt": prompt, "n": 1,
                    "reference_images": [image_data_url(str(source))], "response_format": "b64_json"}
            wire_size = len(json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))
            self.assertGreater(wire_size, source.stat().st_size + len(prompt))
            with mock.patch("muse_client.MAX_REQUEST_BYTES", wire_size - 1), mock.patch.object(client, "call") as called:
                with self.assertRaisesRegex(ClientError, "request_json_too_large"):
                    client.create("image", prompt, "expanded-too-large-001", [str(source)])
                called.assert_not_called()
            self.assertFalse(state.exists())

    def test_open_enforces_utf8_json_limit_before_network(self):
        with tempfile.TemporaryDirectory() as folder:
            client = Client("http://localhost:18688", "test-key", Path(folder))
            with mock.patch("muse_client.MAX_REQUEST_BYTES", 20), mock.patch.object(client.opener, "open") as opened:
                with self.assertRaisesRegex(ClientError, "request_json_too_large"):
                    client.call("POST", "/chat/completions", {"prompt": "纸" * 10})
                opened.assert_not_called()

    def test_receipt_updates_match_exact_base_resource_and_record_name(self):
        def handle(h):
            respond(h, 200, b'{"id":"image-target","status":"completed"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            state = Path(folder) / "state"
            client = Client(base, "test-key", state)
            other = Client("https://other.example.com/v1", "test-key", state)
            match = seed_receipt(client, "match-001", "image-target")
            other_base = seed_receipt(other, "other-base-001", "image-target")
            other_id = seed_receipt(client, "other-id-001", "image-other")
            mismatched_path = seed_receipt(client, "mismatched-001", "image-target")
            changed = json.loads(mismatched_path.read_text())
            changed["request_id"] = "different-name"
            mismatched_path.write_text(json.dumps(changed), encoding="utf-8")
            untouched = {path: path.read_bytes() for path in (other_base, other_id, mismatched_path)}
            self.assertEqual(client.status("image-target")["local_receipt_updates"], 1)
            self.assertEqual(json.loads(match.read_text())["state"], "completed")
            for path, original in untouched.items():
                self.assertEqual(path.read_bytes(), original)

    def test_failed_download_does_not_publish_delivery_evidence(self):
        def handle(h):
            respond(h, 206, b"partial", {"Content-Range": "bytes 0-6/30"})
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder) / "state")
            path = seed_receipt(client, "failed-download", "image-target")
            original = path.read_bytes()
            with self.assertRaisesRegex(ClientError, "complete_http_200"):
                client.download("image-target", Path(folder) / "result.bin")
            self.assertEqual(path.read_bytes(), original)
            self.assertFalse((Path(folder) / "result.bin").exists())

    def test_direct_download_advances_queued_receipt_without_new_creation(self):
        def handle(h):
            respond(h, 200, b"completed-video-data")
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder) / "state")
            path = seed_receipt(client, "queued-original", "video-original", state="response_received")
            receipt = json.loads(path.read_text())
            receipt.pop("error")
            receipt.pop("http_status")
            receipt["status"] = "queued"
            path.write_text(json.dumps(receipt), encoding="utf-8")
            client.download("video-original", Path(folder) / "result.mp4")
            recovered = client.read_receipt("queued-original")
            self.assertEqual(recovered["state"], "completed")
            self.assertEqual(recovered["status"], "completed")
            self.assertEqual(recovered["submission_state"], "response_received")
            self.assertEqual(recovered["submission_status"], "queued")
            self.assertTrue(recovered["delivery_verified"])

    def test_status_response_identity_mismatch_cannot_update_receipt(self):
        def handle(h):
            respond(h, 200, b'{"id":"image-different","status":"completed"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            client = Client(base, "test-key", Path(folder) / "state")
            path = seed_receipt(client, "target-original", "image-target")
            original = path.read_bytes()
            with self.assertRaisesRegex(ClientError, "resource_identity_mismatch"):
                client.status("image-target")
            self.assertEqual(path.read_bytes(), original)

    def test_readonly_actions_without_receipt_do_not_create_a_record(self):
        def handle(h):
            if h.path.endswith("/content"):
                respond(h, 200, b"complete-data")
            else:
                respond(h, 200, b'{"id":"video-existing","status":"completed"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            state = Path(folder) / "unused-state"
            client = Client(base, "test-key", state)
            self.assertEqual(client.status("video-existing")["local_receipt_updates"], 0)
            self.assertEqual(client.download("video-existing", Path(folder) / "result.bin")["local_receipt_updates"], 0)
            self.assertFalse(state.exists())

    def test_symbolic_link_receipt_is_not_opened_or_updated(self):
        with tempfile.TemporaryDirectory() as folder:
            state = Path(folder) / "state"
            client = Client("http://localhost:18688", "test-key", state)
            path = client.receipt_path("linked-001")
            path.parent.mkdir(parents=True)
            outside = Path(folder) / "outside.json"
            outside.write_text(json.dumps({"request_id": "linked-001", "base_url": client.base,
                                           "resource_id": "image-target", "state": "unknown"}), encoding="utf-8")
            try:
                path.symlink_to(outside)
            except OSError:
                self.skipTest("This host does not permit creating symbolic links")
            original = outside.read_bytes()
            with mock.patch("muse_client.os.open", wraps=os.open) as opened:
                self.assertIsNone(client._read_receipt_file(path))
                opened.assert_not_called()
            self.assertEqual(client._update_receipts("image-target", upstream_status="completed"), 0)
            self.assertEqual(outside.read_bytes(), original)
            with self.assertRaisesRegex(ClientError, "not_found_or_unsafe"):
                client.read_receipt("linked-001")

    def test_localhost_ignores_environment_proxy(self):
        calls = []
        def handle(h):
            calls.append(h.path)
            respond(h, 200, b'{"id":"video-test","status":"completed"}')
        malicious_proxy = {key: "http://127.0.0.1:1" for key in
                           ("HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy")}
        malicious_proxy.update(NO_PROXY="", no_proxy="")
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            with mock.patch.dict(os.environ, malicious_proxy):
                result = Client(base, "test-key", Path(folder)).status("video-test")
            self.assertEqual(result["status"], "completed")
            self.assertEqual(calls, ["/v1/videos/video-test"])

    def test_redirect_is_never_followed_with_authorization(self):
        calls = []
        def destination(h):
            calls.append(h.headers.get("Authorization"))
            respond(h, 200, b'{}')
        with tempfile.TemporaryDirectory() as folder, server(destination) as other:
            def handle(h):
                respond(h, 302, headers={"Location": other + "/target"})
            with server(handle) as base:
                with self.assertRaises(ClientError):
                    Client(base, "private-test-key", Path(folder)).call("GET", "/models")
            self.assertEqual(calls, [])

    def test_download_rejects_partial_status_and_content_range(self):
        for status, headers in [(206, {}), (200, {"Content-Range": "bytes 0-2/6"})]:
            with self.subTest(status=status), tempfile.TemporaryDirectory() as folder:
                def handle(h):
                    respond(h, status, b"abc", headers)
                with server(handle) as base:
                    output = Path(folder) / "video.mp4"
                    with self.assertRaisesRegex(ClientError, "complete_http_200"):
                        Client(base, "test-key", Path(folder)).download("video-test", output)
                    self.assertFalse(output.exists())
                    self.assertEqual(len(list(Path(folder).glob("*.part-*"))), 1)

    def test_download_requires_content_length_match(self):
        class Response(io.BytesIO):
            status = 200
            headers = {"Content-Length": "9"}
            def __enter__(self):
                return self
            def __exit__(self, *args):
                self.close()
        with tempfile.TemporaryDirectory() as folder:
            client = Client("http://localhost:18688", "test-key", Path(folder))
            client.open = lambda *args, **kwargs: Response(b"short")
            output = Path(folder) / "result.mp4"
            with self.assertRaisesRegex(ClientError, "incomplete_download"):
                client.download("video-test", output)
            self.assertFalse(output.exists())

    def test_complete_download_verifies_hash_and_refuses_overwrite(self):
        def handle(h):
            respond(h, 200, b"complete-test-data")
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            output = Path(folder) / "video.mp4"
            client = Client(base, "test-key", Path(folder))
            result = client.download("video-test", output)
            self.assertEqual(output.read_bytes(), b"complete-test-data")
            self.assertEqual(result["bytes"], len(b"complete-test-data"))
            self.assertEqual(len(result["sha256"]), 64)
            with self.assertRaisesRegex(ClientError, "output_already_exists"):
                client.download("video-test", output)

    def test_image_bytes_are_written_without_printing_base64(self):
        picture = b"\x89PNG\r\n\x1a\nsynthetic-test-bytes"
        def handle(h):
            body = json.loads(h.rfile.read(int(h.headers["Content-Length"])))
            self.assertEqual(body["response_format"], "b64_json")
            self.assertEqual(h.headers["Idempotency-Key"], "img-001")
            self.assertEqual(h.headers["X-Request-ID"], "img-001")
            respond(h, 200, json.dumps({"id": "image-test", "data": [{"b64_json": base64.b64encode(picture).decode()}]}).encode())
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            result = Client(base, "test-key", Path(folder)).create("image", "a test", "img-001")
            self.assertEqual(Path(result["output_path"]).read_bytes(), picture)
            self.assertEqual(result["resource_id"], "image-test")
            self.assertNotIn("b64_json", result)

    def test_wait_unknown_stops_without_post(self):
        calls = []
        def handle(h):
            calls.append(h.command)
            respond(h, 200, b'{"id":"video-test","status":"unknown"}')
        with tempfile.TemporaryDirectory() as folder, server(handle) as base:
            with self.assertRaisesRegex(ClientError, "do_not_resubmit"):
                Client(base, "test-key", Path(folder)).wait("video-test", 1, 0.01)
            self.assertEqual(calls, ["GET"])

    def test_unsafe_configuration_and_identifiers_are_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            for base in ["http://api.example.com/v1", "https://name:password@api.example.com/v1", "https://api.example.com/v1?key=x"]:
                with self.assertRaises(ClientError):
                    Client(base, "test-key", Path(folder))
            client = Client("http://localhost:18688", "test-key", Path(folder))
            for value in ["../escape", "a/b", ""]:
                with self.assertRaises(ClientError):
                    client.receipt_path(value)


if __name__ == "__main__":
    unittest.main()
