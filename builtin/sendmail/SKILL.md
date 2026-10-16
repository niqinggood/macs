---
name: SendMail CLI
description: A Python-based command-line email sending tool that supports HTML content, DataFrame tables, automatic CSV conversion, and attachments across Linux, macOS, and Windows.
read_when:
  - Sending automated email reports
  - Distributing data reports via email
  - Sending formatted HTML emails with tables
  - Automating email notifications from scripts
  - Converting CSV files to email tables
metadata:
  clawdbot:
    emoji: "📧"
    requires:
      bins:
        - python3
        - pip
      pip_packages:
        - pandas
allowed-tools: Bash(skills/sendmail/sendmail.py:*)
version: 1.0.0
author: OpenKitty
status: stable
---

# SendMail CLI Tool

A command-line email sending tool that supports HTML content, DataFrame tables, automatic CSV conversion, and attachments across Linux, macOS, and Windows.

## IRON RULES (non-negotiable)

1. **Never hardcode SMTP credentials**: Credentials must come from config files (`--config`) or environment variables. Never embed passwords, authorization codes, or API keys in scripts, command lines, or version-controlled files.
2. **Verify recipient before send**: Always echo back the recipient address to the user and wait for confirmation before sending. Wrong recipient = data leak. This is non-negotiable for any automated send.
3. **No spam or unsolicited bulk email**: This tool is for automated reports, notifications, and user-requested sends only. Do not use it for marketing blasts, cold emails, or any unsolicited bulk communication.
4. **Validate HTML content**: All HTML in email body must be well-formed. Unclosed tags, script injection, or malformed HTML can trigger spam filters, get the sender blacklisted, or create security vulnerabilities.
5. **Attachment safety check**: Review attachments for sensitive data (PII, credentials, internal secrets) before sending. Warn the user if the attachment exceeds 10MB — many mail servers reject oversized attachments.

## Features

- Send HTML-formatted emails
- Auto-convert CSV files to HTML tables
- Support file attachments
- Local email backup
- Custom SMTP configuration
- Cross-platform (Linux, macOS, Windows)

## Installation

### Prerequisites
- Python 3.8+
- pandas library

### Setup

```bash
pip install pandas
```

The script is already located at `skills/sendmail/sendmail.py`.

## Basic Usage

```bash
python skills/sendmail/sendmail.py --to "recipient@example.com" --subject "Email Subject" --body '["Content 1", "Content 2"]'
```

## Platform-Specific Commands

### 🐧 Linux / macOS

Use **single quotes** around JSON array with double quotes inside:

```bash
# Plain text email
python skills/sendmail/sendmail.py \
  --to "user@163.com" \
  --subject "Test Email" \
  --body '["# Heading 1", "Paragraph text", "## Subheading"]' \
  --summary "This is a summary"

# Email with CSV file
python skills/sendmail/sendmail.py \
  --to "user@163.com" \
  --subject "Data Report" \
  --body '["# Sales Data", "data.csv", "## Conclusion", "All good"]' \
  --summary "Report generated on 2026-05-01"

# With attachment
python skills/sendmail/sendmail.py \
  --to "team@company.com" \
  --subject "Weekly Report" \
  --body '["# Weekly Report", "Please see attachment"]' \
  --attach "/path/to/report.pdf" \
  --summary "Week 18 Report"
```

### 🪟 Windows (PowerShell)

Use **single quotes** (simplest) or escaped double quotes:

```powershell
# Method 1: Single quotes (Recommended for PowerShell)
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test Email" --body '["# Heading 1", "Paragraph text", "## Subheading"]' --summary "Summary here"

# Method 2: Escaped double quotes
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test Email" --body "[\"# Heading 1\", \"Paragraph text\", \"## Subheading\"]" --summary "Summary here"

# With CSV file
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Data Report" --body '["# Sales Data", "data.csv", "## Conclusion", "All good"]' --summary "Report generated"
```

### 🪟 Windows (CMD)

Use double quotes with escaped inner quotes:

```cmd
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test Email" --body "[\"# Heading 1\", \"Paragraph text\", \"## Subheading\"]" --summary "Summary here"
```

## CSV File Handling

If any item in `--body` ends with `.csv`, it will be automatically loaded as a DataFrame and converted to an HTML table.

### Example CSV file (`data.csv`):

```csv
name,age,city
Alice,25,Beijing
Bob,30,Shanghai
```

### Commands:

```bash
# Linux/macOS
python skills/sendmail/sendmail.py --to "user@163.com" --subject "CSV Example" --body '["# User Data", "data.csv"]'

# Windows PowerShell (using single quotes)
python skills/sendmail/sendmail.py --to "user@163.com" --subject "CSV Example" --body '["# User Data", "data.csv"]'
```

## Custom SMTP Configuration

### Option 1: JSON file (`config.json`)

```json
{
  "host": "smtp.qq.com",
  "user": "yourname@qq.com",
  "pass": "your_authorization_code"
}
```

```bash
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test" --body '["Content"]' --config config.json
```

### Option 2: JSON string

```bash
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test" --body '["Content"]' --config '{"host":"smtp.163.com","user":"test@163.com","pass":"password"}'
```

## Command Line Arguments

| Argument | Short | Required | Description |
|----------|-------|----------|-------------|
| `--to` | `-t` | Yes | Recipient email(s), comma-separated |
| `--subject` | `-s` | Yes | Email subject line |
| `--body` | `-b` | Yes | JSON array of content (strings and/or CSV paths) |
| `--summary` | `-sum` | No | Summary text displayed at email top |
| `--attach` | `-a` | No | Path to attachment file |
| `--config` | `-c` | No | SMTP config (JSON file path or JSON string) |
| `--email-dir` | None | No | Local backup directory (default: `email_data`) |
| `--tag` | None | No | Prefix for backup filenames |

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Hardcoding SMTP credentials in the sendmail.py script | Credentials leak into source control; anyone with repo access can send email as you | Use `--config config.json` or environment variables exclusively |
| 2 | Sending without confirming the recipient with the user | Accidental data leak to wrong address; potential privacy/compliance violation | Always echo back the full recipient list and wait for user confirmation |
| 3 | Using `--body` with malformed JSON (wrong quotes, unescaped characters) | Command-line parsing fails with cryptic errors | Follow the platform-specific escaping rules: single quotes on Linux/macOS/PowerShell, escaped double quotes on CMD |
| 4 | Attaching large files (>10MB) without warning | Email silently fails or bounces due to server size limits | Check attachment size before sending and warn the user |
| 5 | Using the default SMTP config for production sends without verifying it works | Default config may be rate-limited or blocked; production emails silently fail | Test with a small send first, or always provide your own `--config` for critical sends |

## Troubleshooting

### Error: "unrecognized arguments"

**Solution for Windows PowerShell:** Use single quotes around the JSON array:

```powershell
python skills/sendmail/sendmail.py --to "user@163.com" --subject "Test" --body '["# Title", "Content"]'
```

### Error: "JSON解析失败"

**Solution:** Ensure your JSON uses double quotes inside the array.

## Default SMTP Configuration

The script includes a default 163.com SMTP configuration. For production use, provide your own configuration.

## Pre-Action Checklist

- [ ] Confirm the recipient email address with the user before sending
- [ ] Verify the subject line and body content are correct and appropriate
- [ ] Check that all CSV files referenced in `--body` exist and are readable
- [ ] For attachments: verify file exists, is not sensitive, and is under 10MB
- [ ] Use the correct quoting style for the current platform (Linux/macOS/Windows)
- [ ] Confirm SMTP config is valid — test with a small send first for new configs
- [ ] Verify the send completed successfully and the email was delivered