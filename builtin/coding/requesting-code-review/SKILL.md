---
name: requesting-code-review
description: "Pre-commit review: security scan, quality gates, auto-fix. Use after implementing features, before commit or push."
read_when:
  - Before git commit or push
  - After implementing a feature or bug fix
  - User says "commit", "push", "review", "verify"
  - Completing 2+ file edits in a git repo
---

# Pre-Commit Code Review

No agent should verify its own work. Fresh context finds what you miss.

## Step 1: Get the diff

```bash
git diff --cached
```

If empty, try `git diff` then `git diff HEAD~1 HEAD`. If still empty, nothing to verify.

## Step 2: Static security scan

```bash
# Hardcoded secrets
git diff --cached | grep "^+" | grep -iE "(api_key|secret|password|token)\s*=\s*['\"][^'\"]{6,}['\"]"

# Shell injection
git diff --cached | grep "^+" | grep -E "os\.system\(|subprocess.*shell=True"

# Dangerous eval
git diff --cached | grep "^+" | grep -E "\beval\(|\bexec\("
```

## Step 3: Self-review checklist

- [ ] No hardcoded secrets, API keys, or credentials
- [ ] Input validation on user-provided data
- [ ] SQL queries use parameterized statements
- [ ] File operations validate paths
- [ ] External calls have error handling
- [ ] No debug print/console.log left behind
- [ ] No commented-out code
- [ ] New code has tests

## Step 4: Tests and linting

Auto-detect language, run tests + linter:
```bash
# Python: pytest + ruff/mypy | Node: npm test + eslint | Go: go test + go vet | Rust: cargo test + clippy
```

## Step 5: Independent reviewer via sub_agent

Spawn a sub_agent to review the diff independently:

```
sub_agent(
  prompt: "You are an independent code reviewer. Review this git diff for security issues and logic errors. Return JSON: {passed: bool, security_concerns: [], logic_errors: [], suggestions: [], summary: ''}",
  context: "Current diff: [git diff output]"
)
```

## Step 6: Evaluate

- **All passed** → commit with `git add -A && git commit -m "[verified] <description>"`
- **Failures** → fix (max 2 cycles), then re-verify. If still failing, escalate to user.

## Pitfalls

- Empty diff → `git status`, nothing to verify
- Not a git repo → skip, tell user
- Lint tools not installed → skip silently, don't fail
- Fix agent introduces new issues → counts as new failure
