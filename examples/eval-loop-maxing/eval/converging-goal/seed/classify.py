# classify.py — deterministic rules routing a support ticket to an intent and
# a priority. No model call: this is a plain keyword-routing table, so a
# fixed set of rules is either right or wrong for a given ticket, with
# nothing to "prompt" its way around.
#
# Intents and their fixed priority (the mapping the rules route into):
#   bug     -> P1   (something is broken)
#   billing -> P2   (money, invoices, charges)
#   account -> P3   (login, password, profile)
#   other   -> P4   (anything else)
#
# INTENDED FIX (the one real bug seeded here): a ticket reporting the product
# is down, crashing, or erroring is a "bug" ticket, but the word "crash" is
# not in BUG_KEYWORDS below — texts that say "crashes" or "crashed" fall
# through to "other" instead of "bug". The fix is one keyword rule, and it
# generalizes: it must fix every crash-shaped ticket, not just the ones
# visible in eval/cases.json.

BUG_KEYWORDS = ["bug", "broken", "error", "not working", "doesn't work", "fails"]
BILLING_KEYWORDS = ["invoice", "charge", "charged", "refund", "billing", "payment", "subscription"]
ACCOUNT_KEYWORDS = ["password", "login", "log in", "locked out", "account", "profile", "username"]

PRIORITY = {
    "bug": "P1",
    "billing": "P2",
    "account": "P3",
    "other": "P4",
}


def classify(text):
    """Return (intent, priority) for a ticket's text."""
    t = text.lower()

    if any(k in t for k in BUG_KEYWORDS):
        intent = "bug"
    elif any(k in t for k in BILLING_KEYWORDS):
        intent = "billing"
    elif any(k in t for k in ACCOUNT_KEYWORDS):
        intent = "account"
    else:
        intent = "other"

    return intent, PRIORITY[intent]
