#!/usr/bin/env python3
"""20-task iterative test with regression checking and optimization loop"""
import requests, json, time, os, sys

BASE = "http://localhost:8090/api"
AGENT = "openkitty-A"
OUTDIR = "storage/round1/reports"
MAX_WAIT = 420  # 7 min per task
RESULT_FILE = "iterative_test_results.json"

# ============================================================
# ROUND 1: Foundation (tasks 1-5)
# ============================================================
ROUND1 = [
    ("R1T1-工作区初始化",
     f"在storage/round1/下创建3个子目录: data, reports, code。然后在reports下创建README.md，"
     "内容包含: 项目名openkitty压力测试、测试日期2026-05-24、测试目的验证AI agent能力。创建完成后用list_dir验证目录结构。"),

    ("R1T2-项目能力报告",
     "读取config.yaml和go.mod两个文件，综合分析后生成一份项目能力报告写入"
     f"{OUTDIR}/capability.md，报告需包含: 1.配置了哪些agent及其模型 2.go.mod的直接依赖数量和前5个 3.skills目录有多少技能 4.项目的git最新commit。不要只列数据，要有分析段落。"),

    ("R1T3-Web深度搜索",
     "搜索'2026 AI agent framework open source comparison'，至少用2个不同搜索引擎，"
     "对比至少3个AI agent框架的功能差异，把研究结果写成一篇结构化文章保存到"
     f"{OUTDIR}/ai_frameworks.md，要求至少300字、包含对比表格。写完后用read_file验证内容完整性。"),

    ("R1T4-Git深度分析",
     "对openkitty项目做完整git分析: 1.最早的commit是什么时候 2.有多少次commit "
     "3.哪些文件被修改最多次 4.最近5次commit的详细message。"
     f"把分析结果写成一份报告保存到{OUTDIR}/git_analysis.md。"),

    ("R1T5-Sicurrent社区综合发帖",
     "先读取sicurrent.com论坛现有的帖子了解社区风格，然后在Sicurrent社区发一个新帖: "
     "标题'openkitty Round1 基础能力验证报告'，内容需要包含: "
     "1.刚才完成的工作区初始化、项目能力分析、Web深度搜索、Git深度分析四项任务的简述 "
     "2.每项任务的成功/失败状态 3.对openkitty当前能力的评价。"
     "发帖后读取论坛验证帖子是否成功发布。"),
]

# ============================================================
# ROUND 2: Complexity (tasks 6-10)
# ============================================================
ROUND2 = [
    ("R2T6-Python代码生成执行",
     f"在storage/round1/code/下创建analyzer.py，功能是: 扫描{OUTDIR}/目录下所有.md文件，"
     "提取每个文件的标题(第一个#开头的行)、字数、段落数，生成一个汇总JSON写入"
     f"{OUTDIR}/reports_index.json。用code_check run执行这个脚本，如果出错就修复，"
     "然后验证JSON文件是否正确生成。"),

    ("R2T7-论坛追踪和扩展",
     "1.读取sicurrent.com论坛，找到我们之前发的Round1报告帖子(标题含'Round1') "
     "2.基于Round1报告中发现的项目能力，搜索相关的技术改进建议 "
     "3.发一个'Round1追踪帖'到Sicurrent社区，包含:原始帖子的关键发现、新搜索到的改进建议、下一步测试计划。"
     "如果找不到之前的帖子，就发一个新帖说明情况。"),

    ("R2T8-最大文件代码分析",
     "1.用工具找出openkitty项目中最大的Go文件(按行数) 2.读取该文件分析: "
     "总函数数量、最长的5个函数及其行数、import了哪些包、主要数据结构定义 "
     f"3.把完整的代码结构分析写入{OUTDIR}/code_structure.md。"
     "要求分析深度足够，不只是列出函数名，还要说明各模块的职责。"),

    ("R2T9-多维数据综合",
     "这是一个组合任务，请依次完成: "
     "a)查询北京、上海、深圳三城市今日天气 "
     "b)查询苹果(AAPL)、特斯拉(TSLA)、英伟达(NVDA)三只股票最新价 "
     "c)搜索今日全球金融市场要闻 "
     f"d)将以上三类数据整合成一份'市场天气综合日报'写入{OUTDIR}/market_weather.md，"
     "要求包含数据表格、趋势分析和投资建议三个板块。"),

    ("R2T10-技术债务审计",
     "1.用grep在整个项目中搜索以下模式: TODO, FIXME, HACK, XXX, BUG, DEPRECATED "
     "2.对搜索结果去重并按文件分组 "
     "3.分析哪些文件技术债务最重 "
     f"4.写一份技术债务审计报告到{OUTDIR}/todo_audit.md，"
     "包含:总发现数、按类型统计、按文件统计Top10、改进优先级建议。"),
]

