const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm');
const fs=require('node:fs'),path=require('node:path');
const app=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');
const code=app.slice(app.indexOf('function authorizeWidget('),app.indexOf('function refreshWidgetAuthorization('));
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject};};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const calls=[],errors=[],toasts=[];let frame;
 const rec={id:'same-widget',sessionID:'main',content:'<p>Widget</p>',widgetOrigin:'http://widget.test',el:{querySelector:()=>frame}};
 const widgets=new Map([[rec.id,rec]]);
 const ctx=vm.createContext({widgets,WIDGET_HOSTING:{grantURL:'/api/widget/grant'},window:{PrismTheme:{composeWidgetDoc:content=>content}},
  legacyWidgetStorage:()=>({}),widgetLoadingError:item=>errors.push(item),showToast:value=>toasts.push(value),fetch:()=>{const request=deferred();calls.push(request);return request.promise;}});
 vm.runInContext(code,ctx);
 function source(){return {messages:[],postMessage(value){this.messages.push(value);}};}
 function mount(source){frame={contentWindow:source};}
 function authorize(source){ctx.authorizeWidget(rec,source,rec.widgetOrigin,true);}
 function accept(index){calls[index].resolve({ok:true,json:async()=>({token:'synthetic-grant-'+index})});}
 return {calls,errors,toasts,rec,source,mount,authorize,accept};
}
test('restored widget can authorize while its removed frame still awaits a grant',async()=>{
 const f=fixture(),old=f.source(),current=f.source();f.mount(old);f.authorize(old);
 f.mount(current);f.authorize(current);assert.equal(f.calls.length,2);
 f.accept(0);await tick();assert.equal(old.messages.length,0);assert.equal(f.rec.authorizing,current);
 f.accept(1);await tick();assert.equal(current.messages.length,1);assert.equal(current.messages[0].type,'prism-widget-render');assert.equal(!!f.rec.authorizing,false);
});
test('failed old grant cannot replace a newer frame loader with an error',async()=>{
 const f=fixture(),old=f.source(),current=f.source();f.mount(old);f.authorize(old);f.mount(current);f.authorize(current);
 f.calls[0].reject(new Error('Old request lost'));await tick();
 assert.equal(f.errors.length,0);assert.equal(f.toasts.length,0);assert.equal(old.messages.length,0);assert.equal(f.rec.authorizing,current);
 f.accept(1);await tick();assert.equal(current.messages.length,1);
});
test('the same frame shares its pending authorization and reports its own failure',async()=>{
 const f=fixture(),current=f.source();f.mount(current);f.authorize(current);f.authorize(current);assert.equal(f.calls.length,1);
 f.calls[0].reject(new Error('Current request failed'));await tick();
 assert.equal(f.errors.length,1);assert.equal(f.toasts.length,1);assert.equal(current.messages[0].type,'prism-widget-error');assert.equal(!!f.rec.authorizing,false);
});
test('theme acknowledgement preserves author important styles without negotiating forever',()=>{
 const theme=fs.readFileSync(path.join(__dirname,'../theme.js'),'utf8');
 const bootstrap=theme.match(/const WIDGET_BOOTSTRAP = `([\s\S]*?)`;/)[1];
 const inline=new Map(),messages=[];let receive;
 const parent={postMessage:value=>messages.push(value)};
 const ctx={window:{parent,PRISM_PARENT_ORIGIN:'http://dashboard.test',addEventListener:(_,fn)=>{receive=fn;}},
  document:{documentElement:{style:{setProperty:(key,value)=>inline.set(key,value),getPropertyValue:key=>inline.get(key)||''}}},
  getComputedStyle:()=>({getPropertyValue:key=>key==='color-scheme'?'light':'#ffffff'})};
 vm.runInNewContext(bootstrap,ctx);
 const vars={'--bg1':'#10151f','--text':'#eef2fb','--text2':'#b9c3d6','--accent':'#9baeff','--border':'#293549','color-scheme':'dark'};
 receive({source:parent,origin:'http://dashboard.test',data:{type:'prism-theme',vars}});
 assert.equal(messages.length,1);assert.equal(messages[0].type,'prism-widget-theme-ready');
 assert.equal(messages[0].theme,Object.values(vars).join('|'));
 assert.equal(ctx.getComputedStyle().getPropertyValue('color-scheme'),'light','author important declaration must remain effective');
});
