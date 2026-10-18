"""Wails HTTP contract fixture. Never invokes Win32, microphones or real APIs."""
import copy
import json
import os
import time
from urllib.parse import urlsplit
from playwright.sync_api import sync_playwright

base = f"http://127.0.0.1:{os.environ.get('UI_TEST_PORT', '5173')}"
config = {
    "version": 1, "mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A",
    "recording": {"device_id": "default", "max_seconds": 120},
    "asr": {"endpoint": "https://asr.example/v1/audio/transcriptions", "model": "legacy-asr-model", "language": "", "timeout_seconds": 45},
    "llm": {"base_url": "", "model": "", "timeout_seconds": 45, "task_timeout_seconds": 90, "output_format": "json_schema", "dictation_prompt": "", "dialogue_prompt": "", "max_tool_rounds": 2},
    "exa": {"enabled": False, "search_type": "instant", "max_calls": 2, "timeout_seconds": 10},
    "profile": {"custom_background": ""},
    "popup": {"auto_hide_seconds": 30, "always_on_top": True},
    "privacy": {"save_history": False, "log_text": False},
}
state = {"task_id": "fixture", "intent": "dictation", "turn": 1, "question": "", "replying": False, "revision": 1, "stage": "idle", "visible": False,
         "raw_text": "", "text": "", "notice": "", "pinned": False,
         "hide_at": 0, "mode": "live", "hotkey": "Ctrl+Alt+R", "level": 0, "sources": []}
