"""Actual shell navigation guard + actual Mail against a delayed synthetic store.
No running Prism, SMTP server, model, or external network is used.
"""
import base64
import json
import os
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import sync_playwright

web=Path(__file__).resolve().parents[1]
app_source=(web/'app.js').read_text()
guard=app_source[app_source.index('let viewNavigation = 0'):app_source.index('// applyView switches')]
assert 'function setView(' in guard
state={'drafts':{},'next':1,'hold':True,'held':None,'fail':False,'writes':0};errors=[];external=[]
def metadata(d):return dict({key:value for key,value in d.items() if key!='attachments'},attachmentCount=len(d.get('attachments',[])))
def save(payload):
 ident=payload.get('id') or state['next']
 if not payload.get('id'):state['next']+=1
 previous=state['drafts'].get(ident,{})
 draft=dict(previous,**payload);draft.update(id=ident,revision=previous.get('revision',0)+1,status='draft',updatedAt='2026-11-24T16:00:00Z')
 state['drafts'][ident]=draft
 return {'draft':metadata(draft)}
def route_handler(route):
 url=urlsplit(route.request.url);path=url.path
 if url.hostname!='127.0.0.1':external.append(route.request.url);route.abort();return
 if path=='/qa-parent':
  html='''<!doctype html><button id="rail" onclick="window.nav=setView({type:'app',name:'notes'})">Notes</button><button id="board" onclick="window.nav=setView({type:'board',workspace:'finance'})">Finance</button><button id="settings" onclick="window.nav=setView({type:'settings',tab:'email'})">Settings</button><iframe id="app-frame" src="/apps/email.html" style="width:1200px;height:850px"></iframe><script>
let currentView={type:'app',name:'email'};window.applied=[];window.notices=[];
function showToast(value){notices.push(value)}
function applyView(view){currentView=view;applied.push(view);document.getElementById('app-frame').src='about:blank'}
'''+guard+'</script>'
  route.fulfill(content_type='text/html',body=html);return
 if not path.startswith('/api/'):
  asset=web/path.lstrip('/')
  if asset.is_file():route.fulfill(path=str(asset))
  else:route.fulfill(status=404,body='Not found')
  return
 if path=='/api/email/config':data={'configured':True,'has_password':True,'user':'owner@example.test','list_limit':60}
 elif path=='/api/email/list':data={'messages':[]}
 elif path=='/api/email/tags':data={}
 elif path=='/api/builtin/email':data={'result':json.dumps([{'name':'INBOX','role':'inbox','selectable':True}])}
 elif path=='/api/email/drafts':
  if route.request.method=='GET':data={'drafts':[metadata(d) for d in state['drafts'].values()]}
  else:
   state['writes']+=1
   if state['fail']:route.fulfill(status=503,body='Storage unavailable');return
   if state['hold']:state['held']=(route,route.request.post_data_json);return
   data=save(route.request.post_data_json)
 else:raise AssertionError('Unexpected API '+path)
 route.fulfill(content_type='application/json',body=json.dumps(data))

with sync_playwright() as p:
 options={'headless':True,'args':['--no-sandbox']}
 if os.getenv('CHROMIUM_EXECUTABLE_PATH'):options['executable_path']=os.environ['CHROMIUM_EXECUTABLE_PATH']
 browser=p.chromium.launch(**options);page=browser.new_page(viewport={'width':1260,'height':1000})
 page.route('**/*',route_handler);page.on('pageerror',lambda error:errors.append(str(error)))
 def start():
  state.update(hold=True,held=None,fail=False)
  page.goto('http://127.0.0.1/qa-parent');frame=page.frame_locator('#app-frame');frame.locator('.new-message').wait_for()
  frame.locator('.new-message').click();frame.locator('#c-to').fill('recipient@example.test');frame.locator('#c-body').fill('Typed immediately before navigation.')
  frame.locator('#c-file').set_input_files({'name':'résumé.txt','mimeType':'text/plain','buffer':'Pièce jointe'.encode()})
  return frame
 def held():
  for _ in range(100):
   if state['held']:return state['held']
   page.wait_for_timeout(10)
  raise AssertionError('The navigation did not flush the draft')
 for destination in ['rail','board','settings']:
  frame=start();page.locator('#'+destination).click();route,payload=held()
  assert page.evaluate('applied.length')==0
  assert frame.locator('#c-body').input_value()=='Typed immediately before navigation.'
  assert base64.b64decode(payload['attachments'][0]['data']).decode()=='Pièce jointe'
  state['hold']=False;route.fulfill(content_type='application/json',body=json.dumps(save(payload)))
  assert page.evaluate('window.nav') is True
  assert page.evaluate('applied.length')==1
  assert state['drafts'][state['next']-1]['body']=='Typed immediately before navigation.'
 # Concurrent destinations wait on one snapshot; only the last destination wins.
 frame=start();writes=state['writes'];page.locator('#rail').click();route,payload=held();page.locator('#settings').click()
 assert page.evaluate('applied.length')==0
 state['hold']=False;route.fulfill(content_type='application/json',body=json.dumps(save(payload)))
 assert page.evaluate('window.nav') is True
 assert page.evaluate('applied')==[{'type':'settings','tab':'email'}]
 assert state['writes']==writes+1
 # Failed storage prevents all destructive iframe replacement and retains edits.
 frame=start();state['hold']=False;state['fail']=True;page.locator('#board').click()
 assert page.evaluate('window.nav') is False
 assert page.evaluate('applied.length')==0
 assert frame.locator('#overlay').evaluate("element=>element.classList.contains('open')")
 assert frame.locator('#c-body').input_value()=='Typed immediately before navigation.'
 assert 'Storage unavailable' in frame.locator('#c-status').inner_text()
 assert 'résumé.txt' in frame.locator('#c-atts').inner_text()
 assert not errors,errors
 assert not external,external
 print(json.dumps({'actual_guard':'passed','rail_board_settings_flush':'passed','held_attachment_snapshot':'passed','latest_destination_wins':'passed','failed_save_blocks_navigation':'passed','javascript_errors':errors,'external_requests':external,'emails_sent':0}))
 browser.close()
