import base64
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
from config_utils import read_env
from init_config import initialize


class ConfigurationTests(unittest.TestCase):
    def test_private_generation_is_valid_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            output = root / ".env"
            initialize(output, root / "runtime")
            values = read_env(output)
            self.assertGreaterEqual(len(values["MUSE2API_API_KEY"]), 32)
            self.assertEqual(len(base64.b64decode(values["MUSE2API_SESSION_KEY"], validate=True)), 32)
            original = output.read_bytes()
            with self.assertRaises(FileExistsError):
                initialize(output, root / "runtime")
            self.assertEqual(output.read_bytes(), original)
            if os.name != "nt":
                self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_env_is_literal_and_preserves_base64_padding(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "config"
            path.write_text('MUSE2API_SESSION_KEY=fixture==\nMUSE2API_DATA_DIR="$(never-execute)"\n', encoding="utf-8")
            value = read_env(path)
            self.assertEqual(value["MUSE2API_SESSION_KEY"], "fixture==")
            self.assertEqual(value["MUSE2API_DATA_DIR"], "$(never-execute)")

    def test_duplicate_and_unrelated_keys_are_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "config"
            for text in ['HOME=unrelated\n', 'MUSE2API_LISTEN=a\nMUSE2API_LISTEN=b\n', 'MUSE2API_DATA_DIR="unterminated\n']:
                path.write_text(text, encoding="utf-8")
                with self.assertRaises(ValueError):
                    read_env(path)


if __name__ == "__main__":
    unittest.main()
