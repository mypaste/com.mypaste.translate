/**
 * Pure reading helpers for the translation page: how plain text is split into
 * paragraphs, how each paragraph is split into clickable words, and how the
 * runtime's JSON answers are read. Nothing here touches the DOM or the host,
 * so the same functions run in the page and in the page's unit test.
 */

/** One paragraph of the entry, in the order it will be drawn. */
export interface Block {
  text: string;
}

/** One piece of a paragraph: either a lookupable word or the text between. */
export interface WordSpan {
  text: string;
  word: boolean;
}

export interface ParagraphAnswer {
  translation: string;
  source: string;
  target: string;
}

export interface WordSense {
  word: string;
  reverse: string[];
}

export interface WordEntry {
  part: string;
  terms: string[];
  senses: WordSense[];
}

/** One pronunciation, labelled by accent when the dictionary names one. */
export interface WordPhonetic {
  label: string;
  value: string;
}

export interface WordCard {
  term: string;
  phonetics: WordPhonetic[];
  translit: string;
  translation: string;
  entries: WordEntry[];
  dictionary: string;
  source: string;
  target: string;
}

export interface ReadingSettings {
  provider: string;
  target: string;
  model: string;
}

/** One model a translation service offers. */
export interface ModelInfo {
  id: string;
  name: string;
}

/** One translation service exactly as the runtime declares it. */
export interface ProviderInfo {
  id: string;
  name: string;
  description: string;
  targets: string[];
  models: ModelInfo[];
  defaultModel: string;
}

/** The runtime's answer to translate.settings: the choice plus the choices. */
export interface SettingsAnswer {
  settings: ReadingSettings;
  providers: ProviderInfo[];
}

/**
 * The offered directions. The runtime owns which services accept which
 * direction; this table only names each code for the reader.
 */
export const TARGETS: { code: string; label: string }[] = [
  { code: "auto", label: "自动" },
  { code: "zh-CN", label: "简体中文" },
  { code: "zh-TW", label: "繁體中文" },
  { code: "en", label: "English" },
  { code: "ja", label: "日本語" },
  { code: "ko", label: "한국어" },
  { code: "fr", label: "Français" },
  { code: "de", label: "Deutsch" },
  { code: "es", label: "Español" },
  { code: "pt", label: "Português" },
  { code: "ru", label: "Русский" },
  { code: "it", label: "Italiano" },
  { code: "ar", label: "العربية" },
  { code: "nl", label: "Nederlands" },
  { code: "th", label: "ไทย" },
  { code: "vi", label: "Tiếng Việt" },
];

/** targetLabel names a direction; an unknown code reads as itself. */
export function targetLabel(code: string): string {
  return TARGETS.find((entry) => entry.code === code)?.label ?? code;
}

/** The one syntax a language code may take before it reaches the runtime. */
const TARGET_PATTERN = /^[a-z]{2,3}(?:-[A-Za-z]{2,8})?$/;

export function isTarget(value: unknown): value is string {
  return typeof value === "string" &&
    (value === "auto" || TARGET_PATTERN.test(value));
}

/** A block longer than this keeps its text but loses clickable words. */
export const WORD_WRAP_LIMIT = 20000;

/**
 * splitBlocks keeps the entry's own paragraph rhythm. Blank lines are the
 * primary boundary; a text whose lines each hold a paragraph (the shape most
 * copied lists and articles take) falls back to one block per line when no
 * blank line exists.
 */
export function splitBlocks(raw: string): Block[] {
  const text = raw.replace(/\r\n?/g, "\n").trim();
  if (text === "") return [];
  let pieces = text.split(/\n[ \t]*\n+/);
  if (pieces.length === 1 && (text.match(/\n/g) ?? []).length >= 2) {
    pieces = text.split("\n");
  }
  return pieces
    .map((piece) => piece.trim())
    .filter((piece) => piece !== "")
    .map((piece) => ({ text: piece }));
}

type SegmentResult = { segment: string; isWordLike?: boolean };
type SegmenterLike = {
  segment(input: string): Iterable<SegmentResult>;
};

function segmenter(): SegmenterLike | null {
  const intl = Intl as unknown as {
    Segmenter?: new (
      locales?: string | string[],
      options?: { granularity?: "word" },
    ) => SegmenterLike;
  };
  if (typeof intl.Segmenter !== "function") return null;
  try {
    return new intl.Segmenter(undefined, { granularity: "word" });
  } catch {
    return null;
  }
}

