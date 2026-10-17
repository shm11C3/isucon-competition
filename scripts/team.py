#!/usr/bin/env python3
"""Small coordination ledger. Run all writers on the single control host."""
import argparse
import contextlib
import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import sqlite3
import sys
import uuid


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def connect(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(str(path), timeout=10)
    db.row_factory = sqlite3.Row
    db.executescript("""
        CREATE TABLE IF NOT EXISTS tasks (
            id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
            evidence TEXT NOT NULL, priority INTEGER NOT NULL,
            status TEXT NOT NULL DEFAULT 'TODO', owner TEXT,
            base_sha TEXT, source_sha TEXT, review TEXT, reason TEXT, human_decision TEXT,
            updated_at TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS reservation (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            owner TEXT NOT NULL, token TEXT NOT NULL,
            purpose TEXT NOT NULL, acquired_at TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS events (
            id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL,
            actor TEXT NOT NULL, action TEXT NOT NULL, details TEXT NOT NULL
        );
    """)
    return db


@contextlib.contextmanager
def operation_lock(path):
    lock = Path(str(path) + '.operation.lock')
    lock.parent.mkdir(parents=True, exist_ok=True)
    with lock.open('a') as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('a target operation is running; inspect it before release or handoff')
        yield handle


def reservation(db, agent=None, token=None):
    row = db.execute('SELECT * FROM reservation').fetchone()
    if agent is not None and (row is None or row['owner'] != agent or row['token'] != token):
        raise ValueError('environment owner/token mismatch; do not reuse an old reservation')
    return dict(row) if row else None


def task(db, name):
    if not re.fullmatch(r'T-\d+', name):
        raise ValueError('task id must be T-0001 or equivalent')
    row = db.execute('SELECT * FROM tasks WHERE id=?', (int(name[2:]),)).fetchone()
    if row is None:
        raise ValueError('unknown task ' + name)
    return row


def public_task(row):
    result = dict(row)
    result['id'] = 'T-{:04d}'.format(row['id'])
    return result


def event(db, args, result):
    db.execute('INSERT INTO events(at,actor,action,details) VALUES (?,?,?,?)',
               (now(), getattr(args, 'agent', 'observer'), args.command,
                json.dumps(result, ensure_ascii=False)))


def dispatch(db, args):
    command = args.command
    if command == 'status':
        return {'environment': reservation(db), 'tasks': [public_task(r) for r in db.execute(
            'SELECT * FROM tasks ORDER BY priority DESC,id')]}
    if command == 'task-list':
        return [public_task(r) for r in db.execute('SELECT * FROM tasks ORDER BY priority DESC,id')]
    if command == 'task-create':
        cur = db.execute('INSERT INTO tasks(title,evidence,priority,updated_at) VALUES (?,?,?,?)',
                         (args.title, args.evidence, args.priority, now()))
        return public_task(db.execute('SELECT * FROM tasks WHERE id=?', (cur.lastrowid,)).fetchone())
    if command == 'task-claim':
        row = task(db, args.id) if args.id else db.execute(
            "SELECT * FROM tasks WHERE status='TODO' ORDER BY priority DESC,id LIMIT 1").fetchone()
        if row is None:
            return {'task': None}
        if row['status'] != 'TODO':
            raise ValueError('task is already owned or is not TODO')
        db.execute("UPDATE tasks SET status='CLAIMED',owner=?,updated_at=? WHERE id=?",
                   (args.agent, now(), row['id']))
        return public_task(db.execute('SELECT * FROM tasks WHERE id=?', (row['id'],)).fetchone())
    if command.startswith('task-'):
        row = task(db, args.id)
        if command == 'task-finish':
            env = reservation(db)
            if args.result == 'BLOCKED':
                if row['owner'] != args.agent or row['status'] not in ('CLAIMED', 'READY'):
                    raise ValueError('only the current task owner may block active work')
            elif not env or env['owner'] != args.agent or row['status'] not in ('READY', 'NEEDS_HUMAN'):
                raise ValueError('only the environment owner may finish a submitted candidate')
            if args.result == 'ACCEPTED' and (row['review'] == 'human' or row['status'] == 'NEEDS_HUMAN') and not args.human_decision:
                raise ValueError('human-reviewed candidate needs a recorded human decision reference')
            db.execute('UPDATE tasks SET status=?,reason=?,human_decision=?,updated_at=? WHERE id=?',
                       (args.result, args.reason, args.human_decision, now(), row['id']))
        else:
            if row['owner'] != args.agent:
                raise ValueError('task belongs to another session')
            if command == 'task-ready':
                if row['status'] != 'CLAIMED':
                    raise ValueError('only CLAIMED tasks may be submitted')
                if not all(re.fullmatch(r'[a-fA-F0-9]{40}', s) for s in (args.base, args.sha)):
                    raise ValueError('submit full 40-character base and source SHAs')
                db.execute("UPDATE tasks SET status='READY',base_sha=?,source_sha=?,review=?,reason=?,updated_at=? WHERE id=?",
                           (args.base, args.sha, args.review, args.reason, now(), row['id']))
            elif command == 'task-release':
                if row['status'] not in ('CLAIMED', 'BLOCKED'):
                    raise ValueError('only CLAIMED/BLOCKED tasks may return to TODO')
                db.execute("UPDATE tasks SET status='TODO',owner=NULL,reason=?,updated_at=? WHERE id=?",
                           (args.reason, now(), row['id']))
            elif command == 'task-handoff':
                if row['status'] not in ('CLAIMED', 'BLOCKED', 'READY', 'NEEDS_HUMAN'):
                    raise ValueError('cannot hand off a finished task')
                db.execute('UPDATE tasks SET owner=?,reason=?,updated_at=? WHERE id=?',
                           (args.to, args.reason, now(), row['id']))
        return public_task(db.execute('SELECT * FROM tasks WHERE id=?', (row['id'],)).fetchone())
    if command == 'env-acquire':
        if reservation(db):
            raise ValueError('environment is reserved; inspect status, do not steal it')
        db.execute('INSERT INTO reservation VALUES (1,?,?,?,?)',
                   (args.agent, uuid.uuid4().hex, args.purpose, now()))
        return reservation(db)
    previous = reservation(db, args.agent, args.token)
    if command == 'env-release':
        db.execute('DELETE FROM reservation')
        return {'released': previous, 'reason': args.reason}
    if command == 'env-handoff':
        db.execute('UPDATE reservation SET owner=?,token=?,acquired_at=?',
                   (args.to, uuid.uuid4().hex, now()))
        return {'environment': reservation(db), 'reason': args.reason}
    raise ValueError('unknown command')


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--db', default=os.environ.get('ISUCON_STATE_DB', '/srv/isucon/state/team.sqlite3'))
    commands = p.add_subparsers(dest='command', required=True)
    commands.add_parser('status')
    commands.add_parser('task-list')
    create = commands.add_parser('task-create')
    create.add_argument('--title', required=True)
    create.add_argument('--evidence', required=True)
    create.add_argument('--priority', type=int, default=50, choices=range(101), metavar='0..100')
    claim = commands.add_parser('task-claim')
    claim.add_argument('--agent', required=True)
    claim.add_argument('--id')
    for name in ('task-ready', 'task-release', 'task-handoff', 'task-finish'):
        c = commands.add_parser(name)
        c.add_argument('--id', required=True)
        c.add_argument('--agent', required=True)
        c.add_argument('--reason', required=True)
        if name == 'task-ready':
            c.add_argument('--base', required=True)
            c.add_argument('--sha', required=True)
            c.add_argument('--review', required=True, choices=('auto', 'human'))
        if name == 'task-handoff':
            c.add_argument('--to', required=True)
        if name == 'task-finish':
            c.add_argument('--result', required=True,
                           choices=('ACCEPTED', 'REJECTED', 'NEEDS_HUMAN', 'BLOCKED'))
            c.add_argument('--human-decision', help='reference to the actual human decision; never fabricate one')
    acquire = commands.add_parser('env-acquire')
    acquire.add_argument('--agent', required=True)
    acquire.add_argument('--purpose', required=True)
    for name in ('env-release', 'env-handoff'):
        c = commands.add_parser(name)
        c.add_argument('--agent', required=True)
        c.add_argument('--token', required=True)
        c.add_argument('--reason', required=True)
        if name == 'env-handoff':
            c.add_argument('--to', required=True)
    return p


def main():
    args = parser().parse_args()
    lock = operation_lock(args.db) if args.command in ('env-release', 'env-handoff') else contextlib.nullcontext()
    try:
        with lock, contextlib.closing(connect(args.db)) as db, db:
            db.execute('BEGIN IMMEDIATE')
            result = dispatch(db, args)
            if args.command not in ('status', 'task-list'):
                event(db, args, result)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    except (ValueError, sqlite3.Error, OSError) as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
