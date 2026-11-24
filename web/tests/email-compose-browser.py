"""Mail composer regression check with synthetic API responses; no real mail.
Install Playwright/Chromium, then run this script. Static assets and APIs are
intercepted locally; no running Prism instance or mail server is used.
PRISM_MAIL_TEST_URL and CHROMIUM_EXECUTABLE_PATH can override those defaults.
"""
import json
import os
import base64
import re
from pathlib import Path
from urllib.parse import urlsplit,parse_qs
from playwright.sync_api import sync_playwright
origin=os.getenv('PRISM_MAIL_TEST_URL','http://127.0.0.1:4892')
messages=[{'uid':1054,'from':'Ada <ada@example.test>','to':'me@example.test','cc':'team@example.test','subject':'Earlier','date':'2026-10-15T06:28:32Z','seen':True},{'uid':1055,'from':'Ben <ben@example.test>','to':'me@example.test','subject':'Latest','date':'2026-10-15T04:41:59-07:00','seen':True}]
state={'pause':False,'route':None,'send_requests':0,'drafts':{},'next_draft':1,'fail_save':False,'conflict':False,'allow_send':False}
errors=[];external=[]
def route_handler(route):
 url=urlsplit(route.request.url);path=url.path
 if url.hostname not in ['127.0.0.1','localhost']:
  external.append(route.request.url);route.abort();return
 if path=='/qa-parent':
  route.fulfill(content_type='text/html',body='''<!doctype html><iframe id="mail" src="/apps/email.html" style="width:calc(100vw - 24px);height:850px;border:0"></iframe><script>
window.seq=0;window.waiting=new Map();window.ready=false;
addEventListener('message',e=>{if(e.source!==mail.contentWindow)return;if(e.data.type==='editor-ready')window.ready=true;if(e.data.type==='editor-response'){const f=waiting.get(e.data.id);if(f){waiting.delete(e.data.id);f(e.data.result)}}});
window.rpc=args=>new Promise((resolve,reject)=>{const id='test-'+(++seq);const timer=setTimeout(()=>{waiting.delete(id);reject(Error('Editor did not respond'))},10000);waiting.set(id,result=>{clearTimeout(timer);resolve(result)});mail.contentWindow.postMessage({type:'editor-request',id,args,expires_at:Date.now()+30000},location.origin)});
</script>''');return
 if not path.startswith('/api/'):
  asset=Path(__file__).resolve().parents[1]/path.lstrip('/')
  if asset.is_file():route.fulfill(path=str(asset))
  else:route.fulfill(status=404,body='Not found')
  return
 if path=='/api/email/config':data={'configured':True,'has_password':True,'user':'me@example.test','from':'me@example.test','list_limit':60,'signature':'Vincent\nThe Last Server'}
 elif path=='/api/email/list':data={'messages':messages}
 elif path=='/api/email/tags':data={}
 elif path=='/api/builtin/email':data={'result':json.dumps([{'name':'INBOX','role':'inbox','unread':0,'total':2}])}
 elif path=='/api/email/read':
  if state['pause']:state['route']=route;return
  uid=int(parse_qs(url.query)['uid'][0]);data={'message':dict(next(m for m in messages if m['uid']==uid),body='<html><head><style>.noise{color:red}</style></head><body><p>First paragraph <b>bold</b></p><p>Second<br>line</p><script>window.__unsafe=1</script><img src="https://tracking.invalid/pixel"></body></html>' if uid==1055 else 'Plain original\nSecond line')}
 elif path=='/api/email/drafts':
  query=parse_qs(url.query);method=route.request.method
  def metadata(d):
   return dict({k:v for k,v in d.items() if k!='attachments'},attachmentCount=len(d.get('attachments',[])))
  if method=='GET':
   if 'id' in query:
    draft=state['drafts'].get(int(query['id'][0]))
    if not draft:route.fulfill(status=404,body='Draft not found');return
    data={'draft':draft,'mailboxChanged':False}
   else:data={'drafts':[metadata(d) for d in state['drafts'].values()]}
  elif method=='POST':
   if state['fail_save']:route.fulfill(status=503,body='Storage unavailable');return
   if state['conflict']:route.fulfill(status=409,body='Draft changed in another window');return
   incoming=route.request.post_data_json;ident=incoming.get('id',0)
   if not ident:
    replay=next((d for d in state['drafts'].values() if d['client_id']==incoming['client_id']),None)
    if replay:route.fulfill(content_type='application/json',body=json.dumps({'draft':metadata(replay),'replayed':True}));return
    ident=state['next_draft'];state['next_draft']+=1;draft={'revision':0,'attachments':[]}
   else:
    draft=state['drafts'].get(ident)
    if not draft:route.fulfill(status=404,body='Draft not found');return
    if draft['revision']!=incoming['revision']:route.fulfill(status=409,body='Draft changed');return
   draft=dict(draft,**incoming);draft.update(id=ident,revision=draft['revision']+1,status='draft',updatedAt='2026-11-24T16:00:00Z');state['drafts'][ident]=draft
   data={'draft':metadata(draft)}
  elif method=='DELETE':
   ident=int(query['id'][0]);draft=state['drafts'].get(ident)
   if not draft:route.fulfill(status=404,body='Draft not found');return
   if draft['revision']!=int(query['revision'][0]):route.fulfill(status=409,body='Draft changed');return
   del state['drafts'][ident];data={'ok':True}
  else:raise AssertionError(method)
 elif path=='/api/email/send':
  state['send_requests']+=1
  if not state['allow_send']:route.fulfill(status=500,body='Sending is forbidden in this test');return
  payload=route.request.post_data_json;draft=state['drafts'].get(payload['draft_id']);assert draft and draft['revision']==payload['draft_revision']
  state['sent_draft']=draft;del state['drafts'][draft['id']];data={'ok':True}
 else:data={}
 route.fulfill(content_type='application/json',body=json.dumps(data))
