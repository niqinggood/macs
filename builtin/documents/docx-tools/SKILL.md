---
name: docx-tools
description: macs Word 文档处理。读取和创建 DOCX、检查模板、字段映射、重复表格行、保真填写、审阅修订与批注，并核对生成结果。
triggers: ['docx', 'Word', '模板填写', '文档自动化', '修订', '批注']
version: 2.0.0
author: macs
license: macs project license
---

# Word 文档处理

先确定用户需要读取文档、生成新文档、填写既有模板，还是修改已经排版的原稿。使用当前可用的文档工具完成任务，并检查实际输出文件。本文由 macs 项目独立编写；没有附带其他服务的专有脚本或格式规范副本。

## 读取与结构检查

通过 `tool_detail` 查询 `extract` 的当前参数，提取段落、表格及必要的文档结构。DOCX 是 OOXML 压缩包，文字可能跨多个 run；段落、表格、页眉页脚和文本框应按用户需要分别检查。扫描图片或旧版 DOC 输入应先确认读取或转换能力。

需要细查 OOXML 时，使用 ZIP 与 XML 解析库读取相应 part；不要直接用正则替换整份 XML。检查解包路径位于目标目录内；限制解压后的总大小，不执行文档内宏或外部链接。

## 模板填写

调用 `extract` 的 `inspect_template` 检查模板，再以 `source_data` 和 `preview_mapping` 生成映射候选。核对类型、附近标签、单位、来源路径和歧义；通过 `field_mapping`、`table_cells` 或 `repeat_rows` 表达确定的填写计划。

正式生成使用 `strict` 与 `verify_complete`。使用原有引擎处理字段、书签、内容控件、复选框、固定单元格和重复行；不要为某一客户或模板另写按名称硬编码的引擎。首次成功后可保存模板 profile，后续提供新数据并按结构指纹复用。

复用规则只保存映射与结构，不包含样品值、客户数据或实验结果。保存后核对实际文件、未填字段、表格行列和用户要求的值。字段完整不自动证明业务数值正确。

## 创建与编辑

优先使用现有 `document_generator` 或 `convert` 工具；先发现实际 Schema，不编造参数。必要时可以使用已有的 `python-docx`、标准 ZIP/XML 工具或本机 Office 转换能力。编辑原稿时延续原有样式、节、页眉页脚、编号和关系，避免重建无关内容。

Preserve the source document by default. Write changes to a distinct output and verify that output before replacing any existing result.

遇到文件占用或 `PermissionError`，先确认目标文件和保存位置；do not request elevation。优先 save the completed document under a new filename，并明确告知实际生成的位置。

用户要求修订或批注时，保留已有作者记录、批注锚点和修订结构；接受或拒绝修订只针对用户明确授权的范围。若现有工具不能可靠保留对应结构，先生成独立的审阅意见或副本，并说明尚未实现的格式行为。

## 验收

重新打开最终 DOCX，核对段落、表格、数值及包关系。有 PDF 或 Word 渲染能力时逐页检查裁切、错位、缺字和分页。涉及批注、修订、字段或交互控件，还需核对对应 OOXML 结构；渲染图不能代替这些结构检查。

只有验证过的文件才能作为完成交付。外部程序不可用时说明具体缺失能力，保留可编辑文件和已完成的结构检查结果。
