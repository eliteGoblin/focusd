"""Unit tests for browser_guard.py's pure matching logic.

Run: python3 -m pytest utils/mac-browser-guard/  (or: python3 -m unittest)

CI is Go-only, so these are run manually. They pin the host-extraction + block
semantics to be EQUAL to the Go guard (guard.ExtractHost / guard.IsBlocked): the
same cases in plugins/browser-monitor/internal/guard/guard_test.go must pass here
too, since the two share a generated blocklist and must behave identically.
"""
import unittest

import browser_guard as bg


class TestHostOf(unittest.TestCase):
    def test_extracts_and_lowercases_host(self):
        cases = {
            "https://www.youtube.com/watch?v=x": "www.youtube.com",
            "http://Google.COM/search?q=1": "google.com",
            "https://user:pass@news.com.au:8443/a": "news.com.au",
            "steampowered.com/app/570": "steampowered.com",
            "https://sub.zhihu.com": "sub.zhihu.com",
            "https://alibaba.com": "alibaba.com",
        }
        for url, want in cases.items():
            self.assertEqual(bg.host_of(url), want, url)


class TestIsBlocked(unittest.TestCase):
    def test_exact_and_subdomain_match(self):
        for h in ["youtube.com", "www.youtube.com", "m.youtube.com",
                  "alibaba.com", "deep.sub.alibaba.com", "google.com"]:
            self.assertTrue(bg.is_blocked(h), h)

    def test_non_matches(self):
        # Empty, unrelated, the classic suffix-bypass, and a look-alike prefix.
        for h in ["", "notyoutube.com", "youtube.com.evil.com",
                  "myalibaba.com", "example.com"]:
            self.assertFalse(bg.is_blocked(h), h)

    def test_dot_anchored_boundary(self):
        # "youtube.com.evil.com" must NOT match youtube.com — anchored on a dot.
        self.assertFalse(bg.is_blocked("youtube.com.evil.com"))


class TestBlocklistIsPopulated(unittest.TestCase):
    def test_generated_list_non_empty(self):
        # The generated block must have filled in real entries.
        self.assertGreater(len(bg.BLOCKLIST), 10)
        self.assertIn("alibaba.com", bg.BLOCKLIST)


if __name__ == "__main__":
    unittest.main()
