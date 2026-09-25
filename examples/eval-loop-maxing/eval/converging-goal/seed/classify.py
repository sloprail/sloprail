def classify(text):
    """Return "spam" or "ham" for a short message."""
    t = text.lower()
    if "win" in t:
        return "spam"
    return "ham"
