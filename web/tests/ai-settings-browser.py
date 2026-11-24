"""Offline browser regressions for the shared personal/admin AI settings form.

All requests are intercepted. Credentials, providers and model responses are
synthetic; running this file never calls an inference API.
"""
from copy import deepcopy
import json
import os
from pathlib import Path
import re

from playwright.sync_api import sync_playwright

if "SOURCE" not in globals():
    SOURCE = (Path(__file__).resolve().parents[1] / "ai-settings.js").read_text()
if "CSS" not in globals():
    CSS = (Path(__file__).resolve().parents[1] / "ai-settings.css").read_text()
if "SETTINGS_STATE" not in globals():
    SETTINGS_STATE = (Path(__file__).resolve().parents[1] / "settings-state.js").read_text()


def initial_state():
    return {
        "source": "configured", "serverDefaultsAvailable": True,
        "provider": "openai", "baseURL": "https://api.openai.com/v1",
        "model": "", "chatVision": True, "defaultSource": "primary",
        "sources": [{"id": "primary", "name": "OpenAI", "provider": "openai",
                     "baseURL": "https://api.openai.com/v1", "model": "", "keyConfigured": False}],
        "embedding": {"useSameProvider": False, "sourceID": "", "provider": "openai",
                      "baseURL": "https://api.openai.com/v1", "model": "", "keyConfigured": False},
        "embeddingStatus": "not configured",
    }