keys = {"asr": False, "llm": False, "exa": False}
requests = []
fail_settings_read = False
hold_copy = False
pending_copy = []

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    context = browser.new_context(viewport={"width": 760, "height": 800})
    errors = []

    def route_request(route):
        global config
        parsed = urlsplit(route.request.url)
        assert parsed.netloc == "wails.localhost", "unexpected external request in offline fixture"
        path = parsed.path
        if path == "/wails/runtime":
            payload = route.request.post_data_json
            args = payload.get("args", {})
            method = args.get("methodName", "").rsplit(".", 1)[-1]
            params = args.get("args", [])
            requests.append((method, params))
            if method == "GetSettings":
                if fail_settings_read:
                    route.fulfill(status=500, content_type="application/json", body=json.dumps({"kind": "RuntimeError", "message": "fixture failure"}))
                    return
                result = {"config": config, "keys": keys, "version": "1.4.2-rc.1", "prompt_defaults": {"dictation": "修正语病、成语和冗余表达，保留原意。", "dialogue": "理解请求，生成用户需要的正文。"}}
            elif method == "GetPresentation":
                result = {"mode": config["mode"], "hotkey": config["hotkey"], "ai_hotkey": config["ai_hotkey"]}
            elif method == "Snapshot":
                result = state
            elif method == "StartupError":
                result = ""
            elif method == "SaveSettings":
                assert "test-secret" not in json.dumps(params[0])
                assert params[0]["privacy"] == {"save_history": False, "log_text": False}
                config = copy.deepcopy(params[0])
                result = {"ok": True, "message": "已保存"}
            elif method == "SaveKey":
                keys[params[0]] = True
                result = {"ok": True, "message": "密钥已保存（fixture）"}
            elif method == "DeleteKey":
                keys[params[0]] = False
                result = {"ok": True, "message": "密钥已删除（fixture）"}
            elif method == "TestConnection":
                result = {"ok": False, "message": "连接测试 fixture，不访问真实服务"}
            elif method == "ListDevices":
                result = {"devices": [{"id": "default", "name": "系统默认麦克风"}, {"id": "device-two", "name": "测试麦克风"}], "message": "fixture"}
            elif method == "ReplyToClarification":
                assert len(params) == 3 and params[0] == "fixture"
                assert isinstance(params[1], int) and isinstance(params[2], bool)
                result = None
            elif method == "CopyResult":
                if hold_copy:
                    pending_copy.append(route)
                    return
                result = None
            elif method in ("CloseSettings", "ClosePopup", "ShowSettings", "KeepPopupOpen"):
                result = None
            else:
                raise AssertionError("unexpected Wails fixture method: " + method)
            route.fulfill(status=200, content_type="application/json", body=json.dumps(result, ensure_ascii=False))
            return
        suffix = route.request.url.removeprefix("http://wails.localhost")
        response = context.request.get(base + suffix)
        route.fulfill(response=response)

    context.route("**/*", route_request)
    page = context.new_page()
    page.on("pageerror", lambda err: errors.append(str(err)))
    page.goto("http://wails.localhost/?view=settings")
    page.wait_for_load_state("networkidle")
    assert page.locator("#save").is_enabled()
    assert page.locator("#summary").inner_text() == "v1.4.2-rc.1"
    page.locator("#tab-models").click()
    assert page.title() == "真实语义输入"
    assert page.locator("h1").inner_text() == "真实语义输入"
    assert page.locator("#mode option[value=mock], #forced-note, #asr-protocol-hint, aside, .instruction").count() == 0
    assert page.locator("#asr-endpoint").get_attribute("aria-describedby") is None
    # Legacy fixtures omit provider; display multipart without replacing saved fields.
    assert "provider" not in config["asr"]
    assert page.locator("#asr-provider").input_value() == "openai_compatible"
    assert page.locator("#asr-endpoint").input_value() == config["asr"]["endpoint"]
    assert page.locator("#asr-model").input_value() == "legacy-asr-model"
    assert page.locator("#asr-language").get_attribute("pattern") is None
    assert page.locator("#hotkey").input_value() == "Ctrl+Alt+R"
    assert page.locator("#ai-hotkey").input_value() == "Ctrl+Alt+A"
    page.locator("#tab-input").click()
    page.locator("#hotkey").fill("Ctrl+Shift+R")
    page.locator("#ai-hotkey").fill("Ctrl+Shift+A")
    page.locator("#mode").select_option("live")
    page.locator("#tab-models").click()
    page.locator("#asr-endpoint").fill("https://asr.example/v1/audio/transcriptions")
    page.locator("#asr-model").fill("asr-model")
    page.locator("#llm-base").fill("https://llm.example/v1")
    page.locator("#llm-model").fill("llm-model")
    background = "项目名称：VoicePad\n专业术语：语音转录\n偏好：简洁表达"
    assert page.locator("#profile-name, #profile-nickname, #profile-city, #profile-zone").count() == 0
    page.locator("#tab-input").click()
    assert page.locator("#profile-custom").count() == 1
    page.locator("#profile-custom").fill(background)
    page.locator("#tab-models").click()
    page.locator("#key-asr").fill("test-secret-not-a-real-key")
    page.locator("#save-key-asr").click()
    page.wait_for_function("document.querySelector('#key-status-asr').textContent.includes('已保存')")
    assert page.locator("#key-asr").input_value() == ""
    assert "test-secret" not in page.locator("body").inner_text()
    page.locator("#tab-input").click()
    page.locator("#refresh-devices").click()
    page.wait_for_function("document.querySelector('#device').options.length === 2")
    page.locator("#device").select_option("device-two")
    page.locator("#save").click()
    page.wait_for_function("document.querySelector('#save-message').textContent.includes('已保存')")
    assert config["mode"] == "live" and config["recording"]["device_id"] == "device-two"
    assert config["profile"] == {"custom_background": background}
    assert config["hotkey"] == "Ctrl+Shift+R" and config["ai_hotkey"] == "Ctrl+Shift+A"
    assert config["popup"]["always_on_top"] is True
    assert config["asr"]["provider"] == "openai_compatible"
    assert config["llm"]["max_tool_rounds"] == 2
    assert config["llm"]["dictation_prompt"] == "" and config["llm"]["dialogue_prompt"] == ""

    # An explicitly empty provider has the same legacy fallback after loading.
    config["asr"]["provider"] = ""
    page.reload()
    page.wait_for_load_state("networkidle")
    page.locator("#tab-models").click()
    assert page.locator("#asr-provider").input_value() == "openai_compatible"
    assert page.locator("#asr-endpoint").input_value() == "https://asr.example/v1/audio/transcriptions"
    assert page.locator("#asr-model").input_value() == "asr-model"
    page.locator("#asr-language").fill("ja")
    page.locator("#asr-provider").select_option("mimo")
    # Protocol selection must not silently migrate existing endpoint/model/language.
    assert page.locator("#asr-endpoint").input_value() == "https://asr.example/v1/audio/transcriptions"
    assert page.locator("#asr-model").input_value() == "asr-model"
    assert page.locator("#asr-language").input_value() == "ja"
    assert not page.locator("#asr-language").evaluate("input => input.checkValidity()")
    saves_before = sum(method == "SaveSettings" for method, _ in requests)
    page.locator("#save").click()
    assert sum(method == "SaveSettings" for method, _ in requests) == saves_before
    assert config["asr"]["provider"] == ""
    assert page.locator("#asr-endpoint").get_attribute("placeholder") == "https://api.xiaomimimo.com/v1/chat/completions"
    assert page.locator("#asr-model").get_attribute("placeholder") == "mimo-v2.5-asr"
    for language in ("", "auto", "zh", "en"):
        page.locator("#asr-language").fill(language)
        assert page.locator("#asr-language").evaluate("input => input.checkValidity()")
    page.locator("#asr-endpoint").fill("https://api.xiaomimimo.com/v1/chat/completions")
    page.locator("#asr-model").fill("mimo-v2.5-asr")
    page.locator("#asr-provider").select_option("openai_compatible")
    assert page.locator("#asr-language").get_attribute("pattern") is None
    assert page.locator("#asr-endpoint").input_value() == "https://api.xiaomimimo.com/v1/chat/completions"
    assert page.locator("#asr-model").input_value() == "mimo-v2.5-asr"
    assert page.locator("#asr-language").input_value() == "en"
    page.locator("#asr-provider").select_option("mimo")
    page.locator("#save").click()
    page.wait_for_function("document.querySelector('#save-message').textContent.includes('已保存')")
    assert config["asr"] == {
        "provider": "mimo", "endpoint": "https://api.xiaomimimo.com/v1/chat/completions",
        "model": "mimo-v2.5-asr", "language": "en", "timeout_seconds": 45,
    }
    page.reload()
    page.wait_for_load_state("networkidle")
    page.locator("#tab-models").click()
    assert page.locator("#hotkey").input_value() == "Ctrl+Shift+R"
    assert page.locator("#ai-hotkey").input_value() == "Ctrl+Shift+A"
    assert page.locator("#asr-provider").input_value() == "mimo"
    assert page.locator("#asr-endpoint").input_value() == config["asr"]["endpoint"]
    assert page.locator("#asr-model").input_value() == config["asr"]["model"]
    assert page.locator("#asr-language").input_value() == "en"
    page.locator("#test-asr").click()
    page.wait_for_function("document.querySelector('#key-message-asr').textContent.includes('fixture')")
    assert page.locator("#metrics, #metrics-refresh, #always-on-top").count() == 0
    assert not any(method == "GetMetrics" for method, _ in requests)
    page.locator("#delete-key-asr").click()
    page.wait_for_function("document.querySelector('#key-status-asr').textContent === '未配置'")

    page.goto("http://wails.localhost/")
    page.wait_for_load_state("networkidle")
    assert "语音润色" in page.locator("#mode-badge").inner_text()
    assert "Ctrl+Shift+R" in page.locator("#countdown").inner_text()
    assert "Ctrl+Shift+A" in page.locator("#countdown").inner_text()
    def presentation(view, height):
        page.wait_for_function("""expected => {
          const panel=document.querySelector('.popup');
          return panel.dataset.view===expected.view &&
            Math.abs(panel.getBoundingClientRect().height-Math.min(expected.height,innerHeight))<=1;
        }""", arg={"view": view, "height": height})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth && document.documentElement.scrollHeight <= innerHeight")
        for selector in ("#settings", "#close", "#copy", "#reply"):
            if page.locator(selector).is_visible():
                box = page.locator(selector).bounding_box()
                assert box and box["y"] >= 0 and box["y"] + box["height"] <= page.viewport_size["height"], selector

    presentation("working", 240)
    assert page.locator("#raw-section, #result-section, #copy").evaluate_all("nodes => nodes.every(node => node.hidden)")
    malicious = "<img src=x onerror=window.__pwned=true>原文"
    update = {**state, "intent": "dialogue", "hotkey": "Ctrl+Shift+A", "revision": 2, "mode": "live", "stage": "clarify", "visible": True, "raw_text": malicious,
              "question": "你要查询哪里的天气？", "notice": "",
              "sources": [{"title": "<script>来源</script>", "url": "javascript:alert(1)", "highlights": []}], "hide_at": 0}

    def dispatch(changes):
        update.update(changes)
        page.evaluate("data => window._wails.dispatchWailsEvent({name:'task:state',data})", update)

    dispatch({})
    presentation("question", 400)
    assert page.locator("#raw").inner_text() == malicious
    assert page.locator("#raw img").count() == 0
    assert not page.evaluate("Boolean(window.__pwned)")
    assert page.locator("#source-list a").count() == 0
    assert page.locator("#copy").is_enabled()
    assert page.locator("#question-section").is_visible()
    assert "AI 对话" in page.locator("#mode-badge").inner_text()
    assert "Ctrl+Shift+A" in page.locator("#countdown").inner_text()
    assert page.locator("#question").inner_text() == "你要查询哪里的天气？"
    assert page.locator("input, textarea, [contenteditable=true]").count() == 0
    assert "开始补充" in page.locator("#countdown").inner_text()
    page.locator("#copy").click()
    page.wait_for_timeout(100)
    assert any(method == "CopyResult" and params == ["fixture"] for method, params in requests)
    assert page.locator("#copy-message").inner_text() == "已复制"
    assert page.locator("#notice").is_hidden()
    assert any(method == "KeepPopupOpen" and params == ["fixture"] for method, params in requests)
    assert page.locator("#duration").count() == 0
    dispatch({"revision": 3, "pinned": True})
    assert "开始补充" in page.locator("#countdown").inner_text()
    page.evaluate("data => window._wails.dispatchWailsEvent({name:'task:state',data})", state)
    assert page.locator("#stage").inner_text() == "需要补充信息"
    page.locator("#reply").click()
    page.wait_for_timeout(100)
    assert ("ReplyToClarification", ["fixture", 1, False]) in requests
    dispatch({"revision": 4, "turn": 2, "stage": "recording", "replying": True, "level": .1})
    presentation("question", 400)
    assert page.locator("#popup-footer #reply").count() == 1
    assert page.locator("#level").is_visible()
    assert page.locator("#stage").inner_text() == "正在录制补充"
    assert page.locator("#question").inner_text() == "你要查询哪里的天气？"
    assert page.locator("#reply").inner_text() == "结束补充"
    assert "结束补充" in page.locator("#countdown").inner_text()
    page.locator("#reply").click()
    page.wait_for_timeout(100)
    assert ("ReplyToClarification", ["fixture", 2, True]) in requests
    transcript = "原始语音：帮我查天气\n\n问题 1：你要查询哪里的天气？\n补充 1：上海"
    dispatch({"revision": 5, "stage": "asr"})
    assert page.locator("#stage").inner_text() == "识别补充语音"
    assert page.locator("#reply").is_disabled()
    dispatch({"revision": 6, "stage": "llm", "raw_text": transcript})
    presentation("question", 400)
    assert page.locator("#reply").is_disabled()
    assert page.locator("#raw-label").inner_text() == "语音记录（含补充）"
    assert page.locator("#raw").inner_text() == transcript
    dispatch({"revision": 7, "stage": "clarify", "replying": False, "question": "今天还是明天？"})
    assert page.locator("#question").inner_text() == "今天还是明天？"
    assert page.locator("#reply").is_enabled()
    assert page.locator("#reply").inner_text() == "语音补充"
    page.set_viewport_size({"width": 400, "height": 560})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.screenshot(path="/tmp/voice_input-clarification.png")
    page.locator("#reply").click()
    page.wait_for_timeout(100)
    assert ("ReplyToClarification", ["fixture", 2, False]) in requests
    dispatch({"revision": 8, "turn": 3, "stage": "recording", "replying": True, "mode": "live"})
    assert page.locator("#stage").inner_text() == "正在录制补充"
    assert page.locator("#level").is_visible()
    page.locator("#reply").click()
    page.wait_for_timeout(100)
    assert ("ReplyToClarification", ["fixture", 3, True]) in requests
    dispatch({"revision": 9, "stage": "done", "mode": "live", "replying": False, "question": "", "notice": "", "text": "您好：\n\n  结合原始请求与补充生成的固定测试正文。📦"})
    assert page.locator("#question-section").is_hidden()
    presentation("result", 560)
    assert page.locator("#result").inner_text() == update["text"]
    assert page.locator("#result").evaluate("node => getComputedStyle(node).whiteSpace") == "pre-wrap"
    page.screenshot(path="/tmp/voice_input-multiline.png")
    assert page.locator("#countdown").inner_text() == "保持显示"
    assert page.locator("#notice").is_hidden()
    assert page.locator("#stage").inner_text() == "已输入"
    assert page.locator("#target").count() == 0
    dispatch({"revision": 10, "pinned": False, "hide_at": int(time.time()*1000)+30000})
    assert "秒后隐藏" in page.locator("#countdown").inner_text()
    dispatch({"revision": 11, "intent": "dictation", "hotkey": "Ctrl+Shift+R", "turn": 1, "stage": "llm", "raw_text": "帮我写一封请假邮件", "text": "", "hide_at": 0})
    assert "语音润色" in page.locator("#mode-badge").inner_text()
    assert page.locator("#stage").inner_text() == "正在润色文本"
    assert page.locator("#question-section").is_hidden()
    dispatch({"revision": 12, "stage": "done", "text": "帮我写一封请假邮件。"})
    assert page.locator("#result").inner_text() == "帮我写一封请假邮件。"
    assert page.locator("#reply").is_disabled()
    page.screenshot(path="/tmp/voice_input-dictation.png")
    # Follow the final text once, not the source list or repeated status events.
    long_raw = "\n".join(f"原始语音第 {i} 行，保留用于向上回看。" for i in range(160))
    long_result = "\n".join(f"整理后的正文第 {i} 行，保留段落与条件。" for i in range(70))
    sources = [{"title": "正文下方较长的来源说明。" * 12, "url": f"https://example.com/{i}", "highlights": []} for i in range(10)]
    dispatch({"revision": 13, "task_id": "follow-long", "turn": 2, "intent": "dialogue", "stage": "injecting", "question": "今天还是明天？", "raw_text": long_raw, "text": long_result, "sources": sources})
    page.locator("#sources").evaluate("node => node.open = true")
    assert page.locator("#popup-body").evaluate("node => node.scrollTop") == 0
    focused = page.evaluate("document.activeElement.id")
    dispatch({"revision": 14, "stage": "done", "question": "", "replying": False})

    def assert_result_visible():
        page.wait_for_function("""() => {
          const body=document.querySelector('#popup-body'), result=document.querySelector('#result');
          const viewport=body.getBoundingClientRect(), end=result.getBoundingClientRect().bottom;
          return body.scrollTop>0 && end>viewport.top && end<=viewport.bottom+1;
        }""")

    assert_result_visible()
    assert page.evaluate("document.activeElement.id") == focused
    assert page.locator("#popup-body").evaluate("node => node.scrollTop < node.scrollHeight-node.clientHeight-40")
    assert page.locator("#copy").bounding_box()["y"] < 560
    page.screenshot(path="/tmp/voice_input-result-follow.png")
    page.locator("#popup-body").evaluate("node => node.scrollTop = 0")
    dispatch({"revision": 15, "pinned": True})
    page.wait_for_timeout(350)
    assert page.locator("#popup-body").evaluate("node => node.scrollTop") == 0

    # A short, copyable result on input failure follows a long ASR just as Done does.
    dispatch({"revision": 16, "task_id": "follow-error", "turn": 1, "stage": "error", "text": "可手动复制的最终正文。", "notice": "自动输入失败"})
    assert_result_visible()
    assert page.locator("#copy").is_enabled()
    page.screenshot(path="/tmp/voice_input-result-error.png")

    # A late RAF for an old task cannot move the next recording's viewport.
    old = {**update, "revision": 17, "task_id": "late-result", "stage": "done", "text": long_result}
    new = {**state, "revision": 18, "task_id": "new-recording", "stage": "recording", "visible": True}
    page.evaluate("""states => { for(const data of states)
      window._wails.dispatchWailsEvent({name:'task:state',data}); }""", [old, new])
    update = new
    page.wait_for_timeout(100)
    assert page.locator("#popup-body").evaluate("node => node.scrollTop") == 0
    assert page.locator("#level").is_visible()
    assert page.locator("#copy").is_disabled()

    # Scrolling before a delayed follow cancels it; identical revisions do not retry.
    manual = {**update, "revision": 19, "task_id": "manual-before-frame", "stage": "done", "raw_text": long_raw, "text": long_result}
    page.evaluate("""data => {
      window._wails.dispatchWailsEvent({name:'task:state',data});
      const body=document.querySelector('#popup-body'); body.scrollTop=0;
      body.dispatchEvent(new WheelEvent('wheel',{deltaY:-100,bubbles:true}));
    }""", manual)
    update = manual
    page.wait_for_timeout(100)
    dispatch({"revision": 20, "pinned": True})
    page.wait_for_timeout(350)
    assert page.locator("#popup-body").evaluate("node => node.scrollTop") == 0

    # The first terminal Snapshot after opening also follows the final body.
    state.update({**update, "revision": 21, "task_id": "terminal-snapshot", "text": "初次读取已有结果。"})
    page.reload()
    page.wait_for_load_state("networkidle")
    assert_result_visible()

    # Compact, clarification and reading surfaces are driven by real state data.
    dispatch({"revision": 22, "task_id": "compact-contract", "turn": 1, "intent": "dictation", "hotkey": "Ctrl+Shift+R", "stage": "recording", "replying": False, "question": "", "raw_text": "", "text": "", "notice": "", "sources": [], "pinned": False, "hide_at": 0, "level": .1})
    presentation("working", 240)
    assert page.locator("#raw-section").is_hidden() and page.locator("#result-section").is_hidden()
    assert page.locator("#copy").is_hidden() and page.locator("#reply").is_hidden()
    assert page.locator("#waveform").is_visible()
    assert page.locator("#level").evaluate("node => Math.abs(node.value-.8)<.001")
    heights = page.locator(".wave-bars span").evaluate_all("nodes => nodes.map(node => parseInt(node.style.height,10))")
    assert len(heights) == 9 and min(heights) >= 2 and max(heights) == 20
    page.screenshot(path="/tmp/voice_input-design-recording.png")
    page.locator(".popup").screenshot(path="/tmp/voice_input-design-recording-panel.png")
    dispatch({"revision": 23, "level": 0})
    assert page.locator(".wave-bars span").evaluate_all("nodes => nodes.every(node => node.style.height==='2px')")
    dispatch({"revision": 24, "stage": "asr"})
    presentation("working", 240)
    assert page.locator("#waveform").is_hidden()
    dispatch({"revision": 25, "stage": "llm", "raw_text": "请把这个设置保存下来。"})
    presentation("working", 240)
    assert page.locator("#raw-section").is_visible() and page.locator("#result-section").is_hidden()
    assert page.locator("#copy").is_visible(), "processing with ASR still permits copying"
    page.screenshot(path="/tmp/voice_input-design-processing.png")
    dispatch({"revision": 26, "intent": "dialogue", "stage": "search"})
    presentation("working", 240)
    assert page.locator("#stage").inner_text() == "查证事实"
    page.screenshot(path="/tmp/voice_input-design-search.png")

    # Question takes precedence over compact mode throughout a supplement turn.
    dispatch({"revision": 27, "question": "你希望保留原来的语气，还是改成正式表达？", "stage": "clarify"})
    presentation("question", 400)
    page.screenshot(path="/tmp/voice_input-design-question.png")
    dispatch({"revision": 28, "stage": "recording", "replying": True, "level": .06})
    presentation("question", 400)
    assert page.locator("#question-section").is_visible()
    assert page.locator("#popup-footer #reply").is_enabled()
    page.screenshot(path="/tmp/voice_input-design-supplement.png")
    dispatch({"revision": 29, "stage": "llm"})
    presentation("question", 400)
    assert page.locator("#reply").is_disabled()

    # Error retains both the copyable text and its error after copy feedback.
    dispatch({"revision": 30, "task_id": "copy-error-contract", "question": "", "stage": "error", "replying": False, "raw_text": long_raw, "text": "自动输入失败后仍然可复制的正文。", "notice": "自动输入失败，请手动复制。", "pinned": True})
    presentation("result", 560)
    assert_result_visible()
    page.locator("#copy").click()
    page.wait_for_function("document.querySelector('#copy-message').textContent==='已复制'")
    assert page.locator("#notice").inner_text() == "自动输入失败，请手动复制。"
    page.screenshot(path="/tmp/voice_input-design-copy-error.png")

    # A response to a previous task's Copy cannot add feedback to a new task.
    hold_copy = True
    page.locator("#copy").click()
    for _ in range(100):
        if pending_copy:
            break
        page.wait_for_timeout(20)
    assert pending_copy
    dispatch({"revision": 31, "task_id": "after-delayed-copy", "turn": 1, "stage": "recording", "question": "", "replying": False, "raw_text": "", "text": "", "notice": "", "sources": [], "level": 0})
    pending_copy.pop(0).fulfill(status=200, content_type="application/json", body="null")
    hold_copy = False
    page.wait_for_timeout(100)
    presentation("working", 240)
    assert page.locator("#copy-message").is_hidden() and page.locator("#notice").is_hidden()

    dispatch({"revision": 32, "task_id": "only-raw-contract", "stage": "error", "raw_text": long_raw, "text": "", "notice": "语义处理失败，原文仍可复制。"})
    presentation("result", 560)
    assert page.locator("#raw-section").is_visible() and page.locator("#result-section").is_hidden()
    assert page.locator("#copy").is_visible()
    page.screenshot(path="/tmp/voice_input-design-raw-error.png")

    # Sources are independent of the result section, including partial snapshots.
    dispatch({"revision": 33, "task_id": "source-only-contract", "stage": "done", "raw_text": "", "text": "", "notice": "", "sources": sources[:1]})
    presentation("working", 240)
    assert page.locator("#result-section").is_hidden() and page.locator("#copy").is_hidden()
    assert page.locator("#sources").is_visible()
    assert page.locator("#source-list li").count() == 1

    code = 'def greeting():\n    return "你好，世界 📦"\n\nprint(greeting())'
    dispatch({"revision": 34, "task_id": "code-contract", "stage": "done", "raw_text": "帮我写一个问候示例。", "text": code, "sources": []})
    presentation("result", 560)
    assert page.locator("#result").inner_text() == code
    assert page.locator("#result").evaluate("node => getComputedStyle(node).whiteSpace") == "pre-wrap"
    for selector in ("#raw", "#result", "#sources"):
        assert page.locator(selector).evaluate("node => getComputedStyle(node).maxHeight") == "none"
        assert page.locator(selector).evaluate("node => getComputedStyle(node).overflowY") != "auto"
    page.screenshot(path="/tmp/voice_input-design-code.png")
    page.set_viewport_size({"width": 360, "height": 400})
    presentation("result", 560)
    page.screenshot(path="/tmp/voice_input-design-narrow-result.png")

    # Long errors remain fully scrollable even in the compact surface with larger text.
    page.set_viewport_size({"width": 400, "height": 240})
    long_notice = "\n".join(f"错误说明 {i}：请检查服务配置，本次没有自动重试。" for i in range(12))
    dispatch({"revision": 35, "task_id": "empty-error-contract", "stage": "error", "question": "", "raw_text": "", "text": "", "notice": long_notice, "sources": []})
    presentation("working", 240)
    assert page.locator("#copy").is_hidden() and page.locator("#raw-section").is_hidden()
    large_text = page.add_style_tag(content=".content,.notice,#countdown{font-size:20px!important}")
    presentation("working", 240)
    assert page.locator("#notice").inner_text() == long_notice
    assert page.locator("#popup-body").evaluate("node => node.clientHeight>0 && node.scrollHeight>node.clientHeight")
    page.locator("#popup-body").evaluate("node => node.scrollTop=node.scrollHeight")
    assert page.evaluate("document.querySelector('#notice').getBoundingClientRect().bottom <= document.querySelector('#popup-body').getBoundingClientRect().bottom+1")
    page.screenshot(path="/tmp/voice_input-design-long-error.png")
    large_text.evaluate("node => node.remove()")
    page.emulate_media(reduced_motion="reduce")
    dispatch({"revision": 36, "task_id": "reduced-motion-contract", "stage": "llm", "notice": ""})
    presentation("working", 240)
    assert page.locator("#indicator").evaluate("node => getComputedStyle(node).animationName") == "none"
    page.emulate_media(reduced_motion="no-preference")
    page.locator("#close").click()
    page.wait_for_timeout(100)
    assert any(method == "ClosePopup" for method, _ in requests)

    fail_settings_read = True
    page.goto("http://wails.localhost/?view=settings")
    page.wait_for_load_state("networkidle")
    assert page.locator("#save").is_disabled()
    assert "禁用保存" in page.locator("#bridge-error").inner_text()
    assert not errors, errors
    browser.close()
print("PASS: B+A+D state/size matrix, real-level bars, footer actions, copy feedback isolation, long compact errors, reduced motion; dual shortcuts/settings/keys/devices, AI replies, pure multiline/code rendering, revision/RAF/result-follow guards and XSS")
