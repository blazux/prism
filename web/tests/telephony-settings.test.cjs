const {test}=require('node:test');
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../../internal/server/admin_console_telephony.go'),'utf8');
const marker='const adminTelephonyJS = `';
const script=source.slice(source.indexOf(marker)+marker.length,source.lastIndexOf('`'));
const json=(body,status=200)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
function harness({current,save,apply,password=''}={}){
 const elements={
  'tel-smsg':{textContent:''},'tel-sip-save':{disabled:false},'sip-password':{value:password},
  'sip-registrar':{value:' new.example.test '},'sip-registrar_ip':{value:'192.0.2.1'},'sip-username':{value:'fixture'},
  'sip-domain':{value:'example.test'},'sip-tls_port':{value:'5072'},'sip-callerid_name':{value:'Reception'},'sip-transfer_method':{value:'refer'},
  'tel-voice':{},'tel-voicehint':{},'tel-clonebox':{style:{}},
 };
 const calls=[];
 const ctx={
  window:{addEventListener:()=>{}},
  $:id=>elements[id],esc:s=>s,loadTelephony:()=>{},setTimeout:()=>{},
  jget:async()=>current===undefined?{sip_transport_mode:'udp',sip_udp_port:'5090',sip_registrar_match:'192.0.2.0/24',sip_password:'',unused:'ignore'}:current,
  fetch:async(url,options)=>{calls.push({url,options});const result=url.endsWith('/apply')?apply:save;if(result instanceof Error)throw result;return result||json(url.endsWith('/apply')?{ok:true,detail:'Reloaded'}:{ok:true});},
 };
 vm.createContext(ctx);vm.runInContext(script,ctx);
 return {ctx,elements,calls};
}
test('SIP save retains hidden transport settings and explicitly preserves blank password',async()=>{
 const h=harness();await h.ctx.saveTelSip();
 assert.equal(h.calls.length,2);
 const body=JSON.parse(h.calls[0].options.body);
 assert.equal(body.sip_password,'');assert.equal(body.sip_transport_mode,'udp');
 assert.equal(body.sip_udp_port,'5090');assert.equal(body.sip_registrar_match,'192.0.2.0/24');
 assert.equal(body.sip_registrar,'new.example.test');assert.equal(body.unused,undefined);
 assert.equal(h.elements['tel-smsg'].textContent,'✓ Saved & applied');
 assert.equal(h.elements['tel-sip-save'].disabled,false);
});
test('SIP read/save failure never reloads or announces success',async()=>{
 for(const options of [{current:null},{save:json({detail:'Invalid configuration'},400)},{save:new Error('Network unavailable')}]){
  const h=harness({...options,password:'fixture-only'});await h.ctx.saveTelSip();
  assert.ok(h.calls.length<=1);assert.ok(!h.elements['tel-smsg'].textContent.includes('✓'));
  assert.equal(h.elements['sip-password'].value,'fixture-only');assert.equal(h.elements['tel-sip-save'].disabled,false);
 }
});
test('SIP reload failure stays visible even after a successful database save',async()=>{
 for(const response of [json({ok:false,detail:'ARI reload refused'}),json({detail:'Upstream unavailable'},502),new Error('Network unavailable'),json({})]){
  const h=harness({apply:response,password:'fixture-only'});await h.ctx.saveTelSip();
  assert.equal(h.calls.length,2);
  assert.match(h.elements['tel-smsg'].textContent,/Saved, but could not apply:/);
  assert.ok(!h.elements['tel-smsg'].textContent.includes('✓'));
  assert.equal(h.elements['sip-password'].value,'');assert.equal(h.elements['tel-sip-save'].disabled,false);
 }
});
test('double-click does not trigger a second save or reload',async()=>{
 const h=harness();let release;
 const pending=new Promise(resolve=>release=resolve);
 h.ctx.jget=()=>pending;
 const first=h.ctx.saveTelSip();await h.ctx.saveTelSip();
 release({sip_transport_mode:'tls'});await first;
 assert.equal(h.calls.length,2);
});
test('voice cloning follows actual capabilities; unavailable engine is not called local',async()=>{
 for(const [backend,display] of [[{backend:'elevenlabs',clone_enabled:true},'flex'],[{backend:'melo',clone_enabled:false},'none'],[null,'none']]){
  const h=harness();h.ctx.jget=async url=>url.endsWith('/backend')?backend:{voices:['Fixture voice'],current:'Fixture voice'};
  await h.ctx.loadTelVoiceGreeting();
  assert.equal(h.elements['tel-clonebox'].style.display,display);
  assert.ok(h.elements['tel-voice'].innerHTML.includes('Fixture voice'));
  if(!backend)assert.match(h.elements['tel-voicehint'].textContent,/status unavailable/);
 }
});
