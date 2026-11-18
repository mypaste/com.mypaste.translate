/**
 * The translation page (protocol v3, surface role "open").
 *
 * The host hands over one text entry. The page keeps its paragraph rhythm: the
 * original stays in place and each paragraph gains its translation directly
 * underneath, so the two can be read side by side. Translation is on demand:
 * one explicit click starts it, paragraphs are queued as they scroll into view,
 * and at most three requests run at once through this package's own runtime.
 *
 * Every word of the original is clickable. A click asks the runtime for a
 * dictionary card — phonetic, part of speech, common senses — and shows it next
 * to the word. Nothing the host sends is ever inserted as HTML.
 */

import { connectSurface, type InvokeResult, type SurfaceDelivery } from "./bridge";
import {
  WORD_WRAP_LIMIT,
  cardText,
  isTarget,
  lookupable,
  parseParagraph,
  parseSettingsAnswer,
  parseWord,
  partLabel,
  splitBlocks,
  targetLabel,
  tokenize,
  type Block,
  type ProviderInfo,
  type WordCard,
} from "./text";

const PLUGIN_ID = "com.mypaste.translate";
const COMMANDS = {
  paragraph: "translate.paragraph",
  word: "translate.word",
  settings: "translate.settings",
} as const;

const CONCURRENCY = 3;
const CARD_CACHE_LIMIT = 200;

type BlockStatus = "idle" | "loading" | "done" | "error";

interface ReadingBlock {
  index: number;
  source: Block;
  node: HTMLElement;
  translation: HTMLParagraphElement;
  status: BlockStatus;
  error: string;
}

interface PageState {
  blocks: ReadingBlock[];
  queue: ReadingBlock[];
  active: number;
  translating: boolean;
  /** Bumped whenever queued answers stop applying (new item, new service). */
  epoch: number;
  provider: string;
  model: string;
  providers: ProviderInfo[];
  target: string;
  targetTouched: boolean;
  detectedSource: string;
  detectedTarget: string;
  openTerm: string;
  anchor: DOMRect | null;
  currentCard: WordCard | null;
  cards: Map<string, WordCard>;
  cache: Map<string, string>;
}

const state: PageState = {
  blocks: [],
  queue: [],
  active: 0,
  translating: false,
  epoch: 0,
  provider: "bilibili",
  model: "",
  providers: [],
  target: "auto",
  targetTouched: false,
  detectedSource: "",
  detectedTarget: "",
  openTerm: "",
  anchor: null,
  currentCard: null,
  cards: new Map(),
  cache: new Map(),
};

const bridge = connectSurface();

function one<T extends Element>(selector: string): T {
  const found = document.querySelector<T>(selector);
  if (found === null) throw new Error(`页面缺少 ${selector}`);
  return found;
}

const docTitle = one<HTMLHeadingElement>("#doc-title");
const blocksHost = one<HTMLDivElement>("#blocks");
const emptyState = one<HTMLDivElement>("#empty");
const progress = one<HTMLParagraphElement>("#progress");
const sourceHint = one<HTMLParagraphElement>("#source-hint");
const translateButton = one<HTMLButtonElement>("#translate-button");
const providerSelect = one<HTMLSelectElement>("#provider-select");
const providerField = one<HTMLElement>("#provider-field");
const modelSelect = one<HTMLSelectElement>("#model-select");
const modelField = one<HTMLElement>("#model-field");
const targetSelect = one<HTMLSelectElement>("#target-select");
const docClose = one<HTMLButtonElement>("#doc-close");
const reader = one<HTMLElement>("#reader");

const card = one<HTMLElement>("#word-card");
const cardTerm = one<HTMLHeadingElement>("#card-term");
const cardPhonetic = one<HTMLParagraphElement>("#card-phonetic");
const cardMain = one<HTMLParagraphElement>("#card-main");
const cardEntries = one<HTMLDivElement>("#card-entries");
const cardSource = one<HTMLSpanElement>("#card-source");
const cardCopy = one<HTMLButtonElement>("#card-copy");
const cardClose = one<HTMLButtonElement>("#card-close");

const blockByNode = new Map<Element, ReadingBlock>();
let activeWord: HTMLElement | null = null;

