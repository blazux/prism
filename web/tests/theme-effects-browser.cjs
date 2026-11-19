const {chromium}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'..');
const sceneThemes=['prism','tech','matrix','synthwave','dracula','amber-terminal','arcade','bubblegum'];
async function routeFixture(page){
 await page.route('http://effects.fixture/**',route=>{
  const file=new URL(route.request().url()).pathname;
  if(['/theme.js','/bgfx.js','/style.css'].includes(file))return route.fulfill({path:path.join(root,file)});
  return route.fulfill({contentType:'text/html',body:'<!doctype html><html><head><link rel="stylesheet" href="/style.css"><script src="/theme.js"></script></head><body><div id="bg-fx"></div><script src="/bgfx.js"></script></body></html>'});
 });
}
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
  const page=await browser.newPage({viewport:{width:960,height:640}}),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(()=>{
   const request=window.requestAnimationFrame.bind(window),cancel=window.cancelAnimationFrame.bind(window);
   window.fixtureFrames={executed:0,pending:new Set()};
   window.requestAnimationFrame=callback=>{
    let id;id=request(time=>{fixtureFrames.pending.delete(id);fixtureFrames.executed++;callback(time)});
    fixtureFrames.pending.add(id);return id;
   };
   window.cancelAnimationFrame=id=>{fixtureFrames.pending.delete(id);cancel(id)};
   // Exercise the browser's visibility event path deterministically in headless
   // Chromium; opening another headless page does not reliably hide this page.
   window.fixtureHidden=false;
   Object.defineProperty(document,'hidden',{get:()=>fixtureHidden,configurable:true});
  });
  await routeFixture(page);
  await page.emulateMedia({reducedMotion:'no-preference'});
  await page.goto('http://effects.fixture/');
  const canvasVisible=()=>page.evaluate(()=>{const canvas=document.getElementById('bgfx-canvas');return !!canvas&&getComputedStyle(canvas).display!=='none'});
  async function stopped(){
   await page.waitForFunction(()=>fixtureFrames.pending.size===0,null,{polling:25});
   const before=await page.evaluate(()=>fixtureFrames.executed);
   await page.waitForTimeout(120);
   assert.equal(await page.evaluate(()=>fixtureFrames.executed),before,'animation callbacks continued after stop');
  }
  async function animating(){
   const before=await page.evaluate(()=>fixtureFrames.executed);
   await page.waitForFunction(before=>fixtureFrames.executed>before+2,before,{polling:25});
   assert(await canvasVisible(),'expected a visible animated canvas');
  }
  for(const id of ['graphite','boardroom']){
   await page.evaluate(id=>PrismTheme.applyTheme(id),id);await stopped();assert.equal(await canvasVisible(),false);
   const pseudo=await page.evaluate(()=>['::before','::after'].map(p=>{const s=getComputedStyle(document.getElementById('bg-fx'),p);return {display:s.display,animation:s.animationName}}));
   assert(pseudo.every(s=>s.display==='none'&&s.animation==='none'),id+' still has a moving background layer');
  }
  await page.evaluate(()=>PrismTheme.applyTheme('matrix'));await animating();
  await page.evaluate(()=>PrismTheme.applyBgfx(0));await stopped();
  assert.equal(await page.locator('html').getAttribute('data-bgfx-disabled'),'true');
  assert.equal(await page.locator('#bg-fx').isVisible(),false);
  await page.evaluate(()=>PrismTheme.applyBgfx(100));await animating();
  await page.emulateMedia({reducedMotion:'reduce'});await stopped();assert(await canvasVisible(),'reduced motion should retain a static illustration');
  assert(await page.evaluate(()=>{const canvas=document.getElementById('bgfx-canvas');return canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data.some((v,i)=>i%4===3&&v>0)}),'reduced-motion canvas is empty');
  assert.equal(await page.evaluate(()=>getComputedStyle(document.getElementById('bg-fx'),'::after').animationName),'none');
  await page.emulateMedia({reducedMotion:'no-preference'});await animating();
  await page.evaluate(()=>{fixtureHidden=true;document.dispatchEvent(new Event('visibilitychange'))});await stopped();
  assert.equal(await page.locator('html').getAttribute('data-bgfx-paused'),'true');assert.equal(await canvasVisible(),false);
  assert.equal(await page.evaluate(()=>getComputedStyle(document.getElementById('bg-fx'),'::after').animationPlayState),'paused');
  await page.evaluate(()=>{fixtureHidden=false;document.dispatchEvent(new Event('visibilitychange'))});await animating();
  await page.evaluate(()=>PrismTheme.applyTheme('cyberpunk'));await animating();
  const nonemptyCanvas=()=>page.evaluate(()=>{
   const canvas=document.getElementById('bgfx-canvas');
   return canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data.some((v,i)=>i%4===3&&v>0);
  });
  assert(await nonemptyCanvas(),'animated circuit is empty');
  await page.emulateMedia({reducedMotion:'reduce'});await stopped();
  assert(await nonemptyCanvas(),'reduced-motion circuit is empty');
  const staticCircuit=await page.locator('canvas').evaluate(canvas=>canvas.toDataURL());
  await page.waitForTimeout(150);
  assert.equal(await page.locator('canvas').evaluate(canvas=>canvas.toDataURL()),staticCircuit,'reduced-motion circuit changed');
  for(const size of [{width:390,height:844},{width:160,height:120}]){
   await page.setViewportSize(size);
   await page.waitForFunction(size=>{const c=document.getElementById('bgfx-canvas');return c.width===size.width&&c.height===size.height},size);
   assert(await nonemptyCanvas(),'circuit disappeared after resizing to '+size.width+'×'+size.height);
  }
  await page.evaluate(()=>{const host=document.getElementById('bg-fx');host.style.width='0px';host.style.height='0px';window.dispatchEvent(new Event('resize'))});
  assert.deepEqual(await page.locator('canvas').evaluate(c=>[c.width,c.height]),[0,0]);
  await page.emulateMedia({reducedMotion:'no-preference'});await animating();
  await page.evaluate(()=>{document.getElementById('bg-fx').removeAttribute('style');window.dispatchEvent(new Event('resize'))});
  await animating();assert(await nonemptyCanvas(),'circuit did not recover from zero dimensions');

  // Every scene must remain recognizable with animation disabled, and recover
  // through the same lifecycle after resizing or temporarily hiding its host.
  // Use one mobile size per scene; the circuit above also covers a tiny canvas.
  await page.setViewportSize({width:960,height:640});
  for(const theme of sceneThemes){
   await page.evaluate(theme=>PrismTheme.applyTheme(theme),theme);await animating();
   assert(await nonemptyCanvas(),theme+' animated canvas is empty');
   const animated=await page.locator('canvas').evaluate(canvas=>canvas.toDataURL());
   await page.waitForTimeout(150);
   assert(await page.locator('canvas').evaluate((canvas,before)=>canvas.toDataURL()!==before,animated),theme+' canvas did not move');

   await page.emulateMedia({reducedMotion:'reduce'});await stopped();
   assert(await canvasVisible(),theme+' reduced motion hid the illustration');
   assert(await nonemptyCanvas(),theme+' reduced-motion canvas is empty');
   const still=await page.locator('canvas').evaluate(canvas=>canvas.toDataURL());
   await page.waitForTimeout(100);
   assert(await page.locator('canvas').evaluate((canvas,before)=>canvas.toDataURL()===before,still),theme+' reduced-motion image changed');
   await page.emulateMedia({reducedMotion:'no-preference'});await animating();

   await page.evaluate(()=>PrismTheme.applyBgfx(0));await stopped();
   assert.equal(await page.locator('#bg-fx').isVisible(),false,theme+' intensity zero left the background visible');
   await page.evaluate(()=>PrismTheme.applyBgfx(100));await animating();
   await page.evaluate(()=>{fixtureHidden=true;document.dispatchEvent(new Event('visibilitychange'))});await stopped();
   assert.equal(await canvasVisible(),false,theme+' hidden tab left the canvas visible');
   await page.evaluate(()=>{fixtureHidden=false;document.dispatchEvent(new Event('visibilitychange'))});await animating();

   await page.setViewportSize({width:390,height:844});
   await page.waitForFunction(()=>{const c=document.getElementById('bgfx-canvas');return c.width===390&&c.height===844});
   await animating();assert(await nonemptyCanvas(),theme+' disappeared after mobile resize');
   await page.evaluate(()=>{const host=document.getElementById('bg-fx');host.style.width='0px';host.style.height='0px';window.dispatchEvent(new Event('resize'))});
   assert.deepEqual(await page.locator('canvas').evaluate(c=>[c.width,c.height]),[0,0],theme+' ignored zero dimensions');
   await page.evaluate(()=>{document.getElementById('bg-fx').removeAttribute('style');window.dispatchEvent(new Event('resize'))});
   await animating();assert(await nonemptyCanvas(),theme+' did not recover from zero dimensions');
   await page.setViewportSize({width:960,height:640});
  }

  // Drive the real renderer with a deterministic clock and seed. Comparing its
  // pixels catches frame-based speed regressions without exposing production state.
  const clockPage=await browser.newPage({viewport:{width:960,height:640}});
  clockPage.on('pageerror',error=>errors.push(error.message));
  await clockPage.addInitScript(()=>{
   let nextId=0;const pending=new Map();
   window.requestAnimationFrame=callback=>{pending.set(++nextId,callback);return nextId};
   window.cancelAnimationFrame=id=>pending.delete(id);
   window.fixtureStep=time=>{const callbacks=[...pending.values()];pending.clear();for(const callback of callbacks)callback(time)};
  });
  await routeFixture(clockPage);await clockPage.emulateMedia({reducedMotion:'no-preference'});await clockPage.goto('http://effects.fixture/');
  const timings=await clockPage.evaluate(themes=>{
   const pixels=()=>{const canvas=document.getElementById('bgfx-canvas');return canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data};
   function render(theme,hz){
    PrismTheme.applyTheme('graphite');let seed=42;
    Math.random=()=>{seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed/4294967296};
    PrismTheme.applyTheme(theme);fixtureStep(0);const initial=pixels();
    for(let frame=1;frame<=hz;frame++)fixtureStep(frame*1000/hz);
    return {initial,final:pixels()};
   }
   return themes.map(theme=>{
    const sixty=render(theme,60),fast=render(theme,120);let moved=0,maxDelta=0,different=0;
    for(let i=0;i<sixty.final.length;i++){
     if(sixty.initial[i]!==sixty.final[i])moved++;
     const delta=Math.abs(sixty.final[i]-fast.final[i]);
     if(delta)different++;maxDelta=Math.max(maxDelta,delta);
    }
    return {theme,moved,maxDelta,different,channels:sixty.final.length};
   });
  },['cyberpunk',...sceneThemes.filter(theme=>theme!=='tech')]);
  for(const timing of timings){
   assert(timing.moved>100,timing.theme+' did not move during one second');
   assert(timing.maxDelta<=1&&timing.different<timing.channels*0.001,timing.theme+' speed depends on refresh rate: '+JSON.stringify(timing));
  }
  await clockPage.close();
  assert.deepEqual(errors,[]);
  console.log('Theme effects: professional stillness; Prism, Cyberpunk and seven retro scenes render, animate, respect reduced motion, intensity and visibility, recover after resize, and keep equivalent 60/120 Hz motion for the new scenes.');
 } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1});
