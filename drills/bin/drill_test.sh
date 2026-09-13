#!/bin/sh
# drill_test.sh — exercise the drill state machine against a fake target.
#
# No Docker, no real stack: a stub adapter in $TMPDIR toggles a marker file that
# the break/fix probes read, so every transition and guard is checked
# deterministically. Run: sh drills/bin/drill_test.sh
set -eu

DRILL=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/drill
WORK=$(mktemp -d "${TMPDIR:-/tmp}/drilltest.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

export DRILL_HOME="$WORK"
export DRILL_PROBE_INTERVAL=0

# Sealing exercises real git. Isolate it from the operator's config and identity
# so the test is deterministic and needs nothing from $HOME (unreadable under the
# sandbox anyway).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=drilltest GIT_AUTHOR_EMAIL=drilltest@example.com
export GIT_COMMITTER_NAME=drilltest GIT_COMMITTER_EMAIL=drilltest@example.com
# Keep git's core.excludesFile lookup inside the sandbox (default is ~/.config).
export XDG_CONFIG_HOME="$WORK/xdg"

pass=0; fail=0
ok()   { pass=$((pass+1)); printf 'ok   - %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf 'FAIL - %s\n' "$1"; }
check() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (want [$3], got [$2])"; fi; }
# succeeds <desc> <cmd...>  /  fails <desc> <cmd...>
succeeds() { _d=$1; shift; if "$@" >/dev/null 2>&1; then ok "$_d"; else bad "$_d (command failed)"; fi; }
fails()    { _d=$1; shift; if "$@" >/dev/null 2>&1; then bad "$_d (command unexpectedly succeeded)"; else ok "$_d"; fi; }

state() { sed -n 's/.*to: \([A-Za-z]*\).*/\1/p' "$WORK"/runs/*/run.yaml 2>/dev/null | tail -n 1; }

# --- build the fake target adapter -----------------------------------------
mkdir -p "$WORK/targets/fake" "$WORK/scenarios" "$WORK/runs" "$WORK/commands"
T="$WORK/targets/fake"

cat > "$T/target.yaml" <<'EOF'
name: fake
components:
  - {name: widget, kind: service}
primitives: [proc.stop, proc.start, code.patch]
load_profiles: [steady]
EOF

cat > "$T/env" <<'EOF'
#!/bin/sh
d=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
printf 'export FAKE_STATE=%s/state\n' "$d"
EOF

cat > "$T/inject" <<'EOF'
#!/bin/sh
set -eu
d=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LOG="${DRILL_REVERT_LOG:-$d/.revert}"
case "$1" in
proc.stop)  : > "$d/state"; printf 'proc.start %s\n' "$2" >> "$LOG" ;;
proc.start) rm -f "$d/state"; printf 'proc.stop %s\n' "$2" >> "$LOG" ;;
*) echo "fake: unsupported $1" >&2; exit 1 ;;
esac
EOF

cat > "$T/revert" <<'EOF'
#!/bin/sh
set -eu
d=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LOG="${DRILL_REVERT_LOG:-$d/.revert}"
[ -f "$LOG" ] || exit 0
scratch="$LOG.reverting"; : > "$scratch"
while IFS= read -r line || [ -n "$line" ]; do
	[ -n "$line" ] || continue
	# shellcheck disable=SC2086
	DRILL_REVERT_LOG="$scratch" "$(dirname -- "$0")/inject" $line
done < "$LOG"
rm -f "$scratch" "$LOG"
EOF

for s in up down reset health observe; do
	printf '#!/bin/sh\nexit 0\n' > "$T/$s"
done
cat > "$T/load" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$T"/*

# --- build a valid scenario and an invalid one -----------------------------
S="$WORK/scenarios/demo"
mkdir -p "$S/probes"
cat > "$S/scenario.yaml" <<'EOF'
name: demo
target: fake
level: L1
tier: A
load: steady
break_probe_attempts: 2
EOF
printf 'inject:\n  - proc.stop widget\n' > "$S/fault.yaml"
printf '# PAGE: the widget is unhappy\n\nSomething is wrong.\n' > "$S/brief.md"
printf '## Tier 1\n\nLook at the widget.\n\n## Tier 2\n\nThe widget is stopped.\n' > "$S/hints.md"
printf '| Dimension | Note |\n| --- | --- |\n| Detection | x |\n' > "$S/rubric.md"
printf 'Restart the widget.\n' > "$S/solution.md"
# Symptom present while the marker exists.
printf '#!/bin/sh\n[ -f "$FAKE_STATE" ]\n' > "$S/probes/break"
printf '#!/bin/sh\n[ ! -f "$FAKE_STATE" ]\n' > "$S/probes/fix"
chmod +x "$S/probes/break" "$S/probes/fix"

# Scenario that names a primitive the target does not support.
BAD="$WORK/scenarios/unsupported"
mkdir -p "$BAD/probes"
cp "$S/scenario.yaml" "$BAD/scenario.yaml"
printf 'inject:\n  - net.partition widget\n' > "$BAD/fault.yaml"
for f in brief hints rubric solution; do cp "$S/$f.md" "$BAD/$f.md"; done
cp "$S/probes/break" "$BAD/probes/break"; cp "$S/probes/fix" "$BAD/probes/fix"
chmod +x "$BAD/probes/break" "$BAD/probes/fix"

# --- validation -------------------------------------------------------------
succeeds "target validate accepts a well-formed adapter" "$DRILL" target validate fake
succeeds "scenario validate accepts a well-formed scenario" "$DRILL" scenario validate demo
fails    "scenario validate rejects an unsupported primitive" "$DRILL" scenario validate unsupported

# --- guards before start ----------------------------------------------------
fails "propose refused with no active drill" "$DRILL" propose -

# --- happy path -------------------------------------------------------------
succeeds "start boots, injects, confirms symptom, pages" "$DRILL" start demo
check "start leaves state BRIEFED" "$(state)" BRIEFED
[ -f "$WORK/.active" ] && ok "start writes the lock" || bad "start writes the lock"
fails "a second start is refused while active" "$DRILL" start demo

"$DRILL" hint >/dev/null 2>&1
grep -q 'hint tier 1 revealed' "$WORK"/runs/*/run.yaml && ok "hint is recorded" || bad "hint is recorded"

echo "restart the widget" | "$DRILL" propose - >/dev/null 2>&1
check "propose advances to PROPOSED" "$(state)" PROPOSED
[ -f "$WORK"/runs/*/proposals/1.md ] && ok "proposal 1 is written" || bad "proposal 1 is written"

succeeds "execute advances to EXECUTING" "$DRILL" execute
check "execute leaves state EXECUTING" "$(state)" EXECUTING

# Evaluate before applying the fix: the symptom is still present, so it fails
# and the state does not advance.
fails "evaluate fails while the symptom persists" "$DRILL" evaluate
check "a failed evaluate stays EXECUTING" "$(state)" EXECUTING

# Apply the fix the way an Executor would, then evaluate.
rm -f "$WORK/state"
succeeds "evaluate passes once the fix is applied" "$DRILL" evaluate
check "evaluate advances to EVALUATED" "$(state)" EVALUATED

succeeds "resolve advances to DEBRIEFED" "$DRILL" resolve
check "resolve leaves state DEBRIEFED" "$(state)" DEBRIEFED

succeeds "end tears down and debriefs" "$DRILL" end
[ -f "$WORK"/runs/*/debrief.md ] && ok "end writes a debrief" || bad "end writes a debrief"
[ ! -f "$WORK/.active" ] && ok "end drops the lock" || bad "end drops the lock"
grep -q '^result: resolved' "$WORK"/runs/*/run.yaml && ok "run record ends resolved" || bad "run record ends resolved"

# --- abort path -------------------------------------------------------------
rm -rf "$WORK/runs"/*
succeeds "start a fresh drill to abort" "$DRILL" start demo
succeeds "abort tears down without scoring" "$DRILL" abort
[ ! -f "$WORK/.active" ] && ok "abort drops the lock" || bad "abort drops the lock"
grep -q '^result: aborted' "$WORK"/runs/*/run.yaml && ok "run record ends aborted" || bad "run record ends aborted"
[ ! -f "$WORK"/runs/*/debrief.md ] && ok "abort writes no debrief" || bad "abort writes no debrief"

# --- ordering guards --------------------------------------------------------
rm -rf "$WORK/runs"/*
"$DRILL" start demo >/dev/null 2>&1
fails "resolve refused before a passing fix probe" "$DRILL" resolve
"$DRILL" abort >/dev/null 2>&1

# --- Tier B: sealed history -------------------------------------------------
# A throwaway source repo stands in for the target's checkout. DRILL_REPO_ROOT
# points the sealing machinery at it; the codebug scenario patches a tracked
# file so the probes can read the fault out of the build tree.
rm -rf "$WORK/runs"/*
export DRILL_REPO_ROOT="$WORK/src"
mkdir -p "$WORK/src/services"
printf 'OK\n' > "$WORK/src/services/app.txt"
git -C "$WORK/src" init -q
git -C "$WORK/src" add -A
git -C "$WORK/src" commit -q -m init

CB="$WORK/scenarios/codebug"
mkdir -p "$CB/probes" "$CB/faults"
# Generate the fault patch (OK -> BROKEN) with git, then restore pristine.
printf 'BROKEN\n' > "$WORK/src/services/app.txt"
git -C "$WORK/src" diff > "$CB/faults/bug.patch"
git -C "$WORK/src" checkout -q -- services/app.txt

cat > "$CB/scenario.yaml" <<'EOF'
name: codebug
target: fake
level: L2
tier: B
load: steady
break_probe_attempts: 2
EOF
printf 'inject:\n  - code.patch faults/bug.patch\n' > "$CB/fault.yaml"
printf '# PAGE: app misbehaving\n\nA logic fault is loose.\n' > "$CB/brief.md"
printf '## Tier 1\n\nRead the app.\n\n## Tier 2\n\nThe app is BROKEN.\n' > "$CB/hints.md"
printf '| Dimension | Note |\n| --- | --- |\n| Detection | x |\n' > "$CB/rubric.md"
printf 'Restore the app to OK.\n' > "$CB/solution.md"
# Probes read the fault out of the build tree the drill points them at.
printf '#!/bin/sh\n[ "$(cat "$DRILL_BUILD_ROOT/services/app.txt" 2>/dev/null)" = BROKEN ]\n' > "$CB/probes/break"
printf '#!/bin/sh\n[ "$(cat "$DRILL_BUILD_ROOT/services/app.txt" 2>/dev/null)" = OK ]\n' > "$CB/probes/fix"
chmod +x "$CB/probes/break" "$CB/probes/fix"

wt_of() { sed -n 's/^worktree: //p' "$WORK"/runs/*/run.yaml | tail -n 1; }

# A code.patch scenario seals by default.
succeeds "sealed start boots from the baked build tree" "$DRILL" start codebug
check "sealed start records sealed: true" "$(sed -n 's/^sealed: //p' "$WORK"/runs/*/run.yaml)" true
WT=$(wt_of)
[ -n "$WT" ] && [ -d "$WT" ] && ok "sealed start creates the build tree" || bad "sealed start creates the build tree"
check "the sealed tree is a single commit" "$(git -C "$WT" log --oneline | wc -l | tr -d ' ')" 1
check "no other ref reaches the pristine tree" "$(git -C "$WT" rev-list --all | wc -l | tr -d ' ')" 1
[ -s "$WORK"/runs/*/seal/fault.patch ] && ok "the fault patch is stored outside the tree" || bad "the fault patch is stored outside the tree"
check "the fault is baked into the build tree" "$(cat "$WT/services/app.txt")" BROKEN

echo "restore the app" | "$DRILL" propose - >/dev/null 2>&1
"$DRILL" execute >/dev/null 2>&1
fails "sealed evaluate fails while the fault stands" "$DRILL" evaluate
# The engineer applies their fix in the build tree, as an Executor would.
printf 'OK\n' > "$WT/services/app.txt"
succeeds "sealed evaluate passes once the fix lands" "$DRILL" evaluate
succeeds "sealed resolve" "$DRILL" resolve
succeeds "sealed end tears down and debriefs" "$DRILL" end
grep -q 'What the fault changed' "$WORK"/runs/*/debrief.md && ok "debrief shows the fault patch" || bad "debrief shows the fault patch"
grep -q 'Your fix vs the reference' "$WORK"/runs/*/debrief.md && ok "debrief compares the fix" || bad "debrief compares the fix"
[ -s "$WORK"/runs/*/seal/engineer-fix.diff ] && ok "the engineer fix diff is captured" || bad "the engineer fix diff is captured"
[ ! -d "$WT" ] && ok "sealed end removes the build tree" || bad "sealed end removes the build tree"
[ ! -f "$WORK/.active" ] && ok "sealed end drops the lock" || bad "sealed end drops the lock"

# --unsealed keeps history: the base commit plus the fault commit are visible.
rm -rf "$WORK/runs"/*
succeeds "unsealed start" "$DRILL" start --unsealed codebug
check "unsealed start records sealed: false" "$(sed -n 's/^sealed: //p' "$WORK"/runs/*/run.yaml)" false
WT=$(wt_of)
[ "$(git -C "$WT" log --oneline | wc -l | tr -d ' ')" -gt 1 ] && ok "the unsealed tree keeps its history" || bad "the unsealed tree keeps its history"
succeeds "unsealed abort cleans up" "$DRILL" abort
[ ! -d "$WT" ] && ok "unsealed abort removes the worktree" || bad "unsealed abort removes the worktree"

# --sealed on a scenario with no code.patch is refused.
fails "--sealed refused without a code.patch fault" "$DRILL" start --sealed demo

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
