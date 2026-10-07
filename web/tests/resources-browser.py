"""Isolated browser check of maintenance UI; never calls a live Prism API."""
import json
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

if 'SOURCES' not in globals():
    root = Path(__file__).resolve().parents[1]
    SOURCES = {name: (root / name).read_text() for name in ('resources.js', 'modal.js', 'settings.html', 'style.css')}

settings = SOURCES['settings.html']
renderer = settings[settings.index('async function renderResourcesTab('):settings.index('async function renderToolsTab(')]
rows = [
    {'id': 'widget:a/weather', 'kind': 'widget', 'name': 'weather', 'session': 'a', 'tracked': False, 'usedBy': []},
    {'id': 'tool:fetch', 'kind': 'tool', 'name': 'fetch', 'session': 'a', 'tracked': True, 'usedBy': []},
    {'id': 'file:data/shared.json', 'kind': 'file', 'name': 'data/shared.json', 'session': 'a', 'tracked': True, 'protected': True, 'usedBy': ['shared consumer']},
]
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=os.environ.get('CHROMIUM_EXECUTABLE'), args=['--no-sandbox', '--disable-dev-shm-usage'])
    page = browser.new_page()
    page.set_default_timeout(5000)
    errors, calls = [], []
    fail = {'apply': False}
    page.on('pageerror', lambda e: errors.append(str(e)))

    def route(r):
        if '/api/sessions' in r.request.url:
            r.fulfill(json={'sessions': [{'id': 'a', 'name': 'Weather'}]})
        elif '/api/resources' in r.request.url:
            if r.request.method == 'GET':
                r.fulfill(json=rows)
                return
            data = r.request.post_data_json
            calls.append(data)
            if data.get('dry_run') is False and fail['apply']:
                r.fulfill(status=409, json={'error': 'Fixture cron installation failed'})
                return
            if data['action'] == 'link':
                r.fulfill(json={'widget': data['widget'], 'resources': data['ids']})
            else:
                plan = {'remove': [rows[1]], 'keep': [rows[2]]}
                r.fulfill(json={'plan': plan, 'removed': ['tool:fetch'] if data.get('dry_run') is False else [], 'errors': []})
        else:
            r.fulfill(content_type='text/html', body='<div id="app"></div>')

    page.route('**/*', route)
    page.goto('https://fixture.test/')
    page.add_style_tag(content=SOURCES['style.css'])
    page.add_style_tag(content=settings.split('<style>', 1)[1].split('</style>', 1)[0])
    page.add_script_tag(content='''
      function settingsHeading(title, text) { return '<h2>'+title+'</h2><p>'+text+'</p>' }
      function escHtml(s) { const el=document.createElement('span'); el.textContent=s; return el.innerHTML }
      const settingsFetch = fetch;
    ''' + SOURCES['modal.js'] + SOURCES['resources.js'] + renderer)
    page.evaluate('renderResourcesTab(document.getElementById("app"))')
    page.get_by_label('Select tool:fetch', exact=True).wait_for(state='attached')
    assert page.get_by_label('Select file:data/shared.json', exact=True).is_disabled()
    assert page.get_by_label('Select widget:a/weather', exact=True).is_disabled()
    assert page.locator('.toggle-track').count() == 3
    page.get_by_label('Select tool:fetch', exact=True).locator('..').click()
    page.locator('#res-widget').select_option('widget:a/weather')
    page.locator('#res-link').click()
    page.get_by_role('button', name='OK', exact=True).click()
    page.get_by_text('Resource links saved.', exact=True).wait_for()
    assert calls[-1] == {'action': 'link', 'widget': 'weather', 'ids': ['tool:fetch']}

    page.get_by_label('Select tool:fetch', exact=True).locator('..').click()
    page.locator('#res-clean').click()
    page.locator('.pm-title').filter(has_text='Clean up selected resources').wait_for()
    assert calls[-1].get('dry_run') is not False
    assert page.locator('.pm-box input').is_disabled()
    page.get_by_role('button', name='Cancel', exact=True).click()
    assert not any(c.get('dry_run') is False for c in calls)
    assert page.locator('#res-clean').is_enabled()

    fail['apply'] = True
    page.locator('#res-clean').click()
    page.get_by_role('button', name='Delete', exact=True).click()
    page.get_by_text('Fixture cron installation failed', exact=True).wait_for()
    assert page.locator('#res-clean').is_enabled()
    fail['apply'] = False
    page.locator('#res-clean').click()
    page.get_by_role('button', name='Delete', exact=True).click()
    page.locator('#res-status').filter(has_text='1 resource(s) removed.').wait_for()
    assert calls[-1]['ids'] == ['tool:fetch']
    assert not errors, errors
    print('Resources UI: themed toggles, linking, preview/cancel, protected resources, failure and retry: PASS')
    browser.close()