def run_workflow(browser, admin):
    context = browser.new_context(viewport={"width": 1280, "height": 900})
    page = context.new_page()
    page.set_default_timeout(5000)
    state, secrets, calls, errors = initial_state(), {}, [], []
    failures = {"save": False, "models": False, "read": False}
    metadata = {"model-a": {"contextWindow": 500000, "vision": True},
                "model-b": {"contextWindow": 32768, "vision": False}}
    page.on("pageerror", lambda error: errors.append(error.stack))

    def write_calls():
        return [call for call in calls if call.get("action") in ("save", "chat", "model_settings", "DELETE")]

    def public_state():
        result = deepcopy(state)
        assert all("apiKey" not in source for source in result["sources"])
        assert "apiKey" not in result["embedding"]
        assert not any(value in json.dumps(result) for value in secrets.values())
        return result

    def route(request_route):
        request = request_route.request
        # Fulfil even unexpected domains locally: this fixture has no network egress.
        if request.url.endswith("/api/me"):
            request_route.fulfill(json={"multiUser": False})
            return
        if "/api/ai/config" not in request.url:
            request_route.fulfill(content_type="text/html", body='''
              <style>:root{--bg:#111827;--bg2:#182235;--bg3:#243249;--text:#e5e7eb;
              --text2:#b7c0cf;--text3:#94a3b8;--border:#344155;--accent:#8b9cff}
              *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px system-ui}
              button,input,select{font:inherit}</style><div id="settings-content"><div id="app"></div></div><span id="settings-save-status"></span><a href="/away" id="leave">Leave</a>''')
            return
        if request.method == "GET":
            if failures["read"]:
                request_route.fulfill(status=503, json={"error": "Fixture reload unavailable"})
                return
            request_route.fulfill(json=public_state())
            return
        if request.method == "DELETE":
            calls.append({"action": "DELETE"})
            state.clear()
            state.update(initial_state())
            state["source"] = "server"
            secrets.clear()
            request_route.fulfill(json={"ok": True})
            return
        data = request.post_data_json
        calls.append(deepcopy(data))
        action = data["action"]
        if failures.get(action):
            request_route.fulfill(status=502, json={"error": "Fixture provider unavailable"})
            return
        if action == "model_info":
            source = next((s for s in state["sources"] if s["id"] == data["sourceID"]), None)
            if source is None:
                request_route.fulfill(status=400, json={"error": "Save this source before detecting capabilities"})
            else:
                request_route.fulfill(json={"detected": metadata.get(data["model"], {}),
                                           "settings": source.get("modelSettings", {}).get(data["model"], {})})
            return
        if action.endswith("models"):
            request_route.fulfill(json={"models": ["model-a", "model-b"] if action == "models" else ["embed-a"]})
            return
        if action == "model_settings":
            raise AssertionError("Model dialog must not persist independently of the global save")
        if action in ("save", "chat"):
            assert action == "save", "UI must not autosave via the legacy chat action"
            previous = {s["id"]: s for s in state["sources"]}
            next_sources = []
            for draft in data["sources"]:
                source = deepcopy(draft)
                source_id = source["id"]
                old = previous.get(source_id, {})
                if source["provider"] == "openai" and source["baseURL"] != "https://api.openai.com/v1":
                    source["provider"] = "other"
                same_endpoint = (old.get("provider"), old.get("baseURL")) == (source["provider"], source["baseURL"])
                entered = source.pop("apiKey", "")
                if source.pop("clearKey", False):
                    secrets.pop(source_id, None)
                elif entered:
                    secrets[source_id] = entered
                elif not same_endpoint:
                    secrets.pop(source_id, None)
                source["keyConfigured"] = source_id in secrets
                next_sources.append(source)
            state["sources"] = next_sources
            state["defaultSource"] = data["defaultSource"]
            primary = next(s for s in next_sources if s["id"] == state["defaultSource"])
            for field in ("provider", "baseURL", "model"):
                state[field] = primary[field]
            state["source"] = "configured"
            embedding = deepcopy(data["embedding"])
            old_embedding = state["embedding"]
            entered = embedding.pop("apiKey", "")
            cleared = embedding.pop("clearKey", False)
            dedicated = not embedding.get("useSameProvider") and not embedding.get("sourceID")
            old_dedicated = not old_embedding.get("useSameProvider") and not old_embedding.get("sourceID")
            same_endpoint = (embedding.get("provider"), embedding.get("baseURL", "").rstrip("/")) == (old_embedding.get("provider"), old_embedding.get("baseURL", "").rstrip("/"))
            if not dedicated or cleared:
                secrets.pop("embedding", None)
            elif entered:
                secrets["embedding"] = entered
            elif not old_dedicated or not same_endpoint:
                secrets.pop("embedding", None)
            shared = primary if embedding.get("useSameProvider") else next((s for s in next_sources if s["id"] == embedding.get("sourceID")), None)
            embedding["keyConfigured"] = bool(shared and shared["keyConfigured"]) if not dedicated else "embedding" in secrets
            state["embedding"] = embedding
            state["embeddingStatus"] = "not configured" if not state["embedding"]["model"] else "ready"
            request_route.fulfill(json={"ok": True})
            return
        request_route.fulfill(json={"ok": True, "dimension": 8})

    page.route("**/*", route)
    page.goto("https://fixture.test/")
    page.add_style_tag(content=CSS)
    if admin:
        page.locator("#settings-content").evaluate("e => e.id = 'adm-content'")
    page.add_script_tag(content=SETTINGS_STATE)
    page.add_script_tag(content=SOURCE)

    def reopen():
        page.evaluate("admin => renderAITab(document.getElementById('app'), {admin})", admin)
        page.locator("[data-action=models]").wait_for(state="visible")
        page.locator("[data-save]").wait_for(state="visible")

    def saved():
        page.locator("[data-save]").click()
        page.get_by_text("All changes saved", exact=True).wait_for()
        assert page.locator("[data-save]").is_disabled()
        assert not page.evaluate("PrismSettingsState.dirty()"), "saved values remain dirty in the shared tracker"

    def fill_model(value):
        page.locator("[name=model]").fill(value)
        page.locator("[name=model]").press("Tab")

    reopen()
    assert page.locator("button[type=submit]").count() == 2  # global form + closed model dialog
    assert page.locator("[data-save]").inner_text() == "Save changes"
    assert page.locator("[data-save]").is_disabled()
    assert not page.get_by_text("Save other changes", exact=True).count()
    page.locator("[name=apiKey]").fill("fixture-first-key")
    assert page.evaluate("PrismSettingsState.dirty()")
    page.locator("[data-action=models]").click()
    page.get_by_text("Connection verified · 2 models", exact=True).wait_for()
    probe = next(call for call in reversed(calls) if call["action"] == "models")
    assert probe["sources"][0]["apiKey"] == "fixture-first-key", "busy state lost the unsaved credential"
    assert not write_calls()
    fill_model("model-a")
    assert not write_calls() and state["model"] == ""
    assert page.locator("[data-save]").is_enabled()
    saved()
    assert len(write_calls()) == 1 and state["model"] == "model-a"
    reopen()
    assert page.locator("[name=model]").input_value() == "model-a"
    assert page.locator("[name=apiKey]").input_value() == ""
    assert page.locator('[data-key-status=""]').inner_text() == "Saved securely"
    assert page.locator('[data-replace-key=""]').is_visible()
    assert page.locator('[data-remove-key=""]').is_visible()
    page.locator("[data-model-settings]").click()
    page.locator("[name=maxOutputTokens]").fill("1024")
    assert page.evaluate("PrismSettingsState.dirty()")
    page.locator("[data-model-close]").click()
    page.wait_for_function("!PrismSettingsState.dirty()")
    assert not page.evaluate("PrismSettingsState.dirty()"), "cancelled dialog remains dirty"
    assert page.locator("[data-save]").is_disabled()

    # Failed probes and source switches must retain replacement drafts.
    page.locator('[data-replace-key=""]').click()
    page.locator("[name=apiKey]").fill("fixture-replacement-key")
    before = len(write_calls())
    failures["models"] = True
    page.locator("[data-action=models]").click()
    page.locator('[data-connected=""]').filter(has_text="Fixture provider unavailable").wait_for()
    assert page.locator("[name=apiKey]").input_value() == "fixture-replacement-key"
    assert len(write_calls()) == before and secrets["primary"] == "fixture-first-key"
    failures["models"] = False
    page.locator("[data-add-source]").click()
    page.locator("[data-sources] button").first.click()
    assert page.locator("[name=apiKey]").input_value() == "fixture-replacement-key"
    page.locator("[data-sources] button").nth(1).click()
    page.locator("[data-remove-source]").click()
    assert len(state["sources"]) == 1 and len(write_calls()) == before
    page.locator('[data-cancel-key=""]').click()
    assert page.locator("[name=apiKey]").input_value() == ""
    page.locator('[data-remove-key=""]').click()
    assert secrets["primary"] == "fixture-first-key" and len(write_calls()) == before
    page.locator('[data-cancel-key=""]').click()
    page.locator('[data-replace-key=""]').click()
    page.locator("[name=apiKey]").fill("fixture-replacement-key")
    saved()
    assert secrets["primary"] == "fixture-replacement-key"
    page.locator('[data-remove-key=""]').click()
    assert secrets["primary"] == "fixture-replacement-key"
    saved()
    assert "primary" not in secrets and not state["sources"][0]["keyConfigured"]
    assert page.locator("[name=apiKey]").is_visible()
    page.locator("[name=apiKey]").fill("fixture-restored-key")
    saved()

    # A custom endpoint is visible, and replacing it does not reuse an old key.
    advanced = page.locator('[data-connection-advanced=""]')
    if advanced.get_attribute("open") is None:
        advanced.locator("summary").click()
    page.locator("[name=baseURL]").fill("https://custom.fixture.test/v1")
    page.locator("[name=baseURL]").press("Tab")
    assert page.locator("[name=baseURL]").is_visible()
    assert page.locator("[name=apiKey]").is_visible()
    assert page.locator("[name=apiKey]").input_value() == ""
    page.locator("[name=apiKey]").fill("fixture-custom-key")
    saved()
    reopen()
    assert page.locator("[name=baseURL]").is_visible()
    assert state["baseURL"] == "https://custom.fixture.test/v1"

    # Per-model overrides join the same pending changes; applying the dialog is local.
    before = len(write_calls())
    page.locator("[data-model-settings]").click()
    page.locator("[name=settingsModel]").fill("model-b")
    page.locator("[name=settingsModel]").press("Tab")
    page.locator("[name=contextMode]").select_option("manual")
    page.locator("[name=contextWindow]").fill("500000")
    page.locator("[name=maxOutputTokens]").fill("65536")
    page.locator("[name=modelVision]").select_option("no")
    page.locator("[data-model-form] button[type=submit]").click()
    assert not page.locator("dialog").is_visible()
    assert len(write_calls()) == before and not state["sources"][0].get("modelSettings", {}).get("model-b")
    page.once("dialog", lambda dialog: dialog.dismiss())
    page.locator("#leave").click()
    assert page.url == "https://fixture.test/", "staged model settings did not warn before leaving"
    saved()
    assert state["sources"][0]["modelSettings"]["model-b"] == {"contextWindow": 500000, "maxOutputTokens": 65536, "vision": False}
    assert state["model"] == "model-a"
    page.locator("[data-model-settings]").click()
    page.locator("[name=maxOutputTokens]").fill("65536")
    page.locator("[data-model-form] button[type=submit]").click()
    saved()
    assert state["sources"][0]["modelSettings"]["model-a"]["maxOutputTokens"] == 65536
    reopen()
    page.locator("[data-model-settings]").click()
    assert page.locator("[name=maxOutputTokens]").input_value() == "65536"
    page.locator("[data-model-reset]").click()
    page.locator("[data-model-form] button[type=submit]").click()
    saved()
    assert not state["sources"][0].get("modelSettings", {}).get("model-a", {}).get("maxOutputTokens")

    # Adding/default/removing a source are a single staged save, not independent writes.
    before = len(write_calls())
    page.locator("[data-add-source]").click()
    page.locator("[name=sourceName]").fill("Second")
    page.locator("[name=apiKey]").fill("fixture-second-key")
    fill_model("model-b")
    page.locator("[data-default-source]").click()
    assert len(write_calls()) == before and state["defaultSource"] == "primary"
    saved()
    second = state["defaultSource"]
    assert second != "primary" and state["model"] == "model-b"
    assert state["sources"][0]["modelSettings"]["model-b"]["maxOutputTokens"] == 65536
    assert not state["sources"][1].get("modelSettings", {}).get("model-b")
    reopen()
    assert page.locator("[name=model]").input_value() == "model-b"
    before = len(write_calls())
    page.once("dialog", lambda dialog: dialog.accept())
    page.locator("[data-remove-source]").click()
    assert len(write_calls()) == before and len(state["sources"]) == 2
    saved()
    assert len(state["sources"]) == 1 and state["defaultSource"] == "primary"

    # Disabling embeddings changes only the draft until explicit save.
    page.locator("[data-embedding-section] > summary").click()
    page.locator("[name=embeddingMode]").select_option("dedicated")
    page.locator("[name=embed-model]").fill("embed-a")
    page.locator("[name=embed-apiKey]").fill("fixture-embedding-key")
    page.locator("[name=reindex]").check()
    before = len(write_calls())
    assert state["embedding"]["model"] == "" and len(write_calls()) == before
    saved()
    assert state["embedding"]["model"] == "embed-a"
    if not page.locator("[name=embeddingMode]").is_visible():
        page.locator("[data-embedding-section] > summary").click()
    assert page.locator('[data-key-status="embed-"]').inner_text() == "Saved securely"
    page.locator('[data-remove-key="embed-"]').click()
    assert secrets["embedding"] == "fixture-embedding-key"
    page.locator('[data-cancel-key="embed-"]').click()
    if not page.locator("[name=embeddingMode]").is_visible():
        page.locator("[data-embedding-section] > summary").click()
    page.locator("[name=embeddingMode]").select_option("disabled")
    assert state["embedding"]["model"] == "embed-a"
    saved()
    assert state["embedding"]["model"] == "" and secrets["embedding"] == "fixture-embedding-key"
    reopen()
    page.locator("[data-embedding-section] > summary").click()
    assert page.locator("[name=embeddingMode]").input_value() == "disabled"
    page.locator("[name=embeddingMode]").select_option("dedicated")
    assert page.locator('[data-key-status="embed-"]').inner_text() == "Saved securely"
    assert page.locator("[name=embed-apiKey]").input_value() == ""
    page.locator("[name=embed-model]").fill("embed-a")
    page.locator("[name=reindex]").check()
    saved()
    assert state["embedding"]["model"] == "embed-a" and secrets["embedding"] == "fixture-embedding-key"

    # Removing a dedicated key and disabling in the same save keeps the removal.
    page.locator('[data-remove-key="embed-"]').click()
    page.locator("[name=embeddingMode]").select_option("disabled")
    assert secrets["embedding"] == "fixture-embedding-key"
    saved()
    assert state["embedding"]["model"] == "" and "embedding" not in secrets
    last_save = write_calls()[-1]["embedding"]
    assert last_save["clearKey"] and last_save["apiKey"] == ""
    reopen()
    page.locator("[data-embedding-section] > summary").click()
    page.locator("[name=embeddingMode]").select_option("dedicated")
    assert page.locator('[data-key-status="embed-"]').inner_text() == "Not set"
    page.locator("[name=embed-apiKey]").fill("fixture-embedding-restored")
    page.locator("[name=embed-model]").fill("embed-a")
    page.locator("[name=reindex]").check()
    saved()

    # A replacement entered before disabling is not dropped with the hidden form.
    page.locator('[data-replace-key="embed-"]').click()
    page.locator("[name=embed-apiKey]").fill("fixture-embedding-replacement")
    page.locator("[name=embeddingMode]").select_option("disabled")
    assert secrets["embedding"] == "fixture-embedding-restored"
    saved()
    assert state["embedding"]["model"] == "" and secrets["embedding"] == "fixture-embedding-replacement"
    last_save = write_calls()[-1]["embedding"]
    assert last_save["apiKey"] == "fixture-embedding-replacement" and not last_save["clearKey"]
    reopen()
    page.locator("[data-embedding-section] > summary").click()
    page.locator("[name=embeddingMode]").select_option("dedicated")
    assert page.locator('[data-key-status="embed-"]').inner_text() == "Saved securely"
    page.locator("[name=embed-model]").fill("embed-a")
    page.locator("[name=reindex]").check()
    saved()
    assert state["embedding"]["model"] == "embed-a" and secrets["embedding"] == "fixture-embedding-replacement"

    # Failed saves keep active configuration and pending values; discard restores saved state.
    fill_model("model-b")
    failures["save"] = True
    page.locator("[data-save]").click()
    page.locator("[data-status]").filter(has_text="Fixture provider unavailable").wait_for()
    assert state["model"] == "model-a" and page.locator("[name=model]").input_value() == "model-b"
    assert page.locator("[data-save]").is_enabled()
    page.once("dialog", lambda dialog: dialog.dismiss())
    page.locator("#leave").click()
    assert page.url == "https://fixture.test/"
    failures["save"] = False
    page.locator("[data-discard]").click()
    page.get_by_text("All changes saved", exact=True).wait_for()
    assert page.locator("[name=model]").input_value() == "model-a"
    assert not page.evaluate("PrismSettingsState.dirty()")

    # Restore-server action is also staged and can be discarded.
    before = len(write_calls())
    defaults_panel = page.locator("[data-server-defaults]")
    if defaults_panel.get_attribute("open") is None:
        defaults_panel.locator(":scope > summary").click()
    page.once("dialog", lambda dialog: dialog.accept())
    page.locator("[data-action=reset]").click()
    assert len(write_calls()) == before
    page.locator("[data-discard]").click()
    page.get_by_text("All changes saved", exact=True).wait_for()
    assert len(write_calls()) == before

    # A successful save followed by a failed refresh cannot invite a duplicate save.
    fill_model("model-b")
    failures["read"] = True
    page.locator("[data-save]").click()
    page.locator("[data-status]").filter(has_text="Saved, but unable to refresh").wait_for()
    assert state["model"] == "model-b" and page.locator("[data-save]").is_disabled()
    assert page.locator("[name=apiKey]").input_value() == ""
    assert not page.evaluate("PrismSettingsState.dirty()")
    failures["read"] = False
    page.locator("[data-discard]").click()
    page.get_by_text("All changes saved", exact=True).wait_for()
    assert page.locator("[name=model]").input_value() == "model-b"
    assert not page.evaluate("PrismSettingsState.dirty()")

    # Surface narrow layouts without screenshots containing replacement key drafts.
    shot_dir = os.environ.get("PRISM_UI_SCREENSHOT_DIR")
    for width in (1280, 390):
        page.set_viewport_size({"width": width, "height": 900})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"horizontal overflow at {width}px"
        if shot_dir:
            directory = Path(shot_dir)
            directory.mkdir(parents=True, exist_ok=True)
            page.screenshot(path=str(directory / f"ai-settings-{'admin' if admin else 'personal'}-{width}.png"), full_page=True)

    # Restore defaults is committed only by the same global save.
    defaults_panel = page.locator("[data-server-defaults]")
    if defaults_panel.get_attribute("open") is None:
        defaults_panel.locator(":scope > summary").click()
    page.once("dialog", lambda dialog: dialog.accept())
    page.locator("[data-action=reset]").click()
    before = len(write_calls())
    saved()
    assert len(write_calls()) == before + 1 and write_calls()[-1]["action"] == "DELETE"
    assert state["source"] == "server"

    # A stalled verification releases controls, preserves the draft and never saves.
    page.locator("[name=apiKey]").fill("fixture-timeout-key")
    before = len(write_calls())
    page.evaluate("window.originalTimeout=window.setTimeout; window.setTimeout=(fn,ms,...args)=>window.originalTimeout(fn,ms===40000?100:ms,...args)")
    stalled = []
    def stall_verification(request_route):
        if request_route.request.method == "POST" and request_route.request.post_data_json.get("action") == "models":
            stalled.append(request_route)
        else:
            request_route.fallback()
    page.route("**/api/ai/config", stall_verification)
    page.locator("[data-action=models]").click()
    page.locator("[data-status]").filter(has_text="The server did not respond in time").wait_for()
    assert page.locator("[data-action=models]").is_enabled()
    assert page.locator("[name=apiKey]").input_value() == "fixture-timeout-key"
    assert len(write_calls()) == before
    for request_route in stalled:
        try:
            request_route.abort()
        except Exception:
            pass
    page.locator("[data-discard]").click()
    page.get_by_text("All changes saved", exact=True).wait_for()

    assert not errors, errors
    print(f"{'Admin' if admin else 'Personal'} AI settings: explicit-save, credentials, model overrides, sources, embeddings, errors and layout PASS", flush=True)
    context.close()


