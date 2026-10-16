---
name: test-driven-development
description: "TDD: enforce RED-GREEN-REFACTOR cycle. Write tests before code, verify they fail, implement minimal code, verify pass, then refactor. Use with plan-mode for multi-step features."
read_when:
  - Writing new code or features
  - Fixing bugs (write regression test first)
  - Refactoring (tests must pass before and after)
  - Implementing plan-mode tasks
  - User asks for TDD or test-first development
---

# Test-Driven Development

## Overview

TDD creates reliable code through a disciplined RED → GREEN → REFACTOR cycle. Every line of production code is backed by a test that was written first.

**Core principle:** You cannot write implementation code until you have a failing test that demands it.

## The TDD Cycle

```
RED    → Write a failing test
GREEN  → Write minimal code to pass
REFACTOR → Clean up while keeping green
REPEAT
```

## When to Use

**Always use when:**
- Writing new features
- Fixing bugs (write regression test first)
- Refactoring (tests prove behavior preserved)
- Implementing plan-mode tasks
- Working with code that has no tests

**Don't skip when:**
- "It's just a simple function" — simple functions have edge cases
- "I'll add tests later" — later never comes
- "Tests take too long" — debugging without tests takes longer

## The Rules

### Rule 1: RED First

Write a test that FAILS before writing any implementation code.

```python
# Step 1: Write the test
def test_add_user():
    db = Database()
    db.add_user("alice", "alice@example.com")
    user = db.get_user("alice")
    assert user.name == "alice"
    assert user.email == "alice@example.com"

# Step 2: Run and verify it FAILS
# pytest tests/test_db.py::test_add_user -v
# Expected: FAIL — Database not defined
```

**Verify the failure:** The test must fail for the EXPECTED reason (missing class/method), not a syntax error or wrong assertion.

### Rule 2: GREEN — Minimal Implementation

Write the ABSOLUTE MINIMUM code to make the test pass. No more, no less.

```python
# Step 3: Minimal implementation
class Database:
    def add_user(self, name, email):
        self._users = getattr(self, '_users', {})
        self._users[name] = {"name": name, "email": email}
    
    def get_user(self, name):
        return type('User', (), self._users[name])()

# Step 4: Run and verify it PASSES
# pytest tests/test_db.py::test_add_user -v
# Expected: PASS
```

**Don't add:** validation, error handling, logging, additional methods, "future-proofing" — unless a test demands it.

### Rule 3: REFACTOR

Clean up the code while keeping tests green. Improve names, extract helpers, remove duplication.

```python
# Step 5: Refactor
class Database:
    def __init__(self):
        self._users = {}
    
    def add_user(self, name, email):
        self._users[name] = User(name, email)
    
    def get_user(self, name):
        return self._users[name]

class User:
    def __init__(self, name, email):
        self.name = name
        self.email = email

# Step 6: Run tests again — must still PASS
# pytest tests/test_db.py -v
```

### Rule 4: Commit After Each Cycle

```bash
git add tests/test_db.py src/database.py
git commit -m "feat: add Database.add_user and get_user"
```

## Bug Fix TDD

For bugs, write a test that reproduces the bug FIRST:

```python
# 1. Write regression test that demonstrates the bug
def test_delete_user_cleans_references():
    db = Database()
    db.add_user("alice", "alice@example.com")
    db.add_post("alice", "Hello world")
    db.delete_user("alice")
    posts = db.get_posts_by_user("alice")
    assert posts == []  # Bug: posts not cleaned up!

# 2. Run — verify it FAILS (bug confirmed)

# 3. Fix the bug

# 4. Run — verify it PASSES (bug fixed, no regression)
```

## Test Structure

### AAA Pattern

```python
def test_function_name():
    # Arrange — set up test data and state
    calculator = Calculator()
    
    # Act — perform the action being tested
    result = calculator.add(2, 3)
    
    # Assert — verify the expected outcome
    assert result == 5
```

### Test Names

Name tests to describe the scenario: `test_<function>_<scenario>_<expected_result>`

```python
def test_divide_by_zero_raises_error():
def test_get_user_nonexistent_returns_none():
def test_add_item_empty_cart_adds_first_item():
```

### One Assert Per Test (generally)

```python
# Good — focused tests
def test_add_user_creates_record():
def test_add_user_duplicate_raises_error():
def test_add_user_empty_name_raises_error():

# Avoid — testing multiple things
def test_add_user():
    db.add_user("alice", "a@b.com")
    assert len(db.users) == 1
    assert db.users[0].name == "alice"
    assert db.users[0].email == "a@b.com"
    # What broke? Which assert failed?
```

## Red Flags

If you catch yourself:
- Writing implementation before test → STOP. Write test first.
- Writing more code than the test needs → STOP. Minimal only.
- Skipping the "verify it fails" step → STOP. You need to see RED.
- Adding "probably useful" methods → STOP. YAGNI.
- "This is too simple to test" → STOP. Simple code has edge cases.

## Quick Reference

```
RED:    Write test → verify FAIL
GREEN:  Minimal code → verify PASS
REFACTOR: Clean up → verify PASS
COMMIT: git add + git commit

REPEAT for each tiny increment
```

**No untested code. No skipped cycles. RED-GREEN-REFACTOR always.**