const LANGUAGE_LABELS: Record<string, string> = {
  auto: "自动",
  zh: "中文",
  "zh-cn": "简体中文",
  "zh-tw": "繁體中文",
  en: "英语",
  ja: "日语",
  ko: "韩语",
  fr: "法语",
  de: "德语",
  es: "西班牙语",
  pt: "葡萄牙语",
  ru: "俄语",
  it: "意大利语",
};

/** languageLabel speaks the reader's language, falling back to the code. */
function languageLabel(code: string): string {
  if (code === "") return "";
  const key = code.toLowerCase();
  const base = key.split("-")[0] ?? key;
  return LANGUAGE_LABELS[key] ?? LANGUAGE_LABELS[base] ?? code;
}

function currentProvider(): ProviderInfo | null {
  return state.providers.find((info) => info.id === state.provider) ?? null;
}

function providerName(): string {
  return currentProvider()?.name ?? "翻译服务";
}

/** providerValue is the one value the service select carries per choice. */
function providerValue(provider: string, model: string): string {
  return model === "" ? provider : `${provider}:${model}`;
}

function cacheKey(target: string, text: string): string {
  return `${state.provider}\u0000${state.model}\u0000${target}\u0000${text}`;
}

function refreshHint(): void {
  if (state.detectedSource !== "") {
    const target = state.detectedTarget !== ""
      ? state.detectedTarget
      : state.target === "auto" ? "zh-CN" : state.target;
    sourceHint.textContent =
      `${languageLabel(state.detectedSource)} → ${languageLabel(target)}` +
      ` · 译文来自 ${providerName()}`;
    return;
  }
  sourceHint.textContent = "原文会分段发往所选的翻译服务，结果只留在本页。";
}

function refreshProgress(): void {
  const total = state.blocks.length;
  if (total === 0) {
    progress.textContent = "";
    return;
  }
  let done = 0;
  let failed = 0;
  for (const block of state.blocks) {
    if (block.status === "done") done += 1;
    else if (block.status === "error") failed += 1;
  }
  if (state.translating) {
    progress.textContent = `正在翻译 ${done}/${total} 段`;
    return;
  }
  if (done === total) {
    progress.textContent = `全文 ${total} 段已译完`;
    return;
  }
  if (failed > 0) {
    progress.textContent = `已译 ${done}/${total} 段 · ${failed} 段未译出`;
    return;
  }
  progress.textContent = done > 0 ? `已译 ${done}/${total} 段 · 已停止` : "";
}

function refreshButton(): void {
  const total = state.blocks.length;
  translateButton.disabled = total === 0;
  if (state.translating) {
    translateButton.textContent = "停止";
    translateButton.classList.add("is-running");
    return;
  }
  translateButton.classList.remove("is-running");
  if (total > 0 && state.blocks.every((block) => block.status === "done")) {
    translateButton.textContent = "重译";
    return;
  }
  const started = state.blocks.some(
    (block) => block.status === "done" || block.status === "error",
  );
  translateButton.textContent = started ? "继续" : "翻译";
}

function refreshControls(): void {
  refreshHint();
  refreshProgress();
  refreshButton();
}

function setTranslation(block: ReadingBlock, text: string): void {
  block.translation.className = "translation";
  block.translation.textContent = text;
  block.translation.hidden = false;
}

function setPending(block: ReadingBlock): void {
  block.translation.className = "translation pending";
  block.translation.textContent = "翻译中…";
  block.translation.hidden = false;
}

function setFailure(block: ReadingBlock, message: string): void {
  block.translation.className = "translation failed";
  block.translation.replaceChildren();
  block.translation.append(document.createTextNode(message));
  const retry = document.createElement("button");
  retry.type = "button";
  retry.className = "retry";
  retry.dataset.retry = String(block.index);
  retry.textContent = "重试";
  block.translation.append(retry);
  block.translation.hidden = false;
}

function clearTranslation(block: ReadingBlock): void {
  block.translation.className = "translation";
  block.translation.replaceChildren();
  block.translation.hidden = true;
}

/**
 * settleIfDone ends the run once every paragraph has an answer. Paragraphs
 * that never entered the viewport stay idle, so a long entry keeps waiting for
 * the reader's scroll instead of being declared finished.
 */
function settleIfDone(): void {
  if (!state.translating) return;
  if (state.active > 0 || state.queue.length > 0) return;
  if (state.blocks.some((block) => block.status === "idle" || block.status === "loading")) {
    return;
  }
  state.translating = false;
}

