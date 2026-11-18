from pathlib import Path
import sys
import subprocess
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
from audit_release import inspect_index, inspect_text, path_problem


class ReleaseAuditTests(unittest.TestCase):
    def test_private_paths_are_rejected_before_reading(self):
        for value in [".env", "prod.env", "docs/.env.example", "runtime/session.enc", "private/settings.json", "docs/../../session.json", "browser-extension/pairing.json", "docs/demo.mp4"]:
            self.assertIsNotNone(path_problem(value), value)
        for value in [".env.example", "cmd/muse2api/main.go", "browser-extension/manifest.json", "skills/muse2api-client/SKILL.md"]:
            self.assertIsNone(path_problem(value), value)

    def test_findings_do_not_include_matched_private_values(self):
        marker = "fixture-private-identifier"
        findings = inspect_text("Document mentions " + marker, [marker])
        self.assertEqual(findings, [(1, "private-identifier")])
        self.assertNotIn(marker, repr(findings))

    def test_real_patterns_and_example_exceptions(self):
        header = "-----BEGIN " + "PRIVATE KEY-----"
        self.assertIn((1, "private-key"), inspect_text(header, []))
        self.assertFalse(inspect_text("Contact you@example.com at 127.0.0.1 or 203.0.113.4", []))
        self.assertIn((1, "non-example-email"), inspect_text("Contact name" + "@" + "private.invalid", []))

    def test_unquoted_environment_secrets_are_not_missed(self):
        literal = "MUSE2API_API_KEY=" + "synthetic-value-that-is-not-a-placeholder"
        self.assertIn((1, "nonempty-secret-environment-assignment"), inspect_text(literal, []))
        self.assertFalse(inspect_text("MUSE2API_API_KEY=\nMUSE2API_SESSION_KEY=\n", []))

    def test_quoted_json_and_headers_and_fake_prefix_cannot_bypass(self):
        import json
        value = "test-" + "synthetic-unpublished-value-" * 2
        for text in [json.dumps({"cookies": {"hatch_sess": value}}), json.dumps({"api_key": value}),
                     "Authorization: " + "Bearer " + value, "Cookie: " + "hatch_sess=" + value,
                     "MUSE2API_API_KEY=" + value]:
            self.assertTrue(inspect_text(text, []), "credential format was missed")

    def test_placeholder_cannot_hide_a_later_secret_on_the_same_line(self):
        import json
        value = "synthetic-unpublished-value-" * 2
        texts = [json.dumps({"api_key": "replace_with_your_own_key", "access_token": value}),
                 "Bearer replace_with_your_own_key; " + "Bearer " + value,
                 "hatch_sess=replace_with_your_own_key; " + "hatch_vml=" + value]
        for text in texts:
            self.assertTrue(inspect_text(text, []))

    def test_yaml_bare_secret_values_and_session_encryption_key(self):
        import json
        value = "synthetic-unpublished-value-" * 2
        for key in ("MUSE2API_API_KEY", "MUSE2API_SESSION_KEY", "hatch_sess", "hatch_native_auth_device", "hatch_vml"):
            self.assertTrue(inspect_text(key + ": " + value, []))
            self.assertTrue(inspect_text(json.dumps({key: value}), []))

    def test_audit_reads_staged_blob_and_rejects_private_path(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            subprocess.run(["git", "init", "-q", root], check=True)
            source = root / "README.md"
            source.write_text("Public staged document.\n", encoding="utf-8")
            subprocess.run(["git", "add", "--", "README.md"], cwd=root, check=True)
            marker = "fixture-private-working-copy-marker"
            source.write_text(marker, encoding="utf-8")
            self.assertTrue(inspect_index(root, [marker])["passed"])
            subprocess.run(["git", "add", "--", "README.md"], cwd=root, check=True)
            report = inspect_index(root, [marker])
            self.assertFalse(report["passed"])
            self.assertNotIn(marker, repr(report["violations"]))
            secret = root / ".env"
            secret.write_text("fixture-private-configuration", encoding="utf-8")
            subprocess.run(["git", "add", "-f", "--", ".env"], cwd=root, check=True)
            report = inspect_index(root, [])
            self.assertTrue(any(item["file"] == ".env" for item in report["violations"]))
            self.assertTrue(all(item["path"] != ".env" for item in report["files"]))


if __name__ == "__main__":
    unittest.main()
