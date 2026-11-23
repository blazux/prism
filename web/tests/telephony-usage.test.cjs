const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const page = fs.readFileSync(path.join(__dirname, '../../internal/server/admin_console_page.go'), 'utf8');
const start = page.indexOf('async function loadTelephonyUsage(){');
const end = page.indexOf('async function loadLogs()', start);
assert.ok(start >= 0 && end > start, 'load the real embedded usage function');
const source = page.slice(start, end);
const platformURL = '/api/platform';
const runtimeURL = '/api/vox/runtime-status';

function response(body, {status = 200, invalidJSON = false} = {}) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      if (invalidJSON) throw new SyntaxError('fixture-private JSON detail');
      return body;
    },
  };
}

function harness(steps, {missingBox = false} = {}) {
  const box = {style: {display: ''}, innerHTML: 'stale phone statistics'};
  const banner = {textContent: 'Could not save an unrelated group setting.'};
  const initialBanner = banner.textContent;
  const calls = [], cards = [], statusWrites = [];
  const queue = [...steps];
  const context = {
    $: id => id === 'us-tel' && !missingBox ? box : null,
    fetch: async url => {
      calls.push(url);
      const next = queue.shift();
      assert.ok(next, 'unexpected optional request: ' + url);
      assert.equal(url, next.url);
      if (next.error) throw next.error;
      return typeof next.result === 'function' ? next.result() : next.result;
    },
    jget() { throw new Error('optional telephony usage must not use the global error reporter'); },
    PrismSettingsState: {
      status(text) { statusWrites.push(text); banner.textContent = text; },
    },
    usCard(label, value) {
      cards.push([label, value]);
      return `<span data-label="${label}">${value}</span>`;
    },
  };
  vm.createContext(context);
  vm.runInContext(source, context);
  return {
    box, calls, cards, queue,
    load: () => context.loadTelephonyUsage(),
    assertBannerUnchanged() {
      assert.equal(banner.textContent, initialBanner);
      assert.deepEqual(statusWrites, []);
    },
  };
}

const platform = docked => ({url: platformURL, result: response({voxDocked: docked})});
const runtime = body => ({url: runtimeURL, result: response(body)});
const connected = {sip_ok: true, calls_today: 7, total_calls: 29, avg_duration_secs: 125, transfer_pct: 40, active_calls: 2};

test('absent telephony clears stale cards without a proxy call or global banner', async () => {
  const h = harness([platform(false)]);
  await h.load();
  assert.deepEqual(h.calls, [platformURL]);
  assert.equal(h.box.style.display, 'none');
  assert.equal(h.box.innerHTML, '');
  assert.deepEqual(h.cards, []);
  h.assertBannerUnchanged();
});

test('connected telephony retains all counters and its call-history link', async () => {
  const h = harness([platform(true), runtime(connected)]);
  await h.load();
  assert.deepEqual(h.calls, [platformURL, runtimeURL]);
  assert.equal(h.box.style.display, '');
  assert.deepEqual(h.cards, [['SIP', 'online'], ['calls today', 7], ['total', 29],
    ['avg duration', '2m5s'], ['% transferred', '40%'], ['active', 2]]);
  assert.ok(h.box.innerHTML.includes('href="/?app=telephony&group=admin&tab=logs"'));
  assert.doesNotMatch(h.box.innerHTML, /stale phone statistics|currently unavailable/);
  h.assertBannerUnchanged();
});

test('HTTP, JSON and network failures stay inside the optional telephony block', async t => {
  const cases = [
    ['platform HTTP failure', [{url: platformURL, result: response({}, {status: 503})}]],
    ['platform JSON failure', [{url: platformURL, result: response(null, {invalidJSON: true})}]],
    ['platform network failure', [{url: platformURL, error: new TypeError('fixture-private network detail')}]],
    ['offline Vox', [platform(true), {url: runtimeURL, result: response({error: 'VOX_URL unset'}, {status: 502})}]],
    ['runtime JSON failure', [platform(true), {url: runtimeURL, result: response(null, {invalidJSON: true})}]],
    ['runtime network failure', [platform(true), {url: runtimeURL, error: new TypeError('fixture-private network detail')}]],
  ];
  for (const [name, steps] of cases) {
    await t.test(name, async () => {
      const h = harness(steps);
      await h.load();
      assert.equal(h.box.style.display, '');
      assert.match(h.box.innerHTML, /Telephony usage is currently unavailable\./);
      assert.doesNotMatch(h.box.innerHTML, /stale phone statistics|fixture-private|VOX_URL/);
      assert.deepEqual(h.cards, []);
      assert.equal(h.queue.length, 0);
      h.assertBannerUnchanged();
    });
  }
});

test('malformed runtime payloads cannot become successful zero-valued statistics', async t => {
  for (const body of [null, [], 'not a runtime object', {error: 'fixture-private upstream error'}]) {
    await t.test(JSON.stringify(body), async () => {
      const h = harness([platform(true), runtime(body)]);
      await h.load();
      assert.match(h.box.innerHTML, /Telephony usage is currently unavailable\./);
      assert.deepEqual(h.cards, []);
      h.assertBannerUnchanged();
    });
  }
});

test('connected → disconnected → connected rereads platform state and replaces old cards', async () => {
  const h = harness([platform(true), runtime(connected), platform(false),
    platform(true), runtime({...connected, calls_today: 1, total_calls: 31, active_calls: 0})]);
  await h.load();
  assert.equal(h.box.style.display, '');
  await h.load();
  assert.equal(h.box.style.display, 'none');
  assert.equal(h.box.innerHTML, '');
  assert.deepEqual(h.calls, [platformURL, runtimeURL, platformURL]);
  await h.load();
  assert.deepEqual(h.calls, [platformURL, runtimeURL, platformURL, platformURL, runtimeURL]);
  assert.equal(h.box.style.display, '');
  assert.deepEqual(h.cards.slice(-6), [['SIP', 'online'], ['calls today', 1], ['total', 31],
    ['avg duration', '2m5s'], ['% transferred', '40%'], ['active', 0]]);
  assert.equal(h.queue.length, 0);
  h.assertBannerUnchanged();
});

test('stale cards are cleared before the asynchronous platform request finishes', async () => {
  let resolve;
  const pending = new Promise(done => { resolve = done; });
  const h = harness([{url: platformURL, result: () => pending}]);
  const loaded = h.load();
  assert.equal(h.box.style.display, 'none');
  assert.equal(h.box.innerHTML, '');
  resolve(response({voxDocked: false}));
  await loaded;
  h.assertBannerUnchanged();
});

test('pages without the optional block make no telephony request', async () => {
  const h = harness([], {missingBox: true});
  await h.load();
  assert.deepEqual(h.calls, []);
  h.assertBannerUnchanged();
});