function pump(): void {
  while (state.active < CONCURRENCY && state.queue.length > 0) {
    const block = state.queue.shift();
    if (block === undefined) break;
    state.active += 1;
    void translateBlock(block).finally(() => {
      state.active -= 1;
      pump();
      settleIfDone();
      refreshProgress();
      refreshButton();
    });
  }
  refreshProgress();
}

function enqueue(block: ReadingBlock, force = false): void {
  if (block.status !== "idle") return;
  if (!state.translating && !force) return;
  const key = cacheKey(state.target, block.source.text);
  const cached = state.cache.get(key);
  if (cached !== undefined) {
    block.status = "done";
    setTranslation(block, cached);
    refreshProgress();
    refreshButton();
    return;
  }
  if (state.queue.includes(block)) return;
  block.status = "loading";
  setPending(block);
  state.queue.push(block);
  pump();
}

async function translateBlock(block: ReadingBlock): Promise<void> {
  const epoch = state.epoch;
  const target = state.target;
  const provider = state.provider;
  const model = state.model;
  let answer: InvokeResult;
  try {
    answer = await bridge.invoke(
      PLUGIN_ID,
      COMMANDS.paragraph,
      JSON.stringify({
        text: block.source.text,
        provider,
        model,
        target,
      }),
    );
  } catch (error) {
    answer = {
      ok: false,
      message: error instanceof Error ? error.message : "翻译失败",
    };
  }
  if (epoch !== state.epoch) {
    // The item or the direction changed while the answer was in flight. The
    // reset already put this block back to idle; drop the stale answer.
    if (block.status === "loading") {
      block.status = "idle";
      clearTranslation(block);
    }
    return;
  }
  if (!answer.ok) {
    block.status = "error";
    block.error = answer.message ?? "翻译失败";
    setFailure(block, block.error);
    return;
  }
  const parsed = parseParagraph(answer.output);
  if (parsed === null) {
    block.status = "error";
    block.error = "译文无法解析";
    setFailure(block, block.error);
    return;
  }
  state.cache.set(cacheKey(target, block.source.text), parsed.translation);
  block.status = "done";
  block.error = "";
  setTranslation(block, parsed.translation);
  if (state.detectedSource === "" && parsed.source !== "") {
    state.detectedSource = parsed.source;
    state.detectedTarget = parsed.target;
    refreshHint();
  }
}

/** queueAroundViewport starts the paragraphs the reader can already see. */
function queueAroundViewport(): void {
  const view = reader.getBoundingClientRect();
  for (const block of state.blocks) {
    const rect = block.node.getBoundingClientRect();
    const near = rect.bottom >= view.top - 600 && rect.top <= view.bottom + 600;
    if (near) enqueue(block);
  }
}

function startTranslating(): void {
  if (state.blocks.length === 0) return;
  state.translating = true;
  for (const block of state.blocks) {
    if (block.status === "error") {
      block.status = "idle";
      block.error = "";
      clearTranslation(block);
    }
  }
  refreshButton();
  queueAroundViewport();
  settleIfDone();
  refreshControls();
}

function stopTranslating(): void {
  state.translating = false;
  state.queue = [];
  refreshButton();
  refreshProgress();
}

function resetBlocks(): void {
  state.epoch += 1;
  state.queue = [];
  for (const block of state.blocks) {
    block.status = "idle";
    block.error = "";
    clearTranslation(block);
  }
}

const observer = new IntersectionObserver(
  (entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      const block = blockByNode.get(entry.target);
      if (block === undefined) continue;
      enqueue(block);
    }
  },
  { root: reader, rootMargin: "600px 0px 0px 0px" },
);

function showEmpty(): void {
  emptyState.replaceChildren();
  const strong = document.createElement("strong");
  strong.textContent = "这条条目没有可翻译的文字";
  const detail = document.createElement("span");
  detail.textContent = "翻译对照只打开文本条目；图片或文件条目不会出现在这里。";
  emptyState.append(strong, detail);
  emptyState.hidden = false;
}

