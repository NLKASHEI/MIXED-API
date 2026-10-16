import { readFileSync } from 'node:fs';
import { Script } from 'node:vm';
import assert from 'node:assert/strict';

const source=readFileSync('internal/gateway/index.html','utf8');
for(const file of ['web/index.html','android/app/src/main/assets/index.html']) {
  assert.equal(readFileSync(file,'utf8'),source,`${file} differs from the shared console`);
}
assert.equal(readFileSync('internal/gateway/upstreams.json','utf8'),readFileSync('android/app/src/main/assets/upstreams.json','utf8'),'Android upstream catalog is stale');
for(const script of source.matchAll(/<script>([\s\S]*?)<\/script>/g))new Script(script[1]);
const ids=[...source.matchAll(/\bid="([^"]+)"/g)].map(m=>m[1]);
assert.equal(new Set(ids).size,ids.length,'duplicate element IDs');
assert(!source.includes('https://cdn.'),'console must work offline');
console.log('Shared console, JavaScript syntax and catalog parity passed.');
