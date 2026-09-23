const assert = require('node:assert/strict')
const fs = require('node:fs')
const vm = require('node:vm')
const source = fs.readFileSync('web/ai-settings.js', 'utf8')
const code = source.slice(source.indexOf('  let monitorGeneration'), source.indexOf('  async function load()'))
;(async () => {
  const node = {textContent:''}
  const saved = {sources:[{apiKey:'unsaved-draft'}]}
  let requests=0, refreshes=0
  const context = vm.createContext({
    pane:{isConnected:true,querySelector:()=>node},saved,
    refresh:()=>refreshes++,setTimeout:fn=>fn(),
    fetchConfig:async()=>({ok:true,json:async()=>{requests++;return {embeddingApplying:false,embeddingPending:false,embeddingStatus:'ready'}}}),
  })
  await vm.runInContext(code+'\nmonitorEmbedding(0)',context)
  assert.equal(requests,1)
  assert.equal(node.textContent,'Embeddings: ready')
  assert.equal(saved.sources[0].apiKey,'unsaved-draft','polling must preserve unsaved form state')
  assert.equal(refreshes,1)
  vm.runInContext('pane.isConnected=false',context)
  await vm.runInContext('monitorEmbedding(0)',context)
  assert.equal(requests,1,'closed settings must stop polling')
  vm.runInContext('pane.isConnected=true;monitorGeneration++',context)
  await vm.runInContext('monitorEmbedding(0)',context)
  assert.equal(requests,1,'superseded polling must stop')
})().catch(err=>{console.error(err);process.exitCode=1})
