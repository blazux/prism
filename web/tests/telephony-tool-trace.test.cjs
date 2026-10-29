const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname,'../apps/telephony.html'), 'utf8');
class Element {
  constructor(tag) { this.tag=tag; this.children=[]; this.textContent=''; }
  append(...children) { this.children.push(...children); }
  set innerHTML(value) { throw Error('Tool data must be rendered as text'); }
}
const context = { document:{createElement:tag=>new Element(tag)}, t:{args:'Arguments',result:'Result',failed:'Failed',noResult:'Result not received',returned:'Result returned',truncated:'truncated'}, JSON, String };
vm.createContext(context);
vm.runInContext(source.slice(source.indexOf('function showTools('),source.indexOf('function addMessage(')), context);
test('supervision traces preserve results, failures and text safety',()=>{
 const root = new Element('div');
 context.showTools(root,[{name:'calendar',arguments:{action:'add',title:'<img src=x>'},is_error:true,result:'<script>bad()</script>'},{name:'note',outcome:'started',arguments:{},result:'fixture',result_truncated:true}]);
 assert.equal(root.children[0].children[0].textContent,'calendar · add · Failed');
 assert.ok(root.children[0].children[1].textContent.includes('<img src=x>'));
 assert.ok(root.children[0].children[1].textContent.includes('<script>bad()</script>'));
 assert.equal(root.children[1].children[0].textContent,'note · Result not received');
 assert.ok(root.children[1].children[1].textContent.includes('Result (truncated)'));
});
