"""The translation package: its manifest, its built page and its pure helpers.

The page's helpers (`src/text.ts`) are bundled with the same esbuild the
surface build uses and run under Node. The fixtures are short texts: no
request is made, no entry is read. The Go runtime has its own `go test` suite
under `src/internal/...`; this file covers the boundaries the page owns.

The manifest half pins the trust story: the page reads the entry through the
host, sends only paragraphs to the two declared translation hosts through the
host's `network.outbound` proxy, and keeps the reading preference in the
package's private storage. It never asks for `surface.network`, which is
reserved for pinned official pages (D-1089 / D-1095).
"""

from __future__ import annotations

import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ESBUILD = ROOT / "node_modules" / ".bin" / "esbuild"
NODE = shutil.which("node")
MAX_DOCUMENT_BYTES = 1 << 20

RUNNER = r"""
import assert from "node:assert/strict";
import path from "node:path";
import { pathToFileURL } from "node:url";

const work = process.argv[2];
const {
  WORD_WRAP_LIMIT, isTarget, lookupable, splitBlocks, tokenize,
  tokenizeFallback, parseParagraph, parseWord, parseSettingsAnswer,
  targetLabel, partLabel, cardText,
} = await import(pathToFileURL(path.join(work, "text.mjs")).href);

// -- paragraphs keep the entry's own rhythm --------------------------------
assert.deepEqual(splitBlocks("第一段\n\n第二段"), [{ text: "第一段" }, { text: "第二段" }]);
assert.deepEqual(
  splitBlocks("一行一段\n二行一段\n三行一段"),
  [{ text: "一行一段" }, { text: "二行一段" }, { text: "三行一段" }],
);
assert.deepEqual(splitBlocks("  \n\n  "), []);
assert.deepEqual(splitBlocks("  缩进的段落  \r\n\r\n下一段"), [
  { text: "缩进的段落" }, { text: "下一段" },
]);

// -- words: every byte is kept, words become clickable ---------------------
const sample = "Hello, 世界! It's fine.";
const spans = tokenize(sample);
assert.equal(spans.map((span) => span.text).join(""), sample);
assert.ok(spans.some((span) => span.word && span.text === "Hello"));
assert.ok(spans.every((span) => !span.word || span.text.trim() !== ""));

const fallback = tokenizeFallback("Hello 世界");
assert.equal(fallback.map((span) => span.text).join(""), "Hello 世界");
assert.deepEqual(
  fallback.filter((span) => span.word).map((span) => span.text),
  ["Hello", "世", "界"],
);

assert.equal(lookupable("Hello"), true);
assert.equal(lookupable("don't"), true);
assert.equal(lookupable("123"), false);
assert.equal(lookupable(""), false);
assert.equal(lookupable("a".repeat(49)), false);
assert.equal(WORD_WRAP_LIMIT, 20000);

// -- the runtime's answers are validated, never trusted --------------------
const paragraph = parseParagraph(JSON.stringify({
  translation: "你好", source: "en", target: "zh-CN",
}));
assert.equal(paragraph.translation, "你好");
assert.equal(paragraph.source, "en");
assert.equal(paragraph.target, "zh-CN");
assert.equal(parseParagraph("not json"), null);
assert.equal(parseParagraph(""), null);
assert.equal(parseParagraph(undefined), null);
assert.equal(parseParagraph(JSON.stringify({ source: "en" })), null);

const word = parseWord(JSON.stringify({
  source: "en",
  target: "zh-CN",
  word: {
    term: "run",
    phonetics: [{ label: "英", value: "rʌn" }, { label: "美", value: "rən" }],
    translit: "",
    translation: "跑",
    dictionary: "有道词典",
    entries: [{
      part: "vt.",
      terms: ["跑", "运行"],
      senses: [{ word: "跑步", reverse: ["run"] }],
    }],
  },
}));
assert.equal(word.term, "run");
assert.deepEqual(word.phonetics, [
  { label: "英", value: "rʌn" }, { label: "美", value: "rən" },
]);
assert.equal(word.dictionary, "有道词典");
assert.equal(partLabel(word.entries[0].part), "及物动词");
assert.equal(word.entries.length, 1);
assert.equal(word.entries[0].part, "vt.");
assert.equal(word.entries[0].senses[0].reverse[0], "run");
assert.equal(parseWord(JSON.stringify({ word: { translation: "跑" } })), null);

// A service without a dictionary still answers with term and translation.
const plain = parseWord(JSON.stringify({
  source: "en", target: "zh-CN",
  word: { term: "run", translation: "跑步" },
}));
assert.equal(plain.translation, "跑步");
assert.deepEqual(plain.entries, []);
assert.deepEqual(plain.phonetics, []);
assert.equal(plain.dictionary, "");

// -- settings: the runtime owns the offered services and directions --------
const providers = [
  {
    id: "google", name: "Google 翻译", description: "x",
    targets: ["auto", "zh-CN", "en", "fr"], models: [], defaultModel: "",
  },
  {
    id: "bilibili", name: "Bilibili Index-Translate", description: "y",
    targets: ["auto", "zh-CN", "en", "ja"],
    models: [{ id: "index-nailong-9b", name: "Index-Nailong-9B" }],
    defaultModel: "index-nailong-9b",
  },
];
const view = parseSettingsAnswer(JSON.stringify({
  settings: { provider: "bilibili", target: "ja", model: "index-nailong-9b" },
  providers,
}));
assert.equal(view.settings.provider, "bilibili");
assert.equal(view.settings.target, "ja");
assert.equal(view.settings.model, "index-nailong-9b");
assert.equal(view.providers.length, 2);
assert.deepEqual(view.providers[1].targets, ["auto", "zh-CN", "en", "ja"]);
assert.equal(view.providers[1].models[0].name, "Index-Nailong-9B");
assert.equal(view.providers[1].defaultModel, "index-nailong-9b");

assert.equal(parseSettingsAnswer(undefined), null);
assert.equal(parseSettingsAnswer("not json"), null);
assert.equal(parseSettingsAnswer(JSON.stringify({ settings: { provider: "google", target: "auto" }, providers: [] })), null);
assert.equal(parseSettingsAnswer(JSON.stringify({
  settings: { provider: "google", target: "klingon" },
  providers,
})), null);
assert.equal(parseSettingsAnswer(JSON.stringify({
  settings: { provider: "google", target: "auto" },
  providers: [{ id: "google", name: "Google 翻译", targets: [] }],
})), null);

// `auto` is the one literal the page may accept before the runtime answers.
assert.equal(isTarget("auto"), true);
assert.equal(isTarget("zh-CN"), true);
assert.equal(isTarget("auto extra"), false);
assert.equal(targetLabel("zh-CN"), "简体中文");
assert.equal(targetLabel("xx"), "xx");
assert.equal(partLabel("verb"), "动词");
assert.equal(partLabel("vt."), "及物动词");
assert.equal(partLabel("n."), "名词");
assert.equal(partLabel("other"), "other");

assert.equal(
  cardText({
    term: "run",
    phonetics: [{ label: "英", value: "rʌn" }, { label: "美", value: "rən" }],
    translit: "", translation: "跑", dictionary: "有道词典",
    entries: [{ part: "verb", terms: ["跑"], senses: [] }],
    source: "en", target: "zh-CN",
  }),
  "run\n英 /rʌn/  美 /rən/\n跑\n动词  跑",
);

console.log("translate helpers: ok");
"""


class TranslateManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.manifest = json.loads(
            (ROOT / "mypaste.plugin.json").read_text(encoding="utf-8"))

    def test_the_page_opens_a_text_entry(self) -> None:
        manifest = self.manifest
        self.assertEqual(manifest["manifestVersion"], 3)
        self.assertEqual(manifest["kind"], "ui")
        self.assertEqual(manifest["id"], "com.mypaste.translate")
        self.assertEqual(manifest["surface"], {
            "entry": "preview.html",
            "forms": ["text"],
            "role": "open",
            "chrome": "page",
        })

    def test_every_translation_host_is_declared(self) -> None:
        runtime = self.manifest["runtime"]
        self.assertEqual(runtime["mode"], "command")
        self.assertEqual(runtime["entrypoint"], "translate")
        self.assertEqual(
            sorted(runtime["networkHosts"]),
            [
                "api.deepseek.com",
                "api.mymemory.translated.net",
                "dict.youdao.com",
                "index-translate.bilibili.com",
                "translate.googleapis.com",
            ],
        )
        self.assertEqual(runtime["configuredHosts"], ["openaiurl", "claudeurl"])
        description = self.manifest["description"]
        self.assertLessEqual(len(description), 280)
        self.assertIn("translate.googleapis.com", description)
        self.assertIn("api.mymemory.translated.net", description)
        self.assertIn("api.deepseek.com", description)
        self.assertIn("dict.youdao.com", description)

    def test_the_keys_are_host_held_secrets(self) -> None:
        # D-1176, D-1185: the manifest declares each key, the host keeps it and
        # adds it to the requests that leave for the address it is bound to.
        settings = {setting["key"]: setting for setting in self.manifest["settings"]}
        runtime = self.manifest["runtime"]
        deepseek = settings["apiKey"]
        self.assertEqual(deepseek["type"], "secret")
        self.assertEqual(deepseek["host"], "api.deepseek.com")
        self.assertIn(deepseek["host"], runtime["networkHosts"])
        openai = settings["openaikey"]
        self.assertEqual(openai["type"], "secret")
        self.assertEqual(openai["host"], "openaiurl")
        self.assertIn(openai["host"], runtime["configuredHosts"])
        self.assertNotIn("header", openai)
        claude = settings["claudekey"]
        self.assertEqual(claude["type"], "secret")
        self.assertEqual(claude["host"], "claudeurl")
        self.assertIn(claude["host"], runtime["configuredHosts"])
        self.assertEqual(claude["header"], "x-api-key")
        for setting in settings.values():
            if setting["type"] == "secret":
                self.assertNotIn("default", setting)
        for key in ("openaiurl", "claudeurl"):
            self.assertEqual(settings[key]["type"], "string")
            self.assertIn("https", settings[key]["description"])
        self.assertIn("network.outbound", self.manifest["capabilities"])
        self.assertEqual(self.manifest["version"], "1.5.0")

    def test_capabilities_stay_inside_the_closed_list(self) -> None:
        capabilities = set(self.manifest["capabilities"])
        self.assertEqual(
            capabilities,
            {"entity.read", "network.outbound", "storage.plugin"},
        )
        self.assertNotIn("surface.network", capabilities)
        for command in self.manifest["commands"]:
            self.assertLessEqual(set(command["capabilities"]), capabilities)

    def test_commands_are_the_pages_own(self) -> None:
        commands = {command["id"]: command for command in self.manifest["commands"]}
        self.assertEqual(
            set(commands),
            {"translate.paragraph", "translate.word", "translate.settings"},
        )
        for command in commands.values():
            self.assertNotIn("placement", command)
            self.assertEqual(command["forms"], ["text"])


