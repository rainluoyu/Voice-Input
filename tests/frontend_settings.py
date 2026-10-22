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
defaults_by_locale = {"zh-CN": defaults, "en": {"dictation": "Default dictation 😀 <img src=x onerror=window.__pwned=true>", "dialogue": "Default dialogue 📦"}}
settings_locale = "zh-CN"
system_locale = "zh-CN"
hold_test = False
pending_test = []
general_defaults = {"start_with_windows": False, "start_as_admin": False, "auto_copy": False, "mute_while_recording": False}
enabled_general = {key: True for key in general_defaults}
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
    context = browser.new_context(viewport={"width": 760, "height": 800}, locale="zh-CN")

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
        if method == "TestConnection" and hold_test:
            pending_test.append(route)
            return
        if method == "StartupError":
            result = startup_error
        elif method == "ListDevices":
            result = {"devices": [{"id": "default", "name": "系统默认麦克风", "name_i18n": {"code": "popup.device.default"}}, {"id": "real-device", "name": "正在录音<img src=x onerror=window.__pwned=true>"}], "message": "已保存", "message_i18n": {"code": "popup.feedback.saved"}}
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

    def fulfill_read(ok=True, message="", identity=None):
        wait_pending(pending_read)
        route = pending_read.pop(0)
        result = {"config": config, "locale": settings_locale, "system_locale": system_locale, "prompt_defaults_by_locale": defaults_by_locale, "prompt_defaults": defaults_by_locale[settings_locale], "keys": {"asr": False, "llm": False, "exa": False}, "version": "1.4.2-rc.1", "credential_message": message, "credential_message_i18n": identity} if ok else {"kind": "RuntimeError", "message": "fixture"}
        route.fulfill(status=200 if ok else 500, content_type="application/json", body=json.dumps(result, ensure_ascii=False))

    def tab(name):
        page.locator("#tab-" + name).click()

    def general_draft():
        return {key: page.locator("#" + key.replace("_", "-")).is_checked() for key in general_defaults}

    def save_result(ok=True, http_error=False, saved=False, identity=None):
        global config
        wait_pending(pending_save)
        route, submitted = pending_save.pop(0)
        if (ok or saved) and not http_error:
            config = copy.deepcopy(submitted)
        result = {"kind": "RuntimeError", "message": "fixture"} if http_error else {"ok": ok, "saved": saved, "message": "已保存" if ok else "fixture rejected", "message_i18n": identity}
        route.fulfill(status=500 if http_error else 200, content_type="application/json", body=json.dumps(result))
        page.wait_for_function('!document.querySelector("#save").disabled')
        return submitted

    page.goto("http://wails.localhost/?view=settings", wait_until="domcontentloaded")
    wait_pending(pending_read)
    for selector in ("#mode", "#start-with-windows", "#start-as-admin", "#auto-copy", "#mute-while-recording", "#auto-hide-seconds", "#key-asr", "#save-key-asr", "#delete-key-asr", "#test-asr", "#refresh-devices", "#save"):
        assert page.locator(selector).is_disabled(), selector
    assert page.locator("#summary").inner_text() == "加载设置…"
    fulfill_read()
    page.wait_for_function('!document.querySelector("#save").disabled')
    assert page.locator("#summary").inner_text() == "v1.4.2-rc.1"
    assert page.locator("#panel-input").is_visible(), "Input remains the default category"
    assert page.locator("#test-asr").is_enabled(), "legacy defaults must not dirty ASR"
    assert page.locator(".settings-tabs [role=tab]").all_text_contents() == ["输入", "通用", "模型服务", "提示词", "联网与预算"]
    tab("general")
    assert "general" not in config, "legacy fixtures exercise the default fallback"
    assert general_draft() == general_defaults
    assert page.locator("#auto-hide-seconds").input_value() == "77"
    assert page.locator("#start-as-admin").is_disabled()
    for field, label in (("start-with-windows", "开机自启"), ("start-as-admin", "以管理员身份自启"), ("auto-copy", "自动复制文本"), ("mute-while-recording", "录音时静音播放"), ("auto-hide-seconds", "自动隐藏等待时间（秒）")):
        assert page.locator(f'label[for="{field}"] span').inner_text() == label
        assert page.locator(f"#{field}").get_attribute("aria-describedby") == field + "-hint"
        assert page.locator(f"#{field}-hint").inner_text()
    before = sum(method == "SaveSettings" for method, _ in requests)
    page.locator("#start-with-windows").check()
    assert page.locator("#start-as-admin").is_enabled()
    assert not page.locator("#start-as-admin").is_checked()
    page.locator("#start-as-admin").check()
    page.locator("#start-with-windows").uncheck()
    assert page.locator("#start-as-admin").is_disabled()
    assert not page.locator("#start-as-admin").is_checked()
    page.locator("#start-with-windows").check()
    assert not page.locator("#start-as-admin").is_checked(), "re-enabling startup must not restore admin"
    page.locator("#start-as-admin").check()
    page.locator("#auto-copy").check()
    page.locator("#mute-while-recording").check()
    assert sum(method == "SaveSettings" for method, _ in requests) == before, "General edits remain local drafts"
    for invalid, validation in (("", "请填写此字段。"), ("0", "请输入 1 到 3600 之间的值。"), ("3601", "请输入 1 到 3600 之间的值。"), ("1.5", "请输入整数。")):
        page.locator("#auto-hide-seconds").fill(invalid)
        assert page.locator("#auto-hide-seconds").evaluate("node => node.validationMessage") == validation
        tab("input")
        page.locator("#save").click()
        assert page.locator("#panel-general").is_visible()
        assert page.locator("#auto-hide-seconds").evaluate("node => node === document.activeElement")
        assert sum(method == "SaveSettings" for method, _ in requests) == before
    # Both inclusive boundaries round-trip, preserving unrelated popup properties.
    for seconds in (1, 3600):
        page.locator("#auto-hide-seconds").fill(str(seconds))
        page.locator("#save").click()
        submitted = save_result()
        assert submitted["general"] == enabled_general
        assert submitted["popup"] == {"auto_hide_seconds": seconds, "always_on_top": False}
        page.reload(wait_until="domcontentloaded")
        fulfill_read()
        page.wait_for_function('!document.querySelector("#save").disabled')
        tab("general")
        assert general_draft() == enabled_general
        assert page.locator("#start-as-admin").is_enabled()
        assert page.locator("#auto-hide-seconds").input_value() == str(seconds)
    page.locator("#auto-hide-seconds").fill("88")
    tab("models")
    assert page.locator("#test-asr").is_enabled()
    assert page.locator("#test-llm").is_enabled(), "General and popup settings do not dirty service probes"
    tab("network")
    assert page.locator("#test-exa").is_enabled()
    tab("models")
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
    assert submitted["popup"] == {"auto_hide_seconds": 88, "always_on_top": False}
    assert submitted["general"] == enabled_general
    assert submitted["privacy"] == {"save_history": True, "log_text": True}
    assert submitted["profile"]["custom_background"] == "独立背景😀\n未保存修改"

    # Successful save advances only the submitted snapshot, not newer draft edits.
    tab("general")
    page.locator("#start-with-windows").uncheck()
    page.locator("#auto-copy").uncheck()
    page.locator("#mute-while-recording").uncheck()
    page.locator("#auto-hide-seconds").fill("100")
    tab("models")
    page.locator("#llm-model").fill("submitted")
    page.locator("#save").click()
    wait_pending(pending_save)
    assert page.locator("#save").is_disabled()
    assert page.locator("#llm-model").is_enabled()
    tab("general")
    for field in ("start-with-windows", "start-as-admin", "auto-copy", "mute-while-recording"):
        assert page.locator("#" + field).is_enabled(), "draft controls remain editable during save"
        page.locator("#" + field).check()
    page.locator("#auto-hide-seconds").fill("101")
    tab("models")
    page.locator("#llm-model").fill("newer-draft")
    submitted = save_result()
    assert submitted["llm"]["model"] == "submitted"
    assert submitted["general"] == general_defaults and submitted["popup"]["auto_hide_seconds"] == 100
    assert general_draft() == enabled_general and page.locator("#auto-hide-seconds").input_value() == "101"
    assert page.locator("#llm-model").input_value() == "newer-draft"
    assert page.locator("#test-llm").is_disabled()
    page.locator("#llm-model").fill("submitted")
    assert page.locator("#test-llm").is_enabled()

    # Rejected or failed requests leave both the draft and previous baseline intact.
    for http_error in (False, True):
        page.locator("#llm-model").fill("failed-draft")
        page.locator("#save").click()
        save_result(False, http_error=http_error)
        assert general_draft() == enabled_general and page.locator("#auto-hide-seconds").input_value() == "101"
        assert config["general"] == general_defaults and config["popup"]["auto_hide_seconds"] == 100
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

    # Language is a local preview, resolved independently of the applied locale.
    config["ui_language"] = "en"
    config["llm"]["dictation_prompt"] = ""
    config["llm"]["dialogue_prompt"] = defaults_by_locale["en"]["dialogue"]
    settings_locale = "en"
    system_locale = "zh-CN"
    page.reload(wait_until="domcontentloaded")
    fulfill_read(message="unsafe credential detail", identity={"code": "popup.feedback.credentials_unavailable"})
    page.wait_for_function('!document.querySelector("#save").disabled')
    assert page.title() == "voice input"
    assert page.locator("html").get_attribute("lang") == "en"
    assert page.locator("#ui-language").input_value() == "en"
    read_count = sum(method == "GetSettings" for method, _ in requests)

    def language(value):
        page.locator("#ui-language").evaluate("(node, value) => { node.value=value; node.dispatchEvent(new Event('change',{bubbles:true})); }", value)

    def translated(locale):
        dictionary = {}
        for name in ("messages", "popup", "settings"):
            dictionary.update(json.loads((Path(__file__).resolve().parents[1] / "internal" / "i18n" / (name + ".json")).read_text())[locale])
        for node in page.locator("[data-i18n]").all():
            assert node.text_content() == dictionary[node.get_attribute("data-i18n")]
        for attribute in ("aria-label", "title", "placeholder"):
            for node in page.locator("[data-i18n-" + attribute + "]").all():
                assert node.get_attribute(attribute) == dictionary[node.get_attribute("data-i18n-" + attribute)]
        assert page.locator("html").get_attribute("lang") == locale
        assert page.locator("#summary").inner_text() == "v1.4.2-rc.1"

    translated("en")
    tab("general")
    for field, label in (("start-with-windows", "Start with Windows"), ("start-as-admin", "Start as administrator"), ("auto-copy", "Automatically copy text"), ("mute-while-recording", "Mute playback while recording"), ("auto-hide-seconds", "Auto-hide delay (seconds)")):
        assert page.locator(f'label[for="{field}"] span').inner_text() == label
    page.locator("#auto-hide-seconds").fill("1.5")
    assert page.locator("#auto-hide-seconds").evaluate("node => node.validationMessage") == "Please enter a whole number."
    assert page.locator("#bridge-error").inner_text() == "Windows credentials are unavailable; check your user session and Credential Manager"
    assert "unsafe credential detail" not in page.locator("body").inner_text()
    assert page.locator("#dictation-prompt").input_value() == defaults_by_locale["en"]["dictation"]
    assert page.locator("#dialogue-prompt").get_attribute("data-prompt-source") == "custom"
    language("auto")
    translated("zh-CN")
    assert page.locator("#auto-hide-seconds").evaluate("node => node.validationMessage") == "请输入整数。"
    page.locator("#auto-hide-seconds").fill("101")
    assert general_draft() == enabled_general
    assert page.locator("#dictation-prompt").input_value() == defaults["dictation"]
    assert page.locator("#dialogue-prompt").input_value() == defaults_by_locale["en"]["dialogue"], "custom equal to another locale's default is still custom"
    page.locator("#save").click()
    submitted = save_result()
    assert submitted["ui_language"] == "auto"
    assert submitted["llm"]["dictation_prompt"] == ""
    assert submitted["llm"]["dialogue_prompt"] == defaults_by_locale["en"]["dialogue"]
    language("en")
    tab("models")
    page.locator("#key-llm").fill("pending-input-secret")
    page.locator("#key-llm").focus()
    page.locator("#key-llm").evaluate("node => { window.__retainedKey=node; node.setSelectionRange(2,6); }")
    page.locator("#preferences").evaluate("node => node.scrollTop=200")
    scroll = page.locator("#preferences").evaluate("node => node.scrollTop")
    language("zh-CN")
    assert page.locator("#key-llm").evaluate("node => node===window.__retainedKey && node===document.activeElement && node.selectionStart===2 && node.selectionEnd===6")
    assert page.locator("#key-llm").input_value() == "pending-input-secret"
    assert page.locator("#preferences").evaluate("node => node.scrollTop") == scroll
    assert page.locator("#panel-models").is_visible()
    page.locator("#key-llm").fill("")
    assert page.locator("#test-llm").is_enabled(), "language alone must not dirty a service"
    tab("prompts")
    page.locator("#prompt-tab-dialogue").click()
    page.locator("#dialogue-prompt").fill("输入法草稿 😀")
    page.locator("#dialogue-prompt").evaluate("node => { window.__ime=node; node.focus(); node.setSelectionRange(1,3); node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true,data:'输'})); }")
    language("en")
    assert page.locator("#dialogue-prompt").evaluate("node => node===window.__ime && node===document.activeElement && node.selectionStart===1 && node.selectionEnd===3")
    assert page.locator("#dialogue-prompt").input_value() == "输入法草稿 😀"
    page.locator("#dialogue-prompt").evaluate("node => node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true,data:'输入'}))")
    def accept_english_restore(dialog):
        assert dialog.message == "Restore the default prompt? Your current edits will be replaced."
        dialog.accept()

    page.once("dialog", accept_english_restore)
    page.locator("#reset-dialogue").click()
    assert page.locator("#dialogue-prompt").get_attribute("data-prompt-source") == "default"
    language("zh-CN")
    assert page.locator("#dialogue-prompt").input_value() == defaults["dialogue"]
    language("en")
    tab("general")
    page.locator("#start-with-windows").uncheck()
    page.locator("#auto-copy").uncheck()
    page.locator("#mute-while-recording").uncheck()
    page.locator("#auto-hide-seconds").fill("202")
    tab("models")
    page.locator("#llm-model").fill("snapshot-en")
    page.locator("#save").click()
    wait_pending(pending_save)
    language("auto")
    tab("general")
    for field in ("start-with-windows", "start-as-admin", "auto-copy", "mute-while-recording"):
        page.locator("#" + field).check()
    page.locator("#auto-hide-seconds").fill("203")
    tab("models")
    page.locator("#llm-model").fill("newer-zh-draft")
    page.locator("#key-llm").fill("newer-unsaved-key")
    submitted = save_result(False, saved=True, identity={"code": "popup.feedback.saved"})
    assert submitted["ui_language"] == "en" and submitted["llm"]["model"] == "snapshot-en"
    assert submitted["general"] == general_defaults and submitted["popup"]["auto_hide_seconds"] == 202
    assert config["general"] == general_defaults and config["popup"]["auto_hide_seconds"] == 202
    assert general_draft() == enabled_general and page.locator("#auto-hide-seconds").input_value() == "203"
    assert submitted["llm"]["dictation_prompt"] == "" and submitted["llm"]["dialogue_prompt"] == ""
    assert page.locator("#ui-language").input_value() == "auto"
    assert page.locator("html").get_attribute("lang") == "zh-CN"
    assert page.locator("#save-message").inner_text() == "已保存"
    assert page.locator("#llm-model").input_value() == "newer-zh-draft"
    assert page.locator("#key-llm").input_value() == "newer-unsaved-key"
    page.locator("#key-llm").fill("")
    page.locator("#llm-model").fill("snapshot-en")
    assert page.locator("#test-llm").is_enabled(), "saved=true/ok=false must advance the submitted service baseline"
    language("en")
    assert page.locator("#save-message").inner_text() == "Saved", "feedback retains message identity, not its old translation"

    # Save/test/key requests may finish after another language and newer draft edits.
    for http_error in (False, True):
        page.locator("#llm-model").fill("failed-en-snapshot")
        page.locator("#save").click()
        wait_pending(pending_save)
        language("zh-CN")
        page.locator("#llm-model").fill("preserved-new-draft")
        save_result(False, http_error=http_error)
        assert page.locator("#llm-model").input_value() == "preserved-new-draft"
        assert page.locator("#ui-language").input_value() == "zh-CN"
        page.locator("#llm-model").fill("snapshot-en")
        assert page.locator("#test-llm").is_enabled()
        language("en")
    hold_test = True
    page.locator("#test-llm").click()
    wait_pending(pending_test)
    language("zh-CN")
    assert page.locator("#test-llm").is_disabled()
    assert page.locator("#key-message-llm").inner_text() == "测试中……"
    page.locator("#llm-model").fill("edited-during-test")
    pending_test.pop(0).fulfill(status=200, content_type="application/json", body=json.dumps({"ok": False, "message": "raw unsafe provider error", "message_i18n": {"code": "popup.feedback.test_failed"}}))
    page.wait_for_function('!document.querySelector("#save-key-llm").disabled')
    assert page.locator("#test-llm").is_disabled()
    assert page.locator("#llm-model").input_value() == "edited-during-test"
    assert "raw unsafe provider error" not in page.locator("body").inner_text()
    language("en")
    assert page.locator("#key-message-llm").inner_text() == "Connection test failed; check the address, model, key and proxy"
    hold_test = False
    page.locator("#llm-model").fill("snapshot-en")
    page.locator("#key-llm").fill("failed-key")
    page.locator("#save-key-llm").click()
    wait_pending(pending_key)
    assert page.locator("#key-llm").input_value() == ""
    language("zh-CN")
    page.locator("#key-llm").fill("new-secret-must-survive")
    pending_key.pop(0)[0].fulfill(status=200, content_type="application/json", body=json.dumps({"ok": False, "message": "unsafe provider secret", "message_i18n": {"code": "not.registered"}}))
    page.wait_for_function('!document.querySelector("#save-key-llm").disabled')
    assert page.locator("#key-llm").input_value() == "new-secret-must-survive"
    assert page.locator("#key-message-llm").inner_text() == "操作失败，请重试"
    assert page.locator("#test-llm").is_disabled()
    language("en")
    assert page.locator("#key-message-llm").inner_text() == "Operation failed. Please try again."
    assert "unsafe provider secret" not in page.locator("body").inner_text()
    tab("input")
    page.locator("#refresh-devices").click()
    page.wait_for_function('document.querySelector("#device").options.length===2')
    assert page.locator('#device option[value="default"]').inner_text() == "System default microphone"
    assert page.locator('#device option[value="real-device"]').inner_text() == "正在录音<img src=x onerror=window.__pwned=true>"
    page.locator("#device").evaluate("node => window.__deviceOption=node.options[0]")
    language("zh-CN")
    assert page.locator('#device option[value="default"]').inner_text() == "系统默认麦克风"
    assert page.locator("#device").evaluate("node => node.options[0]===window.__deviceOption")
    assert page.locator("#device-message").inner_text() == "已保存"
    assert sum(method == "GetSettings" for method, _ in requests) == read_count
    assert not page.evaluate("Boolean(window.__pwned)")

    # Typed startup feedback is translated, while named arguments remain literal text.
    startup_error = {"message": "do not expose this", "message_i18n": {"code": "popup.feedback.shortcut_failed", "args": {"shortcuts": "Ctrl+X<img src=x>"}}}
    page.wait_for_function('document.querySelector("#startup-error").textContent.includes("Ctrl+X<img src=x>")')
    language("en")
    assert page.locator("#startup-error").inner_text() == "Could not register shortcuts: Ctrl+X<img src=x>"
    assert page.locator("#startup-error img").count() == 0
    translated("en")
    config["ui_language"] = "zh-CN"
    settings_locale = "zh-CN"

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
            for group in ("input", "general", "models", "prompts", "network"):
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

    # Old configs may omit the new group and delay; defaults must not replace popup siblings.
    config.pop("general")
    config["popup"].pop("auto_hide_seconds")
    page.reload(wait_until="domcontentloaded")
    fulfill_read()
    page.wait_for_function('!document.querySelector("#save").disabled')
    tab("general")
    assert general_draft() == general_defaults
    assert page.locator("#start-as-admin").is_disabled()
    assert page.locator("#auto-hide-seconds").input_value() == "30"
    page.locator("#save").click()
    submitted = save_result()
    assert submitted["general"] == general_defaults
    assert submitted["popup"] == {"auto_hide_seconds": 30, "always_on_top": False}

    page.reload(wait_until="domcontentloaded")
    fulfill_read(False)
    page.wait_for_function('document.querySelector("#bridge-error").textContent.includes("禁用保存")')
    before = sum(method == "SaveSettings" for method, _ in requests)
    for selector in ("#save", "#mode", "#start-with-windows", "#start-as-admin", "#auto-copy", "#mute-while-recording", "#auto-hide-seconds", "#key-asr", "#save-key-asr", "#delete-key-asr", "#test-asr"):
        assert page.locator(selector).is_disabled(), selector
    page.locator("#preferences").evaluate("form => form.requestSubmit()")
    page.wait_for_timeout(50)
    assert sum(method == "SaveSettings" for method, _ in requests) == before
    assert not errors, errors
    browser.close()
print("PASS: General defaults/round-trip, startup/admin dependency, 1..3600 integer validation and bilingual labels/hints; popup siblings preserved; bilingual settings/feedback and system-resolved auto; explicit default/custom prompt sources; DOM/focus/selection/IME/scroll/key retention; pending save/test/key and failures preserve drafts; saved=true/ok=false snapshot baseline; dirty guards, typed/legacy messages, devices/XSS and existing viewport/validation regressions")
