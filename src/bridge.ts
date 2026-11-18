/**
 * The host's surface bridge, as seen from inside the translation page.
 *
 * The document runs in a sandboxed frame with an opaque origin and holds no
 * host binding. It can receive the invoked item, receive the application's
 * resolved theme, invoke one of this package's own internal commands (the Go
 * runtime does the network request), ask for a copy, and close the container.
 */

export type SurfaceTextItem = { kind: "text"; title: string; text: string };

export interface BridgeResult {
  ok: boolean;
  message?: string;
}

export interface InvokeResult extends BridgeResult {
  output?: string;
}

export interface SurfaceDelivery {
  item: SurfaceTextItem;
}

export type SurfaceScheme = "light" | "dark";

export type SurfaceTokens = Record<string, string>;

interface ResultMessage extends BridgeResult {
  requestId: number;
  output?: string;
}

const TOKEN_NAME = /^[a-z][a-z0-9-]*$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function parseTextItem(value: unknown): SurfaceTextItem | null {
  if (!isRecord(value) || value.kind !== "text" ||
    typeof value.title !== "string" || typeof value.text !== "string") {
    return null;
  }
  return { kind: "text", title: value.title, text: value.text };
}

function parseItem(data: unknown): SurfaceTextItem | null {
  if (!isRecord(data) || data.v !== 1 || data.type !== "item") return null;
  return parseTextItem(data.item);
}

function parseTheme(data: unknown): { scheme: SurfaceScheme; tokens: SurfaceTokens } | null {
  if (!isRecord(data) || data.v !== 1 || data.type !== "theme") return null;
  if (data.scheme !== "light" && data.scheme !== "dark") return null;
  const tokens: SurfaceTokens = {};
  if (isRecord(data.tokens)) {
    for (const [name, value] of Object.entries(data.tokens)) {
      if (typeof value === "string" && value !== "" && TOKEN_NAME.test(name)) {
        tokens[name] = value;
      }
    }
  }
  return { scheme: data.scheme, tokens };
}

/**
 * applyTheme writes the host's resolved values onto this document's root as
 * `--mp-<name>` (D-1043). The stylesheet prefers those variables and keeps
 * its own palette as the fallback, so a new host repaints whenever the pair
 * changes.
 */
function applyTheme(theme: { scheme: SurfaceScheme; tokens: SurfaceTokens }): void {
  const root = document.documentElement;
  root.dataset.scheme = theme.scheme;
  for (const [name, value] of Object.entries(theme.tokens)) {
    root.style.setProperty(`--mp-${name}`, value);
  }
}

export interface SurfaceBridge {
  readonly delivery: SurfaceDelivery | null;
  /** Registers the one receiver for the delivery; it fires at most once. */
  onItem(listener: (delivery: SurfaceDelivery) => void): void;
  /** Runs one of this package's own internal commands against text. */
  invoke(pluginId: string, commandId: string, text: string): Promise<InvokeResult>;
  copy(text: string): Promise<BridgeResult>;
  /** Asks the host to close the surface; the page owns the whole card. */
  close(): void;
}

export function connectSurface(): SurfaceBridge {
  let delivery: SurfaceDelivery | null = null;
  const listeners: ((delivery: SurfaceDelivery) => void)[] = [];
  const pending = new Map<number, (result: ResultMessage) => void>();
  let nextRequest = 1;
  let delivering = false;

  function post(message: unknown): void {
    parent.postMessage(message, "*");
  }

  function request(message: Record<string, unknown>): Promise<ResultMessage> {
    const requestId = nextRequest++;
    return new Promise((resolve) => {
      pending.set(requestId, resolve);
      post({ ...message, v: 1, requestId });
    });
  }

  function deliver(item: SurfaceTextItem): void {
    if (delivering) return;
    delivering = true;
    delivery = { item };
    for (const listener of listeners.splice(0)) listener(delivery);
  }

  document.documentElement.dataset.scheme =
    matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";

  window.addEventListener("message", (event) => {
    if (event.source !== parent) return;
    const data = event.data;
    const item = parseItem(data);
    if (item) {
      deliver(item);
      return;
    }
    const theme = parseTheme(data);
    if (theme) {
      applyTheme(theme);
      return;
    }
    if (!isRecord(data) || data.v !== 1 || data.type !== "result") return;
    if (typeof data.requestId !== "number") return;
    const resolve = pending.get(data.requestId);
    if (!resolve) return;
    pending.delete(data.requestId);
    resolve({
      requestId: data.requestId,
      ok: data.ok === true,
      message: typeof data.message === "string" ? data.message : undefined,
      output: typeof data.output === "string" ? data.output : undefined,
    });
  });

  post({ v: 1, type: "ready" });

  return {
    get delivery() {
      return delivery;
    },
    onItem(listener) {
      if (delivery !== null) listener(delivery);
      else listeners.push(listener);
    },
    async invoke(pluginId, commandId, text) {
      const answer = await request({ type: "invoke", pluginId, commandId, text });
      return { ok: answer.ok, message: answer.message, output: answer.output };
    },
    copy(text) {
      return request({ type: "copy", text });
    },
    close() {
      post({ v: 1, type: "close" });
    },
  };
}
