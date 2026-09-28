DEFAULT_CURRENCY = "USD"


def format_total(amount, currency=DEFAULT_CURRENCY):
    """Render a total like 'USD 12.50'."""
    return f"{currency} {amount:.2f}"