# ============================================================
# ROUND 3: Stress (tasks 11-15)
# ============================================================
ROUND3 = [
    ("R3T11-全量代码索引",
     "读取openkitty项目根目录下所有.go文件(不含子目录的bak/oldversion/node_modules)，"
     "对每个文件提取:导出的函数名、导出的类型名、导出的常量。"
     f"生成一份完整的代码索引写入{OUTDIR}/code_index.md，"
     "包含文件列表、每个文件的导出符号清单、按类型分类的全局索引。"),

    ("R3T12-架构深度文档",
     "深入分析openkitty的核心架构，重点研究gateway.go、mac.go、skill.go、"
     "sub_agent_tool.go、safety.go这5个核心文件之间的关系。生成一份800字以上的架构文档写入"
     f"{OUTDIR}/architecture.md，包含: "
     "1.整体架构图(用ASCII art) 2.各模块职责 3.数据流/请求流 4.模块间依赖关系 5.设计模式识别。"),

    ("R3T13-错误恢复测试",
     "依次完成以下错误场景并记录恢复过程: "
     "a)创建storage/round1/code/broken_py.py写入有3个语法错误Python代码，用code_check找出并修复 "
     "b)创建storage/round1/code/broken_go.go写入有类型错误的Go代码，用code_check找出问题 "
     "c)尝试读取一个不存在的HTTP URL(如http://invalid-nonexistent-12345.com)，优雅处理网络错误 "
     f"d)把以上3个场景的错误原因、修复方法、恢复结果写成错误处理报告到{OUTDIR}/error_recovery.md。"),

    ("R3T14-长上下文压力",
     "这是对上下文管理能力的压力测试: "
     "1.依次读取项目中最大的5个文件(gateway.go, mac.go, web_fetch.go, ProjectCanvas.js, main.go)，对每个文件写2-3句内容摘要 "
     "2.然后读取storage/round1/reports/下所有已生成的报告 "
     "3.基于所有已读内容，生成一份'openkitty项目全景概述'写入"
     f"{OUTDIR}/project_overview.md，内容需要综合所有读取的文件信息，字数不少于500。"
     "注意: 中间不要丢失早期的文件信息，这测试的是上下文保持能力。"),

    ("R3T15-Sicurrent社区分析",
     "对sicurrent.com论坛做全面分析: "
     "1.读取所有论坛帖子(分页获取直到读完) "
     "2.统计:总帖数、作者分布、最活跃话题、帖子时间分布趋势 "
     "3.写一份社区分析报告发到Sicurrent社区(标题'openkitty Sicurrent社区分析报告') "
     f"4.同时把报告保存到{OUTDIR}/community_analysis.md。"
     "注意: 发帖需要先登录，参考skills/nova-forum/SKILL.md的指引。"),
]

# ============================================================
# ROUND 4: Integration (tasks 16-20)
# ============================================================
ROUND4 = [
    ("R4T16-项目健康检查",
     "创建openkitty项目全面健康检查系统，需要检查: "
     "a)Git状态(是否有未提交更改) b)依赖审计(过期依赖、安全漏洞) "
     "c)配置验证(config.yaml完整性检查) d)技能清单(所有技能是否可读取) "
     f"e)工具可用性(统计注册了多少工具) f)生成健康检查报告到{OUTDIR}/health_check.md，"
     "每项给PASS/WARN/FAIL评级并说明原因。"),

    ("R4T17-回归验证",
     "现在重新执行Round1的全部5个任务: "
     "1.验证storage/round1/reports/目录和README.md是否仍然存在且内容完整 "
     "2.重新读取config.yaml和go.mod，对比capability.md中的信息是否仍然准确 "
     "3.重新搜索'2026 AI agent framework'，对比ai_frameworks.md的内容是否需要更新 "
     "4.重新做git分析，对比git_analysis.md看是否有新commit "
     "5.读取Sicurrent社区验证Round1的帖子是否还在 "
     f"将对比结果写成回归测试报告保存到{OUTDIR}/regression_test.md。"),

    ("R4T18-贡献者指南",
     "基于对整个openkitty项目的深入理解，撰写一份完整的'Contributor Guide'写入"
     f"{OUTDIR}/contributor_guide.md，最少500字，包含: "
     "1.项目整体结构(目录树) 2.如何添加一个新skill(详细步骤) "
     "3.如何添加一个新tool(详细步骤) 4.代码规范 5.测试规范 6.提交流程。"
     "需要引用实际代码示例来说明。"),

    ("R4T19-项目展示",
     f"创建一份'openkitty项目展示'文档写入{OUTDIR}/presentation.md，"
     "格式为markdown幻灯片(用---分隔每页)，包含: "
     "1.封面页(项目名+一句话描述) 2.架构概览页 3.核心能力页 "
     "4.本次测试结果页(包含关键数据) 5.技术栈页 6.未来规划页。"
     "每页都要有实质内容，不要空洞的标题。共6-8页。"),

    ("R4T20-终极综合",
     "这是最终的综合测试: "
     f"a)读取storage/round1/reports/下所有20个任务生成的报告文件 "
     f"b)创建一份总索引INDEX.md在storage/round1/下，链接到所有报告并附一句话摘要 "
     "c)生成一份'openkitty最简测试报告'写入"
     f"{OUTDIR}/final_summary.md，包含:测试总数、通过率、发现的3个最大优势和3个最大不足"
     "d)在Sicurrent社区发最终帖，标题'openkitty 20项综合能力测试完成报告'，总结全部测试 "
     "e)读取Sicurrent社区验证帖子发布成功。"),
]

