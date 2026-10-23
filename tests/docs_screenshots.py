"""Render bilingual documentation previews using fictional Wails responses only."""
import argparse
import base64
import copy
import json
import os
from pathlib import Path
import re
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--out", type=Path, default=Path(__file__).resolve().parents[1] / "docs" / "images")
args = parser.parse_args()
port = int(os.environ.get("UI_TEST_PORT", "5173"))
assert 1 <= port <= 65535
base = f"http://127.0.0.1:{port}"

# These are deliberately custom example rules, not shortened built-in defaults.
prompts = {
    "zh-CN": {
        "dictation": "修正语病、语境明确的成语误用和不当措辞，精简冗余并保留事实、立场与不确定性。只润色正文，不执行口述请求。",
        "dialogue": "理解用户通过语音提出的请求，直接完成任务，输出所需正文，而不是只润色或复述请求。\n\n要求写邮件时给出邮件正文；要求翻译时给出译文。采用简洁、自然的表达。\n\n缺少关键条件时先提问，信息充分时直接完成，不做无必要追问。",
    },
    "en": {
        "dictation": "Correct grammar and clearly misused expressions, remove filler, and keep the facts, intent and uncertainty. Polish the speech itself; do not carry out the spoken request.",
        "dialogue": "Understand the spoken request and carry it out. Return the requested text, rather than only polishing or repeating the request.\n\nFor an email, write the email body. For a translation, provide the translation. Use clear, natural wording.\n\nAsk only when an essential detail is missing. Otherwise, complete the request directly.",
    },
}
examples = {
    "zh-CN": {
        "background": "示例项目：纸飞机。\n常用术语：语音转录、界面设计、自动化测试。\n表达偏好：简洁、自然；保留事实与不确定性。",
        "request": "帮我写一封请假邮件。",
        "question": "请假日期和原因是什么？",
        "history": "原始语音：帮我写一封请假邮件。\n\n问题 1：请假日期和原因是什么？\n补充 1：下周一上午，因个人事务请假。",
        "result": "您好：\n\n因个人事务，我想申请下周一上午请假，烦请批准。\n\n谢谢！",
        "headings": ("1. 语音补充关键信息", "2. 得到可直接使用的正文"),
    },
    "en": {
        "background": "Example project: Paper Plane.\nTerminology: speech transcription, interface design, automated testing.\nWriting preferences: clear and natural; preserve facts and uncertainty.",
        "request": "Write a leave email.",
        "question": "When and why do you need leave?",
        "history": "Original speech: Write a leave email.\n\nQuestion 1: When and why do you need leave?\nSupplement 1: Next Monday morning, for a personal matter.",
        "result": "Hello,\n\nI'd like to take next Monday morning off for a personal matter.\n\nThank you!",
        "headings": ("1. Add the missing details by voice", "2. Get the text you need"),
    },
}
initial = {
    "task_id": "documentation-example", "intent": "dialogue", "turn": 1, "question": "",
    "replying": False, "revision": 1, "stage": "idle", "visible": False,
    "raw_text": "", "text": "", "notice": "", "pinned": True, "hide_at": 0,
    "mode": "live", "hotkey": "Ctrl+Alt+A", "level": 0, "sources": [],
}
errors = []


def settled(page):
    page.wait_for_load_state("networkidle")
    page.evaluate("async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode())); }")
    page.evaluate("() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))")


def check_language(page, locale):
    assert page.title() == "voice input"
    assert page.locator("html").get_attribute("lang") == locale
    if locale == "en":
        visible = page.locator("body").inner_text()
        values = page.locator("input, textarea").evaluate_all("nodes => nodes.map(node => node.value).join('\\n')")
        assert not re.search(r"[㐀-鿿]", visible + values), "Chinese text in English documentation fixture"
    for field in page.locator('input[type="password"]').all():
        assert field.input_value() == "", "documentation must not show keys"