function renderBlocks(blocks: Block[]): void {
  observer.disconnect();
  blockByNode.clear();
  blocksHost.replaceChildren();
  state.blocks = [];
  for (const [index, source] of blocks.entries()) {
    const node = document.createElement("article");
    node.className = "block";
    node.dataset.index = String(index);
    const sourceLine = document.createElement("p");
    sourceLine.className = "source";
    if (source.text.length > WORD_WRAP_LIMIT) {
      sourceLine.textContent = source.text;
    } else {
      for (const span of tokenize(source.text)) {
        if (!span.word) {
          sourceLine.append(document.createTextNode(span.text));
          continue;
        }
        const word = document.createElement("span");
        word.className = "word";
        word.dataset.word = span.text;
        word.textContent = span.text;
        sourceLine.append(word);
      }
    }
    const translation = document.createElement("p");
    translation.className = "translation";
    translation.hidden = true;
    node.append(sourceLine, translation);
    blocksHost.append(node);
    const block: ReadingBlock = {
      index,
      source,
      node,
      translation,
      status: "idle",
      error: "",
    };
    state.blocks.push(block);
    blockByNode.set(node, block);
    observer.observe(node);
  }
}

function renderItem(delivery: SurfaceDelivery): void {
  const item = delivery.item;
  closeCard();
  const title = item.title.trim();
  docTitle.textContent = title !== "" ? title : "翻译对照";
  document.title = `${docTitle.textContent} — 翻译对照`;

  state.epoch += 1;
  state.cache.clear();
  state.cards.clear();
  state.queue = [];
  state.translating = false;
  state.detectedSource = "";
  state.detectedTarget = "";

  const blocks = splitBlocks(item.text);
  if (blocks.length === 0) {
    observer.disconnect();
    blockByNode.clear();
    blocksHost.replaceChildren();
    state.blocks = [];
    showEmpty();
  } else {
    emptyState.hidden = true;
    renderBlocks(blocks);
  }
  refreshControls();
  reader.scrollTop = 0;
}

function toggleTranslation(): void {
  if (state.translating) {
    stopTranslating();
    return;
  }
  if (state.blocks.length === 0) return;
  if (state.blocks.every((block) => block.status === "done")) {
    state.cache.clear();
    resetBlocks();
  }
  startTranslating();
}

function persistSettings(): void {
  void bridge.invoke(
    PLUGIN_ID,
    COMMANDS.settings,
    JSON.stringify({
      op: "write",
      settings: {
        provider: state.provider,
        target: state.target,
        model: state.model,
      },
    }),
  );
}

/** resetForService drops every drawn translation after a direction change. */
function resetForService(): void {
  state.detectedSource = "";
  state.detectedTarget = "";
  const touched = state.blocks.some((block) => block.status !== "idle");
  resetBlocks();
  refreshControls();
  refreshHint();
  if (state.translating) {
    queueAroundViewport();
  } else if (touched) {
    refreshProgress();
  }
  if (state.openTerm !== "") {
    closeCard();
  }
}

function changeTarget(value: string): void {
  if (!isTarget(value) || value === state.target) return;
  const info = currentProvider();
  if (info !== null && !info.targets.includes(value)) return;
  state.target = value;
  state.targetTouched = true;
  resetForService();
  persistSettings();
}

function changeService(value: string): void {
  const info = state.providers.find((provider) => provider.id === value);
  if (info === undefined || info.id === state.provider) return;
  state.provider = info.id;
  state.model = info.defaultModel;
  state.targetTouched = true;
  if (!info.targets.includes(state.target)) {
    state.target = "auto";
  }
  buildModelOptions();
  buildTargetOptions();
  resetForService();
  persistSettings();
}

function changeModel(value: string): void {
  const info = currentProvider();
  if (info === null || value === state.model) return;
  if (!info.models.some((model) => model.id === value)) return;
  state.model = value;
  state.targetTouched = true;
  resetForService();
  persistSettings();
}

async function loadSettings(): Promise<void> {
  const answer = await bridge.invoke(
    PLUGIN_ID,
    COMMANDS.settings,
    JSON.stringify({ op: "read" }),
  );
  if (!answer.ok) return;
  const parsed = parseSettingsAnswer(answer.output);
  if (parsed === null || parsed.providers.length === 0) return;
  state.providers = parsed.providers;
  if (state.targetTouched) {
    buildProviderOptions();
    buildModelOptions();
    buildTargetOptions();
    providerField.hidden = false;
    return;
  }
  const info = state.providers.find((provider) => provider.id === parsed.settings.provider)
    ?? state.providers[0];
  if (info === undefined) return;
  state.provider = info.id;
  state.model = info.models.some((model) => model.id === parsed.settings.model)
    ? parsed.settings.model
    : info.defaultModel;
  state.target = info.targets.includes(parsed.settings.target)
    ? parsed.settings.target
    : "auto";
  buildProviderOptions();
  buildModelOptions();
  buildTargetOptions();
  providerField.hidden = false;
  state.detectedSource = "";
  state.detectedTarget = "";
  if (state.blocks.some((block) => block.status !== "idle")) {
    resetBlocks();
  }
  refreshControls();
  refreshHint();
  if (state.translating) queueAroundViewport();
}

