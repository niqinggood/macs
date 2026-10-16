# Changelog 2026-06-08 — 团队协作修复 + 品牌统一

## 总览

修复串行团队模式的信息衰减问题，统一品牌命名为 OpenKitty。

---

## 一、团队协作串行模式修复

### 问题

对比 `tdp_final.html`（团队产出）和 `tdp_v3.html`（单 Agent 产出），串行模式下的团队输出质量明显不如单 Agent：

| 问题 | 现象 |
|------|------|
| 信息衰减 | 成员详情丢失、角色分组被拍平 |
| 设计不一致 | 多个版本色系各不同 |
| 无效功能 | 装饰性分页按钮，不实际工作 |

### 根因

串行模式下每个角色只看到**上一个角色的输出**，原始任务和源数据在传递链中丢失。下游角色不知道上游漏了什么。

### 修复

`assemble_team.go` — `executeSequential()` 重写：

1. **原始任务始终保留** — 每个角色都看到完整原始任务，不再被上游输出替代
2. **所有上游产出可见** — 后续角色可展开查看`所有`历史产出（而非仅上一个），用 `<details>` 折叠
3. **质量指引内置** — "如果发现上游遗漏了原始任务中的关键信息，请补全"
4. **首角色责任明确** — "请为后续角色打好基础：完整覆盖原始任务的所有要点"

### 关键代码变更

```go
// 之前：只传上一个角色输出
if i > 0 && previousOutput != "" {
    roleTask += fmt.Sprintf("...<previous_output>\n%s\n</previous_output>...", previousOutput)
}

// 之后：保留原始任务 + 所有上游产出
roleTask.WriteString(fmt.Sprintf("\n\n## 原始任务 (始终保留，不要丢失上下文)\n%s", task))
if i > 0 {
    for j := 0; j < i; j++ {
        roleTask.WriteString(fmt.Sprintf("### %s 的输出\n<details>...", roles[j].Role, ...))
    }
    roleTask.WriteString("**重要指引**: ...如果发现上游遗漏，请补全。")
}
```

---

## 二、品牌统一：Kitty → OpenKitty

| 文件 | 改动 |
|------|------|
| `build_mac.bat` | 二进制名 `kitty_mac_*` → `openkitty_mac_*` |
| `build_mac.bat` | .app 包名 `kitty_*.app` → `openkitty_*.app` |
| `build_mac.bat` | Info.plist bundle name/identifier 同步更新 |
| `build_mac.bat` | 启动脚本可执行名同步 |
| `build_mac.bat` | 新增 Mac 部署提示：`chmod +x openkitty_mac_*` |

---

## 三、Skill 描述补全

| 文件 | 改动 |
|------|------|
| `skills/remote_deploy/SKILL.md` | 描述补充中文关键词，方便 CJK 意图匹配 |
| `skills/remote_deploy/SKILL.md` | 添加 `tool_detail("remote")` 使用提示 |

---

## 涉及文件

```
assemble_team.go        — 串行团队模式信息保留修复
build_mac.bat           — Kitty → OpenKitty 品牌统一
skills/remote_deploy/SKILL.md — 中英双语描述 + 工具发现提示
```
