#!/usr/bin/env bash
set +x
set -euo pipefail
umask 077

root=$(cd "$(dirname "$0")/.." && pwd -P)
dry_run=${SELFHOST_UPDATER_DRY_RUN:-0}
case "$dry_run" in 0|1) ;; *) printf 'SELFHOST_UPDATER_DRY_RUN must be 0 or 1\n' >&2; exit 1 ;; esac

# JSON and plist serializers preserve literal credentials and escape host paths.
configure() {
  python3 - "$1" <<'PY'
import ipaddress
import json
import os
from pathlib import Path
import plistlib
import re
import stat
import sys
import tempfile
from urllib.parse import urlsplit

def require(condition, message):
    if not condition:
        sys.exit(message)

def no_symlinks(path):
    require(not any(p.is_symlink() for p in (path, *path.parents)),
            'Host paths must not contain symlinks')

def absolute_path(value):
    path = Path(value)
    require(path.is_absolute() and str(path) == value and '..' not in path.parts,
            'Host paths must be absolute and normalized')
    no_symlinks(path)
    return path

names = ('PRODUCTION_ENV_FILE', 'SERVER_URL', 'PRODUCTION_DATABASE',
         'CUSTOM_REMOTE', 'CUSTOM_BRANCH', 'WORKER_TOKEN')
values = {}
for name in names:
    value = os.environ.get('SELFHOST_' + name, '')
    require(bool(value.strip()) and '\r' not in value and '\n' not in value,
            'Missing or invalid SELFHOST_' + name)
    values[name] = value
env_file = absolute_path(values['PRODUCTION_ENV_FILE'])
require(env_file.is_file() and stat.S_IMODE(env_file.stat().st_mode) == 0o600,
        'Production env must be an existing regular file with mode 600')
require(not any(os.path.lexists(parent / '.git') for parent in env_file.parents),
        'Production env must be outside source checkouts')
require(re.fullmatch(r'[a-zA-Z][a-zA-Z0-9_]*', values['PRODUCTION_DATABASE']),
        'Invalid SELFHOST_PRODUCTION_DATABASE')
branch = values['CUSTOM_BRANCH']
require(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._/-]*', branch)
        and not any(part in branch for part in ('..', '//', '@{'))
        and not branch.endswith(('/', '.', '.lock')) and not branch.startswith('self-update/'),
        'Invalid SELFHOST_CUSTOM_BRANCH')
try:
    remote = urlsplit(values['CUSTOM_REMOTE'])
    server = urlsplit(values['SERVER_URL'])
    require(remote.scheme == 'https' and remote.hostname and remote.path
            and remote.username is None and remote.password is None
            and not remote.query and not remote.fragment,
            'Custom remote must be a credential-free HTTPS repository URL')
    require(server.hostname and server.username is None and server.password is None
            and server.path in ('', '/') and not server.query and not server.fragment
            and (server.scheme == 'https' or (server.scheme == 'http'
                 and server.hostname in ('localhost', '127.0.0.1', '::1'))),
            'Server URL must use HTTPS or loopback HTTP')
    resolve_ip = os.environ.get('SELFHOST_EXTERNAL_RESOLVE_IP', '')
    if resolve_ip:
        ipaddress.ip_address(resolve_ip)
except ValueError:
    sys.exit('Invalid URL or external resolver IP')

home = absolute_path(os.environ.get('HOME', ''))
base = home / '.multica/self-update'
agent_dir = home / 'Library/LaunchAgents'
config_path = base / 'worker.json'
plist_path = agent_dir / 'com.multica.self-update.plist'
directories = [home / '.multica', base, *(base / name for name in ('bin', 'repo.git', 'state', 'worktrees'))]
for path in [*directories, agent_dir, config_path, plist_path, base / 'bin/selfhost-updater',
             base / 'state/worker.stdout.log', base / 'state/worker.stderr.log']:
    no_symlinks(path)
require(not any(env_file == path or path in env_file.parents for path in directories[2:]),
        'Production env must be outside managed directories')
if sys.argv[1] == 'validate':
    sys.exit(0)

for directory in directories:
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory.chmod(0o700)
agent_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
config = {
    'repository_dir': str(base / 'repo.git'),
    'worktree_root': str(base / 'worktrees'),
    'state_dir': str(base / 'state'),
    'production_env_file': str(env_file),
    'production_database': values['PRODUCTION_DATABASE'],
    'custom_remote': values['CUSTOM_REMOTE'],
    'custom_branch': branch,
    'server_url': values['SERVER_URL'],
    'worker_token': values['WORKER_TOKEN'],
    'external_resolve_ip': resolve_ip,
}
plist = {
    'Label': 'com.multica.self-update',
    'ProgramArguments': [str(base / 'bin/selfhost-updater'), 'serve', '--config', str(config_path)],
    'WorkingDirectory': str(base / 'state'),
    'RunAtLoad': True,
    'KeepAlive': True,
    'ThrottleInterval': 15,
    'StandardOutPath': str(base / 'state/worker.stdout.log'),
    'StandardErrorPath': str(base / 'state/worker.stderr.log'),
    'EnvironmentVariables': {'HOME': str(home), 'PATH': os.environ['PATH']},
}

def write_private(path, data):
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(data)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

write_private(config_path, (json.dumps(config, indent=2) + '\n').encode())
write_private(plist_path, plistlib.dumps(plist))
PY
}

configure validate
if [[ "$dry_run" != 1 ]]; then
  command -v launchctl >/dev/null || { printf 'macOS launchctl is required\n' >&2; exit 1; }
fi
configure install
base="$HOME/.multica/self-update"
# An existing empty directory would prevent the worker's lazy initialization.
if [[ ! -e "$base/repo.git/HEAD" ]]; then
  git init --bare --quiet "$base/repo.git"
fi
[[ "$(git --git-dir="$base/repo.git" rev-parse --is-bare-repository)" == true ]] || {
  printf 'Managed repository must be bare\n' >&2
  exit 1
}
if [[ "$dry_run" == 1 ]]; then
  printf 'Dry run: wrote private worker configuration and LaunchAgent; build and service loading skipped\n'
  exit 0
fi

bin="$base/bin/selfhost-updater"
# Build to a new inode before replacing a potentially running executable.
staged_bin=$(mktemp "$base/bin/.selfhost-updater.XXXXXX")
trap 'rm -f "$staged_bin"' EXIT
(cd "$root/server" && go build -o "$staged_bin" ./cmd/selfhost-updater)
chmod 700 "$staged_bin"
mv -f "$staged_bin" "$bin"
service="gui/$(id -u)/com.multica.self-update"
if launchctl print "$service" >/dev/null 2>&1; then
  launchctl bootout "$service"
fi
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.multica.self-update.plist"
launchctl kickstart -k "$service"
printf 'Installed durable worker and loaded com.multica.self-update\n'
