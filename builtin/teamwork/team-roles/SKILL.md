---
name: team-roles
description: Pre-defined role templates for assembling multi-agent teams. Each role defines a specialized persona with domain expertise.
---

# Team Roles

12 role templates for assembling role-based AI teams. Each role has a `.md` file defining its persona, expertise, and typical tasks.

## Available Roles

| Role | File | Expertise |
|------|------|-----------|
| Analyst | `analyst.md` | Data analysis, requirements gathering |
| Architect | `architect.md` | System design, architecture decisions |
| Backend | `backend.md` | API design, database, server-side logic |
| Developer | `developer.md` | General-purpose coding |
| Editor | `editor.md` | Content editing, proofreading |
| Frontend | `frontend.md` | UI/UX, client-side development |
| PM | `pm.md` | Project management, scoping |
| QA | `qa.md` | Testing, quality assurance |
| Researcher | `researcher.md` | Information gathering, fact-checking |
| Reviewer | `reviewer.md` | Code review, design review |
| Strategist | `strategist.md` | Strategic planning, roadmap |
| Writer | `writer.md` | Content writing, documentation |

## When to Use

Use `team-roles` when the user says "组建一个团队" or "assemble a dev team". Pick 2-5 roles relevant to the task:

- **Software project**: Architect + Developer/Backend/Frontend + QA + Reviewer
- **Content project**: Writer + Editor + Strategist
- **Research project**: Researcher + Analyst + Strategist
- **Management**: PM + Architect + Strategist

Each role is instantiated as a sub-agent with the role's `.md` content as its system prompt.
