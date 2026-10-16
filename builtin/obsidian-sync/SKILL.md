---
name: obsidian-sync
description: Turn an Obsidian vault into a self-maintaining AI second brain. Ingest new content, auto-rewrite related notes, reconcile contradictions, synthesize patterns, and connect orphan pages. Use when user asks to work with Obsidian vault, manage knowledge base, or build a second brain.
read_when:
  - Working with Obsidian vault markdown files
  - Building or maintaining a personal knowledge base
  - User mentions "obsidian", "vault", "second brain", "知识库"
  - Auto-synthesizing or connecting notes
---

# Obsidian Vault Sync

Turn any Obsidian vault into a living AI knowledge base — notes that rewrite themselves, connect automatically, and compound over time.

## Quick Start

First, explore the vault to understand what's there:

```
vault_explore(action="stats", path="/path/to/vault")
```

Then use standard `read_file` / `write_file` to read and update individual markdown notes.

## Obsidian Syntax Reference

### Wikilinks
```markdown
Link to a note: [[Note Name]]
Link with alias: [[Note Name|display text]]
Link to heading: [[Note Name#Heading]]
```

### Frontmatter (YAML at top of file)
```yaml
---
tags: [ai, knowledge]
created: 2026-06-18
aliases: ["Alt Name"]
---
```

### Callouts
```markdown
> [!info] Title
> Content here

> [!warning] Important
> Content here
```

### Tags
```markdown
#tag-name
#nested/tag
```

## Core Workflows

### 1. Ingest New Content

When a user drops a URL, article, or document into the vault:

1. `vault_explore(action="search", query="related keywords")` to find related notes
2. `read_file` on the top 3-5 most relevant notes
3. **Rewrite those notes** — don't append a new note. Update existing pages with new information
4. Add `## For future AI` section at the bottom with context for the next ingestion
5. Update frontmatter: add tags, update `modified` date

### 2. Connect Orphan Notes

1. `vault_explore(action="orphans")` to find unlinked notes
2. For each orphan, search for related content using keywords from its title and body
3. Add `[[wikilinks]]` in both directions
4. Priority: connect notes modified in the same week

### 3. Reconcile Contradictions

When two notes say different things about the same topic:

1. Read both notes completely
2. Identify the conflicting claims
3. Research the truth using `web_fetch`
4. Update the less accurate note, add a `> [!warning] Historically claimed...` callout
5. Link both notes to each other

### 4. Synthesize Patterns

When multiple notes touch the same theme without a hub page:

1. `vault_explore(action="tags")` to see topic clusters
2. Identify 3+ notes that share tags or link to each other
3. Create a synthesis note (MOC - Map of Content) that:
   - Summarizes the common theme
   - Links to all related notes with brief descriptions
   - Extracts the key insight that's bigger than any single note

### 5. Nightly Maintenance

Can be scheduled as a cron TODO:

```
@项目助理 对 /path/to/vault 执行知识库维护:
1. vault_explore orphans → connect top 5 orphans
2. vault_explore recent → check last 3 modified notes for contradictions
3. vault_explore tags → find growing topic cluster, create MOC if 5+ notes
```

## AI-First Note Format

Every AI-maintained note should have:

```markdown
---
tags: [topic1, topic2]
created: 2026-06-18
modified: 2026-06-18
ai_synthesized: true
---

# Note Title

[Main content]

## Related
- [[Related Note 1]] — brief description
- [[Related Note 2]] — brief description

## For future AI
- Last updated after ingesting: [source]
- Key claims to verify: [list]
- Contradictions to watch: [list]
```

## Avoid

- Creating a new note for every ingestion — prefer rewriting existing pages
- Leaving orphan notes unconnected
- Writing purely human-readable notes without the `## For future AI` section
- Using complex folder structures — flat hierarchies with wikilinks scale better
