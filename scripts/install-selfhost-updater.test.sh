#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd -P)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# macOS /var is a symlink; the worker requires physical, symlink-free paths.
tmp=$(cd "$tmp" && pwd -P)
export HOME="$tmp/home & test"
export SELFHOST_PRODUCTION_ENV_FILE="$tmp/production.env"
export SELFHOST_SERVER_URL=http://localhost:18097
export SELFHOST_PRODUCTION_DATABASE=multica
export SELFHOST_CUSTOM_REMOTE=https://github.com/example/multica.git
export SELFHOST_CUSTOM_BRANCH=codex/self-host-update-release
export SELFHOST_WORKER_TOKEN='test-only-token"<&'
export SELFHOST_EXTERNAL_RESOLVE_IP=103.84.155.217
export SELFHOST_UPDATER_DRY_RUN=1
export INSTALL_TEST_CALLS="$tmp/calls"
mkdir -p "$HOME" "$tmp/fakes"
printf 'DO_NOT_EXECUTE=$(touch "%s/env-was-sourced")\n' "$tmp" > "$SELFHOST_PRODUCTION_ENV_FILE"
chmod 600 "$SELFHOST_PRODUCTION_ENV_FILE"
cat > "$tmp/fakes/launchctl" <<'SH'
#!/usr/bin/env bash
printf 'launchctl %s\n' "$*" >> "$INSTALL_TEST_CALLS"
SH
cat > "$tmp/fakes/go" <<'SH'
#!/usr/bin/env bash
set -eu
[[ "$PWD" == */server && "$1" == build && "$2" == -o && "$4" == ./cmd/selfhost-updater ]]
printf 'go build\n' >> "$INSTALL_TEST_CALLS"
printf '#!/bin/sh\nexit 0\n' > "$3"
chmod 700 "$3"
SH
chmod +x "$tmp/fakes/launchctl" "$tmp/fakes/go"
export PATH="$tmp/fakes:$PATH"

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
run_install() { bash "$root/scripts/install-selfhost-updater.sh" > "$tmp/output" 2>&1; }
reject() {
  if run_install; then fail "$1 was accepted"; fi
  if [[ -e "$INSTALL_TEST_CALLS" ]]; then fail "$1 invoked host commands"; fi
  if rg -Fq 'test-only-token' "$tmp/output"; then fail "$1 leaked the token"; fi
}

[[ -f "$root/scripts/install-selfhost-updater.sh" ]] || fail 'durable installer is missing'
run_install || fail 'valid dry run failed'
[[ ! -e "$INSTALL_TEST_CALLS" ]] || fail 'dry run invoked Go or launchctl'
[[ ! -e "$tmp/env-was-sourced" ]] || fail 'production env was executed'
python3 - <<'PY'
import json, os, pathlib, plistlib, stat
home = pathlib.Path(os.environ['HOME'])
base = home / '.multica/self-update'
for name in ('bin', 'repo.git', 'state', 'worktrees'):
    assert stat.S_IMODE((base / name).stat().st_mode) == 0o700, name
config = base / 'worker.json'
assert stat.S_IMODE(config.stat().st_mode) == 0o600
data = json.loads(config.read_text())
for field, env in (
    ('server_url', 'SELFHOST_SERVER_URL'),
    ('production_database', 'SELFHOST_PRODUCTION_DATABASE'),
    ('production_env_file', 'SELFHOST_PRODUCTION_ENV_FILE'),
    ('custom_remote', 'SELFHOST_CUSTOM_REMOTE'),
    ('custom_branch', 'SELFHOST_CUSTOM_BRANCH'),
    ('worker_token', 'SELFHOST_WORKER_TOKEN'),
    ('external_resolve_ip', 'SELFHOST_EXTERNAL_RESOLVE_IP'),
):
    assert data[field] == os.environ[env], field
for field, directory in (('repository_dir', 'repo.git'), ('state_dir', 'state'), ('worktree_root', 'worktrees')):
    assert data[field] == str(base / directory)
plist_path = home / 'Library/LaunchAgents/com.multica.self-update.plist'
plist = plistlib.loads(plist_path.read_bytes())
assert plist['Label'] == 'com.multica.self-update'
assert plist['ProgramArguments'] == [str(base / 'bin/selfhost-updater'), 'serve', '--config', str(config)]
assert plist['RunAtLoad'] and plist['KeepAlive']
for key in ('StandardOutPath', 'StandardErrorPath'):
    assert pathlib.Path(plist[key]).parent == base / 'state'
