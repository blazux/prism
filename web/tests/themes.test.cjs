const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../theme.js'),'utf8');
const historical=['prism','prism-dark','prism-light','graphite','slate','paper','boardroom','midnight','nord','solarized-dark','rose-pine','tech','cyberpunk','matrix','synthwave'];

function boot(storage=new Map(),account=true) {
 const vars=new Map(),attributes=new Map(),listeners=new Map(),messages=[],writes=[];
 let resolveReady;
 const ready=new Promise(resolve=>{resolveReady=resolve});
 const frames=[{contentWindow:{postMessage:message=>messages.push({frame:'widget',message})}}];
 const appFrame={contentWindow:{postMessage:message=>messages.push({frame:'app',message})}};
 const classes=new Set();
 const root={style:{setProperty:(k,v)=>vars.set(k,String(v)),removeProperty:k=>vars.delete(k),getPropertyValue:k=>vars.get(k)||''},
  classList:{add:(...v)=>v.forEach(x=>classes.add(x)),remove:(...v)=>v.forEach(x=>classes.delete(x)),contains:v=>classes.has(v)},
  setAttribute:(k,v)=>attributes.set(k,String(v)),getAttribute:k=>attributes.get(k)||null};
 const document={documentElement:root,querySelectorAll:()=>frames,getElementById:id=>id==='app-frame'?appFrame:null,
  createElement:()=>({children:[],appendChild(node){this.children.push(node)}})};
 const media={matches:false,addEventListener(){},removeEventListener(){},addListener(){}};
 const context={document,localStorage:{getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,String(v)),removeItem:k=>storage.delete(k)},
  CustomEvent:class {constructor(type,options){this.type=type;this.detail=options?.detail}},matchMedia:()=>media,
  getComputedStyle:()=>root.style,console};
 context.window={document,matchMedia:context.matchMedia,addEventListener(type,fn){if(!listeners.has(type))listeners.set(type,[]);listeners.get(type).push(fn)},
  dispatchEvent(event){for(const fn of listeners.get(event.type)||[])fn(event)}};
 if(account)context.window.PrismPreferences={getItem:k=>storage.get(k)??null,setItem:(k,v)=>{writes.push([k,String(v)]);storage.set(k,String(v))},ready};
 context.window.parent=context.window;
 vm.createContext(context);vm.runInContext(source,context);
 return {theme:context.window.PrismTheme,vars,attributes,storage,messages,writes,ready:()=>{resolveReady();return ready.then(()=>Promise.resolve())},
  preferencesChanged:(key,initial=false)=>context.window.dispatchEvent({type:'prism-preferences-changed',detail:{keys:[key],remote:true,initial}})};
}

