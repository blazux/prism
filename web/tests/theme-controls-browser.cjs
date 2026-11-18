const {chromium}=require('playwright');
const fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'..');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
  const page=await browser.newPage();
  await page.route('http://theme.fixture/**',route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/theme.js')return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(root,'theme.js'),'utf8')});
   return route.fulfill({contentType:'text/html',body:'<!doctype html><html><head><script src="/theme.js"></script></head><body></body></html>'});
  });
  await page.goto('http://theme.fixture/');
  for(const file of ['widget-base.css','admin-console.css','ai-settings.css'])await page.addStyleTag({content:fs.readFileSync(path.join(root,file),'utf8')});
  const calendar=fs.readFileSync(path.join(root,'apps/calendar.html'),'utf8').split('<style>')[1].split('</style>')[0];
  await page.addStyleTag({content:calendar+'\n*{transition:none!important;animation:none!important}body{background:var(--bg1);overflow:auto}'});
  await page.addScriptTag({content:fs.readFileSync(path.join(root,'modal.js'),'utf8')});
  await page.evaluate(()=>{PrismModal.ensureStyle();document.body.innerHTML='<button id="widget" class="btn btn-accent">Generate</button><div class="cell today"><span id="calendar" class="dnum">11</span></div><button id="modal" class="pm-btn pm-primary">Save</button><div class="ai-settings"><button id="ai" class="ai-button primary">Save</button></div><span id="pending" class="badge pending">Pending</span><span id="approved" class="badge approved">Approved</span><span id="disabled" class="badge disabled">Disabled</span>'});
  const failures=[];
  for(const id of await page.evaluate(()=>Object.keys(PrismTheme.THEMES))){
   await page.evaluate(id=>PrismTheme.applyTheme(id),id);
   for(const hover of [false,true]){
    if(hover)await page.locator('#widget').hover();else await page.mouse.move(900,700);
    const ratios=await page.evaluate(()=>{
     const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d');
     function rgb(color,back='#000000'){ctx.clearRect(0,0,1,1);ctx.fillStyle=back;ctx.fillRect(0,0,1,1);ctx.fillStyle=color;ctx.fillRect(0,0,1,1);return [...ctx.getImageData(0,0,1,1).data].slice(0,3)}
     function lum(c){const a=c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);return a[0]*.2126+a[1]*.7152+a[2]*.0722}
     const surface=getComputedStyle(document.body).backgroundColor;
     return ['widget','calendar','modal','ai','pending','approved','disabled'].map(id=>{
      const style=getComputedStyle(document.getElementById(id)),a=lum(rgb(style.color)),b=lum(rgb(style.backgroundColor,surface));
      return {component:id,ratio:(Math.max(a,b)+.05)/(Math.min(a,b)+.05)};
     });
    });
    for(const item of ratios)if(item.ratio<4.5)failures.push({theme:id,state:hover?'widget-hover':'default',...item});
   }
  }
  if(failures.length){console.error(JSON.stringify({failures},null,2));process.exitCode=1;}
  else console.log('Theme controls: readable widget hover, Calendar, Admin, AI and modal controls across all presets.');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
