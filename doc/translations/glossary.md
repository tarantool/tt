# Glossary for the Russian translations

The Russian pages of this site share one vocabulary. This page fixes it, so
that a term is not chosen anew on every page and a reader does not meet two
names for one thing. It is for translators: the site does not link to it.

Rules of the translations themselves - where a translation lives, what a
heading keeps, how the nav and the command reference behave - are in
[Contributing](../contributing.md#translations).

## Terms

A term with a fixed choice keeps that spelling in every Russian page. A dash
in the right-hand column means the row is a decision, not a description of
existing text.

| term | translation | notes |
| --- | --- | --- |
| rock | rock | Latin, never `рок`. Plural `rocks`; inflected as a Russian word of the same gender: `rock'а`, `rock'и`. |
| rockspec | rockspec | Latin, never declined; `файл rockspec` when a noun is wanted. |
| rock server | rock-сервер | Hyphen, lowercase, declined: `rock-серверы`, `rock-сервером`. The page that describes them is `concepts/rock-servers.md`, and its title follows this row. |
| rock index | rock-индекс | Hyphen, lowercase. The file name stays `tt-rocks-index.json`. |
| source rock | исходный rock | The format name `rock-src` stays Latin. |
| binary rock | бинарный rock | A rock for one platform. |
| manifest | манифест | The file itself stays `app.manifest.toml`; LuaRocks' own file is `манифест LuaRocks`. |
| lock, lock file | локфайл | One word, as `whats-new.md` and `migration_from_older_versions.md` write it. The file itself stays `app.manifest.lock`. |
| package | пакет | Also `пакет tt` where `package` alone is ambiguous. |
| scope | область установки (scope) | First mention spells both; later `scope` alone, as `whats-new.md` does. `scope проекта` is the project scope. |
| revision | ревизия | `номер ревизии` where it is the number. |
| runtime | runtime | Latin. `среда выполнения` only where the sentence cannot carry the Latin word. |
| build backend | backend сборки | Latin `backend`, Russian `сборки`. |
| path dependency | зависимость по пути | Or `зависимость через path` where the field name matters. |
| yanked | `"yanked": true` | The field stays Latin; in prose, `отозванная версия`. |
| dirty | dirty | The state of a version stays Latin: `версия dirty`. |
| workspace | workspace | Latin, inflected with an apostrophe: `в workspace'е`. Emphasized only in EmmyLua contexts. |
| source tree | исходное дерево | Not `сорцы`. |
| CI | CI | Latin; `в CI`, `пайплайн CI`. |

Names stay Latin and monospaced in every page: commands and their flags,
paths, file names, field names, environment variables, product names
(`Tarantool`, `tt`, `TCM`, `LuaRocks`, `LuaJIT`, `EmmyLua`, `LuaLS`),
and command output.

## Known disagreements

The pages below still carry a term the rows above would spell otherwise.
They are recorded here rather than fixed: a translation catches up with its
English page, and the term is corrected when that page is next touched.

- `rock-модуль` against `rock`: `how-to/binaries.md`,
  `how-to/wasm-components.md`, `whats-new.md`, `get-started/install.md`
  and `reference/environment.md` say `rock-модуль` where a rock is meant
  as a unit; the term for the artifact is `rock`.

`whats-new.md` and `migration_from_older_versions.md` are written in
Russian on purpose, not translated line by line: their wording, their
lists and their terms are the reference for the rest, `локфайл` and
`область установки (scope)` among them.

## Anchors

A translated heading carries the anchor of the English heading, so that a
link to a section works in both languages:

```markdown
## Сборка из исходников {#build-from-source}
```

A first-level heading carries none: nothing links to a page by its own
anchor. A heading of a translation that stays identical to the English one
needs no explicit anchor either.

## Style

The Russian pages are prose, not telegram style:

- impersonal voice, or `мы`; no imperative addressed to the reader;
- ASCII punctuation: a dash is ` - ` with spaces around it, quotes are
  `"..."`, an arrow is `->`. The letter ё is written everywhere;
- a term with no Russian equivalent stays Latin and is inflected with an
  apostrophe, not transliterated.

Phrases to avoid: `в этом разделе мы рассмотрим`, `важно отметить`,
`стоит упомянуть`, `подводя итог`, formulae such as `это позволяет ...`
repeated from section to section, a summary paragraph at the end of every
section, and bold on every other phrase.

## Checks

```console
mage docs:stale                 # translations behind their English pages
mage docs:build                 # mkdocs build --strict
pre-commit run markdownlint -a  # line length, list and heading style
```

`mage docs:stale` reads git: a translation is stale once its English page
has a commit the translation did not follow. A line is 80 characters or
fewer, headings included, and code blocks and tables are exempt.
