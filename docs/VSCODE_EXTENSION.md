# VS Code 私有扩展

OpenKitty 无需开源，也无需维护 VS Code fork。标准 VS Code Extension 可以是
闭源软件，通过 HTTP API 连接 OpenKitty Gateway。

扩展项目位于 [`vscode-extension/`](../vscode-extension/)，可安装包输出到
`dist/openkitty-vscode-0.1.0.vsix`。完整的构建、配置与使用说明见扩展目录的
README。

推荐分发路径：

1. 内测阶段：直接发送版本化 VSIX，使用 `code --install-extension` 安装。
2. 小团队：把 VSIX 放到需要登录的制品库或内部下载页，并在桌面客户端更新时
   一起分发。
3. 企业规模：部署 VS Code Private Marketplace，获得内网托管、搜索、允许
   列表与自动更新。

不要把 Gateway API Key 固化进 VSIX。扩展使用 VS Code SecretStorage 保存每位
用户自己的 Key。
