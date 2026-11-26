const test = require('node:test'), assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../apps/email-selection.js'), 'utf8');
function runner() { const context = {window: {}}; vm.runInNewContext(source, context); return context.runMailSelectionBatch; }
const tick = () => new Promise(resolve => setImmediate(resolve));
const selection = (length = 7) => ({folder: 'INBOX', account: 'mailbox-one', action: 'archive', items: Array.from({length}, (_, i) => ({uid: i + 1, subject: 'Message ' + (i + 1)}))});

test('batch requests are bounded and retain their original account, folder and selected UIDs', async () => {
  const run = runner(), original = selection(), pending = [], sent = [];
  let active = 0, maximum = 0;
  const finished = run(original, (snapshot, item) => new Promise(resolve => {
    active++; maximum = Math.max(maximum, active);
    sent.push({folder: snapshot.folder, account: snapshot.account, uid: item.uid});
    pending.push(() => {active--; resolve();});
  }));
  assert.equal(sent.length, 3);
  original.folder = 'Archive'; original.account = 'different-mailbox'; original.items[0].uid = 999;
  while (pending.length) { pending.splice(0).forEach(resolve => resolve()); await tick(); }
  const result = await finished;
  assert.equal(maximum, 3);
  assert.deepEqual(sent.map(item => item.uid), [1,2,3,4,5,6,7]);
  assert(sent.every(item => item.folder === 'INBOX' && item.account === 'mailbox-one'));
  assert.equal(result.confirmed.length, 7); assert.equal(result.unconfirmed.length, 0);
});

test('partial failures stay explicit and an unconfirmed side effect is never retried automatically', async () => {
  const calls = new Map(), progress = [];
  const result = await runner()(selection(5), async (_, item) => {
    calls.set(item.uid, (calls.get(item.uid) || 0) + 1);
    if (item.uid === 2) throw Error('Connection lost after sending the request');
  }, state => progress.push(state.confirmed.length + state.unconfirmed.length));
  assert.equal(result.confirmed.length, 4);
  assert.equal(result.unconfirmed.length, 1);
  assert.equal(result.unconfirmed[0].item.uid, 2);
  assert.equal(result.unconfirmed[0].error.message, 'Connection lost after sending the request');
  assert.equal(result.unprocessed.length, 0);
  assert.deepEqual([...calls.values()], [1,1,1,1,1]);
  assert.deepEqual(progress, [1,2,3,4,5]);
});

test('Stop waits for in-flight requests and leaves the rest unprocessed', async () => {
  let stopped = false; const pending = [], calls = [];
  const finished = runner()(selection(), async (_, item) => {
    calls.push(item.uid); await new Promise(resolve => pending.push(resolve));
  }, () => {}, () => stopped);
  assert.equal(calls.length, 3);
  stopped = true; pending.forEach(resolve => resolve());
  const result = await finished;
  assert.deepEqual(calls, [1,2,3]);
  assert.equal(result.confirmed.length, 3);
  assert.deepEqual(Array.from(result.unprocessed, item => item.uid), [4,5,6,7]);
});

test('selection cannot send duplicate or invalid message identifiers', async () => {
  const snapshot = selection(0); snapshot.items = [0,-1,2,2,NaN,7,0x100000000].map(uid => ({uid}));
  const calls = [], result = await runner()(snapshot, async (_, item) => { calls.push(item.uid); });
  assert.deepEqual(calls, [2,7]); assert.equal(result.total, 2);
});
