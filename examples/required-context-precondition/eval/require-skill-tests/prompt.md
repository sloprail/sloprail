Someone on the team is worried that when a tool call comes back with arguments
that fail validation, the agent just gives up instead of retrying. Can you
confirm one way or the other whether that's actually how it behaves, and add
something to the codebase itself so this can't quietly regress later without
anyone noticing?
