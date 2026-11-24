"""Agent work UI against synthetic batch observations; no live Prism required."""
import json
import os
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import sync_playwright

items=[
 dict(id='workspace:finance',name='Finance',runStatus='running',request='Prepare the monthly report from the uploaded CSV.',conversationUrl='/?workspace=finance&chat=open',startedAt='2026-11-24T14:18:00Z',lastActivityAt='2026-11-24T14:19:45Z',recentTools=[{'tool':'exec_command','status':'running'}],subagents=[{'task':'Check the totals','status':'running'}],backgroundJobs=1),
 dict(id='room:7',name='Room · Operations',runStatus='waiting_for_approval',request='Publish the updated maintenance procedure.',conversationUrl='/?app=room&group=7',startedAt='2026-11-24T14:17:00Z',lastActivityAt='2026-11-24T14:19:00Z',queued=2),
 dict(id='workspace:telegram',name='Telegram',runStatus='interrupted',request='Check tomorrow’s appointments.',conversationUrl='/?workspace=telegram&chat=open',lastActivityAt='2026-11-24T14:10:00Z'),
 dict(id='workspace:main',name='Main',runStatus='completed',request='Create the service report and attach the CSV.',conversationUrl='/?workspace=main&chat=open',startedAt='2026-11-24T14:10:00Z',finishedAt='2026-11-24T14:12:00Z'),
 dict(id='workspace:legacy',name='Legacy',runStatus='completed',request='An older saved turn.',conversationUrl='/?workspace=legacy&chat=open'),
 dict(id='workspace:unsafe',name='Unsafe URL test',runStatus='completed',request='<script>window.__unsafe=1</script>',conversationUrl='javascript:alert(1)'),
 dict(id='workspace:idle',name='Idle conversation',runStatus='idle')]
state={'fail':False,'requests':[],'work_requests':0};errors=[];external=[]
def route_handler(route):
 url=urlsplit(route.request.url);path=url.path
 if url.hostname!='127.0.0.1':external.append(route.request.url);route.abort();return
 if path=='/qa-parent':
  route.fulfill(content_type='text/html',body='''<!doctype html><iframe id="activity" src="/apps/activity.html" style="width:calc(100vw - 24px);height:900px;border:0"></iframe><script>window.opened=null;addEventListener('message',e=>{if(e.source===activity.contentWindow&&e.data.type==='open-reference')window.opened=e.data.href})</script>''');return
 if not path.startswith('/api/'):
  asset=Path(__file__).resolve().parents[1]/path.lstrip('/')
  if asset.is_file():route.fulfill(path=str(asset))
  else:route.fulfill(status=404,body='Not found')
  return
 state['requests'].append(path)
 if path=='/api/me':data={'authenticated':False}
 elif path=='/api/activity/work':
  state['work_requests']+=1
  if state['fail']:route.fulfill(status=503,body='Database unavailable');return
  data={'items':items,'observedAt':'2026-11-24T14:20:00Z'}
 elif path=='/api/activity':data={'events':[{'id':1,'ts':'2026-11-24T14:12:00Z','kind':'action','item':'note','session':'main','meta':{'label':'Updated note','title':'Runbook'}}],'nextBefore':0}
 else:raise AssertionError('Unexpected API request: '+path)
 route.fulfill(content_type='application/json',body=json.dumps(data))

with sync_playwright() as p:
 options={'headless':True,'args':['--no-sandbox']}
 if os.getenv('CHROMIUM_EXECUTABLE_PATH'):options['executable_path']=os.environ['CHROMIUM_EXECUTABLE_PATH']
 browser=p.chromium.launch(**options);page=browser.new_page(viewport={'width':1280,'height':1000})
 page.route('**/*',route_handler);page.on('pageerror',lambda error:errors.append(str(error)))
 page.goto('http://127.0.0.1/qa-parent');frame=page.frame_locator('#activity');frame.locator('.work-card').first.wait_for();app=page.frames[1]
 assert frame.locator('[data-group=attention] .work-card').count()==2
 assert frame.locator('[data-group=running] .work-card').count()==1
 assert frame.locator('[data-group=finished] .work-card').count()==3
 assert frame.locator('.work-card').count()==6
 assert 'Idle conversation' not in frame.locator('#feed').inner_text()
 finance=frame.locator('.work-card').filter(has_text='Finance')
 assert 'Elapsed 2m 0s' in finance.inner_text()
 finance.locator('summary').click();assert finance.locator('details').get_attribute('open') is not None
 app.evaluate('load(true)')
 assert finance.locator('details').get_attribute('open') is not None
 legacy=frame.locator('.work-card').filter(has_text='Legacy')
 assert legacy.locator('.work-meta').count()==0
 assert frame.locator('.work-card').filter(has_text='Unsafe URL test').locator('a').count()==0
 assert not app.evaluate('window.__unsafe')
 frame.locator('.work-card').filter(has_text='Room · Operations').locator('.work-open').click()
 page.wait_for_function("window.opened==='/?app=room&group=7'")
 assert all(not path.startswith('/api/sessions') and path!='/api/room/activity' for path in state['requests'])
 state['fail']=True;app.evaluate('load(true)')
 assert frame.locator('.work-card').count()==6
 assert 'Showing the last observation' in frame.locator('#status').inner_text()
 assert 'Database unavailable' in frame.locator('#status').inner_text()
 # Actions and the technical journal remain available beside the work view.
 frame.locator('button[data-view=actions]').click();frame.locator('#feed .item').filter(has_text='Updated note').wait_for()
 state['fail']=False;frame.locator('button[data-view=work]').click();frame.locator('.work-card').first.wait_for()
 assert frame.locator('#status').is_hidden()
 frame.locator('.activity-details summary').click();frame.locator('button[data-view=errors]').click();frame.locator('#feed .row').first.wait_for()
 frame.locator('button[data-view=work]').click();frame.locator('.work-card').first.wait_for()
 if os.getenv('PRISM_ACTIVITY_SCREENSHOT'):page.locator('#activity').screenshot(path=os.environ['PRISM_ACTIVITY_SCREENSHOT'])
 page.set_viewport_size({'width':390,'height':850})
 assert app.evaluate('document.documentElement.scrollWidth<=innerWidth')
 assert not errors,errors
 assert not external,external
 print(json.dumps({'work_groups':'passed','known_duration':'passed','legacy_timestamps':'passed','expanded_details_refresh':'passed','stale_snapshot':'passed','room_link':'passed','batched_requests':'passed','actions_and_details':'passed','unsafe_content':'passed','mobile_overflow':False,'javascript_errors':errors,'external_requests':external}))
 browser.close()
