# macs 脚本导航

- `qa/`：手工回归、压力测试和接口验证脚本。
- `dev/`：维护中的前端检查工具。
- `services/`：可独立启动的辅助服务。
- `build/`：Windows、Linux 和 macOS 构建脚本。
- `github/`：GitHub 公开发布前的仓库预检。
- `quality_gate.ps1` / `quality_gate.sh`：仓库质量门禁。

一次性调研、社区发布、旧 API 索引与私有材料生成脚本已移至仓库外归档，不属于源码交付范围。

脚本默认从仓库根目录执行。正式 Go 单元测试仍使用：

```bash
go test ./...
```

PPT 构图与最终视觉评审文件绑定的聚焦回归：

```powershell
python scripts/qa/test_presentation_design.py
```

这些测试检查布局几何和评审身份，不能代替渲染后的人工或视觉模型审美检查。

Agent 业务基线与重复稳定性评测复用 `testdata/blackbox/` 的入口：

```powershell
python testdata/blackbox/run_agent_baseline.py --validate-only
python -m unittest discover -s testdata/blackbox -p 'test_*.py'
```

默认选择原有 30 题、每题 3 次；实际运行需要隔离测试配置与已构建的 Gateway。参数、指标和证据目录见[重复评测说明](../testdata/blackbox/AGENT_BASELINE.md)。

一次性、带机器配置或可能含凭据的脚本应放入 `.local/ops-history/`，不要继续堆放
在仓库根目录。

版权材料和发行许可辅助入口：

```powershell
python scripts/legal/third_party_inventory.py
python scripts/legal/source_archive.py create --output .local/copyright/macs-v1.1.8 --version V1.1.8
python scripts/legal/source_archive.py verify --directory .local/copyright/macs-v1.1.8 --worktree
python -m unittest discover -s scripts/legal -p 'test_*.py'
```

`third_party_inventory.py --fetch-web` 可从 npm 官方 registry 采集已知版本前端资源的许可原文。组件版本与来源仍需按清单复核。完整源码归档只写入 `.local/`；使用 `--binary` 同时私存对应构建。详见[版权材料说明](../docs/legal/README.md)。

## 在 Windows 上构建三平台版本

先执行发布凭据检查，再使用现有批处理脚本；带版本参数时无需交互，双击仍可输入版本。

```powershell
go run ./cmd/releaseguard
.\abuild_windows.bat 1.1.8
.\scripts\build\build_linux.bat 1.1.8 amd64
.\scripts\build\build_linux.bat 1.1.8 arm64
.\scripts\build\build_mac.bat 1.1.8 fast
```

- Windows x64：根目录 `openKitty.exe` 和 `openKitty-1.1.8-win.exe`；需要安装 `go-winres`（脚本从 Go 安装目录或 PATH 查找）。UPX 和 RAR 为可选打包工具。
- Linux：`dist/openkitty_linux_amd64`、`dist/openkitty_linux_arm64`。
- macOS：根目录 `openkitty-1.1.8-mac-intel`、`openkitty-1.1.8-mac-apple`。复制后执行 `chmod +x`。

三平台均构建 `./cmd/macs` 并通过 `main.CurrentVersion` 注入版本、`mac/internal/app.build` 注入 UTC 构建日期。上述命令生成 Go 单二进制程序，不生成 Electron 安装包。macOS 的 `fast` 模式只编译；正式发布应使用 `release` 模式执行完整质量门。交叉编译成功不代表已通过对应系统的运行测试，也不包含 Apple 公证或代码签名。

`./cmd/openkitty` 继续作为旧源码启动命令的兼容入口；发行文件名、配置文件和数据目录本轮保持兼容。
