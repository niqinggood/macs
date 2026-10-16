# Phase 7: 关联合同/法规交叉引用 (cross-reference)

## 目标

将当前合同与相关合同、法规、判例关联。

## 输入

Phase 1-6 全部输出 + 关联合同 + 相关法规。

## Prompt

```
Cross-reference this contract with related materials:

Current contract analysis: [All Phase 1-6 outputs]
Related contracts: {related_contract_paths}
Relevant regulations: {regulation_list}

Build cross-references:
1. **Contract family tree** — how does this contract relate to others?
   - Master agreement → SOW #3 → Amendment #2
2. **Regulatory mapping** — which clauses address which regulatory requirements?
3. **Precedent watch** — clauses that have been litigated and how courts interpreted them
4. **Template deviation** — how does this contract differ from the standard template? Why?
5. **Knowledge base tags** — tag each unique clause pattern for future retrieval

The goal is to make this contract analysis retrievable when reviewing future contracts.

For every controlling regulation record the official title, issuing authority, article/document number, effective status, applicable date, direct official URL, and retrieval date. Search snippets and third-party summaries are discovery leads only. For every precedent record the case number, court, decision date, case level/type, holding used, and source URL; if these cannot be verified, label the item pending verification and do not infer the holding.
```
