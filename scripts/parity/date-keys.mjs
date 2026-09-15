#!/usr/bin/env node
// Regenerates internal/timeline/testdata/date-keys.json: the oracle's
// parseDateToken() result for every distinct date token used in the vault's
// Timeline entries, plus a few hand-picked edge cases. Run once (or after a
// new date form appears in the vault); date_test.go asserts Go's ParseDate
// against the committed file.
//
// Usage: node scripts/parity/date-keys.mjs [vaultRoot] [outFile]
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const oracleLibPath = process.env.ORACLE_LIB || '/var/www/personal/me/scripts/lib/timeline.mjs';
const { findTimelineBlocks, parseDateToken } = await import(pathToFileURL(oracleLibPath).href);

const root = path.resolve(process.argv[2] || '/var/www/personal/me');
const outFile = path.resolve(process.argv[3] || 'internal/timeline/testdata/date-keys.json');
const dirs = ['wiki', 'me', 'now', 'archive'];

function findMdFiles(dir) {
  const files = [];
  if (!fs.existsSync(dir)) return files;
  (function walk(d) {
    for (const entry of fs.readdirSync(d)) {
      if (entry.startsWith('.')) continue;
      const p = path.join(d, entry);
      const st = fs.statSync(p);
      if (st.isDirectory()) walk(p);
      else if (entry.endsWith('.md')) files.push(p);
    }
  })(dir);
  return files;
}

const tokens = new Set();
const entryRe = /^- \*\*([^*]+)\*\*/;
for (const filePath of dirs.flatMap((d) => findMdFiles(path.join(root, d)))) {
  const content = fs.readFileSync(filePath, 'utf8');
  for (const blk of findTimelineBlocks(content)) {
    const bodyText = content.slice(blk.bodyStart, blk.bodyEnd);
    for (const line of bodyText.split('\n')) {
      const m = line.match(entryRe);
      if (m) tokens.add(m[1]);
    }
  }
}

// Edge cases not guaranteed to be present in the vault today.
for (const t of [
  '2026-08-0x',
  '2026-08-3x',
  '2026-09-10/1',
  '2026-09-9/12',
  'junk',
  'juli/augustus 2026',
  '2026-13-01',
  '',
]) {
  tokens.add(t);
}

const out = {};
for (const t of [...tokens].sort()) {
  out[t] = parseDateToken(t);
}

fs.mkdirSync(path.dirname(outFile), { recursive: true });
fs.writeFileSync(outFile, JSON.stringify(out, null, 2) + '\n', 'utf8');
console.log(`Wrote ${Object.keys(out).length} tokens to ${outFile}`);