function buildProviderOptions(): void {
  providerSelect.replaceChildren();
  for (const info of state.providers) {
    const option = document.createElement("option");
    option.value = info.id;
    option.textContent = info.name;
    providerSelect.append(option);
  }
  providerSelect.value = state.provider;
}

function buildModelOptions(): void {
  const info = currentProvider();
  const models = info?.models ?? [];
  modelSelect.replaceChildren();
  for (const model of models) {
    const option = document.createElement("option");
    option.value = model.id;
    option.textContent = model.name;
    modelSelect.append(option);
  }
  modelField.hidden = models.length === 0;
  if (models.length === 0) {
    state.model = "";
    return;
  }
  if (!models.some((model) => model.id === state.model)) {
    state.model = info?.defaultModel ?? models[0]?.id ?? "";
  }
  modelSelect.value = state.model;
}

function buildTargetOptions(): void {
  const info = currentProvider();
  const targets = info?.targets ?? ["auto"];
  targetSelect.replaceChildren();
  for (const code of targets) {
    const option = document.createElement("option");
    option.value = code;
    option.textContent = targetLabel(code);
    targetSelect.append(option);
  }
  targetSelect.value = state.target;
}

function placeCard(anchor: DOMRect): void {
  card.hidden = false;
  card.style.visibility = "hidden";
  const width = card.offsetWidth;
  const height = card.offsetHeight;
  card.style.visibility = "";
  const margin = 12;
  const left = Math.min(
    Math.max(margin, anchor.left),
    Math.max(margin, window.innerWidth - width - margin),
  );
  let top = anchor.bottom + 8;
  if (top + height > window.innerHeight - margin) {
    top = anchor.top - height - 8;
  }
  top = Math.max(margin, Math.min(top, window.innerHeight - height - margin));
  card.style.left = `${Math.round(left)}px`;
  card.style.top = `${Math.round(top)}px`;
}

function showCardLoading(term: string): void {
  cardTerm.textContent = term;
  cardPhonetic.textContent = "";
  cardPhonetic.hidden = true;
  cardMain.className = "card-main pending";
  cardMain.textContent = "正在查询释义…";
  cardMain.hidden = false;
  cardEntries.replaceChildren();
  cardSource.textContent = "";
  cardCopy.disabled = true;
  state.currentCard = null;
}

function showCardFailure(message: string): void {
  cardMain.className = "card-main failed";
  cardMain.textContent = message;
  cardMain.hidden = false;
  cardEntries.replaceChildren();
  cardSource.textContent = "";
  cardCopy.disabled = true;
  state.currentCard = null;
  if (state.anchor !== null) placeCard(state.anchor);
}

function renderCard(word: WordCard): void {
  state.currentCard = word;
  cardTerm.textContent = word.term;
  if (word.phonetic !== "") {
    cardPhonetic.textContent = `/${word.phonetic}/`;
    cardPhonetic.hidden = false;
  } else if (word.translit !== "") {
    cardPhonetic.textContent = word.translit;
    cardPhonetic.hidden = false;
  } else {
    cardPhonetic.textContent = "";
    cardPhonetic.hidden = true;
  }
  cardMain.className = "card-main";
  cardMain.textContent = word.translation;
  cardMain.hidden = word.translation === "";

  cardEntries.replaceChildren();
  for (const entry of word.entries) {
    const group = document.createElement("div");
    group.className = "sense-group";
    const part = document.createElement("span");
    part.className = "part";
    part.textContent = partLabel(entry.part);
    group.append(part);
    if (entry.terms.length > 0) {
      const terms = document.createElement("span");
      terms.className = "terms";
      terms.textContent = entry.terms.join(" · ");
      group.append(terms);
    }
    if (entry.senses.length > 0) {
      const list = document.createElement("ul");
      list.className = "senses";
      for (const sense of entry.senses) {
        const item = document.createElement("li");
        item.append(document.createTextNode(sense.word));
        if (sense.reverse.length > 0) {
          const reverse = document.createElement("span");
          reverse.className = "reverse";
          reverse.textContent = ` · ${sense.reverse.join(" / ")}`;
          item.append(reverse);
        }
        list.append(item);
      }
      group.append(list);
    }
    cardEntries.append(group);
  }

  const source = languageLabel(word.source);
  const service = providerName();
  cardSource.textContent = source === "" ? service : `${source} · ${service}`;
  cardCopy.disabled = false;
  cardCopy.textContent = "复制释义";
  if (state.anchor !== null) placeCard(state.anchor);
}