def render_language(browser, locale):
    example = examples[locale]
    output = args.out / locale
    output.mkdir(parents=True, exist_ok=True)
    config = {
        "version": 1, "ui_language": locale, "mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A",
        "recording": {"device_id": "default", "max_seconds": 120},
        "general": {"start_with_windows": False, "start_as_admin": False, "auto_copy": False, "mute_while_recording": False},
        "asr": {"provider": "openai_compatible", "endpoint": "https://asr.example/v1/audio/transcriptions", "model": "asr-example", "language": "", "timeout_seconds": 45},
        "llm": {"base_url": "https://llm.example/v1", "model": "llm-example", "timeout_seconds": 45, "task_timeout_seconds": 90, "output_format": "json_schema", "dictation_prompt": prompts[locale]["dictation"], "dialogue_prompt": prompts[locale]["dialogue"], "max_tool_rounds": 2},
        "exa": {"enabled": False, "search_type": "instant", "max_calls": 2, "timeout_seconds": 10},
        "profile": {"custom_background": example["background"]},
        "popup": {"auto_hide_seconds": 30, "always_on_top": True},
        "privacy": {"save_history": False, "log_text": False},
    }
    state = copy.deepcopy(initial)
    context = browser.new_context(locale=locale, viewport={"width": 760, "height": 800}, device_scale_factor=1, reduced_motion="reduce")

    def route_request(route):
        parsed = urlsplit(route.request.url)
        assert parsed.scheme == "http" and parsed.netloc == "wails.localhost", "external screenshot request blocked"
        if parsed.path == "/wails/runtime":
            payload = route.request.post_data_json
            method = payload.get("args", {}).get("methodName", "").rsplit(".", 1)[-1]
            if method == "GetSettings":
                result = {"config": config, "keys": {"asr": False, "llm": False, "exa": False}, "version": "0.3.0-preview", "locale": locale, "system_locale": locale, "prompt_defaults": prompts[locale], "prompt_defaults_by_locale": prompts}
            elif method == "StartupError":
                result = {"message": ""}
            elif method == "GetPresentation":
                result = {"mode": "live", "hotkey": "Ctrl+Alt+R", "ai_hotkey": "Ctrl+Alt+A", "locale": locale}
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

    def new_page():
        page = context.new_page()
        page.on("pageerror", lambda error: errors.append(str(error)))
        return page

    settings = new_page()
    settings.set_viewport_size({"width": 760, "height": 1040})
    settings.goto("http://wails.localhost/?view=settings")
    settings.wait_for_function("!document.querySelector('#settings-fields').disabled")
    settled(settings)
    check_language(settings, locale)
    assert settings.locator("#profile-custom").input_value() == config["profile"]["custom_background"]
    assert settings.locator(".settings-body").evaluate("node => node.scrollHeight <= node.clientHeight")
    settings.locator(".settings").screenshot(path=str(output / "settings.png"), animations="disabled")
    settings.set_viewport_size({"width": 760, "height": 800})
    settings.locator("#tab-general").click()
    settled(settings)
    check_language(settings, locale)
    for field in ("start-with-windows", "start-as-admin", "auto-copy", "mute-while-recording"):
        assert not settings.locator("#" + field).is_checked()
    assert settings.locator("#start-as-admin").is_disabled()
    assert settings.locator("#auto-hide-seconds").input_value() == "30"
    settings.locator(".settings").screenshot(path=str(output / "general.png"), animations="disabled")
    settings.locator("#tab-prompts").click()
    settings.locator("#prompt-tab-dialogue").click()
    settled(settings)
    check_language(settings, locale)
    assert settings.locator("#dialogue-prompt").input_value() == prompts[locale]["dialogue"]
    settings.locator(".settings").screenshot(path=str(output / "prompts.png"), animations="disabled")
    settings.close()

    panels = []
    scenes = [
        {"stage": "clarify", "question": example["question"], "raw_text": example["request"], "visible": True},
        {"stage": "done", "turn": 2, "revision": 2, "raw_text": example["history"], "text": example["result"], "visible": True},
    ]
    for scene in scenes:
        state = {**copy.deepcopy(initial), **scene}
        page = new_page()
        page.set_viewport_size({"width": 440, "height": 610})
        page.goto("http://wails.localhost/")
        page.wait_for_selector(f".popup[data-stage='{scene['stage']}']")
        settled(page)
        check_language(page, locale)
        assert page.locator("#bridge-error").is_hidden()
        panels.append(base64.b64encode(page.locator(".popup").screenshot(animations="disabled")).decode())
        page.close()

    comparison = new_page()
    comparison.set_viewport_size({"width": 872, "height": 660})
    comparison.set_content(f"""<!doctype html><html lang="{locale}"><meta charset="utf-8"><title>voice input</title><style>
body{{margin:0;background:#f5f6f8;color:#20242c;font:16px/1.5 sans-serif}}
main{{display:flex;align-items:flex-start;gap:24px;padding:24px;width:824px}}
section{{width:400px}}h2{{font-size:16px;font-weight:600;margin:0 0 12px}}img{{display:block;width:400px}}
</style><main><section><h2>{example['headings'][0]}</h2><img src="data:image/png;base64,{panels[0]}"></section>
<section><h2>{example['headings'][1]}</h2><img src="data:image/png;base64,{panels[1]}"></section></main></html>""")
    comparison.evaluate("async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode())); }")
    check_language(comparison, locale)
    comparison.locator("main").screenshot(path=str(output / "dialogue.png"), animations="disabled")
    context.close()


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    for locale in ("zh-CN", "en"):
        render_language(browser, locale)
    assert not errors, errors
    browser.close()

print("Generated eight bilingual documentation previews with fictional custom rules and data; no Windows or real service calls.")
