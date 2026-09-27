import re


def slugify(title):
    """Turn a title into a URL slug: lowercase words joined by hyphens."""
    words = re.findall(r"[a-zA-Z]+", title)
    return "-".join(w.lower() for w in words)
