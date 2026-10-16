# DOCX 模板自动化

MACS（内部小名 OpenKitty）的文档自动化目标是把“每家企业、每种样品都写一套填表代码”改成运行时生成声明式填写计划：Agent 检查模板结构，匹配企业/LIMS 数据字段，仅在歧义处补充映射，然后由通用 DOCX 引擎保真执行。

这里由大模型承担跨企业反复出现但语义上不同的机械工作：理解字段含义、结合上下文选择数据来源、提出重复行方案和处理少量例外；确定性 OOXML 引擎承担写入、结构保真和完整性检查。首次成功方案按模板指纹沉淀，后续批次不再重复推理全部字段。

Web 用户可从“工作台 → 文档自动化”进入生产工作区：上传 DOCX 与 JSON/CSV，检查自动映射和置信度，必要时修改数据来源，预览生成结果，再下载最终 Word。页面调用的 `/api/document-automation/*` 与 Agent 使用的 `extract` 工具共享同一套检查、映射、重复行、校验和模板指纹实现。

## 运行闭环

```text
企业 DOCX 模板 + LIMS/Excel/JSON 数据
  -> inspect_template：字段、类型化内容控件、MERGEFIELD、可填写书签、下划线、表格/行/单元格清单
  -> preview_mapping：候选字段、固定单元格建议、置信度、歧义、缺失项
  -> field_mapping / table_cells / repeat_rows：Agent 生成声明式计划
  -> 通用填写引擎：标量字段 + 固定表格单元格 + 重复表格行
  -> strict / verify_complete：未匹配与残留占位检查
  -> save_template_profile：按模板结构指纹保存字段路径和表格规则
  -> 新 DOCX + 可核对回执
```

实现入口是 `internal/tools/content/extract.go`，具体逻辑按职责拆分为：

- `docx_template_fill.go`：OOXML 文本、内容控件、下划线与原子输出。
- `docx_legacy_fields.go`：传统/复杂 Word 模板的 `MERGEFIELD`、书签、复选框及统一字段发现。
- `docx_tables.go`：表格、行、物理单元格及视觉网格列结构检查。
- `docx_table_cells.go`：稳定单元格地址、邻接/表头候选推断、合并关系校验及固定单元格填写。
- `docx_data_mapping.go`：嵌套数据展开、实验室同义字段、置信度与歧义。
- `docx_repeat_rows.go`：按数组克隆模板行、占位符或逐列绑定。
- `docx_template_profile.go`：模板结构指纹、项目级方案保存与后续批次复用。

## 第一步：检查与映射预览

```json
{
  "file": ".openkitty/storage/acme-template.docx",
  "source_data": {
    "enterprise": "某企业",
    "report_no": "R-2026-001",
    "records": [
      {"sample_no": "S-001", "test_item": "pH", "result": "7.20"}
    ]
  },
  "inspect_template": true,
  "preview_mapping": true
}
```

结果包含模板字段、表格编号、物理行列、视觉网格列、跨列数、纵向合并状态，以及每个标量字段的 `source_path`、`confidence`、`status` 和备选项。空白单元格只有在同一行邻接标签或同一视觉列上方存在表头时才成为候选；纵向合并的 continuation 单元格不会成为候选。匹配采用保守策略：高置信度候选可生成 `table_cells` 计划，低置信度或相近候选留给 Agent/用户确认，而不会被引擎静默写入。

## 第二步：声明式填写

```json
{
  "file": ".openkitty/storage/acme-template.docx",
  "output": ".openkitty/storage/acme-report.docx",
  "source_data": {
    "enterprise": "某企业",
    "report_no": "R-2026-001",
    "department": "理化实验室",
    "records": [
      {"sample_no": "S-001", "test_item": "pH", "result": "7.20"},
      {"sample_no": "S-002", "test_item": "水分", "result": "0.13%"}
    ]
  },
  "field_mapping": {
    "委托单位": "enterprise",
    "报告编号": "report_no"
  },
  "table_cells": [
    {
      "part": "word/document.xml",
      "table_index": 1,
      "row": 1,
      "cell": 2,
      "source_path": "department"
    }
  ],
  "repeat_rows": [
    {
      "part": "word/document.xml",
      "table_index": 1,
      "template_row": 2,
      "data_path": "records",
      "column_keys": ["$index", "sample_no", "test_item", "result"]
    }
  ],
  "save_template_profile": true,
  "template_profile_name": "某企业-样品检验报告",
  "strict": true,
  "verify_complete": true
}
```

`table_cells` 用于固定表单格，地址全部来自检查结果，并用 `source_path` 绑定任意企业数据结构；它还可接收一次性的标量 `value`。对于带 `{{sample_no}}` 等占位符的模板行，可以省略 `column_keys`；对于完全没有占位符的旧模板，Agent 根据检查结果按单元格顺序提供 `column_keys`。`$index` 生成从 1 开始的序号。

首次填写成功后，方案保存在项目的 `.openkitty/document-templates/<fingerprint>.json`。文件只记录字段路径、固定单元格地址、重复行地址和映射阈值，不保存报告编号、样品号、检测结果等本批业务值。同一模板的下一批数据可以省略 `field_mapping`、`table_cells` 和 `repeat_rows`：

```json
{
  "file": ".openkitty/storage/acme-template.docx",
  "output": ".openkitty/storage/acme-report-next-batch.docx",
  "source_data": {
    "enterprise": "某企业",
    "report_no": "R-2026-002",
    "records": [
      {"sample_no": "S-101", "test_item": "pH", "result": "7.10"}
    ]
  },
  "use_template_profile": true,
  "strict": true
}
```

未显式设置 `use_template_profile` 时，只要传入 `source_data` 且没有重新指定 `field_mapping`/`table_cells`/`repeat_rows`，引擎也会自动尝试复用已保存的匹配方案；显式设为 `false` 可关闭复用，显式设为 `true` 则在没有匹配方案时报错。模板指纹只取正文、页眉、页脚、脚注、尾注和批注 XML，因此 ZIP 打包时间或 Office 元数据变化不会误伤复用；模板内容或结构变化后会生成新指纹并要求重新确认。

## 覆盖与边界

当前支持：

- 跨多个 Word run 的 `{{key}}` 占位符。
- 使用 tag/alias 命名的 Word 内容控件，包括重复节内嵌套的叶子控件。
- 简单及复杂形式的 Word `MERGEFIELD`，输出时固化为保留原样式的普通文本，避免刷新域后回退；同时支持用于填值的命名书签。
- Word 复选框的真实 checked 状态与显示符号；下拉/组合框保留控件定义并更新显示值。
- 带可见标签的下划线字段，以及按顺序填写的匿名下划线。
- 固定表格单元格的稳定地址、邻接/表头候选建议、横向跨列识别和纵向合并 continuation 防误写。
- 平面表格的模板行复制；行内可混用占位符、内容控件、`MERGEFIELD` 和书签，行、单元格及合并属性随 XML 原样复制。
- JSON 嵌套标量字段及数组数据；常见实验室中英文字段别名。
- 映射预览、显式覆盖、严格匹配和输出完整性检查。
- 按 DOCX 结构指纹保存并复用企业模板方案；原子更新方案且不持久化实验数据值。

仍需后续增强：嵌套表格的独立寻址、图片内容控件、条件段落/章节、公式与单位/范围校验，以及 DOCX 渲染后的分页和溢出视觉检查。
