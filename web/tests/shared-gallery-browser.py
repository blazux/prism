"""Shared-gallery browser regression using fixture publications, never live data."""
import json
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
items = [
    {'id':73,'title':'Legacy clock','ownerId':7,'ownerName':'Alice','groupName':'Home','kind':'widget','count':1,'hasPreview':False},
    {'id':74,'title':'Weather outlook','description':'Five-day forecast, wind and rain at a glance.','ownerId':1,'ownerName':'Vincent','groupName':'Home','kind':'widget','count':1,'hasPreview':True},
    {'id':75,'title':'Operations dashboard','description':'A shared overview of services and upcoming tasks.','ownerId':7,'ownerName':'Alice','groupName':'Home','kind':'dashboard','count':3,'hasPreview':True},
]
calls, errors = [], []
fail = {'add':False}
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=os.environ.get('CHROMIUM_EXECUTABLE'), args=['--no-sandbox','--disable-dev-shm-usage'])
    page = browser.new_page(viewport={'width':1280,'height':900})
    page.set_default_timeout(5000)
    page.on('pageerror',lambda e:errors.append(str(e)))
    def route(r):
        url = r.request.url
        if url.endswith('/preview'):
            r.fulfill(content_type='image/svg+xml',body='''<svg xmlns="http://www.w3.org/2000/svg" width="760" height="320" viewBox="0 0 760 320"><rect width="760" height="320" fill="#121b23"/><text x="28" y="42" fill="#a3b8c8" font-family="sans-serif" font-size="18">Fort-de-France · Five-day forecast</text><text x="28" y="118" fill="#e6edf3" font-family="sans-serif" font-size="56">28°</text><text x="30" y="153" fill="#a3b8c8" font-family="sans-serif" font-size="17">Partly cloudy · Wind 18 km/h</text><path d="M30 265 L160 228 L290 245 L420 206 L550 225 L720 180" stroke="#65c5b1" fill="none" stroke-width="4"/><path d="M30 284 H720" stroke="#35424d"/></svg>''')
        elif '/api/shared' in url:
            calls.append((r.request.method,url,r.request.post_data_json if r.request.post_data else None))
            if r.request.method=='GET':r.fulfill(json={'items':items})
            elif url.split('?')[0].endswith('/add'):
                r.fulfill(status=502,json={'error':'Fixture backend unavailable'}) if fail['add'] else r.fulfill(json={'added':1})
            elif r.request.method=='DELETE':r.fulfill(status=204)
            else:r.fulfill(json={'id':90,'count':1,'hasPreview':True})
        else:r.fulfill(content_type='text/html',body='<html><body></body></html>')
    page.route('**/*',route)
    page.goto('https://fixture.test/')
    page.add_style_tag(content=(root/'style.css').read_text())
    page.add_script_tag(content=(root/'theme.js').read_text())
    page.add_script_tag(content=(root/'modal.js').read_text())
    source=(root/'shared-gallery.js').read_text().replace('export function createSharedGallery','function createSharedGallery',1)
    page.add_script_tag(content=source+'''
      let session='board';const notices=[];
      const gallery=createSharedGallery({getGroups:()=>[{groupId:1,groupName:'Home'}],
       getSession:()=>session,getWidgets:()=>new Map([['weather',{id:'weather',title:'Weather outlook'}]]),
       getMe:()=>({uid:1,isAdmin:false}),showToast:n=>notices.push(n)});
      gallery.openSharedGallery();
    ''')
    page.get_by_text('Legacy clock',exact=True).wait_for()
    assert page.get_by_text('No preview available',exact=True).count()==1
    assert page.locator('.shared-card').count()==3
    assert page.get_by_role('button',name='Unshare Weather outlook',exact=True).count()==1
    page.get_by_role('button',name='Preview Weather outlook',exact=True).click()
    page.locator('.shared-preview-large').wait_for()
    assert page.locator('.shared-preview-dialog').inner_text().find('Five-day forecast')>=0
    page.locator('.shared-preview-dialog').get_by_role('button',name='Close',exact=True).click()
    for theme in ('dark','light'):
        page.evaluate('(t)=>PrismTheme.applyTheme("prism-"+t)',theme)
        page.screenshot(path=f'/tmp/prism-shared-gallery-{theme}.png')
    for width in (390,1280):
        page.set_viewport_size({'width':width,'height':900})
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'),'horizontal overflow'
        page.screenshot(path=f'/tmp/prism-shared-gallery-{width}.png')
    page.locator('.shared-card').filter(has_text='Legacy clock').get_by_role('button',name='Add to workspace').click()
    page.locator('.shared-card').filter(has_text='Legacy clock').get_by_role('button',name='Added ✓').wait_for()
    assert any('/73/add?session=board' in url for _,url,_ in calls)
    fail['add']=True
    page.locator('.shared-card').filter(has_text='Weather outlook').get_by_role('button',name='Add to workspace').click()
    page.wait_for_function('notices.some(n=>n.level==="error")')
    assert page.locator('.shared-card').filter(has_text='Weather outlook').get_by_role('button',name='Add to workspace').is_enabled()
    fail['add']=False
    page.get_by_role('checkbox',name='Select Weather outlook',exact=True).check()
    page.get_by_role('checkbox',name='Select Operations dashboard',exact=True).check()
    page.get_by_role('button',name='Add selected (2)',exact=True).click()
    page.wait_for_function('notices.some(n=>n.message==="2 widget(s) added to this workspace")')
    page.locator('.shared-gallery-dialog').get_by_role('button',name='Share',exact=True).click()
    page.get_by_label('What to share').select_option('weather')
    page.get_by_label('Description (optional)').fill('A useful weather overview.')
    page.locator('.shared-share-form').get_by_role('button',name='Share',exact=True).click()
    page.get_by_role('status').filter(has_text='Published to the group gallery.').wait_for()
    assert any(data and data.get('description')=='A useful weather overview.' and data.get('widgetId')=='weather' for _,_,data in calls)
    page.locator('.shared-gallery-dialog').get_by_role('button',name='Close',exact=True).click()
    page.evaluate('void gallery.shareWidget("weather","Weather outlook")')
    page.get_by_label('Description (optional)').fill('Header share description')
    page.locator('.shared-share-dialog').get_by_role('button',name='Share',exact=True).click()
    page.locator('.shared-share-dialog').wait_for(state='detached')
    assert any(data and data.get('description')=='Header share description' for _,_,data in calls)
    assert not errors,errors
    assert not any('/api/tools' in url or '/plugins/' in url for _,url,_ in calls)
    print('Gallery: legacy cards, previews, enlarge, themes/mobile, single/bulk add, failure/retry, descriptions from both share paths OK')
    browser.close()
