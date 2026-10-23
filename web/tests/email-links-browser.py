"""Exercise real mail rendering/popups with synthetic mail, without a Prism server.

Run with Playwright and Chromium installed. CHROMIUM_EXECUTABLE_PATH can select
an existing browser. All APIs and external destinations are intercepted locally.
"""
import json
import os
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright

WEB = Path(__file__).resolve().parents[1]
TARGETS = [None, "_self", "_parent", "_top", "existing-window", "_blank"]
LINKS = "".join(
    f'<p><a id="link-{i}" href="https://mail-link.example.test/{i}" '
    + (f'target="{target}" ' if target else "")
    + f'rel="opener">Link {i}</a></p>'
    for i, target in enumerate(TARGETS)
)
HTML = (
    '<!DOCTYPE html><html><head><base target="_self">'
    '<style>body{color:rgb(17,34,51)}</style></head><body>'
    + LINKS
    + '<script>window.mailScriptRan=true;parent.mailScriptRan=true</script>'
    '</body></html>'
)
MESSAGE = dict(uid=1, seen=True, subject="Links", date="2026-10-23T12:00:00Z",
               **{"from": "sender@example.test", "to": "me@example.test"})
external = []
errors = []


class Handler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def intercept(route):
    url = urlsplit(route.request.url)
    if url.hostname == "mail-link.example.test":
        route.fulfill(content_type="text/html", body=(
            "<!DOCTYPE html><p>Destination</p>"
            "<script>window.landingScriptRan=true</script>"))
        return
    if url.hostname != "127.0.0.1":
        external.append(route.request.url)
        route.abort()
        return
    if url.path == "/qa-mail":
        route.fulfill(content_type="text/html", body=(
            '<!DOCTYPE html><iframe id="app" src="/apps/email.html" '
            'style="width:1200px;height:850px"></iframe>'))
        return
    if not url.path.startswith("/api/"):
        route.continue_()
        return
    if url.path == "/api/email/config":
        data = dict(configured=True, has_password=True, user="me@example.test")
    elif url.path == "/api/email/list":
        data = {"messages": [MESSAGE]}
    elif url.path == "/api/email/read":
        data = {"message": dict(MESSAGE, body=HTML)}
    elif url.path == "/api/builtin/email":
        data = {"result": json.dumps([dict(name="INBOX", role="inbox", selectable=True)])}
    else:
        data = {}
    route.fulfill(content_type="application/json", body=json.dumps(data))


server = ThreadingHTTPServer(("127.0.0.1", 0), partial(Handler, directory=str(WEB)))
Thread(target=server.serve_forever, daemon=True).start()
try:
    with sync_playwright() as p:
        options = {"headless": True}
        if os.getenv("CHROMIUM_EXECUTABLE_PATH"):
            options["executable_path"] = os.environ["CHROMIUM_EXECUTABLE_PATH"]
        browser = p.chromium.launch(**options)
        context = browser.new_context(viewport={"width": 1260, "height": 930})
        context.set_default_timeout(5000)
        context.route("**/*", intercept)
        page = context.new_page()
        page.on("pageerror", lambda error: errors.append(str(error)))
        origin = f"http://127.0.0.1:{server.server_port}"
        page.goto(origin + "/qa-mail")
        app = page.frame_locator("#app")
        app.locator(".mrow").first.click()
        element = app.locator("iframe.rhtml")
        element.wait_for()
        app.frame_locator("iframe.rhtml").locator("#link-0").wait_for()
        frame = next(f for f in page.frames if f.url == "about:srcdoc")
        assert element.get_attribute("sandbox") == "allow-popups allow-popups-to-escape-sandbox"
        assert frame.evaluate("document.compatMode") == "CSS1Compat"
        assert frame.evaluate("getComputedStyle(document.body).color") == "rgb(17, 34, 51)"
        assert frame.evaluate("typeof window.mailScriptRan") == "undefined"
        for i in range(len(TARGETS)):
            with context.expect_page() as opened:
                frame.locator(f"#link-{i}").click()
            popup = opened.value
            popup.wait_for_load_state()
            assert popup.url == f"https://mail-link.example.test/{i}"
            assert popup.evaluate("window.opener === null")
            assert popup.evaluate("window.landingScriptRan === true")
            assert page.url == origin + "/qa-mail"
            assert frame.url == "about:srcdoc"
            assert frame.locator("#link-0").count() == 1
            popup.close()
        assert not external, external
        assert not errors, errors
        browser.close()
        print("PASS: mail links open isolated tabs; the message, styling, doctype and script sandbox stay intact")
finally:
    server.shutdown()
    server.server_close()
