"""macs AgentOS 调试示例。使用 Python 3.9+，无需安装第三方库。"""

import argparse
import getpass
import json
import math
import os
import sys
import time
import uuid
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description="检查 macs 服务；--submit 会调用模型并可能产生费用。")
    parser.add_argument("--url", default=os.getenv("MACS_AGENTOS_URL", ""), help="AgentOS 地址")
    parser.add_argument("--submit", action="store_true", help="提交一条摘要任务，验证模型")
    parser.add_argument("--local", action="store_true", help="检查本机服务，可省略未启用认证的本机凭证")
    parser.add_argument("--role", default="", help="测试岗位 ID；本机默认选择 Assistant，服务接入默认 document-assistant")
    parser.add_argument("--user-id", default="", help="多用户服务凭证要求的用户 ID")
    parser.add_argument("--task", default="", help="继续查询已有任务，不创建新任务")
    parser.add_argument("--idempotency-key", default="", help="重试同一提交时沿用原编号")
    parser.add_argument("--wait-timeout", type=float, default=300, help="等待结果的秒数")
    args = parser.parse_args()
    if args.submit and args.task:
        parser.error("--submit 和 --task 请分别使用。")
    if not math.isfinite(args.wait_timeout) or args.wait_timeout <= 0:
        parser.error("--wait-timeout 必须是大于 0 的有限秒数。")

    token = os.getenv("MACS_SERVICE_TOKEN", "").strip()
    opener = build_opener(NoRedirect())

    def request(path, data=None, key="", timeout=10):
        headers = {"Accept": "application/json", "X-Request-ID": "check-" + uuid.uuid4().hex}
        if token:
            headers["Authorization"] = "Bearer " + token
        if args.user_id:
            headers["X-OpenKitty-User-ID"] = args.user_id
        if key:
            headers["Idempotency-Key"] = key
        body = None
        if data is not None:
            body = json.dumps(data, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json; charset=utf-8"
        req = Request(base + path, data=body, headers=headers)
        try:
            with opener.open(req, timeout=timeout) as response:
                raw = response.read((1 << 20) + 1)
                if len(raw) > 1 << 20:
                    raise RuntimeError("响应过大，请用完整 SDK 查看任务结果。")
                payload = json.loads(raw.decode("utf-8"))
                if not isinstance(payload, dict):
                    raise RuntimeError("响应格式异常，请检查是否填了正确的 AgentOS 地址。")
                return response.status, payload
        except HTTPError as exc:
            hints = {
                401: "凭证无效，请复制 service_clients 中对应客户端的 token。",
                403: "没有权限，请检查岗位授权和客户端 scopes。",
                404: "接口或任务不存在，请核对 AgentOS 端口和任务 ID。",
                400: "输入不符合要求，请检查岗位参数或 --user-id。",
                409: "请求发生冲突，请检查幂等编号、请求内容和岗位版本。",
                503: "服务尚未就绪，请检查数据库、队列和停机状态。",
            }
            detail = hints.get(exc.code, "请查看服务日志。")
            if 300 <= exc.code < 400:
                detail = "地址发生跳转，请填写最终的 AgentOS 地址。"
            try:
                error = json.loads(exc.read(65536).decode("utf-8"))
                detail += " " + str(error.get("message", ""))
                detail += " request_id=" + str(error.get("request_id", ""))
            except (ValueError, AttributeError):
                pass
            finally:
                exc.close()
            raise RuntimeError("HTTP " + str(exc.code) + "：" + detail) from None
        except URLError:
            raise RuntimeError("连接失败，请确认 macs 已启动，并核对 AgentOS 地址、端口和网络。") from None
        except (ValueError, UnicodeError):
            raise RuntimeError("响应不是有效 JSON，请检查是否误填了 Web 页面地址。") from None

    try:
        base = args.url or input("AgentOS 地址 [http://127.0.0.1:8081]：").strip() or "http://127.0.0.1:8081"
        base = base.strip().rstrip("/")
        parsed = urlsplit(base)
        if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise RuntimeError("请填写完整的 http:// 或 https:// 服务地址，不要附加凭证、查询参数或 #。")
        if args.local and parsed.hostname.lower() not in ("localhost", "127.0.0.1", "::1"):
            raise RuntimeError("--local 只支持本机地址；远程接入需要业务服务凭证。")
        if request("/livez")[1].get("status") != "alive":
            raise RuntimeError("服务存活检查未通过。")
        print("[1/3] 服务已启动")
        if request("/readyz")[1].get("status") != "ready":
            raise RuntimeError("服务尚未就绪，请检查数据库和启动日志。")
        print("[2/3] 服务已就绪")
        if not token and not args.local:
            token = getpass.getpass("业务服务凭证（输入不会显示）：").strip()
        if (not token and not args.local) or token.startswith("REPLACE_WITH_"):
            raise RuntimeError("请填写配置中实际的服务凭证，示例占位符不能用于认证。")
        roles = request("/api/v1/agent/roles")[1].get("roles", [])
        if not isinstance(roles, list) or any(not isinstance(role, dict) or not isinstance(role.get("role_id"), str) for role in roles):
            raise RuntimeError("岗位目录格式异常，请核对 AgentOS 服务地址和版本。")
        if not roles:
            raise RuntimeError("没有可用岗位，请检查 service_roles 和客户端 allowed_roles。")
        print("[3/3] 可用岗位：")
        for role in roles:
            print("  -", role["role_id"], role.get("description", ""))
        print("检查通过：本机服务和助手目录可用；模型回答尚未验证。" if args.local else "检查通过：服务地址和凭证可用。")
        if not args.submit and not args.task:
            print("要验证模型，可增加 --submit（会调用模型并可能扣费），继续沿用当前服务地址和凭证。")
            return 0

        task_id = args.task
        if args.submit:
            role_ids = [role["role_id"] for role in roles]
            role_id = args.role or (("Assistant" if "Assistant" in role_ids else "Assist" if "Assist" in role_ids else role_ids[0]) if args.local else "document-assistant")
            if role_id not in role_ids:
                raise RuntimeError("测试岗位不可用。请用 --role 指定上面列表中的岗位 ID。")
            key = args.idempotency_key or "summary-" + uuid.uuid4().hex
            print("正在提交模型测试，幂等编号：", key)
            print("提交中断后重试时沿用 --idempotency-key 和同一个 --role。")
            _, accepted = request("/api/v1/agent/tasks", {
                "role_id": role_id,
                "message": "将以下内容整理为工作摘要：今天完成了需求讨论与接口设计。",
            }, key=key)
            task_id = accepted.get("task_id", "")
            if not isinstance(task_id, str) or not task_id:
                raise RuntimeError("提交响应缺少任务 ID，请使用原幂等编号核对提交状态。")
        print("任务 ID：", task_id)
        deadline = time.monotonic() + args.wait_timeout
        last_status = ""
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("等待超时，服务端任务仍可能运行。请用 --task 和上面的任务 ID 继续查询。")
            _, task = request("/api/v1/agent/tasks/" + quote(task_id, safe=""), timeout=min(10, remaining))
            status = task.get("status", "")
            if status not in ("pending", "running", "waiting_approval", "completed", "degraded", "failed", "stopped"):
                raise RuntimeError("任务响应缺少有效状态，请核对任务 ID 和服务地址。")
            if status != last_status:
                print("任务状态：", status)
                last_status = status
            if status == "waiting_approval":
                print("任务需要审批，请交给有审批权限的人员处理，再用 --task 继续查询。")
                return 2
            if status in ("completed", "degraded", "failed", "stopped"):
                print("任务结果：")
                output = {field: task.get(field) for field in ("status", "result", "error", "artifacts")}
                print(json.dumps(output, ensure_ascii=False, indent=2).replace(token, "[凭证已隐藏]"))
                print("模型测试成功。" if status == "completed" else "任务未完整成功，请核对结果和错误信息。")
                return 0 if status == "completed" else 1
            time.sleep(min(2, max(0, deadline - time.monotonic())))
    except (RuntimeError, OSError, EOFError, KeyboardInterrupt) as exc:
        message = str(exc) or "操作已中断；已提交的任务仍需查询或明确取消。"
        print("检查未通过：", message.replace(token, "[凭证已隐藏]") if token else message, file=sys.stderr)
        return 1


if __name__ == "__main__":
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
        sys.stderr.reconfigure(encoding="utf-8")
    raise SystemExit(main())
