"""Notes wiki regression with local assets and synthetic APIs. No live account.
Run with Playwright + Chromium installed; no HTTP server is needed.
"""
import json
import os
import re
from pathlib import Path
from urllib.parse import urlsplit, parse_qs
from playwright.sync_api import sync_playwright

stamp='2026-11-24T16:00:00Z'
def note(ident,title,body,scope='personal',can_edit=True,aliases=None):
 return dict(id=ident,title=title,body=body,tags='',scope=scope,canEdit=can_edit,createdAt=stamp,updatedAt=stamp,aliases=aliases or [])
notes=[note('n1','Home','# Welcome\n\n[[Guide]] and [[Old guide|Operations guide]].\n\n[[Config]]\n\n`[[Code only]]`\n\n```md\n[[Fenced only]]\n```'),
 note('n2','Guide','# Original guide\n\n## Setup\n\nStable content.',aliases=['Old guide']),
 note('n3','Config','First configuration'),note('n4','Config','Second configuration'),
 note('g1','Config','Published configuration','group',False),note('g2','Group home','[[Config]]','group',False),
 note('n5','Referencing page','[Stable guide](/?app=notes&id=n2)\n\n[[Guide]]')]
versions=[dict(id='v2',title='Guide',body='# Previous guide\n\nSecond saved text.',tags='',createdAt=stamp),dict(id='v1',title='Guide',body='# Original guide\n\n## Setup\n\nStable content.',tags='',createdAt='2026-11-23T12:00:00Z')]
state={'notes':notes,'trash':[],'versions':versions,'fail_restore':False,'version_reads':0,'new':10,'hold_note_save':False,'held_note_save':None,'fail_notes_read':False,'note_writes':0}
errors=[];external=[]
def save_note(payload):
 state['note_writes']+=1
 ident=payload.get('id')
 if not ident:
  ident='n'+str(state['new']);state['new']+=1;state['notes'].append(note(ident,payload['title'],payload['body']))
 else:
  target=next(n for n in state['notes'] if n['id']==ident)
  if target['title']!=payload['title']:
   target['aliases'].append(target['title'])
   if ident.startswith('vault/'):
    target['aliases'].append(ident);ident='vault/'+payload['title']+'.md';target['id']=ident
    payload=dict(payload,body=re.sub(r'(?m)^title:.*$',lambda m:'title: '+json.dumps(payload['title']),payload['body'],count=1))
  target.update(title=payload['title'],body=payload['body'],tags=payload['tags'])
 return {'id':ident}
def route_handler(route):
 url=urlsplit(route.request.url);path=url.path;query=parse_qs(url.query);method=route.request.method
 if url.hostname!='127.0.0.1':external.append(route.request.url);route.abort();return
 if path=='/qa-parent':
  route.fulfill(content_type='text/html',body='''<!doctype html><iframe id="notes" src="/apps/notes.html" style="width:calc(100vw - 24px);height:850px;border:0"></iframe><script>
let seq=0;const pending=new Map();window.ready=false;
addEventListener('message',e=>{if(e.source!==notes.contentWindow)return;if(e.data.type==='editor-ready')ready=true;if(e.data.type==='editor-response'){const resolve=pending.get(e.data.id);if(resolve){pending.delete(e.data.id);resolve(e.data.result)}}});
window.rpc=args=>new Promise((resolve,reject)=>{const id='qa-'+(++seq);pending.set(id,resolve);notes.contentWindow.postMessage({type:'editor-request',id,args,expires_at:Date.now()+30000},location.origin)});
</script>''');return
 if not path.startswith('/api/'):
  asset=Path(__file__).resolve().parents[1]/path.lstrip('/')
  if asset.is_file():route.fulfill(path=str(asset))
  else:route.fulfill(status=404,body='Not found')
  return
 if path=='/api/me':data={'authenticated':False}
 elif path=='/api/notes/history':
  if 'version' in query:
   state['version_reads']+=1;data={'version':next(v for v in state['versions'] if v['id']==query['version'][0]),'source':'local'}
  else:data={'versions':[{k:v for k,v in version.items() if k!='body'} for version in state['versions']],'source':'local'}
 elif path=='/api/notes/trash':data={'notes':state['trash'],'source':'local'}
 elif path=='/api/notes/restore':
  if state['fail_restore']:route.fulfill(status=503,body='Storage unavailable');return
  payload=route.request.post_data_json;ident=payload['id']
  if payload.get('version'):
   target=next(n for n in state['notes'] if n['id']==ident);version=next(v for v in state['versions'] if v['id']==payload['version'])
   state['versions'].insert(0,dict(id='saved-'+str(len(state['versions'])),title=target['title'],body=target['body'],tags=target['tags'],createdAt=stamp))
   target.update(title=version['title'],body=version['body'],tags=version['tags'])
  else:
   target=next(n for n in state['trash'] if n['id']==ident);state['trash'].remove(target);target.pop('deletedAt',None);state['notes'].append(target)
  data={'id':ident}
 elif path=='/api/notes':
  if method=='GET':
   if state['fail_notes_read']:route.fulfill(status=503,body='Notes temporarily unavailable');return
   if 'id' in query:data={'note':next(n for n in state['notes'] if n['id']==query['id'][0]),'source':'local'}
   else:data={'notes':state['notes'],'source':'local'}
  elif method=='POST':
   payload=route.request.post_data_json
   if state['hold_note_save']:state['held_note_save']=(route,payload);return
   data=save_note(payload)
  elif method=='DELETE':
   target=next(n for n in state['notes'] if n['id']==query['id'][0]);state['notes'].remove(target);target['deletedAt']=stamp;state['trash'].append(target);data={'ok':True}
  else:raise AssertionError(method)
 else:data={}
 route.fulfill(content_type='application/json',body=json.dumps(data))

