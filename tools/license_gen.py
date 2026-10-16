"""
OpenKitty 商业版 License 签发工具
用法: python license_gen.py --tenant-id=xxx --max-users=100 --expires=2027-06-20 --output=license_xxx.lic

需要设置环境变量 OPENKITTY_LICENSE_PRIVATE_KEY 为 RSA 私钥 (PEM格式),
或者把私钥保存在 tools/license_private_key.pem 文件中。
"""
from Crypto.PublicKey import RSA
from Crypto.Signature import pkcs1_15
from Crypto.Hash import SHA256
import json
import base64
import argparse
import os
import sys


def load_private_key():
    """从环境变量或文件加载私钥"""
    pem = os.environ.get("OPENKITTY_LICENSE_PRIVATE_KEY", "")
    if not pem:
        key_file = os.path.join(os.path.dirname(__file__), "license_private_key.pem")
        if os.path.exists(key_file):
            with open(key_file) as f:
                pem = f.read()
    if not pem:
        print("错误: 未找到私钥。请设置环境变量 OPENKITTY_LICENSE_PRIVATE_KEY 或")
        print("将私钥保存到 tools/license_private_key.pem")
        sys.exit(1)
    return RSA.import_key(pem)


def sign_data(data: str, private_key) -> str:
    h = SHA256.new(data.encode())
    signature = pkcs1_15.new(private_key).sign(h)
    return base64.b64encode(signature).decode()


def generate_license(tenant_id: str, max_users: int, expires_at: str,
                     output_file: str, private_key):
    license_data = {
        "tenant_id": tenant_id,
        "max_users": max_users,
        "expires_at": expires_at,
    }
    data_str = json.dumps(license_data, ensure_ascii=False)
    signature = sign_data(data_str, private_key)

    os.makedirs(os.path.dirname(output_file) or ".", exist_ok=True)
    with open(output_file, "w") as f:
        f.write(f"{data_str}\n{signature}")

    print(f"授权文件已生成: {output_file}")
    print(f"  租户: {tenant_id}")
    print(f"  最大用户: {max_users}")
    print(f"  过期时间: {expires_at}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="OpenKitty License 签发工具")
    parser.add_argument("--tenant-id", required=True, help="租户ID")
    parser.add_argument("--max-users", type=int, default=100, help="最大用户数")
    parser.add_argument("--expires", required=True, help="过期日期 YYYY-MM-DD")
    parser.add_argument("--output", required=True, help="输出文件路径")
    args = parser.parse_args()

    private_key = load_private_key()
    generate_license(args.tenant_id, args.max_users, args.expires,
                     args.output, private_key)
