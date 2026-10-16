# Phase 1: 合同结构扫描 (contract-scanner)

## 目标

识别合同类型、主体、基本结构。

## Prompt

```
Analyze this contract and report its structure:

Contract: {contract_text_or_path}

Report:
1. Contract type (NDA, MSA, SOW, License, Employment, etc.)
2. Parties involved (with roles:甲方/乙方, licensor/licensee, etc.)
3. Governing law and jurisdiction
4. Contract date and term/duration
5. Top-level clause structure — list all major sections with article numbers
6. Signature blocks and annexes/appendices
7. Key defined terms section location

Output in structured markdown with section headers.
```
