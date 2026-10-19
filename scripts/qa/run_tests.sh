#!/bin/bash
BASE="http://localhost:8090/api"

do_test() {
  local name="$1" msg="$2"
  echo "=== $name ==="
  local tid=$(curl -s -X POST "$BASE/task" -H "Content-Type: application/json" \
    --data-raw "{\"task_type\":\"chat\",\"message\":\"$msg\",\"agent_name\":\"openkitty-A\"}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task_id','?'))" 2>/dev/null)
  echo "  Task: $tid"
  
  for i in $(seq 1 60); do
    sleep 5
    local status=$(curl -s "$BASE/task/$tid" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','?'))" 2>/dev/null)
    if [ "$status" = "completed" ] || [ "$status" = "failed" ]; then
      local result=$(curl -s "$BASE/task/$tid" | python3 -c "import sys,json; d=json.load(sys.stdin); r=d.get('result',''); print(r[:200] if r else 'no result')" 2>/dev/null)
      echo "  $status: $result"
      return
    fi
  done
  echo "  TIMEOUT"
}

do_test "Test2-文件创建" "在storage目录下创建test_greeting.txt文件，写入Hello from openkitty capability test 2026"
sleep 3
do_test "Test3-Go项目分析" "读取go.mod文件，告诉我有多少个直接依赖，列出前5个"
sleep 3
do_test "Test4-Git历史" "用git工具查看最近3次commit，告诉我最近的commit message"
sleep 3
do_test "Test5-Nova发帖" "到sicurrent.com发帖，标题openkitty测试报告，内容简述：刚才完成了文件创建、项目分析和Git历史查询三个测试"
sleep 3
do_test "Test6-代码修复" "在storage创建bad.py写入print('hello'（故意少括号），然后用code_check run检查，根据错误修复它"
sleep 3
do_test "Test7-综合总结" "总结你今天帮我完成的所有测试任务，列出每项结果，写入storage/test_summary.md"
