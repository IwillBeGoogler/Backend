// Writes catalog.json from the TypeScript core's generated catalog (catalog.generated.ts, not part of this project).
// Usage: node gen/gen-catalog.mjs path/to/catalog.generated.ts  (or CATALOG_SRC=...; needs Node 22.6+ for type stripping).
import { writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const arg = process.argv[2] ?? process.env.CATALOG_SRC;
if (!arg) {
  console.error('usage: node gen/gen-catalog.mjs path/to/catalog.generated.ts (or set CATALOG_SRC)');
  process.exit(1);
}
const src = resolve(arg);
const { CATALOG_ROWS, CLUSTER_ROWS } = await import(pathToFileURL(src).href);
writeFileSync(resolve(here, '../catalog.json'), JSON.stringify({ rows: CATALOG_ROWS, clusters: CLUSTER_ROWS }) + '\n');
console.log(`catalog.json: ${CATALOG_ROWS.length} rows, ${CLUSTER_ROWS.length} clusters`);
