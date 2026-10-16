---
name: Word / DOCX
slug: word-docx
version: 1.2.0
homepage: https://clawic.com/skills/word-docx
description: "Create, inspect, and edit Microsoft Word documents and DOCX files with reliable styles, numbering, tracked changes, tables, sections, and compatibility checks. Use when (1) the task is about Word or `.docx`; (2) the file includes tracked changes, comments, fields, tables, templates, or page layout constraints; (3) the document must survive round-trip editing without formatting drift."
changelog: Extended the declarative LIMS workflow with MERGEFIELD, fillable bookmarks, typed content controls, real checkbox state, and mixed-field repeating rows.
metadata: {"clawdbot":{"emoji":"📘","os":["linux","darwin","win32"]}}
---

## When to Use

Use when the main artifact is a Microsoft Word document or `.docx` file, especially when tracked changes, comments, headers, numbering, fields, tables, templates, or compatibility matter.

## Core Rules

### 1. Treat DOCX as OOXML, not plain text

- A `.docx` file is a ZIP of XML parts, so structure matters as much as visible text.
- The critical parts are usually `word/document.xml`, `styles.xml`, `numbering.xml`, headers, footers, and relationship files.
- Text may be split across multiple runs; never assume one word or sentence lives in one XML node.
- Use different workflows on purpose: structured extraction for quick reading, style-driven generation for new files, and OOXML-aware editing for fragile existing documents.
- If the job is mainly reading, extracting, or reviewing, prefer a structure-preserving read path before touching OOXML.
- For deep edits, inspect the package layout instead of relying only on rendered output.
- Reading, generating, and preserving an existing reviewed document are different jobs even when the format is the same.
- Legacy `.doc` inputs usually need conversion before you can trust modern `.docx` assumptions.

### 2. Preserve styles and direct formatting deliberately

- Prefer named styles over direct formatting so the document stays editable.
- Styles layer: paragraph styles, character styles, and direct formatting do not behave the same.
- Removing direct formatting is often safer than stacking more inline formatting on top.
- When editing an existing file, extend the current style system instead of inventing a parallel one.
- Copying content between documents can silently import foreign styles, theme settings, and numbering definitions.

### 3. Lists and numbering are their own system

- Bullets and numbering belong to Word's numbering definitions, not pasted Unicode characters.
- `abstractNum`, `num`, and paragraph numbering properties all matter, so restart behavior is rarely "visual only".
- Indentation and numbering are related but not identical; a list can have broken numbering even if the indent looks right.
- A list that looks correct in one editor can restart, flatten, or renumber itself later if the underlying numbering state is wrong.

### 4. Page layout lives in sections

- Margins, orientation, headers, footers, and page numbering are section-level behavior.
- First-page and odd/even headers can differ inside the same document, so one header fix may not fix the document.
- Set page size explicitly because A4 and US Letter defaults change pagination and table widths.
- Use section breaks for layout changes; manual spacing and stray page breaks usually create drift.
- Header and footer media use part-specific relationships, so copied IDs often break images or links.
- Tables, page breaks, and headers often drift together, so treat layout fixes as document-wide, not local cosmetic edits.
- Table geometry depends on page width, margins, and fixed widths, so "close enough" table edits often break later in Google Docs or LibreOffice.

### 5. Track changes, comments, and fields need precise edits

- Visible text is not the full document when tracked changes are enabled.
- Insertions, deletions, and comments carry metadata that can survive careless edits.
- Deleted text may still exist in the XML even when it no longer appears on screen.
- Comment anchors and review ranges can break if edits move text without preserving the surrounding structure.
- Comment markers and review wrappers do not behave like inline formatting, so moving text carelessly can orphan or misplace them.
- Comments, footnotes, bookmarks, and linked media may live in separate parts, not only in the main document body.
- Tables of contents, page numbers, dates, cross-references, and mail merge placeholders are fields.
- Edit the field source carefully and expect cached display values to lag until refresh.
- Hyperlinks, bookmarks, and references can break if IDs or relationships stop matching.
- Bookmarks, footnotes, comment ranges, and cross-references depend on stable anchors even when the visible text seems untouched.
- A document can look correct while still containing stale field output that refreshes later into something different.
- For review workflows, make minimal replacements instead of rewriting whole paragraphs.
- In tracked-change workflows, only the changed span should look changed; broad rewrites create noisy reviews and can destroy the original formatting context.
- For legal, academic, or business review documents, default to review-style edits over wholesale paragraph rewrites unless the user explicitly wants a rewrite.

### 6. Verify round-trip compatibility before delivery

