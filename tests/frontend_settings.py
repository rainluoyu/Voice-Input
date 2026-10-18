"""Settings contract fixture; only local assets and intercepted Wails HTTP calls."""
import copy
import json
import os
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import sync_playwright

base = f"http://127.0.0.1:{os.environ.get('UI_TEST_PORT', '5173')}"
screenshots = Path(os.environ.get("UI_SCREENSHOT_DIR", "/tmp/voice_input-redesign"))
screenshots.mkdir(parents=True, exist_ok=True)
startup_error = ""
defaults = {"dictation": "默认润色😀<img src=x onerror=window.__pwned=true>", "dialogue": "默认对话📦"}
config = {
    "version": 1, "mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A",
    "recording": {"device_id": "default", "max_seconds": 120},
    # Legacy configs omit provider; the fallback must not make ASR appear dirty.
    "asr": {"endpoint": "https://asr.example/v1/audio/transcriptions", "model": "asr", "language": "", "timeout_seconds": 45},
    "llm": {"base_url": "https://llm.example/v1", "model": "llm", "timeout_seconds": 45, "task_timeout_seconds": 90, "output_format": "json_schema", "dictation_prompt": "", "dialogue_prompt": "自定义对话", "max_tool_rounds": 2},
    "exa": {"enabled": False, "search_type": "instant", "max_calls": 2, "timeout_seconds": 10},
    "profile": {"custom_background": "独立背景😀"},
    "popup": {"auto_hide_seconds": 77, "always_on_top": False},
    "privacy": {"save_history": True, "log_text": True},
}
pending_read = []
pending_save = []
pending_key = []
requests = []
errors = []
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    context = browser.new_context(viewport={"width": 760, "height": 800})

    def route_request(route):
        parsed = urlsplit(route.request.url)
        assert parsed.netloc == "wails.localhost", "unexpected external request in offline fixture"
        if parsed.path != "/wails/runtime":
            response = context.request.get(base + parsed.path + ("?" + parsed.query if parsed.query else ""))
            route.fulfill(response=response)
            return
        payload = route.request.post_data_json
        args = payload.get("args", {})
        method = args.get("methodName", "").rsplit(".", 1)[-1]
        params = args.get("args", [])
        requests.append((method, copy.deepcopy(params)))
        if method == "GetSettings":
            pending_read.append(route)
            return
        if method == "SaveSettings":
            pending_save.append((route, copy.deepcopy(params[0])))
            return
        if method == "SaveKey":
            pending_key.append((route, copy.deepcopy(params)))
            return
        if method == "StartupError":
            result = startup_error
        elif method in ("TestConnection", "DeleteKey"):
            result = {"ok": True, "message": "fixture"}
        elif method == "CloseSettings":
            result = None
        else:
            raise AssertionError("unexpected fixture method: " + method)
        route.fulfill(status=200, content_type="application/json", body=json.dumps(result, ensure_ascii=False))

    context.route("**/*", route_request)
    page = context.new_page()
    page.on("pageerror", lambda err: errors.append(str(err)))

    def wait_pending(items):
        for _ in range(100):
            if items:
                return
            page.wait_for_timeout(20)
        raise AssertionError("no pending request")

    def fulfill_read(ok=True, message=""):
        wait_pending(pending_read)
        route = pending_read.pop(0)
        result = {"config": config, "prompt_defaults": defaults, "keys": {"asr": False, "llm": False, "exa": False}, "version": "0.2.0", "credential_message": message} if ok else {"kind": "RuntimeError", "message": "fixture"}
        route.fulfill(status=200 if ok else 500, content_type="application/json", body=json.dumps(result, ensure_ascii=False))

    def tab(name):
        page.locator("#tab-" + name).click()

    def save_result(ok=True, http_error=False):
        global config
        wait_pending(pending_save)
        route, submitted = pending_save.pop(0)
        if ok and not http_error:
            config = copy.deepcopy(submitted)
        result = {"kind": "RuntimeError", "message": "fixture"} if http_error else {"ok": ok, "message": "已保存" if ok else "fixture rejected"}
        route.fulfill(status=500 if http_error else 200, content_type="application/json", body=json.dumps(result))
        page.wait_for_function('!document.querySelector("#save").disabled')
        return submitted

    page.goto("http://wails.localhost/?view=settings", wait_until="domcontentloaded")
    wait_pending(pending_read)
    for selector in ("#mode", "#key-asr", "#save-key-asr", "#delete-key-asr", "#test-asr", "#refresh-devices", "#save"):
        assert page.locator(selector).is_disabled(), selector
    fulfill_read()
    page.wait_for_function('!document.querySelector("#save").disabled')
    tab("models")
    assert page.locator("#test-asr").is_enabled()
    assert page.locator("#test-llm").is_enabled()
    page.locator("#asr-endpoint").fill("https://other.example/v1/audio/transcriptions")
    assert page.locator("#test-asr").is_disabled()
    assert page.locator("#test-hint-asr").inner_text() == "先保存设置或密钥"
    assert page.locator("#test-llm").is_enabled()
    page.locator("#asr-endpoint").fill(config["asr"]["endpoint"])
    page.locator("#asr-provider").select_option("mimo")
    assert page.locator("#test-asr").is_disabled()
    page.locator("#asr-provider").select_option("openai_compatible")
    assert page.locator("#test-asr").is_enabled()
    page.locator("#output-format").select_option("json_object")
    assert page.locator("#test-llm").is_disabled()
    assert page.locator("#test-asr").is_enabled()
    page.locator("#output-format").select_option("json_schema")
    page.locator("#test-llm").click()
    page.wait_for_function('document.querySelector("#key-message-llm").textContent === "fixture"')
    assert ("TestConnection", ["llm"]) in requests

    tab("prompts")
    assert page.locator("#dictation-prompt").input_value() == defaults["dictation"]
    assert page.locator("#dictation-count").inner_text().split(" / ")[0] == str(len(defaults["dictation"]))
    assert page.locator("img:not(.mark)").count() == 0
    page.locator("#prompt-tab-dialogue").click()
    assert page.locator("#dialogue-prompt").input_value() == "自定义对话"
    before = sum(method == "SaveSettings" for method, _ in requests)
    page.once("dialog", lambda dialog: dialog.dismiss())
    page.locator("#reset-dialogue").click()
    assert page.locator("#dialogue-prompt").input_value() == "自定义对话"
    page.once("dialog", lambda dialog: dialog.accept())
    page.locator("#reset-dialogue").click()
    assert page.locator("#dialogue-prompt").input_value() == defaults["dialogue"]
    assert page.locator("#dictation-prompt").input_value() == defaults["dictation"]
    assert sum(method == "SaveSettings" for method, _ in requests) == before
    tab("input")
    assert page.locator("#profile-custom").input_value() == "独立背景😀"
    page.locator("#profile-custom").fill("独立背景😀\n未保存修改")
    page.locator("#hotkey").fill("Ctrl+Shift+R")
    tab("network")
    page.locator("#max-tool-rounds").fill("0")
    page.locator("#max-calls").fill("8")
    page.locator("#exa-enabled").check()
    assert page.locator("#test-exa").is_enabled()
    page.locator("#search-type").select_option("fast")
    assert page.locator("#test-exa").is_disabled()
    page.locator("#search-type").select_option("instant")
    page.locator("#search-timeout").fill("12")
    assert page.locator("#test-exa").is_disabled()
    page.locator("#search-timeout").fill("10")
    page.locator("#task-timeout").fill("100")
    page.locator("#llm-timeout").fill("50")
    assert page.locator("#test-llm").is_disabled()
    assert page.locator("#test-exa").is_enabled()
    page.locator("#llm-timeout").fill("45")
    tab("models")
    assert page.locator("#test-asr").is_enabled()
    assert page.locator("#test-llm").is_enabled(), "prompts, background and per-turn budgets do not affect protocol probes"
    page.locator("#save").click()
    submitted = save_result()
    assert submitted["llm"]["dictation_prompt"] == "" and submitted["llm"]["dialogue_prompt"] == ""
    assert submitted["llm"]["max_tool_rounds"] == 0 and submitted["exa"]["max_calls"] == 8
    assert submitted["popup"] == {"auto_hide_seconds": 77, "always_on_top": False}
    assert submitted["privacy"] == {"save_history": True, "log_text": True}
    assert submitted["profile"]["custom_background"] == "独立背景😀\n未保存修改"

    # Successful save advances only the submitted snapshot, not newer draft edits.
    page.locator("#llm-model").fill("submitted")
    page.locator("#save").click()
    wait_pending(pending_save)
    assert page.locator("#save").is_disabled()
    assert page.locator("#llm-model").is_enabled()
    page.locator("#llm-model").fill("newer-draft")
    submitted = save_result()
    assert submitted["llm"]["model"] == "submitted"
    assert page.locator("#llm-model").input_value() == "newer-draft"
    assert page.locator("#test-llm").is_disabled()
    page.locator("#llm-model").fill("submitted")
    assert page.locator("#test-llm").is_enabled()

    # Rejected or failed requests leave both the draft and previous baseline intact.
    for http_error in (False, True):
        page.locator("#llm-model").fill("failed-draft")
        page.locator("#save").click()
        save_result(False, http_error=http_error)
        assert page.locator("#llm-model").input_value() == "failed-draft"
        assert page.locator("#test-llm").is_disabled()
        page.locator("#llm-model").fill("submitted")
        assert page.locator("#test-llm").is_enabled()

    # Keys clear before the response, do not echo, and never enter Config.
    page.locator("#key-llm").fill("fixture-secret")
    assert page.locator("#test-llm").is_disabled()
    page.locator("#save-key-llm").click()
    assert page.locator("#key-llm").input_value() == ""
    wait_pending(pending_key)
    route, params = pending_key.pop(0)
    assert params == ["llm", "fixture-secret"]
    assert page.locator("#test-llm").is_disabled()
    page.locator("#key-llm").fill("new-unsaved-key")
    route.fulfill(status=200, content_type="application/json", body=json.dumps({"ok": True, "message": "key fixture"}))
    page.wait_for_function('document.querySelector("#key-status-llm").textContent === "已保存"')
    assert page.locator("#key-llm").input_value() == "new-unsaved-key"
    assert page.locator("#test-llm").is_disabled()
    page.locator("#key-llm").fill("")
    assert page.locator("#test-llm").is_enabled()
    assert "fixture-secret" not in page.locator("body").inner_text()

    tab("prompts")
    page.locator("#prompt-tab-dictation").click()
    page.locator("#dictation-prompt").fill(" \n\t ")
    page.locator("#prompt-tab-dialogue").click()
    page.locator("#dialogue-prompt").fill("真实自定义😀")
    page.locator("#save").click()
    submitted = save_result()
    assert submitted["llm"]["dictation_prompt"] == ""
    assert submitted["llm"]["dialogue_prompt"] == "真实自定义😀"
    assert "fixture-secret" not in json.dumps(submitted)
    assert not page.evaluate("Boolean(window.__pwned)")
    page.reload(wait_until="domcontentloaded")
    fulfill_read()
    page.wait_for_function('!document.querySelector("#save").disabled')
    tab("prompts")
    assert page.locator("#dictation-prompt").input_value() == defaults["dictation"]
    page.locator("#prompt-tab-dialogue").click()
    assert page.locator("#dialogue-prompt").input_value() == "真实自定义😀"

    # Real fixture errors belong to the form scroll, not the navigation or command bar.
    credential_error = "凭据存储不可用：" + "请检查本地凭据权限与服务设置。" * 12
    startup_error = "快捷键注册失败：" + "请检查占用此组合键的应用。" * 12
    page.reload(wait_until="domcontentloaded")
    fulfill_read(message=credential_error)
    page.wait_for_function('!document.querySelector("#save").disabled')
    page.wait_for_function("message => document.querySelector('#startup-error').textContent === message", arg=startup_error)
    assert page.locator("#bridge-error").inner_text() == credential_error
    assert page.locator("#preferences > #bridge-error, #preferences > #startup-error").count() == 2
    for large in (False, True):
        large_text = page.add_style_tag(content="body *{font-size:20px!important}") if large else None
        for width, height in ((760, 800), (520, 500), (360, 800)):
            page.set_viewport_size({"width": width, "height": height})
            for group in ("input", "models", "prompts", "network"):
                tab(group)
                for purpose in (("dictation", "dialogue") if group == "prompts" else (None,)):
                    if purpose:
                        page.locator(f"#prompt-tab-{purpose}").click()
                    page.locator("#preferences").evaluate("node => { node.scrollTop = 0; }")
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth && document.documentElement.scrollHeight <= innerHeight")
                    assert page.locator("#preferences").evaluate("node => node.scrollHeight > node.clientHeight")
                    before = page.locator("#save").bounding_box()
                    assert before and before["y"] >= 0 and before["y"] + before["height"] <= height
                    name = f"settings-errors-{width}x{height}-{'20px' if large else '14px'}-{group}" + (f"-{purpose}" if purpose else "")
                    page.screenshot(path=str(screenshots / f"{name}-top.png"))
                    page.locator("#preferences").evaluate("node => { node.scrollTop = node.scrollHeight; }")
                    assert page.locator("#save").bounding_box() == before
                    assert page.locator("#settings-close").is_visible()
                    page.screenshot(path=str(screenshots / f"{name}-bottom.png"))
        if large_text:
            large_text.evaluate("node => node.remove()")

    page.reload(wait_until="domcontentloaded")
    fulfill_read(False)
    page.wait_for_function('document.querySelector("#bridge-error").textContent.includes("禁用保存")')
    before = sum(method == "SaveSettings" for method, _ in requests)
    for selector in ("#save", "#mode", "#key-asr", "#save-key-asr", "#delete-key-asr", "#test-asr"):
        assert page.locator(selector).is_disabled(), selector
    page.locator("#preferences").evaluate("form => form.requestSubmit()")
    page.wait_for_timeout(50)
    assert sum(method == "SaveSettings" for method, _ in requests) == before
    assert not errors, errors
    browser.close()
print("PASS: fail-closed loading; backend defaults/custom sentinels; independent restore/background; XSS; preserve config; submitted baseline and in-flight drafts; save failure retention; immediate key clearing and service-specific test guards; long fixture errors remain in the form scroll with reachable commands at all viewports and 20px")
