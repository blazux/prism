"""MCP refresh UI smoke test with fixture data; no live Prism API is called."""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
settings = (root / 'settings.html').read_text()
personal = settings[settings.index('function renderMCPServerList('):settings.index('// ── Secrets tab')]
admin = (root.parent / 'internal/server/admin_console_page.go').read_text()
group = admin[admin.index('// ── Group MCP servers (shared'):admin.index('// ── Group knowledge base (RAG)')]
old = [{'name': 'kept', 'description': 'Original'}, {'name': 'removed', 'description': 'Old tool'}]
new = [{'name': 'kept', 'description': 'Updated'}, {'name': 'added', 'description': 'New tool'}, {'name': 'extra'}]
state = {'id': 'fixture', 'name': 'Fixture MCP', 'url': 'https://mcp.example.test/tools', 'enabled': True, 'tools': old}
fail = False
calls = []

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=os.environ.get('CHROMIUM_EXECUTABLE'), args=['--no-sandbox', '--disable-dev-shm-usage'])
    page = browser.new_page()
    page.set_default_timeout(5000)
    errors = []
    page.on('pageerror', lambda e: errors.append(str(e)))

    def route(r):
        if '/api/' not in r.request.url:
            r.fulfill(content_type='text/html', body='<div class="settings-page" id="app"></div>')
        elif r.request.method == 'POST':
            calls.append((r.request.method, r.request.url))
            if fail:
                r.fulfill(status=502, json={'error': 'Fixture server unavailable'})
            else:
                state['tools'] = new
                r.fulfill(json={'ok': True, 'count': len(new), 'tools': new})
        else:
            r.fulfill(json={'servers': [state], 'secrets': [], 'policy': {'kept': 'admin_only'}})

    page.route('**/*', route)
    page.goto('https://fixture.test/')
    page.add_style_tag(content=(root / 'style.css').read_text())
    page.add_style_tag(content=settings.split('<style>', 1)[1].split('</style>', 1)[0])
    page.add_script_tag(content='''
      const SESSION='board'; let mcpServers=[];
      const settingsFetch=fetch;
      function escHtml(s){const el=document.createElement('span');el.textContent=s;return el.innerHTML}
      function makeToolCard(name,title,description){const el=document.createElement('div');el.textContent=title+' '+description;return el}
    ''' + personal)
    page.evaluate('(s)=>{mcpServers=[s];renderMCPServerList(document.getElementById("app"))}', state)
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_role('status').filter(has_text='Refreshed · 3 tools').wait_for()
    assert 'added' in page.locator('.config-tools-grid').inner_text()
    assert 'Updated' in page.locator('.config-tools-grid').inner_text()
    assert 'removed' not in page.locator('.config-tools-grid').inner_text()
    fail = True
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_role('status').filter(has_text='Refresh failed. Previous list kept.').wait_for()
    assert 'added' in page.locator('.config-tools-grid').inner_text()
    assert page.get_by_role('button', name='Refresh', exact=True).is_enabled()
    fail = False
    state['enabled'] = False
    page.evaluate('(s)=>{mcpServers=[s];renderMCPServerList(document.getElementById("app"))}', state)
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_role('status').filter(has_text='server remains disabled').wait_for()
    assert not page.locator('input[type=checkbox]').is_checked()
    for width in (1280, 390):
        page.set_viewport_size({'width': width, 'height': 800})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'horizontal overflow'
        page.screenshot(path=f'/tmp/prism-mcp-refresh-{width}.png')
    print('Personal refresh: updated list, error retention, disabled state, desktop/mobile OK')

    page.evaluate('document.body.innerHTML=\'<select id="mc-group"><option value="1">Fixture</option></select><select id="gmcp-secret"></select><div id="gmcp"></div><div id="gmcp-status" role="status"></div>\'')
    page.add_script_tag(content='''
      const $=id=>document.getElementById(id); const esc=escHtml;
      async function jget(url){return (await fetch(url)).json()}
    ''' + group)
    page.evaluate('loadGroupMCP()')
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_role('status').filter(has_text='Refreshed · 3 tools').wait_for()
    assert page.locator('#gmcp').get_by_text('added', exact=True).count() == 1
    assert page.locator('#gmcp select').first.input_value() == 'admin_only'
    fail = True
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_role('status').filter(has_text='Refresh failed. Previous list kept.').wait_for()
    assert page.get_by_role('button', name='Refresh', exact=True).is_enabled()
    assert page.locator('#gmcp').get_by_text('added', exact=True).count() == 1
    assert len(calls) == 5 and all(method == 'POST' for method, _ in calls)
    assert not errors, errors
    print('Group admin refresh: updated tools, existing restrictions, failure/retry OK')
    browser.close()
