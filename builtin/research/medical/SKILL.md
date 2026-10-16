---
name: medical-analysis
description: Medical document analysis, ICD coding, clinical NER extraction
tags: [medical, healthcare, hipaa, clinical]
---
# Medical Analysis Skill

Analyze medical documents: extract diagnoses, medications, lab results, procedures.

## Usage
1. Use `redact_pii` first to de-identify patient data
2. Use `read_file` to load medical records (.pdf, .docx, .txt)
3. Extract structured data: ICD codes, drug names, dosages, vital signs
4. Output to `storage/medical/` directory

## HIPAA Compliance
Always run `redact_pii` before sending any data to cloud models.
Use local models (Ollama) for initial screening when possible.
