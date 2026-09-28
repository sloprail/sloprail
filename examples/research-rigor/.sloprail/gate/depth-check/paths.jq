# paths.jq — path and repository spellings shared by research-facts.jq and
# verify-depth.sh's verdict (`include "paths";` with `-L` this directory), so a
# clone's destination and its repository are keyed the same way on both sides.

# Collapse `.`, `..` and repeated slashes in an absolute path, and drop macOS's
# /private prefix, so `/tmp/x`, `/private/tmp/x` and `/tmp/./y/../x` are one
# directory — an agent spells the same clone all of those ways.
def canon:
  (split("/") | reduce .[] as $s ([];
      if $s == "" or $s == "." then .
      elif $s == ".." then (if length > 0 then .[:-1] else . end)
      else . + [$s] end)
  | "/" + join("/"))
  | sub("^/private(?<rest>/(tmp|var|etc)(/.*)?)$"; "\(.rest)");

# One repository, however it was spelled: as the `git clone` argument (placed
# against $base, the directory the clone ran in, when it is a relative path)
# or as git's own reflog records it (`clone: from <url>`, an absolute path for
# a local repository). A trailing slash and `.git` are not part of the name.
# null when a relative path has no base to be placed on.
def repokey($base):
  # git drops the user part when it records a clone (`git@host:p` → `host:p`,
  # `ssh://git@host/p` → `ssh://host/p`, `https://u:secret@host/p` →
  # `https://host/p`) while the command and remote.origin.url keep it: it is
  # not part of the repository's name on any side.
  (if type == "string" then
     if test("://") then sub("://[^/@]+@"; "://") else sub("^[^@/:]+@"; "") end
   else . end)
  | if . == null or . == "" then null
  elif test("^file://") then sub("^file://"; "") | canon | sub("\\.git$"; "")
  elif test("^[A-Za-z][A-Za-z0-9+.-]*://") or test("^[^/]+:") then sub("/+$"; "") | sub("\\.git$"; "")
  elif startswith("/") then canon | sub("\\.git$"; "")
  elif $base == null then null
  else $base + "/" + . | canon | sub("\\.git$"; "")
  end;
