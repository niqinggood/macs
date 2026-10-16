#!/usr/bin/env python3
"""Validate a standalone HTML file for common LLM-generated bugs."""
import re, sys, os

def validate(filepath):
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()

    issues = []
    warnings = []

    # 1. Data integrity: check JS object references match defined keys
    # Find M.xxx used as function args: renderMembers(M.xxx), etc
    render_refs = set(re.findall(r'render\w+\(\s*(?:M|data|Data)\.(\w+)', content))

    # Extract top-level keys from M = {...} by counting braces
    defined = set()
    m_start = re.search(r'(?:var|const|let)\s+M\s*=\s*\{', content)
    if m_start:
        start = m_start.end() - 1  # position of opening {
        depth = 0
        obj = ''
        for i in range(start, len(content)):
            if content[i] == '{': depth += 1
            elif content[i] == '}':
                depth -= 1
                if depth == 0:
                    obj = content[start:i+1]
                    break
        if obj:
            defined = set(re.findall(r'^\s*(\w+)\s*:', obj, re.MULTILINE))

    missing = render_refs - defined
    extra = defined - render_refs
    for m in sorted(missing):
        issues.append(f"DATA MISSING: render calls reference 'M.{m}' but M has no '{m}' key. Did you forget to add the data?")
    for e in sorted(extra):
        print(f"  Extra data (unused): M.{e}")

    # 2. Check for relative image paths (will be broken)
    relative_imgs = re.findall(r'<img[^>]+src=["\'](?!(?:https?:|data:))[^"\']+["\']', content)
    for img in relative_imgs:
        issues.append(f"BROKEN IMAGE: relative/non-HTTP src: {img[:80]}")

    # 3. Check basic HTML structure
    if not content.strip().startswith('<!DOCTYPE html>'):
        issues.append("STRUCTURE: missing <!DOCTYPE html>")
    if content.count('<html') > 1:
        issues.append("STRUCTURE: multiple <html> tags")
    if '<p>' in content and re.search(r'<p>\s*<(html|head|body|style|script)', content):
        warnings.append("STRUCTURE: <p> wrapping HTML elements detected (may be cleaned)")

    # 4. Count vs actual check
    refs_in_code = len(re.findall(r'\bM\.\w+', content))
    keys_in_data = len(re.findall(r'(?:(?:var|const|let)\s+M\s*=\s*\{)', content))
    if refs_in_code > 0 and keys_in_data == 0:
        issues.append("DATA MISSING: M.xxx references found but no 'const M = {...}' data object")

    # Report
    print(f"=== Validate: {filepath} ===")
    print(f"  Render calls:    {len(render_refs)} found, {len(render_refs & defined)} matched, {len(missing)} MISSING")
    print(f"  M object has:    {len(defined)} top-level keys")

    if issues:
        print(f"\n  ISSUES ({len(issues)}):")
        for i in issues:
            print(f"    ❌ {i}")
    if warnings:
        print(f"\n  WARNINGS ({len(warnings)}):")
        for w in warnings:
            print(f"    ⚠️ {w}")
    if not issues and not warnings:
        print("  ✅ All checks passed")

    return len(issues)

if __name__ == '__main__':
    path = sys.argv[1] if len(sys.argv) > 1 else 'index.html'
    sys.exit(validate(path))
