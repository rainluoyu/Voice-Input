# True Semantic Input · 真实语义输入

**English** | [中文](README.zh-CN.md)

A Windows voice-input utility with two separate shortcuts: **polish what you say** or **ask AI to produce the text you need**. Results are inserted at the currently active input location. The application does not press Enter or send messages automatically.

Built with Go, Wails, TypeScript and a native non-activating Win32 popup. The application interface is currently in Chinese.

## Features

- **Voice polishing:** correct grammar, contextually clear idiom misuse and awkward wording; remove filler and redundancy while preserving facts, intent and uncertainty. It does not execute spoken requests.
- **AI dialogue:** write, translate, answer questions or generate code. Answer clarification questions with additional voice turns within the same task.
- **Editable prompts:** separate business instructions for each mode, with independent reset-to-default controls and optional background context.
- **Optional Exa search:** AI dialogue only. Configure search attempts and tool rounds independently; zero tool rounds disables tools.
- **Single- and multi-line input:** Unicode input for a single line; clipboard paste for paragraphs and code, without simulated Enter/Shift+Enter.
- **Desktop controls:** tray access, editable global shortcuts, microphone selection, real input-level feedback, single-instance protection and a topmost popup that does not take focus.
- **Portable configuration:** `config.json` beside the EXE; API keys remain in Windows Credential Manager.

## Requirements

- Windows 10/11, x64.
- Microsoft Edge WebView2 Evergreen Runtime.
- A microphone and configured ASR service; an OpenAI-compatible Chat Completions service for AI processing. Exa is optional.
- A user-writable application directory. Run without administrator privileges.

ASR supports OpenAI-compatible multipart transcription and the MiMo audio Chat Completions protocol. An ASR-only diagnostic mode is also available. Service compatibility and transcription quality depend on the configured provider.

## Quick start

1. Build the application using the instructions below, or unpack a trusted portable archive into a writable folder.
2. Start `voice_input.exe`. Use the tray icon to open settings; incomplete configuration opens settings automatically.
3. Save service addresses, models and ASR protocol, then save the corresponding API keys separately. Connection tests use saved settings and may incur charges.
4. Place the cursor in a disposable text editor. Use the same shortcut once to start recording and again to stop:

   | Mode | Default shortcut | Saying “write a leave-request email” |
   |---|---|---|
   | Voice polishing | `Ctrl+Alt+R` | Polishes the request itself |
   | AI dialogue | `Ctrl+Alt+A` | Produces the email text |

5. If AI asks a clarification question, use the AI dialogue shortcut or the popup's reply button to record your answer.

An unfinished task cannot be replaced or stopped by the other mode's shortcut. Closing the popup cancels the task; closing settings does not quit the application. Use the tray menu to exit.

## Configuration and budgets

- The active file is **`config.json` in the EXE directory**, regardless of the working directory or shortcut location.
- If it is missing, a valid legacy `%APPDATA%\voice_input\config.json` is migrated without modifying the original. Later saves update only the new file. Existing invalid local configuration is never overwritten by migration.
- With neither file present, defaults remain in memory until saved. An unwritable directory causes an error, not elevation or fallback to AppData.
- For a complete reset, exit, back up and move aside both configuration files; removing only the local file can import the legacy copy again.
- Each custom prompt and the background field allow 16,000 Unicode characters. Configuration JSON is limited to 512 KiB. Editable prompts do not replace fixed output, mode or tool-permission boundaries.
- Per processing turn: search attempts default to 2 (range 1–8); tool rounds default to 2 (range 0–8); model requests are limited to tool rounds + 1. Each clarification answer starts a fresh processing budget, not a cumulative task-wide cost limit.

## Privacy and input safety

- **There is no focus, target-window or password-field check.** Output goes to whichever location is active when processing completes. Confirm the destination yourself.
- Multi-line automatic input and manual Copy **replace the clipboard**; the old contents are not restored. Clipboard history, sync and third-party managers may retain text. A target application may interpret a paste as execution or submission even though this utility does not press Enter.
- Automatic input is limited to 8,000 characters after newline normalization. Unsupported control characters or input failures leave text available for copying. Do not blindly retry a partially inserted result.
- Audio and text are processed in memory without application recording/transcript history. Audio is sent to the configured ASR; AI processing sends transcription, supplied background and relevant clarification history to the configured LLM. Optional searches send model-selected queries to Exa. Providers have their own retention policies.
- Configuration can contain private background and prompts: review it before sharing. API keys are stored separately under the current Windows user's Credential Manager. Copying JSON does not transfer keys; this does not protect against malicious software running as the same user.
- Completed results remain for 30 seconds by default. Mouse interaction keeps the popup open until manually closed; awaiting clarification does not expire. WebView2 and the operating system may maintain their own caches.

Start with fictional text in a disposable editor or the bundled `tests/target.html`, not passwords or messages you intend to send.

## Build from source

Use Go **1.26.9** (selected by `go.mod`) and Node.js **22.12+**. Wails/runtime is pinned to `v3.0.0-beta.30`.

In a Windows terminal at the repository root:

```powershell
npm --prefix frontend ci
npm --prefix frontend run build
go run ./tools/winresource
go build -trimpath -ldflags="-H windowsgui" -o bin/voice_input.exe ./cmd/voice_input
.\bin\voice_input.exe
```

Build before running configuration tests: `go run ./cmd/voice_input` uses a temporary EXE directory and therefore a temporary configuration location.

Linux/WSL can build the frontend, run offline tests and cross-compile Windows binaries. It does not provide a Linux desktop application. See the [development guide (Chinese)](docs/DEVELOPMENT.md) for checks, UI fixtures and portable packaging.

## Testing and limitations

Go unit/race tests, local HTTP fixtures and isolated browser tests cover state management, protocol validation, settings, tool budgets, rendering and configuration migration. They do not establish microphone/driver compatibility, native focus behavior, DPI/material rendering or real-model semantic quality. Windows-specific test binaries must be executed separately on Windows. Builds are unsigned and do not bundle a WebView2 installer.

Real-service integration tests require explicit opt-in and may incur fees; they are excluded from ordinary regression runs.

## Documentation

Detailed documentation is currently in Chinese:

- [User guide and troubleshooting](docs/USER_GUIDE.md)
- [Release notes](docs/RELEASE_NOTES.md)
- [Development, tests and packaging](docs/DEVELOPMENT.md)
- [Test matrix](docs/TEST_MATRIX.md)
- [Voice-polishing quality cases](docs/DICTATION_QUALITY_CASES.md)
- [Opt-in real-service tests](docs/REAL_SERVICE_ACCEPTANCE.md)
- [Security-check scope](docs/SECURITY_CHECK.md)

## License

This repository does not currently specify a project license. Public source availability is not a grant of an open-source license. Third-party components retain their respective licenses; portable packages include collected notices under `licenses/`, including Wails and its same-source frontend runtime.
