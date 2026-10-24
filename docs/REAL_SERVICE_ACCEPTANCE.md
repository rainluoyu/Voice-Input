# 真实服务验收

此入口用于验证项目原生 ASR、LLM、Exa Adapter，而不是重新实现一套 HTTP 客户端。默认跳过所有真实请求；只有 `VOICE_INPUT_REAL_ACCEPTANCE=1` 时启用。测试可能产生服务费用，不自动重试，不并发执行。

## 范围与边界

- `semantics`：7 个固定合成语义用例，包括忠实整理、风格改写、纠正、目标 AI 提示词、翻译、skip、clarify。即使开放搜索工具，普通用例也不得尝试搜索；意外尝试在发往 Exa 前被拒绝，并使测试失败。
- `exa`：原生 Exa Adapter 直连，检查非空且有上限的证据与官方来源 URL。
- `tool_loop`：真实 LLM → 一次真实 Exa 搜索 → 真实 LLM 最终结构化输出，检查工具调用确实发生。
- `pipeline`：已知中文合成 WAV → 真实 ASR → 真实 LLM → fake injector。包含真实协调器，但录音器和输入目标仍是替身。

**通过这些测试不代表 Windows 麦克风、原生非激活行为、回填、热键或 Credential Manager 已验收。** 自动内容断言只覆盖部分可观察约束，必须另外复核合成输入、生成结果与来源证据。

## 环境配置

每个子测试独立检查所需配置；不把密钥写进源码、命令行参数或配置 JSON。以下均为测试进程的环境变量，不会保存为应用设置。

| 变量 | 用途 |
|---|---|
| `VOICE_INPUT_REAL_ACCEPTANCE` | 必须明确设为 `1` |
| `VOICE_INPUT_ACCEPTANCE_REPORT` | 必填；指向不存在的新 JSON 文件，父目录须已存在 |
| `VOICE_INPUT_ACCEPTANCE_LLM_BASE_URL` | LLM Base URL，程序追加 `/chat/completions` |
| `VOICE_INPUT_ACCEPTANCE_LLM_MODEL` | 服务实际支持的模型 ID |
| `VOICE_INPUT_ACCEPTANCE_LLM_KEY` | 仅通过环境提供的 LLM 凭据 |
| `VOICE_INPUT_ACCEPTANCE_RESPONSE_FORMAT` | 可选，默认 `json_schema`；可明确选择 `json_object`，不自动降级重试 |
| `VOICE_INPUT_ACCEPTANCE_EXA_KEY` | `exa` 和 `tool_loop` 使用的 Exa 凭据 |
| `VOICE_INPUT_ACCEPTANCE_ASR_PROVIDER` | 仅 `pipeline` 使用；可选 `mimo` 或 `openai_compatible`，留空保持旧 multipart 协议；不会按地址猜测协议 |
| `VOICE_INPUT_ACCEPTANCE_ASR_ENDPOINT` | 仅 `pipeline` 需要；完整转录接口地址，不从 LLM 地址推测 |
| `VOICE_INPUT_ACCEPTANCE_ASR_MODEL` | 仅 `pipeline` 需要；ASR 模型 ID |
| `VOICE_INPUT_ACCEPTANCE_ASR_KEY` | 仅 `pipeline` 需要；ASR 凭据 |
| `VOICE_INPUT_ACCEPTANCE_WAV` | 仅 `pipeline` 需要；已知中文合成 WAV 的本地路径 |

验收客户端显式使用各用例配置的 `general.proxy`，默认跟随平台代理，不读取桌面 EXE 的配置文件。Windows 使用当前用户系统代理；非 Windows 使用标准 `HTTP_PROXY/HTTPS_PROXY/NO_PROXY`，本机回环目标始终直连。

当前 pipeline 固定验收语句为“你好，欢迎来到语音工作室，今天我们验证声音与语速。”，并要求有声 PCM16、16 kHz、单声道 WAV；不要将任意真人录音或私人音频代入此固定用例。音频不随测试文件分发，也不调用 TTS 服务。

## 执行

先通过受信环境准备上述变量。下面的命令不包含密钥；每次运行都必须选择新的 REPORT 文件路径，建议放在仓库外的用户私有目录。不要提交报告、测试凭据或语音文件。

只验收 LLM 与 Exa，不读取 ASR 配置、不调用 ASR：

```bash
go test -count=1 -timeout=15m ./internal/pipeline \
  -run '^TestRealAcceptance$/^(semantics|exa|tool_loop)$' -v
```

ASR 独立端点就绪后，单独验收 pipeline：

```bash
go test -count=1 -timeout=3m ./internal/pipeline \
  -run '^TestRealAcceptance$/^pipeline$' -v
```

未设置显式开关时，正常回归会跳过真实服务入口。可单独执行无网络的验收工具测试：

```bash
go test ./internal/pipeline -run '^TestAcceptanceHarnessOffline$' -v
```

## 预算与证据

- 每次进程硬上限：ASR 1 次、LLM 10 次、Exa 2 次。只运行 LLM/Exa 三组时，正常期望 LLM 9 次、Exa 2 次、ASR 0 次。
- 无自动重试；更改配置或修复后再次运行是新的付费测试，必须保留原有失败记录，并计算累计请求。
- 报告排他创建，权限为 `0600`，拒绝覆盖旧文件；每项结束保存，失败时仍保留已有证据。
- 仅报告固定合成输入/输出、脱敏错误类别、断言、调用次数、耗时和去除查询参数的证据 URL，不报告凭据、服务配置或请求头。
- 终端只输出摘要。审阅报告后，才能补充语义与证据复核结论；不要把 `passed_automatic_checks_only` 当作完整语义和实机验收通过。
- 未选择的 pipeline 标记为 `deferred_not_selected`；配置不足的已选用例标记为 `deferred_missing_configuration`，不记为通过。
