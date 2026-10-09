from pathlib import Path
if "SOURCE" not in globals():
 SOURCE = (Path(__file__).resolve().parents[1] / "ai-settings.js").read_text()

import json
import os
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
 b=p.chromium.launch(headless=True,executable_path=os.environ.get('CHROMIUM_EXECUTABLE'),args=['--no-sandbox','--disable-dev-shm-usage'])
 page=b.new_page()
 state={'source':'configured','provider':'openai','baseURL':'https://api.openai.com/v1','model':'','chatVision':True,'defaultSource':'primary','sources':[{'id':'primary','name':'OpenAI','provider':'openai','baseURL':'https://api.openai.com/v1','model':'','keyConfigured':False}], 'embedding':{'useSameProvider':True,'sourceID':'','provider':'openai','baseURL':'https://api.openai.com/v1','model':''},'embeddingStatus':'not configured'}
 calls=[];fail={'save':False,'chat':False,'models':False};errors=[]
 metadata={'model-a':{'contextWindow':500000,'vision':True},'model-b':{'contextWindow':32768,'vision':False}}
 page.on('pageerror',lambda e:errors.append(str(e)))
 def route(r):
  if '/api/ai/config' not in r.request.url:
   r.fulfill(content_type='text/html',body='<div id="app"></div><a href="/away" id="leave">Leave</a>');return
  if r.request.method=='GET':r.fulfill(json=state);return
  data=r.request.post_data_json;calls.append(data);a=data['action']
  if fail.get(a,False):r.fulfill(status=502,json={'error':'Fixture provider unavailable'});return
  if a=='model_info':
   src=next(v for v in state['sources'] if v['id']==data['sourceID'])
   r.fulfill(json={'detected':metadata.get(data['model'],{}),'settings':src.get('modelSettings',{}).get(data['model'],{})});return
  if a=='model_settings':
   src=next(v for v in state['sources'] if v['id']==data['sourceID'])
   src.setdefault('modelSettings',{})[data['model']]=data['settings']
   r.fulfill(json={'ok':True});return
  if a.endswith('models'):r.fulfill(json={'models':['model-a','model-b'] if a=='models' else ['embed-a']});return
  if a in ('save','chat'):
   state.update(data)
   for src in state['sources']:
    src['keyConfigured']=bool(src.get('apiKey')) or src.get('keyConfigured',False);src.pop('apiKey',None)
   state['embedding'].pop('apiKey',None)
   r.fulfill(json={'ok':True});return
  r.fulfill(json={'ok':True})
 page.route('**/*',route)
 page.goto('https://fixture.test/')
 if 'CSS' in globals():page.add_style_tag(content=CSS)
 page.add_script_tag(content=SOURCE)
 def reopen():page.evaluate('renderAITab(document.getElementById("app"),{admin:true})')
 reopen()
 page.locator('[name=apiKey]').fill('fixture-only-key')
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 assert not any(v['action']=='save' for v in calls)
 page.locator('[name=modelChoice]').select_option('model-a')
 page.get_by_text('All changes saved.',exact=True).wait_for()
 assert state['model']=='model-a'
 assert page.locator('[data-default-source]').is_hidden()
 assert page.locator('[data-save]').is_hidden()
 assert page.locator('[data-current-model]').inner_text()=='model-a'
 page.locator('[data-model-capabilities]').filter(has_text='Context: 500k tokens (auto-detected) · Vision: supported (auto-detected)').wait_for()
 reopen()
 assert page.locator('[name=modelChoice]').input_value()=='model-a'
 assert page.locator('[name=apiKey]').input_value()==''
 print('Connect -> choose -> save -> reopen, key hidden: OK',flush=True)
 page.locator('[data-model-settings]').click()
 page.locator('[data-context-detected]').filter(has_text='500,000').wait_for()
 if 'CSS' in globals():
  for width in (1280,390):
   page.set_viewport_size({'width':width,'height':900})
   assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'), 'horizontal overflow'
   page.screenshot(path=f'/tmp/prism-model-dialog-{width}.png')
  page.set_viewport_size({'width':1280,'height':900})
 page.locator('[name=settingsModel]').fill('model-b')
 page.locator('[name=settingsModel]').press('Tab')
 page.locator('[name=contextMode]').select_option('manual')
 page.locator('[name=contextWindow]').fill('500000')
 page.locator('[name=modelVision]').select_option('no')
 page.locator('[data-model-form] button[type=submit]').click()
 page.get_by_text('Saved. Applied to the next message using this model.',exact=True).wait_for()
 assert state['model']=='model-a' and state['sources'][0]['model']=='model-a'
 assert state['sources'][0]['modelSettings']['model-b']=={'contextWindow':500000,'vision':False}
 page.locator('[data-model-close]').click()
 reopen()
 page.locator('[data-model-settings]').click()
 page.locator('[name=settingsModel]').fill('model-b')
 page.locator('[name=settingsModel]').press('Tab')
 page.locator('[name=contextWindow]').wait_for(state='visible')
 assert page.locator('[name=contextWindow]').input_value()=='500000'
 page.locator('[data-model-reset]').click()
 page.locator('[data-model-form] button[type=submit]').click()
 page.get_by_text('Saved. Applied to the next message using this model.',exact=True).wait_for()
 assert state['model']=='model-a'
 page.locator('[data-model-close]').click()
 print('Per-model override persists, does not change default, reset saves: OK',flush=True)
 # Switching the configured model must refresh context AND text-only detection.
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 page.locator('[name=modelChoice]').select_option('model-b')
 page.get_by_text('All changes saved.',exact=True).wait_for()
 page.locator('[data-model-capabilities]').filter(has_text='Context: 32,768 tokens (auto-detected) · Vision: text only (auto-detected)').wait_for()
 page.locator('[data-model-settings]').click()
 page.locator('[name=contextMode]').select_option('manual')
 page.locator('[name=contextWindow]').fill('500000')
 page.locator('[name=modelVision]').select_option('yes')
 page.locator('[data-model-form] button[type=submit]').click()
 page.get_by_text('Saved. Applied to the next message using this model.',exact=True).wait_for()
 page.locator('[data-model-close]').click()
 page.locator('[data-model-capabilities]').filter(has_text='Context: 500k tokens (manual) · Vision: supported (manual)').wait_for()
 page.locator('[data-model-settings]').click()
 page.locator('[data-model-reset]').click()
 page.locator('[data-model-form] button[type=submit]').click()
 page.get_by_text('Saved. Applied to the next message using this model.',exact=True).wait_for()
 page.locator('[data-model-close]').click()
 page.locator('[data-model-capabilities]').filter(has_text='Context: 32,768 tokens (auto-detected) · Vision: text only (auto-detected)').wait_for()
 metadata['model-b']={}
 reopen()
 page.locator('[data-model-capabilities]').filter(has_text='Context: not detected (conservative limit) · Vision: not detected (existing setting)').wait_for()
 metadata['model-b']={'contextWindow':32768,'vision':False}
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 page.locator('[name=modelChoice]').select_option('model-a')
 page.get_by_text('All changes saved.',exact=True).wait_for()
 print('Detected/manual/unknown capabilities and model switch: OK',flush=True)
 page.locator('[data-add-source]').click()
 assert page.locator('[data-sources] button').count()==2
 page.locator('[data-remove-source]').click()
 assert page.locator('[data-sources] button').count()==1
 assert len(state['sources'])==1
 print('Discard accidental unsaved source: OK',flush=True)
 page.locator('[data-add-source]').click()
 page.locator('[name=sourceName]').fill('Second')
 page.locator('[name=apiKey]').fill('fixture-other-key')
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 page.locator('[name=modelChoice]').select_option('model-b')
 page.get_by_text('All changes saved.',exact=True).wait_for()
 assert len(state['sources'])==2
 assert page.locator('[data-default-source]').is_visible()
 page.locator('[data-default-source]').click()
 page.get_by_text('All changes saved.',exact=True).wait_for()
 assert next(v for v in reversed(calls) if v['action']!='model_info')['action']=='chat'
 assert state['model']=='model-b'
 assert state['defaultSource']==state['sources'][1]['id']
 assert page.locator('[data-default-source]').is_hidden()
 assert page.locator('[data-action=models]').is_enabled()
 reopen()
 assert page.locator('[name=modelChoice]').input_value()=='model-b'
 page.locator('[data-sources] button').first.click()
 page.locator('[data-default-source]').click()
 page.get_by_text('All changes saved.',exact=True).wait_for()
 assert state['model']=='model-a'
 page.locator('[data-sources] button').nth(1).click()
 print('Use as default saves chat, releases controls and persists on reopen: OK',flush=True)
 page.once('dialog',lambda dialog:dialog.accept())
 page.locator('[data-remove-source]').click()
 page.get_by_text('Source removed and saved.',exact=True).wait_for()
 reopen()
 assert len(state['sources'])==1
 assert page.locator('[data-sources] button').count()==1
 print('Saved source removal persists on reopen: OK',flush=True)
 fail['models']=True
 page.locator('[data-action=models]').click()
 page.get_by_text('Connection failed: Fixture provider unavailable',exact=True).wait_for()
 fail['models']=False
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 fail['chat']=True
 page.locator('[name=modelChoice]').select_option('model-b')
 page.locator('[data-status]').filter(has_text='Fixture provider unavailable').wait_for()
 assert state['model']=='model-a'
 assert 'Unsaved changes' in page.locator('[data-save-state]').inner_text()
 page.once('dialog',lambda dialog:dialog.dismiss())
 page.locator('#leave').click()
 assert page.url=='https://fixture.test/'
 # Accelerate only the production request deadline, then stall a save.
 page.evaluate("window.originalTimeout=window.setTimeout; window.setTimeout=(fn,ms,...args)=>window.originalTimeout(fn,ms===40000?100:ms,...args)")
 stalled=[]
 page.route('**/api/ai/config',lambda route: stalled.append(route))
 page.locator('[name=modelChoice]').select_option('model-a')
 page.get_by_text('The server did not respond in time. Reopen settings to check whether your changes were saved before trying again.',exact=True).wait_for()
 assert page.locator('[data-action=models]').is_enabled()
 print('Stalled save times out and releases controls: OK',flush=True)
 for held in stalled:
  try:held.abort()
  except Exception:pass
 assert not errors,errors
 print('Connection/save errors visible; leaving unsaved changes warns: OK',flush=True)
 b.close()