with sync_playwright() as p:
 options={'headless':True,'args':['--no-sandbox']}
 if os.getenv('CHROMIUM_EXECUTABLE_PATH'):options['executable_path']=os.environ['CHROMIUM_EXECUTABLE_PATH']
 browser=p.chromium.launch(**options);page=browser.new_page(viewport={'width':1280,'height':960})
 page.route('**/*',route_handler);page.on('pageerror',lambda error:errors.append(str(error)))
 page.goto('http://127.0.0.1/qa-parent');page.wait_for_function('window.ready')
 frame=page.frame_locator('#notes');frame.locator('.note-row').first.wait_for();app=page.frames[1]
 app.evaluate("select('n1')");frame.locator('#tab-preview').click()
 assert frame.locator('#e-preview .wikilink').count()==3
 assert frame.locator('#e-preview code').filter(has_text='[[Code only]]').count()==1
 frame.locator('#e-preview .wikilink').filter(has_text='Config').click()
 frame.locator('.wiki-page-option').filter(has_text='n4').click()
 page.wait_for_function("notes.contentDocument.querySelector('#e-body').value==='Second configuration'")
 assert app.evaluate('selectedId')=='n4'
 frame.locator('#wiki-back').click();page.wait_for_function("notes.contentDocument.querySelector('#e-title').value==='Home'")
 frame.locator('#wiki-forward').click();page.wait_for_function("notes.contentDocument.querySelector('#e-title').value==='Config'")
 app.evaluate("select('n1');")
 frame.locator('#tab-preview').click();frame.locator('#e-preview .wikilink').filter(has_text='Operations guide').click()
 page.wait_for_function("notes.contentDocument.querySelector('#e-title').value==='Guide'")
 assert app.evaluate('selectedId')=='n2'
 frame.locator('.wiki-context summary').click()
 assert frame.locator('#wiki-backlinks').inner_text().count('Referencing page')==1
 assert 'Home' in frame.locator('#wiki-backlinks').inner_text()
 assert 'Original guide' in frame.locator('#wiki-outline').inner_text()
 # Real agent editor shares the note and saves before reporting success.
 read=page.evaluate("rpc({action:'read'})")
 updated=page.evaluate('args=>rpc(args)',dict(action='update',revision=read['revision'],body='# Agent rewrite\n\nRésumé avec accents.'))
 assert updated['status']=='updated',updated
 assert next(n for n in state['notes'] if n['id']=='n2')['body'].startswith('# Agent rewrite')
 frame.locator('.note-actions summary').click();frame.locator('#history-btn').click()
 frame.locator('.wiki-version-preview').filter(has_text='Previous guide').wait_for()
 assert state['version_reads']>=1
 frame.locator('.wiki-version-item').last.click();frame.locator('.wiki-version-preview').filter(has_text='Stable content.').wait_for()
 state['fail_restore']=True
 frame.locator('.wiki-dialog-footer .btn-accent').click();frame.locator('.pm-primary').filter(has_text='Restore version').click()
 frame.locator('.wiki-error').filter(has_text='Storage unavailable').wait_for()
 assert frame.locator('#e-body').input_value().startswith('# Agent rewrite')
 state['fail_restore']=False
 frame.locator('.wiki-dialog-footer .btn-accent').click();frame.locator('.pm-primary').filter(has_text='Restore version').click()
 frame.locator('.wiki-dialog').wait_for(state='detached')
 assert frame.locator('#e-body').input_value().startswith('# Original guide')
 assert any(v['body'].startswith('# Agent rewrite') for v in state['versions'])
 # Deletion is reversible; restoration does not overwrite an open page.
 frame.locator('.note-actions summary').click();frame.locator('#del-btn').click()
 frame.locator('.pm-danger').filter(has_text='Move to trash').click()
 frame.locator('#editor').wait_for(state='hidden')
 assert any(n['id']=='n2' for n in state['trash'])
 frame.locator('.wiki-library-nav .btn').filter(has_text='Trash').click()
 frame.locator('.wiki-trash-row .btn').filter(has_text='Restore').click()
 frame.locator('.wiki-trash-row .btn').filter(has_text='Open').wait_for()
 frame.locator('.wiki-trash-row .btn').filter(has_text='Open').click()
 frame.locator('.wiki-dialog').wait_for(state='detached')
 assert app.evaluate('selectedId')=='n2'
 if os.getenv('PRISM_NOTES_SCREENSHOT'):
  app.evaluate("setMode('split')")
  page.locator('#notes').screenshot(path=os.environ['PRISM_NOTES_SCREENSHOT'])
 # A server-canonical frontmatter title must not be overwritten by typing during rename.
 state['notes'].append(note('vault/Old.md','Old','---\ntitle: Old\ntags: [ops]\n---\nOriginal body'))
 app.evaluate("load()");app.evaluate("select('vault/Old.md')")
 frame.locator('#e-title').fill('New');state['hold_note_save']=True
 page.evaluate("window.heldNoteSave=document.querySelector('#notes').contentWindow.saveNote();void 0")
 for _ in range(100):
  if state['held_note_save']:break
  page.wait_for_timeout(10)
 assert state['held_note_save'] is not None
 held_route,held_payload=state['held_note_save']
 frame.locator('#e-body').fill(held_payload['body']+'\nTyped while saving.')
 writes_before=state['note_writes']
 state['hold_note_save']=False;state['fail_notes_read']=True
 held_route.fulfill(content_type='application/json',body=json.dumps(save_note(held_payload)))
 assert page.evaluate('window.heldNoteSave') is False
 assert frame.locator('#e-body').input_value()==held_payload['body']+'\nTyped while saving.'
 assert state['note_writes']==writes_before+1
 assert app.evaluate('saveNote()') is False
 assert state['note_writes']==writes_before+1
 state['fail_notes_read']=False
 assert app.evaluate('saveNote()') is True
 assert app.evaluate('selectedId')=='vault/New.md'
 assert 'title: "New"' in frame.locator('#e-body').input_value()
 assert frame.locator('#e-body').input_value().endswith('Typed while saving.')
 canonical=next(n for n in state['notes'] if n['id']=='vault/New.md')
 assert canonical['body']==frame.locator('#e-body').input_value()
 # Group title links select the group page, even with two personal duplicates.
 app.evaluate("select('g2')");frame.locator('#tab-preview').click();frame.locator('#e-preview .wikilink').click()
 page.wait_for_function("notes.contentDocument.querySelector('#e-body').value==='Published configuration'")
 assert app.evaluate('selectedId')=='g1'
 assert frame.locator('#history-btn').is_hidden()
 assert frame.locator('#e-body').get_attribute('readonly') is not None
 # The same interface remains usable on a phone.
 page.set_viewport_size({'width':390,'height':850})
 assert app.evaluate('document.documentElement.scrollWidth<=innerWidth')
 frame.locator('#tab-preview').click()
 assert frame.locator('#e-preview').is_visible()
 assert not errors,errors
 assert not external,external
 print(json.dumps({'wiki_links_and_ambiguity':'passed','code_literals':'passed','navigation':'passed','backlinks_and_outline':'passed','agent_editor':'passed','history_preview_restore':'passed','failed_restore_preserves_text':'passed','trash_restore':'passed','group_scope':'passed','canonical_rename_with_concurrent_typing':'passed','failed_canonical_reload_preserves_edits':'passed','mobile_overflow':False,'javascript_errors':errors,'external_requests':external}))
 browser.close()
