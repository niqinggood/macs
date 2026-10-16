# Skill 市场与安装目录

OpenKitty 的 Web 技能管理支持从以下来源搜索并安装 Skill：

- 腾讯 SkillHub：`https://skillhub.cn`
- OpenClaw / ClawHub：`https://clawhub.ai`

## 安装位置

市场、Git URL、本地目录、新建 Skill 和自动生成 Skill 共用同一个可写目录：

```text
<OpenKitty 启动文件所在目录>/.openkitty/skills
```

例如：

```text
/opt/openkitty/openkitty
└── .openkitty/
    └── skills/
        └── example-skill/
            ├── SKILL.md
            └── origin.json
```

该路径由正在运行的可执行文件解析，不依赖进程当前工作目录、项目 workspace
或用户主目录。历史的 workspace、`skills_config.global_dir` 和
`~/.openkitty/skills` 仍作为兼容只读来源参与加载。

## 市场安装安全边界

市场安装器只获取 `SKILL.md`：

- 不下载软件包、脚本、二进制或依赖；
- 校验来源、slug、owner、版本和响应大小；
- 检查腾讯 SkillHub / ClawHub 返回的安全扫描状态；
- 将远端 `shell`、`python`、`node`、`entrypoint`、`always` 等可执行声明移除；
- 强制保存为 `type: builtin` 的提示型 Skill；
- 执行 OpenKitty 本地 Skill 内容安全扫描；
- 使用临时目录完成写入，再原子移动到最终目录；
- 对全部 Agent 做事务热加载；任何一个目录校验失败都会回滚本次安装；
- 拒绝跨来源安装请求和重复安装。

第三方 Skill 的文字仍属于不可信提示内容。它不能通过安装过程增加新的 Tool，
实际硬件或系统操作仍受 OpenKitty 已注册工具及其权限配置约束。

## HTTP API

```text
GET  /api/skills/sources
GET  /api/skills/search?source=tencent&q=robot
GET  /api/skills/search?source=clawhub&q=robot
POST /api/skills/install
```

安装请求：

```json
{
  "source": "clawhub",
  "slug": "robot-drive",
  "owner": "robotics",
  "version": "1.0.0"
}
```

`source` 也接受 `openclaw`，内部会规范为 `clawhub`。