class TranslatePageTests(unittest.TestCase):
    def test_the_built_page_is_one_self_contained_document(self) -> None:
        page = ROOT / "preview.html"
        self.assertTrue(page.exists(), "run bun run build")
        document = page.read_text(encoding="utf-8")
        self.assertLessEqual(
            len(document.encode("utf-8")), MAX_DOCUMENT_BYTES,
            "the host refuses a surface document over 1 MiB (D-1091)")
        self.assertNotIn("<!--bundle-->", document)
        self.assertNotIn("<script src=", document)
        self.assertNotIn("<link rel=\"stylesheet\"", document)
        for marker in (
            "translate.paragraph", "translate.word", "translate.settings",
            "provider-select", "model-select",
        ):
            self.assertIn(marker, document)


@unittest.skipUnless(NODE and ESBUILD.exists(), "node and esbuild are needed")
class TranslateHelperTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.work = Path(tempfile.mkdtemp(prefix="translate-helpers-"))
        built = subprocess.run(
            [
                str(ESBUILD), "src/text.ts", "--bundle", "--format=esm",
                "--platform=node", f"--outfile={cls.work / 'text.mjs'}",
            ],
            cwd=ROOT, capture_output=True, text=True,
        )
        if built.returncode != 0:
            raise AssertionError(f"esbuild failed: {built.stderr}")
        cls.runner = cls.work / "runner.mjs"
        cls.runner.write_text(RUNNER, encoding="utf-8")

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.work, ignore_errors=True)

    def test_reading_helpers(self) -> None:
        result = subprocess.run(
            [NODE, str(self.runner), str(self.work)],
            capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("translate helpers: ok", result.stdout)


if __name__ == "__main__":
    unittest.main()
