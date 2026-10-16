#!/usr/bin/env python3
"""Collect license evidence without treating discovery as a legal clearance."""

from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile
import urllib.request


WEB = [
    ("chart.js", "4.4.0", ["web/chart.umd.min.js"]),
    ("highlight.js", "11.9.0", ["web/highlight.min.js", "web/github.min.css"]),
    ("html2canvas", "1.4.1", ["web/html2canvas.min.js"]),
    ("lucide", "1.21.0", ["web/lucide.min.js"]),
    ("marked", "14.1.4", ["web/marked.min.js"]),
    ("pdfjs-dist", "2.16.105", ["web/pdf.min.js", "web/pdf.worker.min.js"]),
    ("mammoth", "1.8.0", ["web/mammoth.browser.min.js"]),
    ("mermaid", "10.9.6", ["web/mermaid.min.js"]),
]


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def run(root: Path, *args: str) -> str:
    return subprocess.check_output(args, cwd=root, timeout=90).decode("utf-8-sig")


def json_stream(text: str) -> list[dict]:
    decoder = json.JSONDecoder()
    result = []
    while text.strip():
        text = text.lstrip()
        value, end = decoder.raw_decode(text)
        result.append(value)
        text = text[end:]
    return result


def license_files(directory: Path) -> list[Path]:
    if not directory.is_dir():
        return []
    return sorted(p for p in directory.iterdir() if p.is_file() and re.match(r"^(license|licence|copying|notice|copyright)([._-]|$)", p.name, re.I))


def classify(text: str) -> str:
    low = text.lower()
    if "previously bundled third-party skill" in low:
        return "Project-License"
    if "apache license" in low and "version 2.0" in low:
        return "Apache-2.0 (text detected)"
    if "permission is hereby granted, free of charge" in low:
        return "MIT (text detected)"
    if "redistribution and use in source and binary forms" in low:
        return "BSD family (review exact text)"
    if "permission to use, copy, modify, and/or distribute" in low:
        return "ISC (text detected)"
    return "Review supplied text"


def save_licenses(root: Path, component: str, sources: list[tuple[str, bytes]]) -> list[dict]:
    component_dir = Path("third_party/licenses") / sha(component.encode())[:16]
    records = []
    for name, data in sources:
        safe_name = re.sub(r"[^A-Za-z0-9._-]", "_", name)
        target = component_dir / safe_name
        (root / component_dir).mkdir(parents=True, exist_ok=True)
        (root / target).write_bytes(data)
        records.append({"file": target.as_posix(), "sha256": sha(data), "classification_hint": classify(data.decode("utf-8", errors="replace"))})
    return records


def fetch_npm_license(package: str, version: str) -> list[tuple[str, bytes]]:
    url = f"https://registry.npmjs.org/{package}/{version}"
    with urllib.request.urlopen(url, timeout=20) as response:
        metadata = json.load(response)
    if metadata.get("name") != package or metadata.get("version") != version:
        raise ValueError("npm package identity differs")
    tarball_url = metadata["dist"]["tarball"]
    if not tarball_url.startswith("https://registry.npmjs.org/"):
        raise ValueError("unexpected npm archive host")
    with urllib.request.urlopen(tarball_url, timeout=30) as response:
        data = response.read(30_000_001)
    if len(data) > 30_000_000:
        raise ValueError("npm archive exceeds license collection limit")
    # No archive member is extracted to disk or executed.
    result = []
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for member in archive:
            base = Path(member.name).name
            if member.isfile() and member.size < 2_000_000 and len(Path(member.name).parts) <= 2 and re.match(r"^(license|licence|notice|copying)([._-]|$)", base, re.I):
                stream = archive.extractfile(member)
                if stream is not None:
                    result.append((base, stream.read()))
    return result


