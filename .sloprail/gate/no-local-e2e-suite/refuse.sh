#!/usr/bin/env bash
# The gate's match found a `go test` or a `make test-e2e*`. Count the tests a `go test` over
# tests/ packages would run, deterministically (no compiling): expand the package arguments to
# directories, list the top-level `func TestXxx(` of their *_test.go files, keep those the -run
# regex selects. Refuse above LIMIT, and refuse when the count cannot be known.
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
set -uo pipefail

LIMIT=10
advice='Push the branch and let CI run it (16 parallel shards, ~10–15 min). Locally, run only the few tests you need: literal package paths and a literal -run regex.'
refuse() { jq -n --arg r "$1" '{reason: $r}'; exit 1; }
unresolvable() { refuse "Can't tell how many e2e tests this command runs ($1). Rewrite it into a resolvable form: literal package paths under tests/, a literal -run regex (no variables or command substitution, no -skip or -list), then run it again. $advice"; }

payload="$(cat)"

# Fail closed: a payload jq cannot read is refused, not let through unchecked.
if ! invs="$(printf '%s' "$payload" | jq -c '
  .event.invocations[]? | (.bin | split("/") | last) as $b
  | if $b == "make" and any(.argv // [] | .[]; . == "test-e2e" or . == "test-e2e-shard") then {make: true}
    elif $b == "go" and ((.argv // []) | index("test")) != null
    then (.argv // []) as $v | ($v | index("test")) as $t | (.gaps // []) as $g
      | {argv: [range($t + 1; ($v | length) + 1) as $i | (if ($g | index($i)) != null then "<<GAP>>" else empty end), ($v[$i] // empty)],
         headgap: any($g[]; . >= 1 and . <= $t), cwd: (.cwd // "")}
    else empty end')"; then
  refuse "no-local-e2e-suite could not read the command; refusing rather than letting a possible local e2e run through. $advice"
fi
[ -n "$invs" ] || exit 0

root="${SR_WORKSPACE:-.}"
total=0
while IFS= read -r inv; do
  [ -n "$inv" ] || continue
  if [ "$(printf '%s' "$inv" | jq -r '.make // false')" = true ]; then
    refuse "Don't run the e2e suite locally (sequential, ~1h). $advice"
  fi
  argv=(); while IFS= read -r w; do argv+=("$w"); done < <(printf '%s' "$inv" | jq -r '.argv[]')
  headgap="$(printf '%s' "$inv" | jq -r '.headgap')"
  cwd="$(printf '%s' "$inv" | jq -r '.cwd')"

  # a word lost between `go` and `test` may be `-C dir`: the directory is unknowable
  [ "$headgap" = false ] || unresolvable "a word before the test subcommand comes from a variable or substitution"
  pkgs=(); run=""; hasrun=0; skip_or_list=0; unknownpkg=0; i=0
  while [ $i -lt ${#argv[@]} ]; do
    a="${argv[$i]}"
    case "$a" in
      -run=*|--run=*|-test.run=*|--test.run=*) run="${a#*=}"; hasrun=1 ;;
      -run|--run|-test.run|--test.run) i=$((i+1)); run="${argv[$i]-}"; hasrun=1 ;;
      -skip=*|--skip=*|-test.skip=*|--test.skip=*|-list=*|--list=*|-test.list=*|--test.list=*) skip_or_list=1 ;;
      -skip|--skip|-test.skip|--test.skip|-list|--list|-test.list|--test.list) skip_or_list=1; i=$((i+1)) ;;
      -args|--args) break ;;
      -timeout|--timeout|-count|--count|-tags|--tags|-p|--p|-parallel|--parallel|-cpu|--cpu|-bench|--bench|-benchtime|--benchtime|-coverprofile|--coverprofile|-covermode|--covermode|-coverpkg|--coverpkg|-o|--o|-exec|--exec|-ldflags|--ldflags|-gcflags|--gcflags|-asmflags|--asmflags|-mod|--mod|-modfile|--modfile|-overlay|--overlay|-pkgdir|--pkgdir|-vet|--vet|-C|--C|-fuzz|--fuzz|-fuzztime|--fuzztime|-shuffle|--shuffle|-outputdir|--outputdir|-blockprofile|--blockprofile|-cpuprofile|--cpuprofile|-memprofile|--memprofile|-mutexprofile|--mutexprofile|-trace|--trace|-gcflags|-toolexec|--toolexec|-buildvcs|--buildvcs) i=$((i+1)) ;;
      -*) ;;
      "<<GAP>>") unknownpkg=1 ;;
      *) if printf '%s' "$a" | grep -Eq '^(\./|\.\./)*tests(/|$)'; then pkgs+=("$a"); fi ;;
    esac
    i=$((i+1))
  done
  # a lost word standing where a package goes (`go test $PKGS`, `$(go list ./...)`) may be any package
  [ "$unknownpkg" -eq 0 ] || unresolvable "a package argument comes from a variable or substitution"
  # unit packages only: always permitted, whatever a lost flag value was
  [ ${#pkgs[@]} -gt 0 ] || continue

  [ -n "$cwd" ] || unresolvable "the directory it runs in is not known"
  # .cwd is "." / relative to the workspace, or absolute
  case "$cwd" in /*) dir="$cwd" ;; *) dir="$root/$cwd" ;; esac
  [ "$skip_or_list" -eq 0 ] || unresolvable "-skip and -list change what runs"
  [ "$run" != "<<GAP>>" ] || unresolvable "the -run value comes from a variable or substitution"
  if [ "$hasrun" -eq 1 ] && printf '%s' "$run" | grep -Eq '\$[A-Za-z_{(]|`'; then
    unresolvable "the -run value comes from a variable or substitution"
  fi

  dirs=()
  for p in "${pkgs[@]}"; do
    case "$p" in
      *"..."*)
        case "$p" in */...) ;; *) unresolvable "the package pattern $p is not a plain dir/..." ;; esac
        base="${p%/...}"
        [ -d "$dir/$base" ] || unresolvable "$base does not exist"
        while IFS= read -r d; do dirs+=("$d"); done < <(cd "$dir" && find "$base" \( -name testdata -o -name '.*' -o -name '_*' \) -prune -o -name '*_test.go' -type f -print 2>/dev/null | sed 's|/[^/]*$||' | sort -u)
        ;;
      *)
        [ -d "$dir/$p" ] || unresolvable "$p does not exist"
        dirs+=("$p")
        ;;
    esac
  done

  # top-level test functions of every distinct dir
  names="$(for d in $(printf '%s\n' "${dirs[@]:-}" | sed 's|/*$||; s|^\./||' | sort -u); do
    for f in "$dir/$d"/*_test.go; do
      [ -f "$f" ] && grep -hEo '^func (Test[A-Za-z0-9_]*)\(' "$f" | sed -E 's/^func //; s/\($//' | grep -vx 'TestMain'
    done
  done)"
  if [ "$hasrun" -eq 1 ]; then
    re="${run%%/*}"
    printf '' | grep -E -- "$re" >/dev/null 2>&1; [ $? -le 1 ] || unresolvable "the -run regex $run is not a valid pattern"
    [ -n "$names" ] && names="$(printf '%s\n' "$names" | grep -E -- "$re" || true)"
  fi
  n=0; [ -n "$names" ] && n="$(printf '%s\n' "$names" | wc -l | tr -d ' ')"
  total=$((total + n))
done <<< "$invs"

[ "$total" -gt "$LIMIT" ] || exit 0
refuse "This command would run $total e2e tests (limit $LIMIT). Don't run that many locally: e2e is sequential (~1h for the suite). $advice"
