#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""尝试直接发帖（不登录）和查看API"""

import urllib.request
import urllib.error
import json
import sys
import os

os.environ['PYTHONIOENCODING'] = 'utf-8'

def test_post_no_auth():
    """不登录直接发帖"""
    print("=" * 50)
    print("尝试不登录直接发帖")
    print("=" * 50)
    
    post_url = "https://www.novaexel.com/nova/forum/posts"
    post_data = json.dumps({
        "title": "test",
        "content": "test content",
        "category": "general"
    }).encode('utf-8')
    
    req = urllib.request.Request(post_url, data=post_data,
                                headers={"Content-Type": "application/json"},
                                method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            print(f"状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            print(f"响应: {body[:500]}")
    except urllib.error.HTTPError as e:
        print(f"HTTP错误: {e.code}")
        err_body = e.read().decode('utf-8', errors='replace')
        print(f"错误内容: {err_body[:500]}")
    except Exception as e:
        print(f"异常: {e}")


def test_chatroom_api():
    """查看chatroom API"""
    print("=" * 50)
    print("查看chatroom API")
    print("=" * 50)
    
    urls = [
        "https://www.novaexel.com/api/chatroom/posts",
        "https://www.novaexel.com/api/forum/posts",
        "https://www.novaexel.com/api/posts",
        "https://www.novaexel.com/chatroom/api/posts",
    ]
    
    for url in urls:
        try:
            req = urllib.request.Request(url, method="GET")
            with urllib.request.urlopen(req, timeout=10) as resp:
                print(f"[{url}] 状态码: {resp.status}")
                body = resp.read().decode('utf-8', errors='replace')[:300]
                print(f"[{url}] 响应: {body}")
        except urllib.error.HTTPError as e:
            print(f"[{url}] HTTP错误: {e.code}")
        except Exception as e:
            print(f"[{url}] 异常: {e}")
        print()


def test_chatroom_page():
    """查看chatroom页面源码"""
    print("=" * 50)
    print("查看chatroom页面源码")
    print("=" * 50)
    
    try:
        req = urllib.request.Request("https://www.novaexel.com/chatroom", 
                                    headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=15) as resp:
            print(f"状态码: {resp.status}")
            body = resp.read().decode('utf-8', errors='replace')
            # 查找API端点
            import re
            apis = re.findall(r'https?://[^"\']*api[^"\']*', body)
            print(f"找到的API端点: {apis[:10]}")
            
            # 查找JavaScript文件
            scripts = re.findall(r'src="([^"]*\.js[^"]*)"', body)
            print(f"JS文件: {scripts[:10]}")
    except Exception as e:
        print(f"异常: {e}")


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding='utf-8')
    
    test_post_no_auth()
    print()
    test_chatroom_api()
    print()
    test_chatroom_page()