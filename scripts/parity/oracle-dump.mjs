#!/usr/bin/env node
// Dumps Timeline block parse+sort state as JSON lines, one per file with at
// least one block, matching `vaulty timeline dump`'s schema (DESIGN.md
// §10.3). Used by run.sh as the parity oracle.
//
// Usage: node oracle-dump.mjs <root>
// Env:   ORACLE_LIB — path to the vault's scripts/lib/timeline.mjs
//        (default /var/www/personal/me/scripts/lib/timeline.mjs)
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const oracleLibPath = process.env.ORACLE_LIB || '/var/www/personal/me/scripts/lib/timeline.mjs';
const {
  findTimelineBlocks,
  parseBlock,
  classifyOrder,
  sortAscending,
  serializeBlock,
} = await import(pathToFileURL(oracleLibPath).href);

const root = path.resolve(process.argv[2] || '.');
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

function headingLineOf(text, headingStart) {
  return text.slice(0, headingStart).split('\n').length;
}

const allFiles = dirs.flatMap((d) => findMdFiles(path.join(root, d))).sort();

for (const filePath of allFiles) {
  const relPath = path.relative(root, filePath).split(path.sep).join('/');
  const content = fs.readFileSync(filePath, 'utf8');
  const found = findTimelineBlocks(content);
  if (found.length === 0) continue;

  const blocks = found.map((blk) => {
    const headingLine = headingLineOf(content, blk.headingStart);
    const bodyText = content.slice(blk.bodyStart, blk.bodyEnd);
    const parsed = parseBlock(bodyText);
    if (!parsed.ok) {
      return { heading_line: headingLine, ok: false };
    }
    const order = classifyOrder(parsed.entries);
    const { entries: sortedEntries, gaps: sortedGaps } = sortAscending(
      parsed.entries,
      parsed.gaps,
      order,
    );
    const sortedBody = serializeBlock(sortedEntries, sortedGaps, parsed);
    return {
      heading_line: headingLine,
      ok: true,
      order,
      leading_blanks: parsed.leadingBlanks,
      trailing_blanks: parsed.trailingBlanks,
      gaps: parsed.gaps,
      entries: parsed.entries.map((e) => ({ key: e.dateKey, lines: e.lines })),
      sorted_body: sortedBody,
    };
  });

  console.log(JSON.stringify({ path: relPath, blocks }));
}