const LETTER = /\p{L}/u;
const WORD = /[\p{L}\p{M}\p{N}]+(?:[’'\-][\p{L}\p{M}\p{N}]+)*/gu;
const HAN = /^\p{Script=Han}+$/u;

/** A term is lookupable when it has a letter and is not a wall of text. */
export function lookupable(term: string): boolean {
  return term.length > 0 && term.length <= 48 && LETTER.test(term);
}

/**
 * tokenize splits a paragraph into spans. Words carry the original text and
 * become clickable; everything else stays plain text.
 */
export function tokenize(text: string): WordSpan[] {
  const engine = segmenter();
  if (engine !== null) {
    const spans: WordSpan[] = [];
    for (const part of engine.segment(text)) {
      if (part.segment === "") continue;
      spans.push({
        text: part.segment,
        word: part.isWordLike === true && lookupable(part.segment),
      });
    }
    return spans;
  }
  return tokenizeFallback(text);
}

/**
 * tokenizeFallback is the path for a host without Intl.Segmenter. Han runs are
 * split per character because a run of ideographs is not one word.
 */
export function tokenizeFallback(text: string): WordSpan[] {
  const spans: WordSpan[] = [];
  let at = 0;
  for (const match of text.matchAll(WORD)) {
    const index = match.index ?? 0;
    if (index > at) spans.push({ text: text.slice(at, index), word: false });
    const run = match[0];
    if (HAN.test(run)) {
      for (const character of run) {
        spans.push({ text: character, word: lookupable(character) });
      }
    } else {
      spans.push({ text: run, word: lookupable(run) });
    }
    at = index + run.length;
  }
  if (at < text.length) spans.push({ text: text.slice(at), word: false });
  return spans;
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : null;
}

function strings(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string");
}

export function parseParagraph(raw: string | undefined): ParagraphAnswer | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  const body = record(value);
  if (body === null || typeof body.translation !== "string") return null;
  return {
    translation: body.translation,
    source: typeof body.source === "string" ? body.source : "",
    target: typeof body.target === "string" ? body.target : "",
  };
}

export function parseWord(raw: string | undefined): WordCard | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  const body = record(value);
  const word = record(body?.word);
  if (body === null || word === null || typeof word.term !== "string") return null;
  const entries: WordEntry[] = [];
  if (Array.isArray(word.entries)) {
    for (const item of word.entries) {
      const entry = record(item);
      if (entry === null || typeof entry.part !== "string") continue;
      const senses: WordSense[] = [];
      if (Array.isArray(entry.senses)) {
        for (const sense of entry.senses) {
          const value = record(sense);
          if (value === null || typeof value.word !== "string") continue;
          senses.push({ word: value.word, reverse: strings(value.reverse) });
        }
      }
      entries.push({
        part: entry.part,
        terms: strings(entry.terms),
        senses,
      });
    }
  }
  const phonetics: WordPhonetic[] = [];
  if (Array.isArray(word.phonetics)) {
    for (const item of word.phonetics) {
      const phonetic = record(item);
      if (phonetic === null || typeof phonetic.value !== "string") continue;
      const value = phonetic.value.trim();
      if (value === "") continue;
      phonetics.push({
        label: typeof phonetic.label === "string" ? phonetic.label : "",
        value,
      });
    }
  }
  return {
    term: word.term,
    phonetics,
    translit: typeof word.translit === "string" ? word.translit : "",
    translation: typeof word.translation === "string" ? word.translation : "",
    entries,
    dictionary: typeof word.dictionary === "string" ? word.dictionary : "",
    source: typeof body.source === "string" ? body.source : "",
    target: typeof body.target === "string" ? body.target : "",
  };
}

/**
 * parseSettingsAnswer reads the runtime's settings view: the chosen service,
 * direction and model, plus the closed list of services the page may offer.
 * One malformed entry drops the whole answer, so the page keeps its defaults
 * rather than drawing half a list.
 */
export function parseSettingsAnswer(raw: string | undefined): SettingsAnswer | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  const body = record(value);
  const settings = record(body?.settings);
  if (body === null || settings === null) return null;
  if (typeof settings.provider !== "string" || !isTarget(settings.target)) return null;
  if (!Array.isArray(body.providers)) return null;
  const providers: ProviderInfo[] = [];
  for (const item of body.providers) {
    const entry = record(item);
    if (entry === null || typeof entry.id !== "string" ||
      typeof entry.name !== "string") return null;
    const targets = strings(entry.targets);
    if (targets.length === 0) return null;
    const models: ModelInfo[] = [];
    if (Array.isArray(entry.models)) {
      for (const model of entry.models) {
        const info = record(model);
        if (info === null || typeof info.id !== "string" ||
          typeof info.name !== "string") return null;
        models.push({ id: info.id, name: info.name });
      }
    }
    providers.push({
      id: entry.id,
      name: entry.name,
      description: typeof entry.description === "string" ? entry.description : "",
      targets,
      models,
      defaultModel: typeof entry.defaultModel === "string" ? entry.defaultModel : "",
    });
  }
  if (providers.length === 0) return null;
  return {
    settings: {
      provider: settings.provider,
      target: settings.target,
      model: typeof settings.model === "string" ? settings.model : "",
    },
    providers,
  };
}

const PART_LABELS: Record<string, string> = {
  verb: "动词",
  noun: "名词",
  adjective: "形容词",
  adverb: "副词",
  pronoun: "代词",
  preposition: "介词",
  conjunction: "连词",
  interjection: "感叹词",
  numeral: "数词",
  article: "冠词",
  particle: "助词",
  exclamation: "感叹词",
  // Dictionaries abbreviate parts of speech with a trailing period.
  v: "动词",
  vt: "及物动词",
  vi: "不及物动词",
  aux: "助动词",
  n: "名词",
  adj: "形容词",
  adv: "副词",
  prep: "介词",
  conj: "连词",
  pron: "代词",
  num: "数词",
  art: "冠词",
  int: "感叹词",
  abbr: "缩写",
  phr: "短语",
};

/** partLabel speaks the reader's language; unknown labels pass through. */
export function partLabel(part: string): string {
  const key = part.trim().toLowerCase().replace(/\.$/, "");
  return PART_LABELS[key] ?? part;
}

/** cardText is what the card's copy button puts on the clipboard. */
export function cardText(card: WordCard): string {
  const lines: string[] = [card.term];
  if (card.phonetics.length > 0) {
    lines.push(card.phonetics
      .map((phonetic) =>
        `${phonetic.label === "" ? "" : `${phonetic.label} `}/${phonetic.value}/`
      )
      .join("  "));
  } else if (card.translit !== "") {
    lines.push(card.translit);
  }
  if (card.translation !== "") lines.push(card.translation);
  for (const entry of card.entries) {
    if (entry.terms.length > 0) {
      lines.push(`${partLabel(entry.part)}  ${entry.terms.join(" · ")}`);
    }
  }
  return lines.join("\n");
}