- Complex documents can shift between Word, LibreOffice, Google Docs, and conversion tools.
- Tables, headers, embedded fonts, and copied styles are common sources of layout drift.
- Treat `.docm` as macro-bearing and higher risk; treat `.doc` as legacy input that may need conversion first.
- When layout matters, explicit table widths are safer than auto-fit or percentage-style behavior that different editors reinterpret.
- A document that passes a text check can still fail on pagination, table widths, or reference refresh after the recipient opens it.

### 7. Use the declarative engine for enterprise and LIMS templates

- When filling an existing enterprise template, call `extract` with `inspect_template=true` before writing custom code. The inventory includes a template fingerprint, placeholders, typed content controls, `MERGEFIELD` fields, fillable bookmarks, labeled/ordered underlines, and table/row/cell addresses.
- Pass the real normalized LIMS or enterprise object as `source_data` and use `preview_mapping=true`. Review `confidence`, `ambiguous_fields`, and `unmatched_fields`; resolve only uncertain fields with `field_mapping` instead of creating a per-template program.
- For detail tables, use `repeat_rows` with the inspected 1-based `table_index` and `template_row`. Bind an array using `data_path`; rows can expose fields through `{{field}}`, tagged content controls, `MERGEFIELD`, or fillable bookmarks. Use `column_keys` only when the legacy row has no named fields. `$index` emits a 1-based row number.
- Boolean source values update Word checkbox content controls as real checked/unchecked state while preserving their symbol formatting. Drop-down and combo-box controls keep their existing control definition and receive the selected display value.
- Filled `MERGEFIELD` results are materialized as ordinary formatted text in the generated report so a later Word field refresh cannot revert them; the source template remains unchanged.
- Repeating-section containers may wrap nested leaf content controls. Inspect and map the named leaf controls; do not treat the outer repeating container itself as a scalar field.
- On the first accepted fill, use `save_template_profile=true` and an optional `template_profile_name`. OpenKitty stores only reusable source paths and table rules under `.openkitty/document-templates/`, never the current sample values or test results.
- For later batches using the same template structure, pass new `source_data` and use `use_template_profile=true`; saved profiles are also auto-discovered when no new mapping plan is supplied. If the template structure changes, inspect and confirm the new fingerprint instead of forcing the old plan.
- Use `strict=true` and `verify_complete=true` for final output. A failed strict or completeness check is not a completed report and must not replace a known-good output.
- Fall back to a template-specific implementation only when inspection proves the document depends on an unsupported construct; state that construct explicitly so it can be promoted into the common engine later.

### 8. Let the Agent absorb the repetitive mapping work

- Treat `inspect_template` and `preview_mapping` output as the Agent's working schema. Do not ask the user to rename every LIMS column or manually enumerate obvious one-to-one fields.
- Accept high-confidence deterministic matches first. For unresolved targets, compare the target type, nearby Word context, table headers, source path, units, and ranked alternatives; produce the smallest explicit `field_mapping` needed to resolve them.
- Preview again after adding inferred mappings. If two candidates remain genuinely indistinguishable, ask only about that ambiguity instead of returning the entire template to the user.
- After one accepted result, save the template profile. Subsequent batches should reuse the profile and leave the model only exceptions, newly added fields, or a changed template fingerprint.
- Keep interpretation and execution separate: the model decides the semantic plan, while the OOXML engine applies values and verifies that the output is structurally complete.

## Common Traps

- Copy-paste can import unwanted styles and numbering definitions.
- Header or footer images use part-specific relationships, so reusing IDs blindly breaks them.
- Empty paragraphs used as spacing make templates fragile; spacing belongs in paragraph settings.
- A clean-looking export can still hide unresolved revisions, comments, or stale field values.
- Restarting lists "by eye" usually fails because numbering state lives outside the paragraph text.
- One visible phrase can be split across several runs, bookmarks, revision tags, or field boundaries.
- Replacing a whole paragraph to change one clause often breaks review quality, bookmarks, comments, or nearby inline formatting.
- Deleting all visible text from a paragraph or list item can still leave behind an empty paragraph mark, empty bullet, or unstable numbering.
- Table auto-fit and percentage-like width behavior can look acceptable in Word and still drift in Google Docs or LibreOffice.
- LibreOffice and Google Docs can shift complex tables, section behavior, and embedded fonts even when Word looks perfect.
- Compatibility mode can silently cap newer features or change pagination behavior.
- A single change in page size or margin defaults can ripple through tables, headers, TOC, and cross-references.
- A revision workflow can look accepted on screen while leftover metadata, comments, or field caches still make the file unstable later.
- TOC entries, footnotes, and cross-references can look correct until the recipient updates fields and exposes broken anchors.

## Related Skills
Install with `clawhub install <slug>` if user confirms:
- `documents` — General document handling and format conversion.
- `brief` — Concise business writing and structured summaries.
- `article` — Long-form drafting and editorial structure.

## Feedback

- If useful: `clawhub star word-docx`
- Stay updated: `clawhub sync`
