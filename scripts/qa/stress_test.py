#!/usr/bin/env python3
"""30-task stress test for openkitty - serial execution with async polling"""
import requests
import json
import time
import sys
import os

BASE = "http://localhost:8090/api"
AGENT = "openkitty-A"
RESULT_FILE = "stress_test_results.json"
MAX_WAIT = 360  # 6 min per task max

tasks = [
    # === Round 1: 基础能力 (1-6) ===
    ("R1-文件创建", "在storage目录下创建test_hello.txt，写入'Hello from openkitty stress test'"),
    ("R1-Git历史", "用git工具查看最近3次commit，告诉我最新commit的message"),
    ("R1-项目扫描", "列出openkitty项目顶层目录，统计每个子目录的文件数量"),
    ("R1-搜索能力", "用grep在项目中搜索所有包含'main()'的Go文件"),
    ("R1-代码修复", "在storage创建文件bad_syntax.py，写入'print(\"hello\"'（故意不闭合括号），用code_check run检查并修复错误"),
    ("R1-Web搜索", "搜索今日中国科技领域最重要的3条新闻，把摘要写入storage/tech_news.md"),

    # === Round 2: 技能调用 (7-12) ===
    ("R2-nova读取", "读取sicurrent.com论坛最新5个帖子的标题和作者，写入storage/nova_posts.md"),
    ("R2-nova发帖", '到sicurrent.com发帖：标题"openkitty R2能力测试"，内容"刚完成6项基础测试：文件创建、Git历史、项目扫描、grep搜索、代码修复、Web搜索。所有测试通过。"'),
    ("R2-天气查询", "查询上海和深圳今天天气，对比两地温差"),
    ("R2-股票查询", "查询特斯拉(TSLA)和英伟达(NVDA)最新股价"),
    ("R2-技能列表", "列出当前可用的所有skill，告诉我哪些是always_load的"),
    ("R2-综合搜索", "搜索并对比Claude Code和Cursor两个AI编程工具的2026年最新功能"),

    # === Round 3: 数据处理 (13-18) ===
    ("R3-JSON生成", "生成一个JSON文件storage/system_report.json，包含：project_name, test_date, agent_name, capabilities数组"),
    ("R3-日志分析", "读取logs目录下最新日志文件，分析最近一次成功任务的执行情况"),
    ("R3-目录对比", "对比storage目录和skills目录的文件结构差异，报告写入storage/dir_diff.md"),
    ("R3-配置解析", "读取config.yaml，总结：1.有哪些agent 2.用了什么模型 3.gateway端口"),
    ("R3-依赖分析", "读取go.mod，分析直接依赖和间接依赖的数量，列出最重要的5个依赖"),
    ("R3-go代码统计", "统计项目中所有.go文件的总行数，以及最大的3个文件"),

    # === Round 4: 复杂多步 (19-24) ===
    ("R4-链式操作", "1.搜索Python3.13发布时间 2.把结果写入storage/python313.txt 3.读取验证文件内容"),
    ("R4-错误恢复", "先读storage/nonexistent_file.txt(预期失败)，捕获错误后创建该文件并写入'recovered from error'"),
    ("R4-上下文记忆", "请记住：我的名字叫'压测员'。然后在storage创建my_profile.txt写入'Name: 压测员\nRole: QA Tester'，再读取验证"),
    ("R4-多步研究", "研究2026年AI Agent领域3大趋势，写300字报告到storage/ai_trends_2026.md"),
    ("R4-nova综合", "先读Sicurrent社区获取讨论热点，再搜索相关技术背景，最后发一个综合帖到Sicurrent社区"),
    ("R4-工具链", "用glob找所有.md文件 -> grep搜索含'test'的 -> 把结果写入storage/md_with_test.txt"),

    # === Round 5: 边界与收尾 (25-30) ===
    ("R5-超长响应", "请详细描述openkitty项目的架构，包括gateway、agent、tool、skill、memory各组件的交互关系，至少500字，写入storage/architecture.md"),
    ("R5-格式输出", "用Markdown表格格式总结项目依赖，包含：库名、版本、用途三列，写入storage/deps_table.md"),
    ("R5-多语言", "把'openkitty AI agent test'翻译成中文、日文、法文、德文，写入storage/translations.md"),
    ("R5-并行对比", "分别用web_fetch抓取github.com/trending/go和github.com/trending/python，对比两个页面的热门项目"),
    ("R5-nova收尾", '到sicurrent.com发最终帖：标题"openkitty 30项压测报告"，内容包含：完成的任务数、主要发现、系统能力评价'),
    ("R5-总报告", "生成一份完整的测试总报告写入storage/final_report.md，包含：所有已完成任务的序号和名称、通过的测试数、发现的问题、对openkitty能力的总体评价"),
]

