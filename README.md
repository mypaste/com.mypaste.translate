# 翻译对照

MyPaste extension `com.mypaste.translate` (version `1.3.0`).

把一条外文条目排成对照阅读：每段原文下方给出译文，保留段落与换行；点任意单词弹出卡片，含英文/美式音标、词性与常用义，卡片可以朗读单词（系统语音，不联网）。翻译服务在页面顶部切换：**Bilibili Index-Translate**（中英日互译，可选 Index-Nailong 9B / 2B 模型，默认）、**Google 翻译**（支持的语言最多）、**MyMemory**（无需密钥，每天有免费额度）与 **DeepSeek**（`deepseek-chat` / `deepseek-reasoner`，需要 API 密钥）。英文单词的音标与词性来自有道词典的免费接口，不论用哪个服务翻译都会补齐。DeepSeek 的密钥由宿主保管：在 MyPaste 的「设置 › 扩展 › 翻译对照」里填写或清除，扩展运行时与页面都拿不到明文，只会由宿主在发往 `api.deepseek.com` 的请求上补一个 `Authorization: Bearer` 头；未填写时该服务会直接给出可操作的提示，不会返回空译文。打开页面即自动开始翻译，读到哪里译到哪里；换服务或改目标语言会直接重译，不用再点一次。只把当前段落发往所选服务；原文与译文都不保存，也不改写原条目。按 `Esc` 先关卡片，再关页面。服务、模型与目标语言收在标题右侧的一条选项里，不再占一行工具栏。

This repository is the independent home of the extension; it started as commit `cd9f491` in the former `zhanghuid/mypaste-code-extensions` monorepo. The published `.mypaste-plugin` archives and marketplace index live in [`mypaste-extensions-catalog`](https://github.com/mypaste/mypaste-extensions-catalog).

The manifest speaks MyPaste plugin protocol v3. Installation, capability mediation, and content-digest verification are performed by the MyPaste desktop host.

## Services

| Service | Host | Directions | Notes |
| --- | --- | --- | --- |
| Bilibili Index-Translate | `index-translate.bilibili.com` | 中 ↔ 英, 中 ↔ 日 | keyless; models `index-nailong-9b` (default) and `index-nailong-2b`; streamed SSE, capped per chunk |
| Google 翻译 | `translate.googleapis.com` | 自动检测，16 种目标语言 | keyless web endpoint; its own dictionary card with phonetics and parts of speech |
| MyMemory | `api.mymemory.translated.net` | 自动检测，16 种目标语言 | keyless free API; queries are cut at 480 bytes and sent paragraph by paragraph; reports an exhausted daily quota |
| DeepSeek | `api.deepseek.com` | 自动检测，16 种目标语言 | models `deepseek-chat` (default) and `deepseek-reasoner`; the API key is a host-held `secret` setting (D-1176), added by the host as `Authorization: Bearer`; one non-streamed completion per paragraph |
| 有道词典（词卡） | `dict.youdao.com` | 英文单词 → 音标 / 词性 / 释义 | free `jsonapi_s` endpoint; fills the card in behind whichever service translated the word |

Every service is reached only through the host's `network.outbound` proxy; the runtime never opens a socket. The chosen service, direction and model are the only state kept, in the package's private storage. Pronunciation uses the page's own `speechSynthesis`, so it sends nothing anywhere.

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

- `mypaste.plugin.json`: package identity, capability declaration, the five network hosts and the host-held `secret` setting for the DeepSeek key.
- `src/preview.html`, `src/main.ts`, `src/text.ts`, `src/bridge.ts`: the surface page.
- `src/cmd/mypaste-translate`: the runtime that speaks the plugin v2 line protocol.
- `src/internal/provider`: the Google, Bilibili, MyMemory and DeepSeek services behind one closed list, plus the Youdao dictionary that fills a word card.
- `src/internal/host`: this package's side of the host line protocol.

## License

MIT. See [LICENSE](LICENSE).
