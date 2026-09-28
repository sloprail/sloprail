def parse_amount(raw):
    """Parse a currency amount like '$12.50' into a float."""
    cleaned = raw.replace("$", "").replace(",", "")
    return float(cleaned)