assert '/tmp/' not in plist['ProgramArguments'][0].removeprefix(str(home))
assert os.environ['SELFHOST_WORKER_TOKEN'] not in plist_path.read_text()
PY
git --git-dir="$HOME/.multica/self-update/repo.git" rev-parse --is-bare-repository | rg -qx true
if rg -Fq "$SELFHOST_WORKER_TOKEN" "$tmp/output"; then fail 'token appeared in output'; fi
printf 'PASS: dry run creates private durable configuration and plist\n'

mkdir -p "$tmp/make-checkout"
printf '$(error Checkout env must not be loaded)\n' > "$tmp/make-checkout/.env"
(cd "$tmp/make-checkout" && make -n -f "$root/Makefile" selfhost-updater-install) > "$tmp/make-output" 2>&1 || fail 'Make target loaded checkout env'
rg -qx 'bash scripts/install-selfhost-updater.sh' "$tmp/make-output" || fail 'Make install recipe missing'
printf 'PASS: Make install target ignores checkout env\n'

saved_env=$SELFHOST_PRODUCTION_ENV_FILE
unset SELFHOST_PRODUCTION_ENV_FILE
reject 'unset production env'
export SELFHOST_PRODUCTION_ENV_FILE="$tmp/missing.env"
reject 'missing production env'
export SELFHOST_PRODUCTION_ENV_FILE=relative.env
reject 'relative production env'
export SELFHOST_PRODUCTION_ENV_FILE="$saved_env"
chmod 640 "$saved_env"
reject 'group-readable production env'
chmod 400 "$saved_env"
reject 'non-600 production env'
chmod 600 "$saved_env"
mkdir -p "$tmp/checkout/nested"
printf 'gitdir: elsewhere\n' > "$tmp/checkout/.git"
cp "$saved_env" "$tmp/checkout/nested/production.env"
chmod 600 "$tmp/checkout/nested/production.env"
export SELFHOST_PRODUCTION_ENV_FILE="$tmp/checkout/nested/production.env"
reject 'checkout-contained production env'
ln -s "$saved_env" "$tmp/linked.env"
export SELFHOST_PRODUCTION_ENV_FILE="$tmp/linked.env"
reject 'symlink production env'
export SELFHOST_PRODUCTION_ENV_FILE="$saved_env"
export SELFHOST_CUSTOM_REMOTE='https://secret@example.com/multica.git'
reject 'credential-bearing custom remote'
export SELFHOST_CUSTOM_REMOTE=https://github.com/example/multica.git
export SELFHOST_EXTERNAL_RESOLVE_IP=invalid
reject 'invalid external resolver IP'
export SELFHOST_EXTERNAL_RESOLVE_IP=103.84.155.217
unset SELFHOST_WORKER_TOKEN
reject 'missing worker token'
export SELFHOST_WORKER_TOKEN='test-only-token"<&'
printf 'original-production-settings\n' > "$HOME/.multica/self-update/worker.json"
chmod 600 "$HOME/.multica/self-update/worker.json"
export SELFHOST_PRODUCTION_ENV_FILE="$HOME/.multica/self-update/worker.json"
reject 'production env equal to worker config output'
rg -qx 'original-production-settings' "$HOME/.multica/self-update/worker.json" || fail 'rejected collision overwrote production env bytes'
export SELFHOST_PRODUCTION_ENV_FILE="$saved_env"
printf 'PASS: unsafe or missing protected configuration is rejected\n'

SELFHOST_UPDATER_DRY_RUN=0 run_install || fail 'installation with fake host commands failed'
[[ -x "$HOME/.multica/self-update/bin/selfhost-updater" ]] || fail 'durable binary was not installed'
rg -q '^go build$' "$INSTALL_TEST_CALLS" || fail 'build command missing'
rg -q '^launchctl bootstrap gui/' "$INSTALL_TEST_CALLS" || fail 'LaunchAgent was not loaded'
rg -q '^launchctl kickstart -k gui/' "$INSTALL_TEST_CALLS" || fail 'LaunchAgent was not started'
if rg -Fq "$SELFHOST_WORKER_TOKEN" "$tmp/output" "$INSTALL_TEST_CALLS"; then fail 'token leaked'; fi
printf 'PASS: installation builds the binary and registers the LaunchAgent\n'

printf 'preserve\n' > "$HOME/.multica/self-update/state/existing-journal"
SELFHOST_UPDATER_DRY_RUN=0 run_install || fail 'reinstallation failed'
rg -qx preserve "$HOME/.multica/self-update/state/existing-journal" || fail 'reinstallation removed worker state'
rg -q '^launchctl bootout gui/' "$INSTALL_TEST_CALLS" || fail 'existing LaunchAgent was not replaced'
printf 'PASS: reinstallation preserves existing worker state\n'
