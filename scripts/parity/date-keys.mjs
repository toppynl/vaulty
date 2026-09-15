#!/usr/bin/env node
// Regenerates internal/timeline/testdata/date-keys.json: the oracle's
// parseDateToken() result for a synthetic list of date tokens covering every
// form and edge case in §5.4, run against the oracle so Go's ParseDate can be
// checked for parity with it. The token list below is hand-written and
// synthetic — it is never read from the vault (DESIGN.md §10.1/§15 Q3:
// fixtures stay synthetic even though the repo itself is private).
//
// Usage: node scripts/parity/date-keys.mjs [outFile]
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const oracleLibPath = process.env.ORACLE_LIB || '/var/www/personal/me/scripts/lib/timeline.mjs';
const { parseDateToken } = await import(pathToFileURL(oracleLibPath).href);

const outFile = path.resolve(process.argv[2] || 'internal/timeline/testdata/date-keys.json');

// Synthetic tokens covering every form and edge case in DESIGN.md §5.4:
// plain day dates across several months/years, month-only, the NL
// "(heel maand)" suffix, decade forms (0x/2x/3x), day-range forms (2-digit
// and 1-digit second day), a month-range form, invalid month/day numbers,
// an unparseable NL month-name token, junk, and the empty string.
const tokens = [
  '',
  'junk',
  'februari/maart 2025',
  '2022-04/05',
  '2023-11',
  '2023-11 (heel maand)',
  '2024-01-05',
  '2024-01-19',
  '2024-02-02',
  '2024-06-30',
  '2024-12-24',
  '2025-01',
  '2025-01-0x',
  '2025-01-1x',
  '2025-02-2x',
  '2025-02-3x',
  '2025-02-30',
  '2025-03-14',
  '2025-03-14/15',
  '2025-03-14/1',
  '2025-04-01/2',
  '2025-05',
  '2025-13-01',
  '2025-13',
  '2026-01-09',
  '2026-02-17',
  '2026-02-9/12',
];

const out = {};
for (const t of tokens) {
  out[t] = parseDateToken(t);
}

fs.mkdirSync(path.dirname(outFile), { recursive: true });
fs.writeFileSync(outFile, JSON.stringify(out, null, 2) + '\n', 'utf8');
console.log(`Wrote ${Object.keys(out).length} tokens to ${outFile}`);
