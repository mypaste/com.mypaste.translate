# 翻译对照

MyPaste extension `com.mypaste.translate` (version `1.5.0`).

把一条外文条目排成对照阅读：每段原文下方给出译文，保留段落与换行；点任意单词弹出卡片，含英文/美式音标、词性与常用义，卡片可以朗读单词（系统语音，不联网）。翻译服务在页面顶部切换：**Bilibili Index-Translate**（中英日互译，可选 Index-Nailong 9B / 2B 模型，默认）、**Google 翻译**（支持的语言最多）、**MyMemory**（无需密钥，每天有免费额度）、**DeepSeek**（`deepseek-chat` / `deepseek-reasoner`），以及**OpenAI 兼容**与 **Claude 兼容**服务——后两者的地址与模型就是你填的那一份，可以是官方接口，也可以是任何兼容实现。英文单词的音标与词性来自有道词典的免费接口，不论用哪个服务翻译都会补齐。密钥都由宿主保管：在 MyPaste 的「设置 › 扩展 › 翻译对照」里填写或清除，扩展运行时与页面都拿不到明文，只会由宿主在发往对应地址的请求上补一个头（OpenAI 兼容与 DeepSeek 用 `Authorization: Bearer`，Claude 兼容用 `x-api-key`）；未填写地址或密钥时该服务会直接给出可操作的提示，不会返回空译文。打开页面即自动开始翻译，读到哪里译到哪里；换服务或改目标语言会直接重译，不用再点一次。只把当前段落发往所选服务；原文与译文都不保存，也不改写原条目。按 `Esc` 先关卡片，再关页面。服务、模型与目标语言收在标题右侧的一条选项里，不再占一行工具栏；条目的标题写进正文纸面开头的文档头，工具栏只剩返回与选项，翻译进行时标题下有一条细线标示进度。

This repository is the independent home of the extension; it started as commit `cd9f491` in the former `zhanghuid/mypaste-code-extensions` monorepo. The published `.mypaste-plugin` archives and marketplace index live in [`mypaste-extensions-catalog`](https://github.com/mypaste/mypaste-extensions-catalog).

The manifest speaks MyPaste plugin protocol v3. Installation, capability mediation, and content-digest verification are performed by the MyPaste desktop host.

## Services

| Service | Host | Directions | Notes |
| --- | --- | --- | --- |
| Bilibili Index-Translate | `index-translate.bilibili.com` | 中 ↔ 英, 中 ↔ 日 | keyless; models `index-nailong-9b` (default) and `index-nailong-2b`; streamed SSE, capped per chunk |
| Google 翻译 | `translate.googleapis.com` | 自动检测，16 种目标语言 | keyless web endpoint; its own dictionary card with phonetics and parts of speech |
| MyMemory | `api.mymemory.translated.net` | 自动检测，16 种目标语言 | keyless free API; queries are cut at 480 bytes and sent paragraph by paragraph; reports an exhausted daily quota |
| DeepSeek | `api.deepseek.com` | 自动检测，16 种目标语言 | models `deepseek-chat` (default) and `deepseek-reasoner`; the API key is a host-held `secret` setting (D-1176), added by the host as `Authorization: Bearer`; one non-streamed completion per paragraph |
| OpenAI 兼容 | 你填写的地址（`configuredHosts: openaiurl`） | 自动检测，16 种目标语言 | any OpenAI Chat Completions implementation; `POST <origin>/v1/chat/completions`; the model comes from the `openaimodel` setting (`gpt-4o-mini` when empty); the key is a host-held `secret` bound to the configured address, added by the host as `Authorization: Bearer` (D-1185) |
| Claude 兼容 | 你填写的地址（`configuredHosts: claudeurl`） | 自动检测，16 种目标语言 | any Anthropic Messages implementation; `POST <origin>/v1/messages` with `anthropic-version: 2023-06-01`; the model comes from the `claudemodel` setting (`claude-sonnet-4-5` when empty); the key is a host-held `secret`, added by the host as `x-api-key` (D-1185) |
| 有道词典（词卡） | `dict.youdao.com` | 英文单词 → 音标 / 词性 / 释义 | free `jsonapi_s` endpoint; fills the card in behind whichever service translated the word |

Every service is reached only through the host's `network.outbound` proxy; the runtime never opens a socket. A compatible service's address is a host setting, so the destination list of one invocation is the five declared hosts plus the addresses the reader filled in — the host still refuses any other host (D-1031). The chosen service, direction and model are the only state kept, in the package's private storage. Pronunciation uses the page's own `speechSynthesis`, so it sends nothing anywhere.

## Build

```bash
bun install
bun run build          # src/preview.html + src/main.ts → preview.html
bun run typecheck      # tsc
bun run test           # page helpers and manifest
(cd src && go test ./...)
bun run package        # both macOS binaries + dist/*.mypaste-plugin + .sha256
```

The generated `preview.html` and `dist/` are intentionally ignored; edit `src/preview.html` and `src/main.ts` instead.

## Files

- `mypaste.plugin.json`: package identity, capability declaration, the five declared network hosts, the two user-addressed compatible services, and the host-held `secret` settings for their keys.
- `src/preview.html`, `src/main.ts`, `src/text.ts`, `src/bridge.ts`: the surface page.
- `src/cmd/mypaste-translate`: the runtime that speaks the plugin v2 line protocol.
- `src/internal/provider`: the Google, Bilibili, MyMemory, DeepSeek and compatible-service providers behind one closed list, the chat exchange they share, plus the Youdao dictionary that fills a word card.
- `src/internal/host`: this package's side of the host line protocol.

## License

MIT. See [LICENSE](LICENSE).
