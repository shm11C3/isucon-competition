import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export function parseRunOptions(args) {
  const options = { label: null, sourceSha: null, operator: null, recordPath: null };
  const seen = new Set();
  for (let i = 0; i < args.length; i++) {
    const key = args[i];
    if (!['--label', '--source-sha', '--operator', '--record'].includes(key) || seen.has(key) || !args[i + 1] || args[i + 1].startsWith('--')) {
      throw new Error(`invalid run option: ${key}`);
    }
    seen.add(key);
    const value = args[++i];
    if (key === '--label') options.label = value;
    if (key === '--source-sha') {
      if (!/^[a-f0-9]{40}$/i.test(value)) throw new Error('--source-sha must be a full 40-character SHA');
      options.sourceSha = value.toLowerCase();
    }
    if (key === '--operator') {
      if (!/^[A-Za-z0-9._-]{1,80}$/.test(value)) throw new Error('--operator must be 1-80 safe identifier characters');
      options.operator = value;
    }
    if (key === '--record') options.recordPath = value;
  }
  return options;
}

export function createRunRecorder(options, emit = console.error) {
  const runId = crypto.randomUUID();
  const file = options.recordPath ?? path.join(os.homedir(), '.config/isu-portal/benchmark-runs.jsonl');
  const base = { run_id: runId, operator: options.operator, source_sha: options.sourceSha, label: options.label };
  return {
    runId,
    write(event, fields = {}) {
      const record = { timestamp: new Date().toISOString(), event, ...base, ...fields };
      const line = JSON.stringify(record);
      // stderr is the immediate fallback for attribution if the local journal is unavailable.
      emit(line);
      try {
        fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
        fs.appendFileSync(file, `${line}\n`, { mode: 0o600 });
        fs.chmodSync(file, 0o600);
      } catch (e) {
        emit(`warning: run journal unavailable (${e.code ?? 'write error'}); record preserved above`);
      }
      return record;
    },
  };
}
