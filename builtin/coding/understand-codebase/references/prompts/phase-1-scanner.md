# Phase 1: 项目结构扫描 (project-scanner)

## 目标

快速了解项目的技术栈、目录结构、入口点、模块划分。

## 执行方式

使用单个 Explore agent 扫描项目根目录。

## Prompt

```
Scan the project at {project_path}. Report:
1. Languages and frameworks used (with versions if detectable)
2. Build system and package manager
3. Entry points (main files, CLI entry, server entry)
4. Top-level directory structure with each directory's purpose
5. Configuration file locations
6. Test directory structure and test framework
7. Key dependencies (from package file, go.mod, requirements.txt, etc.)

Be thorough — list all top-level and second-level directories with their apparent purpose.
Output in structured markdown with clear section headers.
```
