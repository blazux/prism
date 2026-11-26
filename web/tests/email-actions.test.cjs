const test = require('node:test'), assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../apps/email-manage.js'), 'utf8');
function fixture() {
  const calls = [], pending = [], error = {textContent: ''}, button = {disabled: false};
  let resets = 0, refreshes = 0, closed = 0;
  const dialog = {close: () => closed++, querySelector: selector => selector === '.move' ? button : selector === '.error' ? error : {value: 'Projects'}};
  const context = vm.createContext({loadFolders: async () => {}, folderOptions: () => '',
    mailDialog: () => dialog, notifyBadge: () => {},
    resetMailReading: () => resets++, refreshMail: async () => refreshes++,
    PrismAPI: {toast: () => {}},
    mailTool: (action, args) => {calls.push({action, ...args}); return new Promise((resolve, reject) => pending.push({resolve, reject}));}});
  vm.runInContext("let folder='INBOX', mailAccount='mailbox-A', current={uid:7}, mailFolders=[{name:'Projects',selectable:true}];" +
    source.slice(source.indexOf('async function mailAction('), source.indexOf('async function manageRules(')), context);
  return {context, calls, pending, button, error, counts: () => ({resets, refreshes, closed})};
}
test('an archive confirmation from the old account leaves the replacement reader intact', async () => {
  const f = fixture(), done = vm.runInContext("mailAction('archive')", f.context);
  assert.deepEqual(f.calls[0], {action: 'archive', uid: 7, folder: 'INBOX', account: 'mailbox-A', target: undefined});
  vm.runInContext("mailAccount='mailbox-B';current={uid:7}", f.context);
  f.pending[0].resolve({}); await done;
  assert.deepEqual(f.counts(), {resets: 0, refreshes: 0, closed: 0});
});
test('a move dialog retains the original account when connection settings change', async () => {
  const f = fixture(); await vm.runInContext('moveMessage()', f.context);
  vm.runInContext("mailAccount='mailbox-B';current={uid:7}", f.context);
  const done = f.button.onclick();
  assert.equal(f.calls[0].account, 'mailbox-A');
  assert.equal(f.calls[0].folder, 'INBOX'); assert.equal(f.calls[0].uid, 7);
  f.pending[0].reject(Object.assign(Error('Mailbox changed. Refresh Email.'), {status: 409})); await done;
  assert.match(f.error.textContent, /Mailbox changed/);
  assert.deepEqual(f.counts(), {resets: 0, refreshes: 0, closed: 0});
  assert.equal(f.button.disabled, false);
});
test('moving one message preserves another message opened while the request was running', async () => {
  const f = fixture(); await vm.runInContext('moveMessage()', f.context);
  const done = f.button.onclick();
  vm.runInContext('current={uid:8}', f.context);
  f.pending[0].resolve({}); await done;
  assert.deepEqual(f.counts(), {resets: 0, refreshes: 1, closed: 1});
  assert.equal(vm.runInContext('current.uid', f.context), 8);
});
