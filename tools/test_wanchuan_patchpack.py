import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("wanchuan_patchpack", ROOT / "tools" / "wanchuan_patchpack.py")
patchpack = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(patchpack)


class WanchuanPatchpackTest(unittest.TestCase):
    def test_manifest_is_valid(self):
        data = patchpack.load_manifest()
        patchpack.validate_manifest(data)
        self.assertEqual(data["release_repo"], "1057300248/sub2api")
        self.assertEqual(data["fork_suffix"], "wanchuan")

    def test_next_version_tracks_upstream_and_revision(self):
        self.assertEqual(
            patchpack.fork_version("v2.9.6", 1, "wanchuan"),
            "2.9.6-wanchuan.1",
        )
        self.assertEqual(
            patchpack.fork_version("2.10.0", 7, "wanchuan"),
            "2.10.0-wanchuan.7",
        )
        with self.assertRaises(ValueError):
            patchpack.fork_version("v2.9.6-rc.1", 1, "wanchuan")

    def test_protected_touch_requires_review(self):
        data = patchpack.load_manifest()
        result = patchpack.classify_paths(
            ["backend/internal/service/cline_ratelimit.go", "README.md"],
            data,
        )
        self.assertEqual(result["risk"], "review")
        self.assertIn("cline-rate-limit-cas", result["module_hits"])

    def test_unowned_documentation_change_is_safe(self):
        data = patchpack.load_manifest()
        result = patchpack.classify_paths(["docs/upstream-only.md"], data)
        self.assertEqual(result["risk"], "safe")
        self.assertEqual(result["review_hits"], [])


if __name__ == "__main__":
    unittest.main()