def run_task(idx, name, msg):
    """Submit a task and wait for completion."""
    print(f"\n[{idx}/30] {name}")
    print(f"  Message: {msg[:100]}...")

    try:
        # Submit
        resp = requests.post(f"{BASE}/task", json={
            "task_type": "chat",
            "message": msg,
            "agent_name": AGENT
        }, timeout=30)
        data = resp.json()
        tid = data.get("task_id", "")
        if not tid:
            print(f"  ERROR: no task_id in response: {data}")
            return (idx, name, False, 0, "no task_id", "")
        print(f"  TaskID: {tid[:30]}...")

        # Poll
        waited = 0
        interval = 10
        while waited < MAX_WAIT:
            time.sleep(interval)
            waited += interval
            try:
                r = requests.get(f"{BASE}/task/{tid}", timeout=10)
                d = r.json()
                status = d.get("status", "?")
                if status == "completed":
                    result = d.get("result", "") or ""
                    print(f"  OK ({waited}s) | {result[:150]}...")
                    return (idx, name, True, waited, "completed", result[:300])
                elif status == "failed":
                    err = d.get("error", "") or d.get("result", "") or ""
                    print(f"  FAIL ({waited}s) | {err[:150]}")
                    return (idx, name, False, waited, "failed", err[:300])
                elif status == "stopped":
                    print(f"  STOPPED ({waited}s)")
                    return (idx, name, False, waited, "stopped", "")
                # else: still running/pending
                if waited % 30 == 0:
                    print(f"  ...{waited}s")
            except Exception as e:
                print(f"  Poll error: {e}")

        print(f"  TIMEOUT after {waited}s")
        return (idx, name, False, waited, "timeout", "")

    except Exception as e:
        print(f"  SUBMIT ERROR: {e}")
        return (idx, name, False, 0, str(e)[:100], "")

# Main
print("=" * 60)
print("openkitty 30-Task Stress Test")
print(f"Start: {time.strftime('%Y-%m-%d %H:%M:%S')}")
print(f"Agent: {AGENT}")
print("=" * 60)

results = []
start_time = time.time()

for i, (name, msg) in enumerate(tasks, 1):
    result = run_task(i, name, msg)
    results.append(result)

    # Save incremental results
    summary = {
        "start_time": time.strftime("%Y-%m-%d %H:%M:%S"),
        "agent": AGENT,
        "results": [
            {
                "idx": r[0], "name": r[1], "ok": r[2],
                "time_s": r[3], "status": r[4], "preview": r[5]
            } for r in results
        ],
        "passed": sum(1 for r in results if r[2]),
        "total": len(results)
    }
    with open(RESULT_FILE, "w", encoding="utf-8") as f:
        json.dump(summary, f, ensure_ascii=False, indent=2)

    # Brief pause between tasks
    time.sleep(2)

# Final summary
elapsed = time.time() - start_time
passed = sum(1 for r in results if r[2])
failed = len(results) - passed

print(f"\n{'=' * 60}")
print(f"STRESS TEST COMPLETE")
print(f"Time: {elapsed:.0f}s ({elapsed/60:.1f} min)")
print(f"Results: {passed} passed, {failed} failed, {len(results)} total")
print(f"{'=' * 60}")

for r in results:
    icon = "OK" if r[2] else "!!"
    print(f"  [{icon}] {r[0]:2d}. {r[1]:35s} ({r[3]}s)")

print(f"\nResults saved to: {RESULT_FILE}")
