import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from slug import slugify  # noqa: E402


class SlugifyTest(unittest.TestCase):
    def test_words(self):
        self.assertEqual(slugify("Hello World"), "hello-world")

    def test_punctuation(self):
        self.assertEqual(slugify("What's new?"), "what-s-new")


if __name__ == "__main__":
    unittest.main()
