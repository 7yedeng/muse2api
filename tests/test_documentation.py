from pathlib import Path
import re
import unittest
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


class DocumentationTests(unittest.TestCase):
    def test_relative_markdown_links_resolve_inside_the_source_tree(self):
        files = [ROOT / "README.md"]
        for folder in ("docs", "skills", "LICENSES", "browser-extension"):
            files.extend((ROOT / folder).rglob("*.md"))
        for path in files:
            for target in re.findall(r"\[[^\]]*\]\(([^\s)]+)\)", path.read_text(encoding="utf-8")):
                if urlsplit(target).scheme or target.startswith("#"):
                    continue
                value = unquote(target.split("#", 1)[0])
                resolved = (path.parent / value).resolve()
                self.assertTrue(resolved.is_relative_to(ROOT), str(path.relative_to(ROOT)))
                self.assertTrue(resolved.exists(), f"{path.relative_to(ROOT)} -> {value}")

    def test_skills_have_scoped_names_and_required_frontmatter(self):
        names = {"muse2api-local", "muse2api-client"}
        for name in names:
            path = ROOT / "skills" / name / "SKILL.md"
            text = path.read_text(encoding="utf-8")
            self.assertTrue(text.startswith("---\n"))
            frontmatter = text.split("---", 2)[1]
            self.assertIn("name: " + name, frontmatter)
            self.assertIn("description:", frontmatter)


if __name__ == "__main__":
    unittest.main()
