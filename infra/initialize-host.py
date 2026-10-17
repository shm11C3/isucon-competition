#!/usr/bin/env python3
"""Initialize the new control host; private Git keys stay on that host."""
import base64
import json
from pathlib import Path
import shlex
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]


def main():
    config = json.loads((ROOT / 'config/control-host.json').read_text())
    plugin = json.loads((ROOT / 'config/plugin.json').read_text())
    base = ['aws', '--region', config['region'], '--no-cli-pager']

    def run_json(command):
        return json.loads(subprocess.check_output(command, text=True))

    def aws(*arguments):
        return run_json([*base, *arguments, '--output', 'json'])

    def remote(command):
        result = aws('ssm', 'send-command', '--document-name', 'AWS-RunShellScript',
                     '--instance-ids', config['instance_id'], '--timeout-seconds', '120',
                     '--parameters', json.dumps({'commands': [command], 'executionTimeout': ['300']}))
        command_id = result['Command']['CommandId']
        print('SSM Command ID: ' + command_id, file=sys.stderr, flush=True)
        for _ in range(150):
            result = subprocess.run([*base, 'ssm', 'get-command-invocation', '--command-id', command_id,
                                     '--instance-id', config['instance_id'], '--output', 'json'],
                                    capture_output=True, text=True)
            if result.returncode:
                if 'InvocationDoesNotExist' not in result.stderr:
                    raise RuntimeError(result.stderr.strip())
            else:
                invocation = json.loads(result.stdout)
                if invocation['Status'] == 'Success':
                    return invocation.get('StandardOutputContent', '')
                if invocation['Status'] in ('Failed', 'TimedOut', 'Cancelled', 'Undeliverable', 'Terminated'):
                    raise RuntimeError(invocation.get('StandardErrorContent', '') + invocation.get('StandardOutputContent', ''))
            time.sleep(2)
        raise RuntimeError('SSM command is pending: ' + command_id + '; inspect before retrying')

    def user_shell(script):
        return remote(shlex.join(['runuser', '-u', 'isucon', '--', 'bash', '-lc', script]))

    def add_key(repository, key, write):
        keys = run_json(['gh', 'api', 'repos/' + repository + '/keys'])
        normalized = ' '.join(key.split()[:2])
        existing = next((k for k in keys if ' '.join(k['key'].split()[:2]) == normalized), None)
        if existing:
            if existing['read_only'] == write:
                raise RuntimeError('existing deploy key permission differs from the intended scope')
            return existing['id']
        created = run_json(['gh', 'api', '--method', 'POST', 'repos/' + repository + '/keys',
                            '-f', 'title=isucon-control-' + config['instance_id'], '-f', 'key=' + key,
                            '-F', 'read_only=' + ('false' if write else 'true')])
        return created['id']

    try:
        identity = aws('sts', 'get-caller-identity')
        if identity['Account'] != config['account_id']:
            raise RuntimeError('AWS account mismatch')
        remote('test -f /var/lib/isucon-control-bootstrap-complete')
        keys = user_shell("""set -euo pipefail
for name in control plugin; do
  [ -f "$HOME/.ssh/$name" ] || ssh-keygen -q -t ed25519 -N '' -C "isucon-$name" -f "$HOME/.ssh/$name"
done
python3 - <<'PY'
import json
from pathlib import Path
p = Path.home() / '.ssh'
print(json.dumps({name: (p / (name + '.pub')).read_text().strip() for name in ('control', 'plugin')}))
PY
""")
        keys = json.loads(keys)
        repo = config['repository']
        plugin_repo = plugin['repository'].removeprefix('https://github.com/').removesuffix('.git')
        control_key_id = add_key(repo, keys['control'], True)
        plugin_key_id = add_key(plugin_repo, keys['plugin'], False)
        official_keys = run_json(['gh', 'api', 'meta'])['ssh_keys']
        known_hosts = '\n'.join('github.com ' + key for key in official_keys) + '\n'
        ssh_config = '''Host github-control
  HostName github.com
  User git
  IdentityFile ~/.ssh/control
  IdentitiesOnly yes
  StrictHostKeyChecking yes
Host github-kaizen
  HostName github.com
  User git
  IdentityFile ~/.ssh/plugin
  IdentitiesOnly yes
  StrictHostKeyChecking yes
'''
        for name, content in (('known_hosts', known_hosts), ('config', ssh_config)):
            payload = base64.b64encode(content.encode()).decode()
            user_shell('set -euo pipefail; printf %s ' + shlex.quote(payload) + ' | base64 -d > "$HOME/.ssh/' + name + '"; chmod 600 "$HOME/.ssh/' + name + '"')
        repository_url = 'git@github-control:' + repo + '.git'
        plugin_url = 'git@github-kaizen:' + plugin_repo + '.git'
        setup = '\n'.join([
            'set -euo pipefail',
            'if [ ! -d /srv/isucon/repo/.git ]; then git clone ' + shlex.quote(repository_url) + ' /srv/isucon/repo; fi',
            'test "$(git -C /srv/isucon/repo remote get-url origin)" = ' + shlex.quote(repository_url),
            'git -C /srv/isucon/repo fetch origin',
            'git -C /srv/isucon/repo merge --ff-only origin/main',
            "git -C /srv/isucon/repo config user.name 'ISUCON Integration'",
            "git -C /srv/isucon/repo config user.email 'isucon@localhost'",
            'if [ ! -d /srv/isucon/kaizen-plugin/.git ]; then git clone ' + shlex.quote(plugin_url) + ' /srv/isucon/kaizen-plugin; fi',
            'git -C /srv/isucon/kaizen-plugin diff --quiet',
            'git -C /srv/isucon/kaizen-plugin fetch origin',
            'git -C /srv/isucon/kaizen-plugin checkout --detach ' + shlex.quote(plugin['revision']),
            'cd /srv/isucon/repo',
            'python3 -m unittest discover -s tests -v',
            'python3 scripts/team.py --db /srv/isucon/state/team.sqlite3 status',
        ])
        output = user_shell(setup)
        print(output)
        print(json.dumps({'instance_id': config['instance_id'], 'repository_deploy_key_id': control_key_id,
                          'plugin_deploy_key_id': plugin_key_id, 'plugin_revision': plugin['revision']}, indent=2))
        return 0
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
