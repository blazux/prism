const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const code=readFileSync(path.join(__dirname,'../ai-settings.js'),'utf8');
function fixture(){
 const values={sourceName:'Secondary',provider:'other',baseURL:'http://secondary/v1',apiKey:'typed-key',clearKey:false,model:'same-model',chatVision:true,useSameProvider:true,embeddingSource:'', 'embed-provider':'ollama','embed-baseURL':'http://embedding:11434','embed-apiKey':'','embed-clearKey':false,'embed-model':'embed','reindex':false};
 const fields=Object.fromEntries(Object.entries(values).map(([k,v])=>[k,typeof v==='boolean'?{checked:v}:{value:v}]));
 const ctx=vm.createContext({f:k=>fields[k]});
 vm.runInContext(`let selectedID='secondary',defaultID='primary',sources=[{id:'primary',name:'Primary',provider:'openai',baseURL:'https://api.openai.com/v1',model:'primary-model',apiKey:''},{id:'secondary',name:'Secondary',provider:'other',baseURL:'http://secondary/v1',model:'old-model',apiKey:''}];`+code.slice(code.indexOf('  function syncSource()'),code.indexOf('  function refresh()')),ctx);
 const run=expr=>JSON.parse(JSON.stringify(vm.runInContext(expr,ctx)));
 return {fields,ctx,run};
}
test('editing a secondary source retains default and other connections on save',()=>{
 const f=fixture(),p=f.run("payload('save')");
 assert.equal(p.defaultSource,'primary');assert.equal(p.model,'primary-model');assert.equal(p.sources.length,2);
 assert.equal(p.sources[1].apiKey,'typed-key');assert.equal(p.sources[0].apiKey,'');assert.equal(p.sources[1].model,'same-model');
});
test('testing a source sends only that connection without changing the chosen default',()=>{
 const f=fixture(),p=f.run("payload('test')");
 assert.equal(p.sourceID,'secondary');assert.equal(p.defaultSource,'secondary');assert.equal(p.sources.length,1);
 assert.equal(f.run('defaultID'),'primary');assert.equal(f.run('sources.length'),2);
});
test('same-provider embeddings follow the default rather than the source being edited',()=>{
 const f=fixture();assert.equal(f.run('effective()').baseURL,'https://api.openai.com/v1');
 vm.runInContext("defaultID='secondary'",f.ctx);
 assert.equal(f.run('effective()').baseURL,'http://secondary/v1');
});
test('embedding source reference and dedicated connection remain independent',()=>{
 const f=fixture();f.fields.useSameProvider.checked=false;f.fields.embeddingSource.value='secondary';
 assert.equal(f.run('effective()').baseURL,'http://secondary/v1');
 assert.equal(f.run("payload('save')").embedding.sourceID,'secondary');
 f.fields.embeddingSource.value='';assert.equal(f.run('effective()').baseURL,'http://embedding:11434');
});
