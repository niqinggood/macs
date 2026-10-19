#!/bin/bash
BASE="http://localhost:8090/api"

test_one() {
  local n="$1" msg="$2"
  echo "[$n] $msg"
  local tid=$(curl -s -X POST "$BASE/task" -H "Content-Type: application/json" \
    --data-raw "{\"task_type\":\"chat\",\"message\":\"$msg\",\"agent_name\":\"openkitty-A\"}" 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('task_id','?'))" 2>/dev/null)
  echo "  TaskID: ${tid:0:30}..."
  
  for i in $(seq 1 72); do
    sleep 5
    local resp=$(curl -s "$BASE/task/$tid" 2>/dev/null)
    local status=$(echo "$resp" | python3 -c "import sys,json;print(json.load(sys.stdin).get('status','?'))" 2>/dev/null)
    if [ "$status" = "completed" ]; then
      local result=$(echo "$resp" | python3 -c "import sys,json;r=json.load(sys.stdin).get('result','');print(r[:200])" 2>/dev/null)
      echo "  PASS ($((i*5))s): $result"
      return 0
    elif [ "$status" = "failed" ]; then
      local err=$(echo "$resp" | python3 -c "import sys,json;e=json.load(sys.stdin).get('error','');print(e[:150])" 2>/dev/null)
      echo "  FAIL ($((i*5))s): $err"
      return 1
    fi
    if [ $((i % 6)) -eq 0 ]; then echo "  ...${i}0s"; fi
  done
  echo "  TIMEOUT"
  return 1
}

echo "=== OpenKitty Quick Capability Test ==="
echo "Start: $(date)"
echo ""

# Run 10 representative tests
test_one 01 "在storage目录下创建quick_test.txt，写入Quick test passed at $(date +%H:%M)"
test_one 02 "用git工具查看最新commit的message和作者"
test_one 03 "列出openkitty项目skills目录下有哪些技能子目录"
test_one 04 "搜索并对比Go和Rust在2026年的最新版本，写入storage/lang_compare.md"
test_one 05 "到sicurrent.com发帖，标题quick_test_report，内容刚刚完成了文件创建、Git查询、技能列表和语言对比四个测试"
test_one 06 "读取config.yaml中model_cfg配置，告诉我有几个模型、分别是什么"
test_one 07 "在storage创建bug.py写入print(missing_quote)，用code_check run检查并修复语法错误"
test_one 08 "用glob找出项目中所有.md文件，统计数量"
test_one 09 "读取go.sum前20行，分析项目依赖"
test_one 10 "总结你今天帮我做的所有测试任务，把总结写入storage/quick_test_summary.md"

echo ""
echo "Done: $(date)"
