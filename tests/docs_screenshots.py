"""Render reviewed documentation images using fictional Wails responses only."""
import argparse
import base64
import copy
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--out", type=Path, default=Path(__file__).resolve().parents[1] / "docs" / "images")
args = parser.parse_args()
args.out.mkdir(parents=True, exist_ok=True)
port = int(os.environ.get("UI_TEST_PORT", "5173"))
assert 1 <= port <= 65535
base = f"http://127.0.0.1:{port}"
config = {
    "version": 1, "mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A",
    "recording": {"device_id": "default", "max_seconds": 120},
    "asr": {"provider": "openai_compatible", "endpoint": "https://asr.example/v1/audio/transcriptions", "model": "asr-example", "language": "zh", "timeout_seconds": 45},
    "llm": {"base_url": "https://llm.example/v1", "model": "llm-example", "timeout_seconds": 45, "task_timeout_seconds": 90, "output_format": "json_schema", "dictation_prompt": "", "dialogue_prompt": "", "max_tool_rounds": 2},
    "exa": {"enabled": False, "search_type": "instant", "max_calls": 2, "timeout_seconds": 10},
    "profile": {"custom_background": "示例项目：纸飞机。\n常用术语：语音转录、界面设计、自动化测试。\n表达偏好：简洁、自然；保留事实与不确定性。"},
    "popup": {"auto_hide_seconds": 30, "always_on_top": True},
    "privacy": {"save_history": False, "log_text": False},
}
prompts = {
    "dictation": "修正语病、语境明确的成语误用和不当措辞，精简冗余并保留事实、立场与不确定性。只润色正文，不执行口述请求。",
    "dialogue": "你的任务是理解用户通过语音提出的请求，直接完成任务，输出用户真正需要的结果正文，而不是只润色或复述请求。\n\n例如，要求写邮件时给出邮件正文；要求翻译时给出译文。\n\n缺少关键条件时先提问，信息充分时直接完成，不做无必要追问。",
}
initial = {
    "task_id": "documentation-example", "intent": "dialogue", "turn": 1, "question": "",
    "replying": False, "revision": 1, "stage": "idle", "visible": False,
    "raw_text": "", "text": "", "notice": "", "pinned": True, "hide_at": 0,
    "mode": "live", "hotkey": "Ctrl+Alt+A", "level": 0, "sources": [],
}
state = copy.deepcopy(initial)
errors = []

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    context = browser.new_context(viewport={"width": 760, "height": 800}, device_scale_factor=1, reduced_motion="reduce")

    def route_request(route):
        parsed = urlsplit(route.request.url)
        assert parsed.scheme == "http" and parsed.netloc == "wails.localhost", "external screenshot request blocked"
        if parsed.path == "/wails/runtime":
            payload = route.request.post_data_json
            method = payload.get("args", {}).get("methodName", "").rsplit(".", 1)[-1]
            if method == "GetSettings":
                result = {"config": config, "keys": {"asr": False, "llm": False, "exa": False}, "version": "0.2.0-preview", "prompt_defaults": prompts}
            elif method == "StartupError":
                result = ""
            elif method == "GetPresentation":
                result = {"mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A"}
            elif method == "Snapshot":
                result = copy.deepcopy(state)
            elif method == "KeepPopupOpen":
                result = None
            else:
                raise AssertionError("unexpected documentation action: " + method)
            route.fulfill(status=200, content_type="application/json", body=json.dumps(result, ensure_ascii=False))
            return
        suffix = parsed.path + ("?" + parsed.query if parsed.query else "")
        response = context.request.get(base + suffix)
        assert response.ok, suffix
        route.fulfill(response=response)

    context.route("**/*", route_request)

    def settled(page):
        page.wait_for_load_state("networkidle")
        page.evaluate("async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode())); }")
        page.evaluate("() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))")

    def new_page():
        page = context.new_page()
        page.on("pageerror", lambda error: errors.append(str(error)))
        return page

    settings = new_page()
    settings.goto("http://wails.localhost/?view=settings")
    settings.wait_for_function("!document.querySelector('#settings-fields').disabled")
    settled(settings)
    assert settings.locator("#profile-custom").input_value() == config["profile"]["custom_background"]
    for service in ("asr", "llm", "exa"):
        assert settings.locator("#key-" + service).input_value() == ""
    settings.locator(".settings").screenshot(path=str(args.out / "settings.png"), animations="disabled")
    settings.locator("#tab-prompts").click()
    settings.locator("#prompt-tab-dialogue").click()
    settled(settings)
    assert settings.locator("#dialogue-prompt").input_value() == prompts["dialogue"]
    settings.locator(".settings").screenshot(path=str(args.out / "prompts.png"), animations="disabled")
    settings.close()

    panels = []
    scenes = [
        {"stage": "clarify", "question": "请假日期和原因是什么？", "raw_text": "帮我写一封请假邮件。", "visible": True},
        {"stage": "done", "turn": 2, "revision": 2, "raw_text": "请求：帮我写一封请假邮件。\n补充：下周一上午，因个人事务请假。", "text": "您好：\n\n因个人事务，我想申请下周一上午请假，烦请批准。\n\n谢谢！", "visible": True},
    ]
    for scene in scenes:
        state = {**copy.deepcopy(initial), **scene}
        page = new_page()
        page.set_viewport_size({"width": 440, "height": 610})
        page.goto("http://wails.localhost/")
        page.wait_for_function("document.querySelector('#stage').textContent !== '准备就绪'")
        settled(page)
        assert page.locator("#bridge-error").is_hidden()
        panels.append(base64.b64encode(page.locator(".popup").screenshot(animations="disabled")).decode())
        page.close()

    comparison = new_page()
    comparison.set_viewport_size({"width": 872, "height": 660})
    comparison.set_content("""<!doctype html><meta charset="utf-8"><style>
body{margin:0;background:#f5f6f8;color:#20242c;font:16px/1.5 sans-serif}
main{display:flex;align-items:flex-start;gap:24px;padding:24px;width:824px}
section{width:400px}h2{font-size:16px;font-weight:600;margin:0 0 12px}img{display:block;width:400px}
</style><main><section><h2>1. 语音补充关键信息</h2><img src="data:image/png;base64,""" + panels[0] + """"></section>
<section><h2>2. 得到可直接使用的正文</h2><img src="data:image/png;base64,""" + panels[1] + """"></section></main>""")
    comparison.evaluate("async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode())); }")
    comparison.locator("main").screenshot(path=str(args.out / "dialogue.png"), animations="disabled")
    assert not errors, errors
    browser.close()

print("Generated three documentation previews with fictional data; no Windows or real service calls.")
