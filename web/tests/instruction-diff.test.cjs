const test = require('node:test');
const assert = require('node:assert/strict');
const {diff, context} = require('../static/instruction-diff.js');
const split = text => text === '' ? [] : text.split('\n');
function verify(before, after, budget) {
  const result = diff(before, after, budget);
  assert.deepEqual(result.lines.filter(x => x.kind !== 'add').map(x => x.text), split(before));
  assert.deepEqual(result.lines.filter(x => x.kind !== 'remove').map(x => x.text), split(after));
  return result;
}
test('instruction diff preserves additions, removals, whitespace, Unicode, and end-of-file changes', () => {
  for (const [a,b] of [['',''],['','New instruction'],['Old instruction',''],['same\nold\nend','same\nnew\nend'],['a\nb','a\nb\n'],['text\r\nnext','text\nnext'],['  keep spaces\n🙂',' keep spaces\n🚀'],['null','null']]) verify(a,b);
});
test('all separated instruction changes are retained and only unchanged context is collapsed', () => {
  const before = Array.from({length:40},(_,i)=>`line ${i}`), after = [...before];
  after[2] = 'first edit'; after[36] = 'second edit';
  const result = verify(before.join('\n'),after.join('\n'));
  const displayed = context(result.lines);
  assert.equal(displayed.filter(x=>x.kind==='add').length,2);
  assert.equal(displayed.filter(x=>x.kind==='remove').length,2);
  assert.ok(displayed.some(x=>x.kind==='gap'&&x.count>10));
});
test('large rewrite fallback preserves complete old and new content', () => {
  const result = verify('a\nb\nc\nd','w\nx\ny\nz',0);
  assert.equal(result.coarse,true);
  assert.equal(result.lines.length,8);
});
test('repeated lines and random edits reconstruct both versions with a minimal edit script', () => {
  let seed = 42;
  const random = n => { seed = (Math.imul(seed,1664525)+1013904223)>>>0; return seed%n; };
  for(let trial=0;trial<300;trial++) {
    const a = Array.from({length:random(10)},()=>String(random(4))), b = Array.from({length:random(10)},()=>String(random(4)));
    const result = verify(a.join('\n'),b.join('\n'));
    const rows = Array.from({length:a.length+1},()=>Array(b.length+1).fill(0));
    for(let i=0;i<=a.length;i++)for(let j=0;j<=b.length;j++)rows[i][j]=!i?j:!j?i:a[i-1]===b[j-1]?rows[i-1][j-1]:Math.min(rows[i-1][j],rows[i][j-1])+1;
    assert.equal(result.lines.filter(x=>x.kind!=='same').length,rows[a.length][b.length]);
  }
});
