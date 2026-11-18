"""Exercise the real executable with a new empty state, never an upstream login."""
import base64
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as stream:
        stream.bind(("127.0.0.1", 0))
        return stream.getsockname()[1]


class ProcessSmokeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not shutil.which("go"):
            raise unittest.SkipTest("Go is required for executable smoke testing")
        cls.temporary = tempfile.TemporaryDirectory()
        directory = Path(cls.temporary.name)
        binary = directory / ("muse2api.exe" if os.name == "nt" else "muse2api")
        built = subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/muse2api"], cwd=ROOT, capture_output=True)
        if built.returncode:
            cls.temporary.cleanup()
            raise RuntimeError("executable_build_failed")
        cls.key = secrets.token_urlsafe(48)
        cls.api_port, cls.admin_port = free_port(), free_port()
        while cls.admin_port == cls.api_port:
            cls.admin_port = free_port()
        env = os.environ.copy()
        env.update(MUSE2API_API_KEY=cls.key, MUSE2API_SESSION_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                   MUSE2API_DATA_DIR=str(directory / "new-empty-state"),
                   MUSE2API_LISTEN=f"127.0.0.1:{cls.api_port}", MUSE2API_ADMIN_LISTEN=f"127.0.0.1:{cls.admin_port}",
                   MUSE2API_PROXY_URL="", MUSE2API_GENERATE_TIMEOUT="1m")
        cls.process = subprocess.Popen([str(binary), "serve"], env=env, cwd=directory,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                       creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        cls.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(200):
            if cls.process.poll() is not None:
                cls.temporary.cleanup()
                raise RuntimeError("empty_state_server_did_not_start")
            try:
                status, _, _ = cls.request(cls.api_port, "/health")
                if status == 200:
                    return
            except (OSError, urllib.error.URLError):
                time.sleep(0.05)
        cls.process.terminate()
        cls.process.wait(timeout=5)
        cls.temporary.cleanup()
        raise RuntimeError("empty_state_server_start_timed_out")

    @classmethod
    def tearDownClass(cls):
        cls.process.terminate()
        cls.process.communicate(timeout=10)
        cls.temporary.cleanup()

    @classmethod
    def request(cls, port, path, method="GET", body=None, headers=None):
        supplied = dict(headers or {})
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            supplied["Content-Type"] = "application/json"
        req = urllib.request.Request(f"http://127.0.0.1:{port}" + path, data=data, method=method, headers=supplied)
        try:
            response = cls.opener.open(req, timeout=3)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            content_type = response.headers.get("Content-Type", "")
            return response.status, json.loads(raw) if "application/json" in content_type else raw.decode(), content_type

    def test_real_listener_auth_and_admin_boundaries_without_login(self):
        status, _, _ = self.request(self.api_port, "/v1/models")
        self.assertEqual(status, 401)
        status, models, _ = self.request(self.api_port, "/v1/models", headers={"Authorization": "Bearer " + self.key})
        self.assertEqual(status, 200)
        self.assertEqual({item["id"] for item in models["data"]}, {"muse-default", "muse-image", "muse-video"})
        self.assertEqual(self.request(self.api_port, "/admin/status")[0], 404)
        status, state, _ = self.request(self.admin_port, "/admin/status")
        self.assertEqual(status, 200)
        self.assertEqual(state, {"ready": False, "persisted": False, "busy": False})
        status, page, _ = self.request(self.admin_port, "/")
        self.assertEqual(status, 200)
        self.assertNotIn(self.key, page)
        self.assertEqual(self.request(self.admin_port, "/admin/pair-code", "POST", {}, {"Origin": "https://example.org"})[0], 403)
        status, pair, _ = self.request(self.admin_port, "/admin/pair-code", "POST", {})
        self.assertEqual(status, 200)
        self.assertEqual(pair["expires_in"], 120)
        # Empty cookies fail before the SDK is invoked; even this failure must
        # consume the one-time code. No synthetic cookie is sent upstream.
        request = {"code": pair["code"], "cookies": {}}
        self.assertEqual(self.request(self.admin_port, "/admin/browser-session", "POST", request)[0], 400)
        self.assertEqual(self.request(self.admin_port, "/admin/browser-session", "POST", request)[0], 403)
        status, failure, _ = self.request(self.api_port, "/v1/chat/completions", "POST",
                                         {"model": "muse-default", "messages": [{"role": "user", "content": "fixture"}]},
                                         {"Authorization": "Bearer " + self.key})
        self.assertEqual(status, 503)
        self.assertEqual(failure["error"]["code"], "session_required")


if __name__ == "__main__":
    unittest.main()