def collect(root: Path, fetch_web: bool = False) -> dict:
    rows = []
    for module in json_stream(run(root, "go", "list", "-m", "-json", "all")):
        if module.get("Main"):
            continue
        actual = module.get("Replace", module)
        directory = Path(actual["Dir"]) if actual.get("Dir") else None
        component = module["Path"] + "@" + module.get("Version", "local")
        files = license_files(directory) if directory else []
        licenses = save_licenses(root, component, [(p.name, p.read_bytes()) for p in files])
        row = {"kind": "go-module", "name": module["Path"], "version": module.get("Version"), "source": "https://" + module["Path"], "license_files": licenses, "status": "license_text_collected" if licenses else "license_evidence_missing"}
        if "Replace" in module:
            row["replacement"] = actual["Path"]
            row["replacement_version"] = actual.get("Version")
        rows.append(row)
    goroot = Path(run(root, "go", "env", "GOROOT").strip())
    go_version = run(root, "go", "version").strip().split()[2]
    rows.append({"kind": "toolchain-runtime", "name": "Go", "version": go_version, "source": "https://go.dev/", "license_files": save_licenses(root, "go-" + go_version, [(p.name, p.read_bytes()) for p in license_files(goroot)]), "status": "license_text_collected"})

    previous = {}
    inventory_path = root / "third_party/components.json"
    if inventory_path.exists():
        previous = {(r["kind"], r["name"]): r for r in json.loads(inventory_path.read_text(encoding="utf-8"))["components"]}
    for package, version, assets in WEB:
        existing_assets = [a for a in assets if (root / a).exists()]
        if not existing_assets:
            continue
        row = {"kind": "web-resource", "name": package, "version": version, "source": f"https://www.npmjs.com/package/{package}" + (f"/v/{version}" if version else ""), "assets": [{"path": a, "sha256": sha((root / a).read_bytes())} for a in existing_assets], "version_evidence": "resource header or embedded version" if version else "exact bundled version requires provenance review", "license_files": []}
        old = previous.get(("web-resource", package), {})
        if old.get("version") == version:
            row["license_files"] = [r for r in old.get("license_files", []) if (root / r["file"]).is_file() and sha((root / r["file"]).read_bytes()) == r["sha256"]]
        if fetch_web and version:
            try:
                sources = fetch_npm_license(package, version)
                row["license_files"] = save_licenses(root, f"npm-{package}@{version}", sources)
                row["license_source"] = f"https://registry.npmjs.org/{package}/{version}"
            except (OSError, ValueError, KeyError, tarfile.TarError) as exc:
                row["collection_error"] = str(exc)[:240]
        row["status"] = "license_text_collected" if row["license_files"] else "license_evidence_missing"
        if package == "mammoth":
            row["version_evidence"] = "Bundled file SHA-256 matches npm mammoth@1.8.0 package/mammoth.browser.min.js"
            row["verified_asset_sha256"] = "deb07bf230d1cb3e190bc5adc6743f35c6531b6571d1e5469b24f452a7f0f4ab"
            if row["assets"][0]["sha256"] != row["verified_asset_sha256"]:
                row["status"] = "provenance_review_required"
        if package == "mermaid":
            row["version_evidence"] = "Embedded 10.9.6 version string; bundled bytes differ from the npm dist file"
            row["modification_review"] = "Bundled distribution variant must be compared with upstream; license collection is not clearance"
            row["status"] = "provenance_review_required"
        rows.append(row)

    for base in [root / "builtin", root / "default-skills"]:
        for skill in sorted(base.rglob("SKILL.md")):
            relative = skill.relative_to(root).as_posix()
            text = skill.read_text(encoding="utf-8-sig", errors="replace")
            current = skill.parent
            files = []
            while current.is_relative_to(base):
                files = license_files(current)
                if files or current == base:
                    break
                current = current.parent
            licenses = save_licenses(root, relative, [(p.name, p.read_bytes()) for p in files])
            author = re.search(r"^author:\s*(.+)$", text, re.M)
            rows.append({"kind": "bundled-skill", "name": relative, "version": None, "author_metadata": author.group(1).strip() if author else None, "source": "repository path; see upstream metadata and original attribution", "sha256": sha(skill.read_bytes()), "license_files": licenses, "status": "license_text_collected" if licenses else "provenance_and_license_review_required"})
    report = {"format_version": 1, "software_name": "macs", "scope": "Go module graph, Go runtime, known bundled frontend resources and skill entrypoints; not a legal ownership certification", "limitations": ["License classification is a text hint, not a compatibility decision.", "Bundled JavaScript may contain further dependency notices; review upstream distributions.", "Skills without explicit license evidence remain unresolved; author metadata is not proof.", "Local npm development tools are outside this binary-resource inventory."], "components": rows, "status_counts": dict(Counter(r["status"] for r in rows))}
    inventory_path.parent.mkdir(parents=True, exist_ok=True)
    inventory_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = ["# macs 第三方材料说明", "", "第三方材料继续适用其原许可证。随发行包保留本文件、third_party/components.json 及 third_party/licenses/ 中的原始许可和 NOTICE。根 LICENSE 不替代这些许可。", "", "本清单由 scripts/legal/third_party_inventory.py 采集。找到许可证文本只表示已有可核对的材料，不表示已经完成权属、兼容性或传递依赖审查。状态和摘要见机器清单。", "", "## 内置文档技能替换", "", "2026-10-15，原 docx-tools 与 pdf-tools 中受到复制、修改和再分发限制的第三方材料已移出公开源码和嵌入资源。相同技能名称现在使用 macs 独立编写的说明，调用现有文档工具；原文和原许可证仅在本机私有目录保留，不属于发行包。此替换不授予对原材料的任何权利。", "", "## 组件与许可证文本", "", "| 类型 | 组件 | 版本 | 证据状态 | 许可文件 |", "| --- | --- | --- | --- | --- |"]
    for row in rows:
        refs = "、".join(f"[{Path(item['file']).name}]({item['file']})" for item in row["license_files"]) or "待补充"
        lines.append(f"| {row['kind']} | {row['name']} | {row.get('version') or '待核对'} | {row['status']} | {refs} |")
    lines += ["", "## 尚需核对", "", "无明确许可的 Skill、版本无法确认的前端资源及打包脚本内部的传递依赖须继续查明来源。确认来源后补充具体授权、许可和修改记录，或替换相关材料。不得因本清单存在就声明整个发行包已完成许可审核。", "", "项目贡献与权利人信息另按 CONTRIBUTING.md 和 docs/legal/README.md 核对。"]
    (root / "THIRD-PARTY-NOTICES.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    return {"components": len(rows), "status_counts": report["status_counts"]}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fetch-web", action="store_true", help="Fetch exact-version license files from the npm registry")
    args = parser.parse_args()
    print(json.dumps(collect(Path(__file__).resolve().parents[2], args.fetch_web), indent=2))


if __name__ == "__main__":
    main()
