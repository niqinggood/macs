#!/usr/bin/env python3
"""30-task stress test for openkitty"""
import requests
import json
import time
import sys

BASE = "http://localhost:8090/api"
TIMEOUT = 300  # max wait per task in seconds

tasks = [
    # === 基础能力 (1-5) ===
    ("基础-Web搜索", "搜索今日中国科技新闻，列出5条最重要的"),
    ("基础-文件读写", "在storage目录下创建一个test_report.md文件，写入今日日期和一句问候语"),
    ("基础-多步推理", "先列出storage目录的文件，再读取config.yaml，告诉我项目用了什么模型"),
    ("基础-技能发现", "用weather技能查询北京今天天气"),
    ("基础-并行任务", "同时做两件事：1.搜索Python最新版本 2.搜索Go最新版本，然后对比哪个更新"),

    # === 技能调用 (6-10) ===
    ("技能-nova论坛读取", "读取sicurrent.com论坛最新5个帖子，总结标题"),
    ("技能-stock查询", "查询苹果(AAPL)和特斯拉(TSLA)今日股价"),
    ("技能-news市场", "整理今日全球金融市场简报"),
    ("技能-发帖", "到sicurrent.com发帖\"openkitty压测报告-基础能力\"，内容简述你刚才完成的任务"),
    ("技能-综合", "用web_fetch抓取https://news.ycombinator.com，总结前3条热门新闻"),

    # === 代码能力 (11-15) ===
    ("代码-写Python", "写一个Python脚本storage/hello.py，打印'Hello from openkitty test'，然后运行它"),
    ("代码-调试", "在storage目录创建bug.py写入print('hello'（少一个括号），然后用code_check检查并修复"),
    ("代码-分析", "读取go.mod文件，分析openkitty项目用了哪些主要依赖"),
    ("代码-重构", "读取web_fetch.go的前100行，分析其代码结构并给出改进建议"),
    ("代码-Git", "用git工具查看最近5次commit记录"),

    # === 数据处理 (16-20) ===
    ("数据-目录扫描", "扫描openkitty项目目录结构，统计各类型文件数量（.go .py .md .yaml）"),
    ("数据-grep搜索", "在项目中搜索所有包含'truncate'函数的文件"),
    ("数据-JSON处理", "读取config.yaml解析其结构,告诉我配置了哪些agent及其模型"),
    ("数据-日志分析", "读取logs目录最新日志文件，分析最近一次任务执行情况"),
    ("数据-综合报告", "生成一份openkitty项目健康报告：文件统计+依赖列表+最新commit"),

    # === 复杂多步 (21-25) ===
    ("复杂-研究任务", "研究对比DeepSeek和GPT-4在2026年的最新进展，写一份300字报告到storage/ai_comparison.md"),
    ("复杂-链式操作", "1.搜索今日天气 2.根据天气推荐穿搭 3.把推荐发到Sicurrent社区"),
    ("复杂-错误恢复", "尝试读取一个不存在的文件storage/nonexist.txt，失败后创建它并写入'created after error'"),
    ("复杂-上下文保持", "记住我名字叫测试员，然后依次：1.创建storage/myname.txt写入\"我的名字是测试员\" 2.读取验证 3.追加一行\"测试时间:今天\""),
    ("复杂-工具链", "用web_fetch抓取一个网页→提取文本→用grep搜索关键词→把结果写入文件→读取验证"),

    # === 边界与鲁棒性 (26-30) ===
    ("边界-超长输入", f"请帮我分析以下文本的主题和情感：{'这是一个测试句子。' * 50}"),
    ("边界-歧义指令", "看看"),
    ("边界-多语言", "Translate 'Hello World' to Chinese, Japanese, French, and German, then write all translations to storage/translations.md"),
    ("边界-格式输出", "生成一个JSON格式的项目状态报告，包含：项目名、时间、状态、任务数，写入storage/status.json"),
    ("边界-总结收尾", "总结你今天完成的所有测试任务，列出每个任务的完成状态，发布到Sicurrent社区"),
]

results = []
for i, (name, msg) in enumerate(tasks, 1):
    print(f"\n{'='*60}")
    print(f"[{i}/30] {name}")
    print(f"{'='*60}")

    try:
        # Submit task
        resp = requests.post(f"{BASE}/task", json={
            "task_type": "chat",
            "message": msg,
            "agent_name": "openkitty-A"
        }, timeout=10)
        data = resp.json()
        task_id = data.get("task_id", "unknown")
        print(f"  Task ID: {task_id}")

        # Poll for completion
        waited = 0
        status = "processing"
        result = ""
        while waited < TIMEOUT:
            time.sleep(10)
            waited += 10
            try:
                r = requests.get(f"{BASE}/task/{task_id}", timeout=5)
                d = r.json()
                status = d.get("status", "unknown")
                if status in ("completed", "failed"):
                    result = d.get("result", "")[:300]
                    break
            except:
                pass
            print(f"  ...waiting {waited}s, status={status}")

        ok = status == "completed"
        print(f"  {'✅ PASS' if ok else '❌ FAIL'} ({waited}s)")
        if result:
            print(f"  Result: {result[:200]}...")
        results.append((i, name, ok, waited, status, result[:100]))

    except Exception as e:
        print(f"  ❌ ERROR: {e}")
        results.append((i, name, False, 0, str(e), ""))

    # Save progress
    with open("test_results.json", "w") as f:
        json.dump([{"id": r[0], "name": r[1], "ok": r[2], "time": r[3], "status": r[4], "preview": r[5]} for r in results], f, ensure_ascii=False, indent=2)

# Final summary
passed = sum(1 for r in results if r[2])
print(f"\n{'='*60}")
print(f"SUMMARY: {passed}/{len(results)} passed")
print(f"{'='*60}")
for r in results:
    icon = "✅" if r[2] else "❌"
    print(f"  {icon} [{r[0]:2d}] {r[1]:30s} ({r[3]}s)")
