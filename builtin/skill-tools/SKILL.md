---
name: skill-tools
description: Create, vet, and manage skills. Use when writing new skills, reviewing skills from ClawHub/GitHub, or managing the skill library.
triggers: ["create skill", "write skill", "vet skill", "review skill", "install skill", "skill template", "SKILL.md"]
version: 1.0.0
author: OpenKitty
status: stable
---
# Skill Tools

## IRON RULES (non-negotiable)

1. **Vet before install**: Every skill from an external source (ClawHub, GitHub, any third-party) MUST pass a full security review before installation. No exceptions, no shortcuts.
2. **REJECT on suspicion**: If a skill contains curl/wget to unknown URLs, data exfiltration patterns (posting data to external endpoints), credential requests, or obfuscated code, reject it immediately. Do not negotiate — a suspicious skill stays out.
3. **One SKILL.md per skill**: Each skill is exactly one SKILL.md file in its own directory under `skills/`. Multi-file skill packages need an explicit, documented reason.
4. **Lowercase hyphenated names only**: Skill names must be lowercase, hyphenated, and descriptive (e.g., `web-tools`, not `WebTools` or `web_tools`). Names with spaces or special characters break cross-platform resolution.
5. **Trigger phrases must be user-facing**: Triggers should match what a user would naturally say or type, not internal implementation jargon. A skill with no triggers is undiscoverable.

## Skill Creator — Write New Skills
Skills are modular packages that extend agent capabilities. They're SKILL.md files with YAML frontmatter.

### Quick Template
```markdown
---
name: my-skill
description: What this skill does. Be specific.
triggers: ["keyword1", "keyword2"]
---
# My Skill
Detailed instructions here.
```

### Best Practices
1. Name: lowercase, hyphenated, descriptive
2. Description: one sentence, specific, include trigger phrases
3. Content: clear step-by-step workflows, concrete examples
4. Place in `skills/my-skill/SKILL.md` at top level

### Structure
```
skills/my-skill/
  SKILL.md          ← required: main instructions
  references/       ← optional: supporting docs
  assets/           ← optional: templates, images
  scripts/          ← optional: executable scripts
```

## Skill Vetter — Security Review
Always vet before installing skills from external sources:

- Check source: author, stars, last update
- Review ALL files for: curl/wget to unknown URLs, data exfiltration, credential requests
- Check permission scope: what tools does it need?
- REJECT if anything suspicious

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Installing a skill without reading all its files | Hidden payloads in scripts/assets/references can compromise security | Read every file in the skill directory tree before approving installation |
| 2 | Writing skills without any trigger phrases | Skill becomes undiscoverable — users cannot invoke it | Always add at least one natural-language trigger phrase |
| 3 | Using overly broad triggers like "help", "do", or "run" | Causes unintended activation on unrelated user requests | Use specific, domain-scoped trigger phrases that won't collide |
| 4 | Bundling multiple unrelated capabilities into one skill | Bloated, confusing skill that tries to do too much | Split into separate focused skills, each with a single responsibility |
| 5 | Copying a skill template without customizing the description | Generic description fails to communicate what the skill actually does | Write a specific one-sentence description that includes trigger phrases |

## Using Installed Skills
- `list_skills <keyword>` — find relevant skills
- `use_skill <name>` — execute a skill
- `read_file skills/<name>/SKILL.md` — read full instructions

## Pre-Action Checklist

- [ ] Understand what the user wants the skill to do — clarify scope if ambiguous
- [ ] For new skills: write a clear one-sentence description with trigger phrases
- [ ] For external skills: review every file for suspicious code (curl, data exfil, credentials)
- [ ] Check the skill name follows conventions (lowercase, hyphenated, descriptive)
- [ ] Verify the SKILL.md is well-structured with clear step-by-step instructions
- [ ] Test the skill's triggers against likely user phrasings to avoid collisions
