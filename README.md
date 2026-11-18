# 翻译对照

MyPaste extension `com.mypaste.translate` (version `1.0.0`).

把一条外文条目排成对照阅读：每段原文下方给出译文，保留段落与换行；点任意单词弹出卡片，Google 翻译下含音标、词性与常用义，其他服务下给出该词的译文。翻译服务在页面顶部切换：**Bilibili Index-Translate**（中英日互译，可选 Index-Nailong 9B / 2B 模型，默认）与 **Google 翻译**（支持的语言最多）。翻译按需开始，只把当前段落发往所选服务；原文与译文都不保存，也不改写原条目。

This repository is the independent home of the extension; it started as commit `cd9f491` in the former `zhanghuid/mypaste-code-extensions` monorepo. The published `.mypaste-plugin` archives and marketplace index live in [`mypaste-extensions-catalog`](https://github.com/mypaste/mypaste-extensions-catalog).

The manifest speaks MyPaste plugin protocol v3. Installation, capability mediation, and content-digest verification are performed by the MyPaste desktop host.

## Services

| Service | Host | Directions | Notes |
| --- | --- | --- | --- |
| Bilibili Index-Translate | `index-translate.bilibili.com` | 中 ↔ 英, 中 ↔ 日 | keyless; models `index-nailong-9b` (default) and `index-nailong-2b`; streamed SSE, capped per chunk |
| Google 翻译 | `translate.googleapis.com` | 自动检测，12 种目标语言 | keyless web endpoint; dictionary card with phonetics and parts of speech |

Both services are reached only through the host's `network.outbound` proxy; the runtime never opens a socket. The chosen service, direction and model are the only state kept, in the package's private storage.

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

- `mypaste.plugin.json`: package identity, capability declaration and the two network hosts.
- `src/preview.html`, `src/main.ts`, `src/text.ts`, `src/bridge.ts`: the surface page.
- `src/cmd/mypaste-translate`: the runtime that speaks the plugin v2 line protocol.
- `src/internal/provider`: the Google and Bilibili services behind one closed list.
- `src/internal/host`: this package's side of the host line protocol.

## License

MIT. See [LICENSE](LICENSE).