function rememberCard(key: string, word: WordCard): void {
  if (state.cards.size >= CARD_CACHE_LIMIT) {
    const oldest = state.cards.keys().next();
    if (!oldest.done) state.cards.delete(oldest.value);
  }
  state.cards.set(key, word);
}

async function lookupWord(term: string): Promise<void> {
  const target = state.target;
  const provider = state.provider;
  const model = state.model;
  const answer = await bridge.invoke(
    PLUGIN_ID,
    COMMANDS.word,
    JSON.stringify({ word: term, provider, model, target }),
  );
  if (state.openTerm !== term || target !== state.target ||
    provider !== state.provider || model !== state.model) return;
  if (!answer.ok) {
    showCardFailure(answer.message ?? "查询失败");
    return;
  }
  const word = parseWord(answer.output);
  if (word === null) {
    showCardFailure("查到的释义无法解析");
    return;
  }
  rememberCard(cacheKey(target, term), word);
  renderCard(word);
  if (state.detectedSource === "" && word.source !== "") {
    state.detectedSource = word.source;
    state.detectedTarget = word.target;
    refreshHint();
  }
}

function openWord(span: HTMLElement): void {
  const term = span.dataset.word ?? "";
  if (!lookupable(term)) return;
  if (activeWord !== null) delete activeWord.dataset.active;
  activeWord = span;
  span.dataset.active = "true";
  state.openTerm = term;
  const anchor = span.getBoundingClientRect();
  state.anchor = anchor;
  showCardLoading(term);
  placeCard(anchor);
  const cached = state.cards.get(cacheKey(state.target, term));
  if (cached !== undefined) {
    renderCard(cached);
    return;
  }
  void lookupWord(term);
}

function closeCard(): void {
  card.hidden = true;
  state.openTerm = "";
  state.anchor = null;
  state.currentCard = null;
  if (activeWord !== null) {
    delete activeWord.dataset.active;
    activeWord = null;
  }
}

translateButton.addEventListener("click", toggleTranslation);

targetSelect.addEventListener("change", () => {
  changeTarget(targetSelect.value);
});

providerSelect.addEventListener("change", () => {
  changeService(providerSelect.value);
});

modelSelect.addEventListener("change", () => {
  changeModel(modelSelect.value);
});

docClose.addEventListener("click", () => {
  bridge.close();
});

cardClose.addEventListener("click", closeCard);

cardCopy.addEventListener("click", () => {
  const word = state.currentCard;
  if (word === null) return;
  void bridge.copy(cardText(word)).then((result) => {
    cardCopy.textContent = result.ok ? "已复制" : "复制失败";
    window.setTimeout(() => {
      cardCopy.textContent = "复制释义";
    }, 1400);
  });
});

blocksHost.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const retry = target.closest("[data-retry]");
  if (retry instanceof HTMLElement) {
    const index = Number(retry.dataset.retry ?? "");
    const block = state.blocks[index];
    if (block !== undefined) {
      block.status = "idle";
      block.error = "";
      clearTranslation(block);
      enqueue(block, true);
      refreshControls();
    }
    return;
  }
  const word = target.closest(".word");
  if (word instanceof HTMLElement) openWord(word);
});

document.addEventListener("pointerdown", (event) => {
  if (card.hidden) return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  if (card.contains(target)) return;
  if (target.closest(".word") !== null) return;
  closeCard();
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !card.hidden) {
    event.stopPropagation();
    closeCard();
  }
});

buildTargetOptions();
refreshControls();
bridge.onItem(renderItem);
void loadSettings();
