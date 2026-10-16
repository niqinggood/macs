# macs 品牌与技术命名规范

本规范用于让 Web、桌面端、命令行、文档和发布包保持一致，同时避免一次性改名破坏现有部署。

## 名称层级

| 层级 | 规范名称 | 用途 |
|---|---|---|
| 正式产品 | macs | 页面标题、产品文档、安装包、版本信息、商务材料 |
| 中文产品名 | 麦卡斯 | 中文介绍、市场与商务材料 |
| 正式全称 | Multi-Agent Collaboration System | 首次介绍、关于页、正式材料 |
| 产品小名 | openkitty | 非正式称呼、产品介绍和历史兼容说明；正式产品名称仍为 macs |
| 权利主体标识 | Silicon Surge Technology / 硅基涌动 | 当前仓库已有的作者与页脚标识；正式发行前应与法定主体核对 |

推荐的首次介绍写法：

> macs（麦卡斯），小名 openkitty，是一个多智能体协作系统（Multi-Agent Collaboration System）。

后续正文统一简称“macs”（全小写，按用户 2026-09-29 最新偏好）；提及小名时写作“openkitty”。正式产品标题使用 macs，小名可以在简介中说明。不要写成 `Macs`、`macs AI` 或 `MAC`。

## 用户可见界面

以下位置必须使用 macs：

- Web 标题、导航 Logo、欢迎页、错误提示和导出物署名；
- 桌面应用名称、窗口标题、快捷方式、托盘提示和安装包产品名；
- `--version` 输出、关于页、正式文档标题和发布说明；
- 新增商务材料中的产品展示名；已存在的许可证、合同和签名文件保留正式原文，不因展示名大小写调整而改写。

## 兼容标识

下面的历史技术标识暂时保留，迁移期内不应机械替换：

- 可执行文件兼容名 `openkitty.exe` 与旧命令 `openkitty`；
- 配置文件 `openkitty-config.yaml`；
- 工作目录 `.openkitty/`；
- 环境变量 `OPENKITTY_*`；
- 浏览器事件名、DOM 属性、数据库名和现有 API 字段中的 `openkitty`；
- Electron 的既有 `appId`，以免升级后被操作系统识别为全新应用。

新增用户文档应把这些名称标为“兼容名称”，而不是产品品牌。将来增加 `macs` 命令、`MACS_*` 环境变量和新配置路径时，应先支持双读双写或别名，再经过弃用周期移除旧名称。

## Go 模块名

根模块当前仍为历史名称 `mac`。不要只改成无所有权信息的 `module macs`。确定代码托管域名和组织名后，应一次性改为可验证的规范地址，例如：

```go
module github.com/<owner>/macs
```

如果拆分公开与商业模块，建议使用：

```text
github.com/<owner>/macs-sdk          公开接口与 SDK
github.com/<owner>/macs-community    源码可见运行时
<private-domain>/macs-core           私有商业核心
<private-domain>/macs-enterprise     企业版装配与连接器
```

在组织名和发布边界确定前，保留 `module mac`，避免制造第二次全仓导入迁移。

## 授权与品牌不是一回事

[LICENSE](LICENSE) 决定源码可以怎样使用，[TRADEMARKS.md](TRADEMARKS.md) 决定 macs 名称和 Logo 可以怎样使用。即使某个客户获得源代码或修改权，也不会自动获得以 macs 品牌发布派生产品的权利。