ALL_ROUNDS = [
    ("Round 1: Foundation", ROUND1),
    ("Round 2: Complexity", ROUND2),
    ("Round 3: Stress", ROUND3),
    ("Round 4: Integration", ROUND4),
]

def ensure_dir(path):
    os.makedirs(os.path.dirname(path) if '.' in os.path.basename(path) else path, exist_ok=True)

def submit_task(msg):
    resp = requests.post(f"{BASE}/task", json={
        "task_type": "chat", "message": msg, "agent_name": AGENT
    }, timeout=30)
    return resp.json().get("task_id", "")

def poll_task(tid, max_wait=MAX_WAIT):
    waited = 0
    while waited < max_wait:
        time.sleep(10)
        waited += 10
        try:
            d = requests.get(f"{BASE}/task/{tid}", timeout=10).json()
            s = d.get("status", "?")
            if s == "completed":
                return True, waited, d.get("result", "") or ""
            elif s == "failed":
                return False, waited, d.get("error", "") or d.get("result", "") or ""
        except:
            pass
    return False, waited, "timeout"

def run_round(round_name, tasks, start_num, all_results):
    print(f"\n{'#'*70}")
    print(f"# {round_name}")
    print(f"{'#'*70}")

    round_results = []
    for i, (name, msg) in enumerate(tasks):
        num = start_num + i
        print(f"\n--- [{num}/20] {name} ---")
        print(f"  Task: {msg[:120]}...")

        tid = submit_task(msg)
        if not tid:
            print(f"  FAIL: could not submit task")
            round_results.append((num, name, False, 0, "submit_failed"))
            continue

        ok, waited, result = poll_task(tid)
        status = "OK" if ok else "FAIL"
        print(f"  {status} ({waited}s)")

        if result:
            # Show key parts of long results
            lines = result.split('\n')
            for line in lines[:15]:
                print(f"    | {line[:120]}")
            if len(lines) > 15:
                print(f"    | ... ({len(lines)} lines total)")

        round_results.append((num, name, ok, waited, result[:500] if result else ""))
        all_results.append((num, name, ok, waited))

        # Save after each task
        save_results(all_results)

    # Round summary
    passed = sum(1 for r in round_results if r[2])
    print(f"\n  Round: {passed}/{len(round_results)} passed")
    return round_results

def save_results(all_results):
    with open(RESULT_FILE, "w", encoding="utf-8") as f:
        json.dump([
            {"num": r[0], "name": r[1], "ok": r[2], "time_s": r[3]}
            for r in all_results
        ], f, ensure_ascii=False, indent=2)

def regression_check(all_results, baseline_passed):
    """Re-run Round 1 tasks and compare with baseline"""
    print(f"\n{'!'*70}")
    print(f"! REGRESSION CHECK: Re-running Round 1")
    print(f"{'!'*70}")

    reg_results = run_round("REGRESSION: Round 1", ROUND1, 0, [])
    reg_passed = sum(1 for r in reg_results if r[2])

    print(f"\n  Baseline: {baseline_passed}/5 | Regression: {reg_passed}/5")
    if reg_passed >= baseline_passed:
        print(f"  NO REGRESSION (or improved)")
    else:
        print(f"  REGRESSION DETECTED! {baseline_passed - reg_passed} previously-passing tasks now fail")

    return reg_passed >= baseline_passed

# ============================================================
# MAIN
# ============================================================
if __name__ == "__main__":
    ensure_dir(f"{OUTDIR}/")
    ensure_dir("storage/round1/data/")
    ensure_dir("storage/round1/code/")

    print("="*70)
    print("openkitty 20-Task Iterative Test with Regression Checking")
    print(f"Start: {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print("="*70)

    all_results = []
    baseline_passed = None

    for round_name, tasks in ALL_ROUNDS:
        start_num = len(all_results) + 1
        round_results = run_round(round_name, tasks, start_num, all_results)
        passed = sum(1 for r in round_results if r[2])

        if round_name == "Round 1: Foundation":
            baseline_passed = passed

        # After Round 2 and Round 3, do regression check
        if round_name in ("Round 2: Complexity", "Round 3: Stress"):
            if baseline_passed is not None:
                ok = regression_check(all_results, baseline_passed)
                if not ok:
                    print("\n*** REGRESSION DETECTED - stopping for investigation ***")
                    print("Fix the issues and re-run from this point.")
                    # Don't stop, just flag it
                    pass

    # Final summary
    passed = sum(1 for r in all_results if r[2])
    print(f"\n{'='*70}")
    print(f"FINAL: {passed}/{len(all_results)} passed")
    print(f"{'='*70}")
    for r in all_results:
        icon = "OK" if r[2] else "!!"
        print(f"  [{icon}] {r[0]:2d}. {r[1]:35s} ({r[3]}s)")
