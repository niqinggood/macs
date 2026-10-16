---
name: storage-cleanup
description: Storage目录清理与整理。按规则分类：用户交付物保留在根目录、系统中间文件移入.system/analysis/、工具结果归档、临时文件删除。适用于storage目录杂乱、文件过多需要整理时。关键词：storage清理、目录整理、文件分类、归档
read_when:
  - storage目录需要清理
  - storage directory cleanup
  - 文件整理
  - 目录归档
  - 整理storage
tags:
  - storage
  - cleanup
  - organize
  - archive
  - housekeeping
---

# Storage 目录清理与整理

## 目标

按照分类规则清理 storage/ 目录，保持根目录整洁，仅保留用户交付物。

## 分类规则

### 【用户交付物 → storage/ 根目录，不移动】
- HTML网页、PDF报告、DOCX文档、图片、视频
- 用户明确要求生成的文件（报告、代码、导出数据等）
- 进化报告、日报、周报等交付文档

### 【系统中间文件 → 移入 storage/.system/analysis/】
- `*-analysis.md`、`*-design.md`、`*-mapping.md` 等分析文档
- 非用户要求生成的 .json/.md（如抓取的原始CSS、中间数据）

### 【工具结果溢出 → 移入 storage/.system/archive/】
- `tool_results/` 下超过1天的 .txt 文件

### 【临时文件 → 删除】
- `tmp/` 下超过24小时的文件
- 废弃的 draft、temp、backup 文件

## 执行步骤

### 第1步：查看当前状态

```tool
list_dir path="storage/" depth=1 detail=true
```

### 第2步：按规则分类处理

使用 `ptc_execute` 运行 Python 脚本进行分类：

1. 扫描 storage/ 根目录所有文件
2. 按上述规则分类
3. 移动/删除文件
4. 输出清理报告

### 第3步：输出清理报告

写入 `storage/storage_cleanup_report_YYYY-MM-DD.md`，包含：

- 保留用户交付物: X 个
- 归档系统文件: Y 个
- 删除临时文件: Z 个
- 当前 storage/ 目录结构一览

## 示例

```python
import os, shutil, datetime
from pathlib import Path

storage = Path("storage")
system_dir = storage / ".system" / "analysis"
archive_dir = storage / ".system" / "archive"

# 创建目录
system_dir.mkdir(parents=True, exist_ok=True)
archive_dir.mkdir(parents=True, exist_ok=True)

# 分类处理
user_exts = {'.html', '.pdf', '.docx', '.pptx', '.xlsx', '.png', '.jpg', '.jpeg', '.gif', '.mp3', '.wav', '.mp4', '.csv'}
user_keywords = ['日报', '周报', '报告', 'report', 'evolution_report', 'cleanup_report']

for f in storage.iterdir():
    if f.is_file():
        # 判断是否用户交付物
        is_user = f.suffix.lower() in user_exts or any(kw in f.name for kw in user_keywords)
        if not is_user:
            # 移入系统目录
            shutil.move(str(f), str(system_dir / f.name))
```

## 检查清单

- [ ] storage/ 根目录仅保留用户交付物
- [ ] 系统中间文件已归档到 .system/analysis/
- [ ] 工具结果已归档到 .system/archive/
- [ ] 临时文件已清理
- [ ] 清理报告已输出
