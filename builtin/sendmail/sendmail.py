import smtplib
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart
from email.mime.application import MIMEApplication
import sys
import inspect
import pandas as pd
import argparse
import json
import ast

# from config import logger
class EmailSender(object):
    def __init__(self, config={"host": "smtp.163.com", "user": "novaexe@163.com", 'pass': "LExZuGhu7t9iYW2r"}):
        self.mail_host = config['host']
        self.mail_user = config['user']
        self.mail_pass = config.get('pass', '')

    def append_file(self, file_path, content, mode='a'):
        """Append content to a file."""
        with open(file_path, mode, encoding='utf-8') as file:
            file.write(content)

    def make_dir(self, path):
        """Create directory if it doesn't exist."""
        import os
        if not os.path.exists(path):
            os.makedirs(path)

    def convert_to_html(self, content_list):
        """Convert markdown-like content to HTML with headers and paragraphs."""
        html_content = ""
        for content in content_list:
            if isinstance(content, str):
                if content.startswith('### '):
                    html_content += f"<h3>{content[4:].strip()}</h3>"
                elif content.startswith('## '):
                    html_content += f"<h2>{content[3:].strip()}</h2>"
                elif content.startswith('# '):
                    html_content += f"<h1>{content[2:].strip()}</h1>"
                else:
                    html_content += f"<p>{content}</p>"
            elif isinstance(content, pd.DataFrame):
                # Convert DataFrame to a clean HTML table
                html_table = content.to_html(index=False, border=0, classes='dataframe', justify='left')
                html_content += html_table
        return html_content

    def _generate_html_content(self, subject, body_content, summary_info=""):
        """Generate HTML content for email body."""
        html_content = f"""
        <html>
        <head>
        <style>
            body {{ font-family: Arial, sans-serif; }}
            h1 {{ color: blue; }}
            h2 {{ color: darkblue; }}
            h3 {{ color: steelblue; }}
            p {{ font-size: 14px; }}
            table {{ border-collapse: collapse; width: 100%; margin-bottom: 20px; }}
            th, td {{ border: 1px solid #ddd; padding: 8px; text-align: left; }}
            th {{ background-color: #e6f2ff; }} /* Light blue header for distinction */
            tr:nth-child(even) {{ background-color: #f2f2f2; }}
            tr:hover {{ background-color: #ddd; }}
        </style>
        </head>
        <body>
        <p>{summary_info}</p>
        {body_content}
        </body></html>
        """
        return html_content

    def work(self, to_addr, subject, body_content, is_table=False, att_file_name="", summary_file_name="", author="DA", upper_file="unknown", suffix='@163.com'):
        """Compose and send the email."""
        to_addr_list = to_addr.split(",")
        to_addr = ",".join([f"{addr}" + suffix if "@" not in addr else addr for addr in to_addr_list])

        sum_info = open(summary_file_name, 'r', encoding='utf-8').read() if summary_file_name else ""
        upper_file = inspect.getsourcefile(inspect.currentframe().f_back) if upper_file == "unknown" else upper_file
        msg = MIMEMultipart()
        # Create the email body with HTML content
        msg_content = MIMEText(self._generate_html_content(subject, body_content, sum_info), "html", "utf-8")

        msg["From"] = self.mail_user
        msg["To"] = to_addr
        msg["Subject"] = subject
        msg.attach(msg_content)

        if att_file_name:
            with open(att_file_name, 'rb') as att_file:
                att = MIMEApplication(att_file.read())
            att.add_header('Content-Disposition', 'attachment', filename=att_file_name.split('/')[-1])
            msg.attach(att)

        c = smtplib.SMTP()
        c.connect(self.mail_host)
        c.login(self.mail_user, self.mail_pass)
        c.sendmail(self.mail_user, to_addr.split(","), msg.as_string())
        c.quit()

    def send_mail(self, to_addr, subject, body_list=[], summary_str='', is_table=True, att_file_name='', email_dir='email_data', tag=''):
        """Send an email with the given content and attachments."""
        import datetime
        import os
        self.make_dir(email_dir)
        today_str = datetime.datetime.now().strftime('%Y-%m-%d %H-%M-%S')
        body_file = os.path.join(email_dir, f"{tag}body_{today_str}.html")
        sum_file = os.path.join(email_dir, f"{tag}sum_{today_str}.txt")

        html_body_content = self.convert_to_html(body_list)

        # Write body content to file
        self.append_file(body_file, html_body_content, 'w')
        self.append_file(sum_file, summary_str, 'w')
        self.work(to_addr, subject, html_body_content, is_table=is_table, att_file_name=att_file_name,
                  summary_file_name=sum_file, author="DA", upper_file="unknown")
        import logging
        logging.info("send email succ")
        print(f"[OK] 邮件发送成功！收件人: {to_addr}")