with sync_playwright() as p:
 options={'headless':True}
 if os.getenv('CHROMIUM_EXECUTABLE_PATH'):options['executable_path']=os.environ['CHROMIUM_EXECUTABLE_PATH']
 browser=p.chromium.launch(**options)
 page=browser.new_page(viewport={'width':1260,'height':930},timezone_id='America/Martinique')
 page.route('**/*',route_handler);page.on('pageerror',lambda e:errors.append(str(e)))
 page.goto(origin+'/qa-parent');page.wait_for_function('window.ready')
 frame=page.frame_locator('#mail');frame.locator('.mrow').first.wait_for()
 assert frame.locator('.msub').all_text_contents()==['Latest','Earlier']
 result=page.evaluate("rpc({action:'open',app:'email',folder:'INBOX',uid:1055})")
 assert result.get('status')=='opened',result
 assert result['reply_to']=={'folder':'INBOX','uid':1055}
 assert '> First paragraph bold' in result['body'] and '> Second\n> line' in result['body'],result['body']
 assert '.noise' not in result['body'] and '__unsafe' not in result['body']
 assert frame.locator('#c-body').evaluate('(e)=>e.selectionStart')==0
 edited=page.evaluate("args=>rpc(args)",dict(action='update',revision=result['revision'],body='My answer'+result['body']))
 assert edited['status']=='updated'
 blocked=page.evaluate("rpc({action:'open',app:'email',uid:1054})")
 assert 'already open' in blocked.get('error',''),blocked
 assert frame.locator('#c-body').input_value().startswith('My answer')
 f=page.frames[1];f.evaluate('closeCompose(true)')
 f.evaluate('openMessage(1054)');frame.locator('.reading .btn-accent').filter(has_text='Reply').wait_for()
 frame.locator('.reading .btn-accent').filter(has_text='Reply').click()
 assert '> Plain original\n> Second line' in frame.locator('#c-body').input_value()
 f.evaluate('closeCompose(true)');f.evaluate('replyAll()')
 assert frame.locator('#c-to').input_value()=='ada@example.test'
 assert frame.locator('#c-cc').input_value()=='team@example.test'
 assert '> Plain original' in frame.locator('#c-body').input_value()
 f.evaluate('closeCompose(true)')
 state['pause']=True
 page.evaluate("()=>{window.pendingReply=rpc({action:'open',app:'email',uid:1054})}")
 page.wait_for_timeout(100)
 assert state['route'] is not None
 f.evaluate("compose();document.getElementById('c-body').value='A draft I started myself'")
 state['route'].fulfill(content_type='application/json',body=json.dumps({'message':dict(messages[0],body='Plain original')}))
 blocked=page.evaluate('window.pendingReply')
 assert 'composer changed' in blocked.get('error',''),blocked
 assert frame.locator('#c-body').input_value()=='A draft I started myself'
 # Persistent draft + attachment, including edits through the real agent/editor bridge.
 state['pause']=False
 f.evaluate('closeCompose(true)')
 f.evaluate('compose()')
 assert 'The Last Server' in frame.locator('#c-body').input_value()
 frame.locator('#c-to').fill('customer@example.test')
 frame.locator('#c-cc').fill('team@example.test')
 frame.locator('#c-bcc').fill('archive@example.test')
 frame.locator('#c-subject').fill('Résumé du dépannage')
 frame.locator('#c-body').fill('Bonjour, voici le résumé — déjà vérifié.')
 frame.locator('#c-file').set_input_files({'name':'résumé.txt','mimeType':'text/plain','buffer':'Pièce jointe avec des accents'.encode()})
 frame.locator('#c-status').filter(has_text=re.compile('^Saved in Prism$')).wait_for()
 saved_id=f.evaluate('draftState.id')
 assert base64.b64decode(state['drafts'][saved_id]['attachments'][0]['data']).decode()=='Pièce jointe avec des accents'
 read=page.evaluate("rpc({action:'read'})")
 edited=page.evaluate('args=>rpc(args)',dict(action='update',revision=read['revision'],cc='second@example.test',bcc='private@example.test',body='Texte relu par l’agent.'))
 assert edited['status']=='updated',edited
 assert state['drafts'][saved_id]['cc']=='second@example.test'
 page.reload();page.wait_for_function('window.ready')
 frame=page.frame_locator('#mail');frame.locator('.mrow').first.wait_for();f=page.frames[1]
 frame.locator('.nav-item').filter(has_text='Prism drafts').click()
 frame.locator('.saved-draft').filter(has_text='Résumé du dépannage').click()
 frame.locator('#overlay.open').wait_for()
 assert frame.locator('#c-body').input_value()=='Texte relu par l’agent.'
 assert frame.locator('#c-cc').input_value()=='second@example.test'
 assert frame.locator('#c-bcc').input_value()=='private@example.test'
 assert frame.locator('#c-atts').inner_text().find('résumé.txt')>=0
 assert f.evaluate('composeAtts[0].text()')=='Pièce jointe avec des accents'
 if os.getenv('PRISM_MAIL_SCREENSHOT'):page.locator('#mail').screenshot(path=os.environ['PRISM_MAIL_SCREENSHOT'])
 page.set_viewport_size({'width':390,'height':850})
 if os.getenv('PRISM_MAIL_MOBILE_SCREENSHOT'):page.locator('#mail').screenshot(path=os.environ['PRISM_MAIL_MOBILE_SCREENSHOT'])
 assert f.evaluate('document.documentElement.scrollWidth<=innerWidth')
 assert frame.locator('.compose-foot').evaluate('e=>e.scrollWidth<=e.clientWidth'), 'Composer footer overflows on mobile'
 page.set_viewport_size({'width':1260,'height':930})
 # Failed storage must leave the editor open and the previous persisted content intact.
 state['fail_save']=True
 frame.locator('#c-body').fill('Modifications non enregistrées')
 f.evaluate('closeCompose()')
 assert frame.locator('#overlay').evaluate("e=>e.classList.contains('open')")
 assert frame.locator('#c-body').input_value()=='Modifications non enregistrées'
 assert 'Not saved' in frame.locator('#c-status').inner_text()
 assert state['drafts'][saved_id]['body']=='Texte relu par l’agent.'
 state['fail_save']=False
 f.evaluate('flushDraft()')
 assert state['drafts'][saved_id]['body']=='Modifications non enregistrées'
 # Stale save stays local; explicit copy cannot overwrite the other window's draft.
 state['conflict']=True
 frame.locator('#c-body').fill('Version locale en conflit')
 f.evaluate('flushDraft().catch(()=>{})')
 assert frame.locator('#draft-save-copy').is_visible()
 assert state['drafts'][saved_id]['body']=='Modifications non enregistrées'
 state['conflict']=False
 frame.locator('#draft-save-copy').click()
 frame.locator('.pm-primary').filter(has_text='Save as new draft').click()
 frame.locator('#c-status').filter(has_text=re.compile('^Saved in Prism$')).wait_for()
 copy_id=f.evaluate('draftState.id');assert copy_id!=saved_id
 assert state['drafts'][saved_id]['body']=='Modifications non enregistrées'
 assert state['drafts'][copy_id]['body']=='Version locale en conflit'
 # Sending uses the saved canonical revision once, with its persisted attachment and Bcc.
 state['allow_send']=True
 frame.locator('#send-mail').click()
 page.wait_for_function("!document.querySelector('#mail').contentDocument.querySelector('#overlay').classList.contains('open')")
 assert state['send_requests']==1
 assert state['sent_draft']['bcc']=='private@example.test'
 assert base64.b64decode(state['sent_draft']['attachments'][0]['data']).decode()=='Pièce jointe avec des accents'
 assert copy_id not in state['drafts']
 assert not errors,errors
 assert not external,external
 page.screenshot(path='/tmp/prism-mail-composer-qa.png')
 print(json.dumps({'chronology':'passed','reply_and_reply_all':'passed','agent_open_update':'passed','draft_race_protection':'passed','persistent_draft_reload':'passed','attachment_recovery':'passed','save_error_and_conflict':'passed','canonical_send':'passed','javascript_errors':errors,'external_requests':external,'emails_sent':0,'simulated_sends':state['send_requests']}))
 browser.close()
