// Isolated telephony pane with synthetic API responses only.
const {chromium}=require('playwright');
const fs=require('fs'),path=require('path'),assert=require('node:assert/strict');
const base=path.resolve(__dirname,'../..');
const source=fs.readFileSync(base+'/internal/server/admin_console_telephony.go','utf8');
const constant=(text,name)=>text.split('const '+name+' = `')[1].split('`')[0];
const pane=constant(source,'adminTelephonyPane'),script=constant(source,'adminTelephonyJS');
const head=constant(fs.readFileSync(base+'/internal/server/admin_console_page.go','utf8'),'adminConsoleHead');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
 const page=await browser.newPage({viewport:{width:1100,height:900}}),writes=[],errors=[];
 page.on('pageerror',e=>errors.push(e.message));
 const init=`const $=id=>document.getElementById(id); const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;'); const jget=async url=>(await fetch(url)).json();`;
 const html='<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css">'+'<link rel="stylesheet" href="/admin-console.css">'+'</head><body><main id="adm-main">'+pane.replace('class="pane"','class="pane active"')+'</main><script>'+init+script+'\nloadPhoneCfg();</script></body></html>';
 await page.route('**/*',route=>{
  const r=route.request(),u=new URL(r.url());assert.equal(u.hostname,'fixture.invalid');
  if(u.pathname==='/')return route.fulfill({contentType:'text/html',body:html});
  if(['/style.css','/admin-console.css'].includes(u.pathname))return route.fulfill({path:base+'/web'+u.pathname});
  if(r.method()==='PUT')writes.push(r.postDataJSON());
  return route.fulfill({contentType:'application/json',body:JSON.stringify(u.pathname==='/api/vox/config'?{llm_model_standard:'fast',llm_model_oncall:'capable',outbound_system_prompt:'Outbound fixture',transfer_dialog_turns:'3'}:{})});
 });
 await page.goto('http://fixture.invalid/');
 await page.locator('#cf-llm_model_standard').waitFor();
 assert.equal(await page.locator('#cf-llm_model_oncall').count(),0);
 assert.equal(await page.locator('#tel-model').count(),1);
 await page.locator('#cf-llm_model_standard').fill('new-fast');
 await page.getByText('Save PrismVox model',{exact:true}).click();
 await page.locator('#tel-imsg').filter({hasText:'Saved'}).waitFor();
 assert.deepEqual(Object.keys(writes[0].values).sort(),['llm_model_standard','llm_url_standard']);
 await page.getByText('Save outbound instructions',{exact:true}).click();
 await page.locator('#tel-omsg').filter({hasText:'Saved'}).waitFor();
 assert.deepEqual(Object.keys(writes[1].values).sort(),['outbound_system_prompt']);
 for(const width of [1100,390]){
  await page.setViewportSize({width,height:900});
  for(const summary of await page.locator('.tel-section > summary').all()){
   await page.locator('.tel-section').evaluateAll(es=>es.forEach(e=>e.open=false));
   await summary.click();
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),await summary.textContent()+' overflow '+width);
  }
 }
 await page.locator('.tel-section').evaluateAll(es=>es.forEach((e,i)=>e.open=i===0));
 await page.screenshot({path:'/tmp/prism-telephony-mobile.png',fullPage:true});
 await page.setViewportSize({width:1100,height:900});
 await page.screenshot({path:'/tmp/prism-telephony-desktop.png',fullPage:true});
 assert.deepEqual(errors,[]);console.log('Telephony: scoped model saves and all sections at desktop/mobile widths passed');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exit(1)});