def parse_body_item(item):
    """
    解析 body_list 中的每个元素
    支持三种类型：
    1. 字符串（普通文本或 markdown）
    2. DataFrame 对象
    3. CSV 文件路径（以 .csv 结尾的字符串）-> 自动读取为 DataFrame
    """
    if isinstance(item, str):
        # 检查是否是 CSV 文件路径
        if item.lower().endswith('.csv'):
            try:
                df = pd.read_csv(item)
                print(f"[DATA] 已加载 CSV 文件: {item}，形状: {df.shape}")
                return df
            except Exception as e:
                print(f"[ERR] 读取 CSV 文件失败 {item}: {e}")
                return f"[错误: 无法读取CSV文件 {item}]"
        else:
            return item
    elif isinstance(item, pd.DataFrame):
        return item
    else:
        # 其他类型转为字符串
        return str(item)


def main():
    parser = argparse.ArgumentParser(description='通过命令行发送格式丰富的邮件')
    parser.add_argument('--to', '-t', required=True, help='收件人邮箱，多个用逗号分隔')
    parser.add_argument('--subject', '-s', required=True, help='邮件主题')
    parser.add_argument('--body', '-b', required=True, help='邮件正文内容（JSON数组格式），支持字符串和CSV文件路径，注意外层用双引号，内层用转义双引号')
    parser.add_argument('--summary', '-sum', default='', help='邮件摘要信息')
    parser.add_argument('--attach', '-a', default='', help='附件文件路径')
    parser.add_argument('--config', '-c', default='', help='SMTP配置文件（JSON格式）')
    parser.add_argument('--email-dir', default='email_data', help='本地备份目录')
    parser.add_argument('--tag', default='', help='备份文件名前缀')

    args = parser.parse_args()

    # 解析 body_list
    try:
        # 尝试解析 JSON 数组
        body_raw = json.loads(args.body)
        if not isinstance(body_raw, list):
            raise ValueError("body 必须是 JSON 数组格式")
        body_list = [parse_body_item(item) for item in body_raw]
    except json.JSONDecodeError as e:
        print(f"[ERR] JSON 解析失败: {e}")
        print(f"   请确保 --body 参数是有效的 JSON 数组格式")
        print(f"   正确示例: --body '[\\\"# 标题\\\", \\\"正文\\\", \\\"## 子标题\\\"]'")
        print(f"   或在 PowerShell 中: --body '[\\\"# 标题\\\",\\\"正文\\\",\\\"## 子标题\\\"]'")
        sys.exit(1)

    # 解析 SMTP 配置
    config = None
    if args.config:
        try:
            with open(args.config, 'r', encoding='utf-8') as f:
                config = json.load(f)
        except:
            try:
                config = json.loads(args.config)
            except:
                print("❌ 配置文件格式错误，使用默认配置")
    
    if config is None:
        config = {"host": "smtp.163.com", "user": "novaexe@163.com", 'pass': "LExZuGhu7t9iYW2r"}

    # 发送邮件
    try:
        sender = EmailSender(config=config)
        sender.send_mail(
            to_addr=args.to,
            subject=args.subject,
            body_list=body_list,
            summary_str=args.summary,
            att_file_name=args.attach,
            email_dir=args.email_dir,
            tag=args.tag
        )
    except Exception as e:
        print(f"[ERR] 邮件发送失败: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)


if __name__ == '__main__':
    if len(sys.argv) > 1:
        # 命令行模式
        main()
    else:
        # 原有的测试模式
        df = pd.DataFrame([list(range(0, 10)), list(range(0, 10)), list(range(0, 10)), list(range(0, 10))],
                          columns=['columns' + str(i) for i in range(10)])
        email_sender = EmailSender()
        email_sender.send_mail(to_addr='niqinggood@163.com',
                               subject='Test Email with Improved Table Style',
                               body_list=['# 你好呀', df, '## 你好呢', df, '### hao', 'Some plain text', df],
                               summary_str='This is the email summary')