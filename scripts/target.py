#!/usr/bin/env python3
"""Run a pinned kaizen operation under the control-host reservation."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import subprocess
import sys

from team import connect, operation_lock, reservation


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--agent', required=True)
    p.add_argument('--token', required=True)
    p.add_argument('--db', default='/srv/isucon/state/team.sqlite3')
    p.add_argument('--config', default=str(Path(__file__).resolve().parents[1] / '.local/target.json'))
    p.add_argument('action', choices=('deploy', 'bench', 'observe', 'verify', 'evaluate', 'experiment', 'recover', 'stable', 'status'))
    p.add_argument('arguments', nargs=argparse.REMAINDER)
    args = p.parse_args()
    try:
        config_path = Path(args.config)
        if not config_path.is_file():
            raise ValueError('target is unconfigured; follow docs/RUNBOOK.md before enabling operations')
        config = json.loads(config_path.read_text())
        if not all(config.get(key) is True for key in ('enabled', 'confirmed_rules', 'confirmed_recovery_drill')):
            raise ValueError('target rules and recovery drill must be confirmed before enabling operations')
        script = Path(config['plugin_root']) / 'scripts' / (args.action + '.sh')
        project = Path(config['project']).resolve()
        if not script.is_file() or not (project / '.kaizen/config.sh').is_file():
            raise ValueError('plugin or target preset is missing')
        with operation_lock(args.db) as lock_handle:
            with contextlib.closing(connect(args.db)) as db:
                reservation(db, args.agent, args.token)
            environment = dict(os.environ, KAIZEN_PROJECT=str(project),
                               KAIZEN_PLUGIN_ROOT=str(Path(config['plugin_root']).resolve()))
            return subprocess.run(['bash', str(script), *args.arguments], cwd=str(project),
                                  env=environment, pass_fds=(lock_handle.fileno(),)).returncode
    except (ValueError, KeyError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