def run_member_guard(browser):
    context = browser.new_context()
    page = context.new_page()
    config_calls = []
    def route(request_route):
        url = request_route.request.url
        if url.endswith("/api/me"):
            request_route.fulfill(json={"multiUser": True})
        elif "/api/ai/config" in url:
            config_calls.append(url)
            request_route.fulfill(status=403, json={"error": "global admin only"})
        else:
            request_route.fulfill(content_type="text/html", body='<div id="settings-content"><div id="app"></div></div>')
    page.route("**/*", route)
    page.goto("https://fixture.test/")
    page.add_script_tag(content=SOURCE)
    page.evaluate("renderAITab(document.getElementById('app'), {admin:false})")
    page.get_by_text("AI providers are configured by the global administrator in Admin → AI provider.", exact=True).wait_for()
    assert page.locator("[data-save]").count() == 0 and page.locator("[name=apiKey]").count() == 0
    assert not config_calls, "a personal multi-user view tried to load deployment AI configuration"
    print("Multi-user personal view: deployment configuration guard PASS", flush=True)
    context.close()


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True, executable_path=os.environ.get("CHROMIUM_EXECUTABLE"), args=["--no-sandbox", "--disable-dev-shm-usage"])
    try:
        for admin in (False, True):
            run_workflow(browser, admin)
        run_member_guard(browser)
    finally:
        browser.close()
