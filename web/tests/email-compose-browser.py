"""Mail composer regression check with synthetic API responses; no real mail.
Serve web/ on 127.0.0.1:4892, install Playwright/Chromium, then run this script.
PRISM_MAIL_TEST_URL and CHROMIUM_EXECUTABLE_PATH can override those defaults.
"""
import json
import os
from pathlib import Path
from urllib.parse import urlsplit,parse_qs
from playwright.sync_api import sync_playwright
origin=os.getenv('PRISM_MAIL_TEST_URL','http://127.0.0.1:4892')
messages=[{'uid':1054,'from':'Ada <ada@example.test>','to':'me@example.test','cc':'team@example.test','subject':'Earlier','date':'2026-10-15T06:28:32Z','seen':True},{'uid':1055,'from':'Ben <ben@example.test>','to':'me@example.test','subject':'Latest','date':'2026-10-15T04:41:59-07:00','seen':True}]
state={'pause':False,'route':None,'send_requests':0}
errors=[];external=[]
def route_handler(route):
 url=urlsplit(route.request.url);path=url.path
 if url.hostname not in ['127.0.0.1','localhost']:
  external.append(route.request.url);route.abort();return
 if path=='/qa-parent':
  route.fulfill(content_type='text/html',body='''<!doctype html><iframe id="mail" src="/apps/email.html" style="width:1200px;height:850px"></iframe><script>
window.seq=0;window.waiting=new Map();window.ready=false;
addEventListener('message',e=>{if(e.source!==mail.contentWindow)return;if(e.data.type==='editor-ready')window.ready=true;if(e.data.type==='editor-response'){const f=waiting.get(e.data.id);if(f){waiting.delete(e.data.id);f(e.data.result)}}});
window.rpc=args=>new Promise((resolve,reject)=>{const id='test-'+(++seq);const timer=setTimeout(()=>{waiting.delete(id);reject(Error('Editor did not respond'))},10000);waiting.set(id,result=>{clearTimeout(timer);resolve(result)});mail.contentWindow.postMessage({type:'editor-request',id,args,expires_at:Date.now()+30000},location.origin)});
</script>''');return
 if not path.startswith('/api/'):
  route.continue_();return
 if path=='/api/email/config':data={'configured':True,'has_password':True,'user':'me@example.test','from':'me@example.test','list_limit':60}
 elif path=='/api/email/list':data={'messages':messages}
 elif path=='/api/email/tags':data={}
 elif path=='/api/builtin/email':data={'result':json.dumps([{'name':'INBOX','role':'inbox','unread':0,'total':2}])}
 elif path=='/api/email/read':
  if state['pause']:state['route']=route;return
  uid=int(parse_qs(url.query)['uid'][0]);data={'message':dict(next(m for m in messages if m['uid']==uid),body='<html><head><style>.noise{color:red}</style></head><body><p>First paragraph <b>bold</b></p><p>Second<br>line</p><script>window.__unsafe=1</script><img src="https://tracking.invalid/pixel"></body></html>' if uid==1055 else 'Plain original\nSecond line')}
 elif path=='/api/email/send':state['send_requests']+=1;route.fulfill(status=500,body='Sending is forbidden in this test');return
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
 assert frame.locator('#c-to').input_value()=='ada@example.test, team@example.test'
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
 assert not errors,errors
 assert not external,external
 assert state['send_requests']==0
 page.screenshot(path='/tmp/prism-mail-composer-qa.png')
 print(json.dumps({'chronology':'passed','reply_and_reply_all':'passed','agent_open_update':'passed','draft_race_protection':'passed','javascript_errors':errors,'external_requests':external,'emails_sent':state['send_requests']}))
 browser.close()
