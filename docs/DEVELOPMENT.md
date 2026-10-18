# 开发、测试与打包

## 工具链与结构

- Go 1.26.9，由 `go.mod` 的 `toolchain` 声明选择；Node.js 22.12+。
- Wails 与前端 runtime 固定为 `v3.0.0-beta.30`。Go/npm 锁文件均应提交。
- Windows 10/11 x64 为运行目标；Linux 入口仅显示平台提示。

| 目录 | 职责 |
|---|---|
| `cmd/voice_input` | Windows 入口、托盘、设置桥接 |
| `internal/app`、`internal/pipeline` | 单任务状态、用途隔离、处理链及澄清历史 |
| `internal/audio`、`internal/asr` | WASAPI、PCM/WAV、ASR 协议 |
| `internal/llm`、`internal/search` | 提示词、严格输出、受限 Exa 工具循环 |
| `internal/settings` | 配置校验、迁移、原子保存、Credential Manager |
| `internal/popup`、`internal/wininput` | 非激活原生浮窗、Unicode 输入和多行粘贴 |
| `internal/branding`、`tools/winresource` | 同源图标、Windows 资源及普通权限 manifest |
| `frontend`、`tests` | 设置/预览、浏览器 fixtures 和离线输入靶页 |
| `tools/package`、`scripts` | 白名单打包及可复现检查 |

设置页使用 Wails/WebView2，任务浮窗使用原生 Win32/GDI。任务通过 TaskID、Turn 和 Revision 隔离异步更新；最终正文跟随以任务、轮次和正文为标识，重复状态不抢滚动。

## Windows 构建

在仓库根目录执行：

```powershell
npm --prefix frontend ci
npm --prefix frontend run build
go run ./tools/winresource
$env:VOICE_INPUT_REAL_ACCEPTANCE = ""
go test -timeout=90s ./...
go build -trimpath -ldflags="-H windowsgui" -o bin/voice_input.exe ./cmd/voice_input
.\bin\voice_input.exe
```

先构建再测试持久配置。`go run ./cmd/voice_input` 使用临时 EXE 目录，不会使用仓库根目录或已有便携目录的配置。

前端 PNG 由同源图标生成器派生：

```bash
go run ./tools/winresource -png frontend/public/app-icon.png
```

派生 PNG 需要提交，生成的 `.syso` 和前端 `dist/` 不提交。编译 Windows 前必须先生成前端和资源。

## Linux/WSL 自动检查

依赖已安装时，可分别执行：

```bash
export VOICE_INPUT_REAL_ACCEPTANCE=
npm --prefix frontend run build
go run ./tools/winresource
go test -race -timeout=90s ./...
go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
mkdir -p bin
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-H windowsgui' -o bin/voice_input.exe ./cmd/voice_input
for package in wininput audio settings app popup instance; do
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o "bin/$package.test.exe" "./internal/$package"
done
```

Windows 二进制在这里仅编译，不执行。离线工作时需预先具备指定 Go 工具链、模块和前端依赖；可设置 `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off` 阻止模块联网，不要把缺失缓存当成代码失败。

## 浏览器测试

```bash
./scripts/test-ui.sh
```

脚本在临时 uv 项目中安装固定 Playwright 及 Chromium headless shell，构建前端并启动本地 Vite 预览。覆盖 smoke、Wails 桥接契约和设置异步状态；生成临时日志/截图。需要 `uv`、Node.js 和浏览器运行库，不向系统 Python 安装包。离线环境应复用已准备好的 uv/浏览器缓存，不重复安装。

预览页面本身只读，不能录音、保存设置、读取凭据或发起真实服务请求；测试使用本地拦截的 Wails fixtures，不是 Windows 集成验收。停止临时服务时只结束脚本保存的 PID。

## 脚本入口与网络

| 入口 | 行为 |
|---|---|
| `scripts/check.sh` | 安装前端依赖、构建、资源生成、race、双平台 vet、Windows 主程序及专项编译 |
| `scripts/test-ui.sh` | 准备隔离浏览器依赖、构建前端、启动预览并执行三组浏览器测试 |
| `scripts/package.sh` | 构建并生成便携 ZIP/SHA-256，不替代完整回归或安全扫描 |
| `scripts/security.sh` | npm 审计及已有 EXE 的漏洞扫描，不重新构建或启动 EXE |

首次安装及安全审计可能联网。对外请求使用实际执行侧的 `PROXY_URL`，并按工具要求传入标准代理变量；本机回环请求直连。Windows 与 WSL 各用自己的配置，不复制另一侧回环地址。

普通回归务必清空 `VOICE_INPUT_REAL_ACCEPTANCE`。需要真实服务时，另按[真实服务测试](REAL_SERVICE_ACCEPTANCE.md)显式启用，并将报告放在仓库外。

## 便携包

构建后执行：

```bash
go run ./tools/package -exe bin/voice_input.exe -out bin/voice_input-0.2.0-windows-amd64.zip
```

打包器验证 PE 架构和普通权限 manifest，只收录明确白名单：程序、双语 README、公开文档、输入靶页、构建信息和第三方许可证。不要改成递归收集工作目录。

输出 `.zip.sha256` 对应 ZIP 校验值，`BUILDINFO.json` 记录 EXE 摘要、平台、工具链及未签名/桌面待验收状态。Wails 根 MIT 声明同时对应锁定版本的同源前端运行时；其他依赖按各自声明收集。项目本身尚未指定许可证。

不要提交真实配置、密钥、音频、截图、原始服务报告、代理工作目录、构建产物或个人部署资料。提交前检查暂存内容；忽略规则不能清除已进入 Git 历史的资料。

验证范围和人工用例见[测试矩阵](TEST_MATRIX.md)，用户行为见[使用指南](USER_GUIDE.md)。