function luminance(color) {
 assert.match(color,/^#[0-9a-f]{6}$/i,'contrast colors use opaque six-digit RGB');
 const [r,g,b]=color.slice(1).match(/../g).map(n=>parseInt(n,16)/255).map(n=>n<=0.04045?n/12.92:((n+0.055)/1.055)**2.4);
 return r*0.2126+g*0.7152+b*0.0722;
}
function contrast(a,b){const x=luminance(a),y=luminance(b);return (Math.max(x,y)+0.05)/(Math.min(x,y)+0.05)}
function readable(vars,label) {
 for(const text of ['--text','--text2','--text3'])for(const surface of ['--bg1','--bg2']){
  const ratio=contrast(vars[text],vars[surface]);
  assert(ratio>=4.5,`${label}: ${text} on ${surface} is ${ratio.toFixed(2)}:1, expected at least 4.5:1`);
 }
 let ratio=contrast(vars['--accent'],vars['--bg1']);
 assert(ratio>=4.5,`${label}: accent links are ${ratio.toFixed(2)}:1, expected at least 4.5:1`);
 ratio=contrast(vars['--on-accent'],vars['--accent']);
 assert(ratio>=4.5,`${label}: filled controls are ${ratio.toFixed(2)}:1, expected at least 4.5:1`);
 for(const [foreground,background]of [['--on-success','--green'],['--on-warning','--yellow'],['--on-danger','--red']]){
  ratio=contrast(vars[foreground],vars[background]);
  assert(ratio>=4.5,`${label}: ${foreground} on ${background} is ${ratio.toFixed(2)}:1, expected at least 4.5:1`);
 }
 assert(vars['--radius'],`${label}: missing reset value for corner radius`);
}

test('preset themes keep existing selections valid and provide readable text/control palettes',()=>{
 const {theme}=boot();
 for(const id of historical)assert(theme.getAllThemes()[id],`saved theme ${id} no longer exists`);
 for(const [id,preset]of Object.entries(theme.getAllThemes())){
  theme.applyTheme(id);readable(theme.activeVars(),id);
  assert.equal(theme.activeVars()['color-scheme'],preset.mode,`${id}: native controls disagree with the advertised light/dark palette`);
 }
 assert.notEqual(theme.THEMES['prism-light'].vars['--bg1'],theme.THEMES['prism-dark'].vars['--bg1']);
 assert.notEqual(theme.THEMES.matrix.vars['--accent'],theme.THEMES.paper.vars['--accent']);
});

test('generated palettes remain readable for bright, dark and low-saturation accents',()=>{
 const {theme}=boot();
 for(const accent of ['#000000','#ffffff','#ffff00','#00ff00','#7c3aed','#268bd2'])
  for(const mode of ['dark','light'])for(const harmony of ['monochromatic','analogous','complementary','triadic'])
   readable(theme.generatePalette(accent,mode,harmony),`${accent}/${mode}/${harmony}`);
});

test('switching themes persists selection and broadcasts complete values to widgets and apps',()=>{
 const {theme,vars,attributes,storage,messages}=boot();
 theme.applyTheme('matrix');
 assert.equal(storage.get(theme.KEYS.THEME),'matrix');assert.equal(attributes.get('data-theme'),'matrix');
 assert.equal(vars.get('--accent'),theme.activeVars()['--accent']);
 assert.equal(messages.length,2);assert.deepEqual(messages.map(m=>m.frame),['widget','app']);
 for(const {message}of messages){assert.equal(message.type,'prism-theme');assert(message.vars['--on-accent']);assert(message.vars['--radius']);assert(['dark','light'].includes(message.vars['color-scheme']))}
 const reloaded=boot(storage);assert.equal(reloaded.theme.getThemeId(),'matrix');
 for(const id of ['missing-preset','__proto__','constructor']) {
  theme.applyTheme(id);assert.equal(theme.getThemeId(),'prism-dark');
  storage.set(theme.KEYS.THEME,id);assert.equal(boot(storage).theme.getThemeId(),'prism-dark');
 }
});

test('partial custom themes are completed, survive reload and do not leave stale preset tokens',()=>{
 const {theme,storage,vars}=boot();
 theme.saveCustomTheme('custom-fixture','My Theme',{'--accent':'#ffaa00','--radius':'17px'});
 theme.applyTheme('custom-fixture');
 assert.equal(theme.getThemeId(),'custom-fixture');assert.equal(vars.get('--radius'),'17px');
 assert(theme.activeVars()['--text']);assert(theme.activeVars()['--bg1']);assert(theme.activeVars()['--on-accent']);
 const reloaded=boot(storage);assert.equal(reloaded.theme.getThemeId(),'custom-fixture');assert(reloaded.vars.get('--text'));
 reloaded.theme.applyTheme('prism-dark');assert.notEqual(reloaded.vars.get('--radius'),'17px');
 reloaded.theme.applyTheme('custom-fixture');reloaded.theme.deleteCustomTheme('custom-fixture');
 assert.equal(reloaded.theme.getThemeId(),'prism-dark');assert(!reloaded.theme.getAllThemes()['custom-fixture']);
});

test('a theme selected in another tab updates sandboxed widget palettes too',async()=>{
 const fixture=boot();await fixture.ready();
 const {theme,storage,messages,attributes,preferencesChanged}=fixture;
 storage.set(theme.KEYS.THEME,'paper');preferencesChanged(theme.KEYS.THEME);
 assert.equal(theme.getThemeId(),'paper');assert.equal(attributes.get('data-theme'),'paper');
 assert(messages.some(({frame,message})=>frame==='widget'&&message.vars['--accent']===theme.activeVars()['--accent']),
  'cross-tab changes did not reach sandboxed widgets');
});

test('loading account preferences and remote changes never save startup defaults',async()=>{
 const f=boot();
 assert.deepEqual(f.writes,[]);
 f.storage.set('prism-theme','matrix');
 f.storage.set('prism-ui-scale','125');
 await f.ready();
 assert.equal(f.attributes.get('data-theme'),'matrix');
 assert.equal(f.vars.get('--ui-scale'),'1.25');
 assert.deepEqual(f.writes,[],'loading settings wrote values back to the server');
 f.storage.set('prism-theme','paper');f.preferencesChanged('prism-theme');
 assert.equal(f.attributes.get('data-theme'),'paper');
 assert.deepEqual(f.writes,[],'remote refresh wrote settings back to the server');
 f.storage.set('prism-theme','matrix');f.preferencesChanged('prism-theme',true);
 assert.equal(f.attributes.get('data-theme'),'matrix','late initial preferences did not apply after a retry');
 f.theme.applyTheme('nord');
 assert.deepEqual(f.writes,[['prism-theme','nord']]);
});

test('generated-content theme helper uses memory without account access or browser persistence',()=>{
 const storage=new Map([['prism-theme','matrix']]);
 const f=boot(storage,false);
 assert.equal(f.theme.getThemeId(),'prism-dark');
 f.theme.applyTheme('nord');
 assert.equal(f.theme.getThemeId(),'nord');
 assert.equal(storage.get('prism-theme'),'matrix');
 assert.deepEqual(f.writes,[]);
});

test('widget documents receive the active complete palette and their explicit conversation ID',()=>{
 const {theme}=boot();theme.applyTheme('nord');
 for(const html of ['<html><head><title>Widget</title></head><body>Body</body></html>','<div>Fragment</div>']){
  const document=theme.composeWidgetDoc(html,'u7-dashboard');
  assert(document.includes('--accent:'+theme.activeVars()['--accent']));
  assert(document.includes('--on-accent:'+theme.activeVars()['--on-accent']));
  assert(document.includes('window.PRISM_SESSION="u7-dashboard"'));
  assert(document.includes('/widget-base.css'));assert(document.includes('prism-theme'));
 }
});
