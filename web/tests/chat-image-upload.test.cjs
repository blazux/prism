const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8');

test('sending selected images preserves filenames and raw vision payloads', () => {
  const sent = [], displayed = [];
  const input = {value:'Process this image'};
  const context = {
    document:{getElementById:()=>input},
    FileReader:class {
      readAsDataURL(file) { this.onload({target:{result:`data:${file.type};base64,${file.bytes}`}}); }
    },
    appendUserMessage:(...args)=>displayed.push(args),
    autoResizeTextarea(){}, renderPreviews(){}, setStreaming(){},
    getDisabledTools:()=>[], send:msg=>sent.push(msg),
  };
  vm.createContext(context);
  vm.runInContext('let pendingImages = [], pendingFiles = [], isStreaming = false;', context);
  for (const name of ['addImageFromFile', 'sendChat']) {
    vm.runInContext(source.match(new RegExp(`function ${name}\\([^]*?^}`, 'm'))[0], context);
  }
  context.addImageFromFile({type:'image/png',name:'discard.png',bytes:'REMOVED'});
  context.addImageFromFile({type:'image/jpeg',name:'photo.jpg',bytes:'ORIGINAL'});
  // The preview removal acts on the complete image object, keeping its name
  // paired with its bytes when a selection is removed.
  vm.runInContext('pendingImages.splice(0, 1)', context);
  context.sendChat();
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])), {
    type:'chat', content:'Process this image', images:['ORIGINAL'],
    imageNames:['photo.jpg'], disabledTools:[],
  });
  assert.deepEqual(Array.from(displayed[0][1]), ['ORIGINAL']);
  assert.equal(vm.runInContext('pendingImages.length', context), 0);
  assert.equal(input.value, '');
  input.value = 'Follow-up without image';
  context.sendChat();
  assert.equal(sent[1].images, undefined);
  assert.equal(sent[1].imageNames, undefined);
});
