from pathlib import Path
SOURCE = (Path(__file__).resolve().parents[1] / "ai-settings.js").read_text()

import json
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
 b=p.chromium.launch(headless=True,args=['--no-sandbox','--disable-dev-shm-usage'])
 page=b.new_page()
 state={'source':'configured','provider':'openai','baseURL':'https://api.openai.com/v1','model':'','chatVision':True,'defaultSource':'primary','sources':[{'id':'primary','name':'OpenAI','provider':'openai','baseURL':'https://api.openai.com/v1','model':'','keyConfigured':False}], 'embedding':{'useSameProvider':True,'sourceID':'','provider':'openai','baseURL':'https://api.openai.com/v1','model':''},'embeddingStatus':'not configured'}
 calls=[];fail={'save':False,'models':False};errors=[]
 page.on('pageerror',lambda e:errors.append(str(e)))
 def route(r):
  if '/api/ai/config' not in r.request.url:
   r.fulfill(content_type='text/html',body='<div id="app"></div><a href="/away" id="leave">Leave</a>');return
  if r.request.method=='GET':r.fulfill(json=state);return
  data=r.request.post_data_json;calls.append(data);a=data['action']
  if fail.get(a,False):r.fulfill(status=502,json={'error':'Fixture provider unavailable'});return
  if a.endswith('models'):r.fulfill(json={'models':['model-a','model-b'] if a=='models' else ['embed-a']});return
  if a=='save':
   state.update(data)
   for src in state['sources']:
    src['keyConfigured']=bool(src.get('apiKey')) or src.get('keyConfigured',False);src.pop('apiKey',None)
   state['embedding'].pop('apiKey',None)
   r.fulfill(json={'ok':True});return
  r.fulfill(json={'ok':True})
 page.route('**/*',route)
 page.goto('https://fixture.test/')
 page.add_script_tag(content=SOURCE)
 def reopen():page.evaluate('renderAITab(document.getElementById("app"),{admin:true})')
 reopen()
 page.locator('[name=apiKey]').fill('fixture-only-key')
 page.locator('[data-action=models]').click()
 page.get_by_text('Connected · 2 models',exact=True).wait_for()
 assert not any(v['action']=='save' for v in calls)
 page.locator('[name=modelChoice]').select_option('model-a')
 page.get_by_text('Saved — configuration is active.',exact=True).wait_for()
 assert state['model']=='model-a'
 reopen()
 assert page.locator('[name=modelChoice]').input_value()=='model-a'
 assert page.locator('[name=apiKey]').input_value()==''
 print('Connect -> choose -> save -> reopen, key hidden: OK',flush=True)
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
 page.get_by_text('Saved — configuration is active.',exact=True).wait_for()
 assert len(state['sources'])==2
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
 fail['save']=True
 page.locator('[name=modelChoice]').select_option('model-b')
 page.get_by_text('Not saved: Fixture provider unavailable',exact=True).wait_for()
 assert state['model']=='model-a'
 assert 'Not saved yet' in page.locator('[data-save-state]').inner_text()
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
