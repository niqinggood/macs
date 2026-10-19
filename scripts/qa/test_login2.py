#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""尝试不同用户名登录"""

import urllib.request
import urllib.error
import json
import sys
import os

os.environ['PYTHONIOENCODING'] = 'utf-8'

def try_login(username, password):
    """尝试登录"""
    login_url = "https://www.sicurrent.com/nova/login"
    credentials = json.dumps({
        "username": username,
        "password": password
    }).encode('utf-8')
    
    req = urllib.request.Request(login_url, data=credentials,
                                headers={"Content-Type": "application/json"},
                                method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            print(f"[{username}] 状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            print(f"[{username}] 响应: {body[:300]}")
            cookies = resp.headers.get_all('Set-Cookie') if hasattr(resp.headers, 'get_all') else []
            if resp.headers.get('Set-Cookie'):
                cookies = [resp.headers.get('Set-Cookie')]
            print(f"[{username}] Cookies: {cookies}")
            return True, cookies, body
    except urllib.error.HTTPError as e:
        print(f"[{username}] HTTP错误: {e.code}")
        err_body = e.read().decode('utf-8', errors='replace')
        print(f"[{username}] 错误: {err_body[:300]}")
        return False, [], err_body
    except Exception as e:
        print(f"[{username}] 异常: {e}")
        return False, [], str(e)


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding='utf-8')
    
    # 尝试不同组合
    try_login("niqinggood", "OpenKitty@2025")
    print()
    try_login("openkitty", "OpenKitty@2025")
    print()
    try_login("niqinggood", "Nova@2025")
    print()
    
    # 看看登录页面的结构
    print("\n=== 查看登录页面 ===")
    try:
        with urllib.request.urlopen("https://www.sicurrent.com/nova/login", timeout=10) as resp:
            body = resp.read().decode('utf-8', errors='replace')
            print(f"状态码: {resp.status}")
            print(f"内容: {body[:500]}")
    except Exception as e:
        print(f"获取失败: {e}")