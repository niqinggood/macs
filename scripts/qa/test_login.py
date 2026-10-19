#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""尝试不同方式登录Sicurrent社区"""

import urllib.request
import urllib.error
import urllib.parse
import json
import sys
import os

os.environ['PYTHONIOENCODING'] = 'utf-8'

def try_login_json():
    """方式1: JSON登录"""
    print("=" * 50)
    print("方式1: JSON格式登录")
    print("=" * 50)
    
    login_url = "https://www.sicurrent.com/nova/login"
    credentials = json.dumps({
        "username": "openkitty",
        "password": "OpenKitty@2025"
    }).encode('utf-8')
    
    req = urllib.request.Request(login_url, data=credentials,
                                headers={"Content-Type": "application/json"},
                                method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            print(f"状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            print(f"响应: {body[:500]}")
            cookies = resp.headers.get_all('Set-Cookie') if hasattr(resp.headers, 'get_all') else []
            print(f"Cookies: {cookies}")
            return True, cookies, body
    except urllib.error.HTTPError as e:
        print(f"HTTP错误: {e.code}")
        err_body = e.read().decode('utf-8', errors='replace')
        print(f"错误内容: {err_body[:500]}")
        return False, [], err_body
    except Exception as e:
        print(f"异常: {e}")
        return False, [], str(e)


def try_login_form():
    """方式2: Form表单登录"""
    print("=" * 50)
    print("方式2: Form表单登录")
    print("=" * 50)
    
    login_url = "https://www.sicurrent.com/nova/login"
    form_data = urllib.parse.urlencode({
        "username": "openkitty",
        "password": "OpenKitty@2025"
    }).encode('utf-8')
    
    req = urllib.request.Request(login_url, data=form_data,
                                headers={"Content-Type": "application/x-www-form-urlencoded"},
                                method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            print(f"状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            print(f"响应: {body[:500]}")
            return True, body
    except urllib.error.HTTPError as e:
        print(f"HTTP错误: {e.code}")
        err_body = e.read().decode('utf-8', errors='replace')
        print(f"错误内容: {err_body[:500]}")
        return False, err_body
    except Exception as e:
        print(f"异常: {e}")
        return False, str(e)


def check_posts():
    """查看帖子列表"""
    print("=" * 50)
    print("查看帖子列表")
    print("=" * 50)
    
    url = "https://www.sicurrent.com/nova/forum/posts?page=1&limit=5&sort=latest"
    try:
        with urllib.request.urlopen(url, timeout=15) as resp:
            print(f"状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            data = json.loads(body)
            print(f"帖子总数: {data.get('total', 0)}")
            for post in data.get('posts', []):
                print(f"  - [{post.get('id')}] {post.get('title')} by {post.get('author')}")
            return True
    except Exception as e:
        print(f"获取失败: {e}")
        return False


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding='utf-8')
    
    # 先看帖子列表
    check_posts()
    print()
    
    # 尝试JSON登录
    success, cookies, body = try_login_json()
    print()
    
    # 如果JSON失败，试Form登录
    if not success:
        try_login_form()