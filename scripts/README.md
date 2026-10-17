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

带版本参数时无需交互，双击仍可输入版本，默认版本为 `2.0.0`。脚本会从自身位置定位仓库，支持从其他工作目录调用；编译前自动运行发布凭据检查。需要 Go 1.26。

```powershell
go install github.com/tc-hib/go-winres@latest
.\abuild_windows.bat 2.0.0 require
.\scripts\build\build_linux.bat 2.0.0 amd64
.\scripts\build\build_linux.bat 2.0.0 arm64
.\scripts\build\build_mac.bat 2.0.0 fast
```

- Windows x64：根目录 `openKitty.exe`、`openKitty-2.0.0-win.exe` 和兼容别名 `openKitty-newicon.exe`；需要安装 `go-winres`，脚本从 GOBIN、GOPATH/bin 或 PATH 查找。可通过 `MACS_WINRES` 指定工具完整路径。
- Linux：`dist/openkitty_linux_amd64`、`dist/openkitty_linux_arm64`。
- macOS：根目录 `openkitty-2.0.0-mac-intel`、`openkitty-2.0.0-mac-apple`。复制后执行 `chmod +x`。

三平台均构建 `./cmd/macs` 并通过 `main.CurrentVersion` 注入版本、`mac/internal/app.build` 注入 UTC 构建日期。上述命令生成 Go 单二进制程序，不生成 Electron 安装包。macOS 的 `fast` 模式只编译；正式发布应使用 `release` 模式执行完整质量门。交叉编译成功不代表已通过对应系统的运行测试，也不包含 Apple 公证或代码签名。

`./cmd/openkitty` 继续作为旧源码启动命令的兼容入口；发行文件名、配置文件和数据目录本轮保持兼容。

### Windows exe 压缩与打包

从 [UPX 官方发布页](https://github.com/upx/upx/releases) 下载 Windows x64 压缩包，解压工具。UPX 查找顺序为 `MACS_UPX` 指定路径、仓库根目录的 `upx.exe`、PATH；工具不进入 Git。RAR 为可选工具，从 PATH、WinRAR 常见安装目录或 `MACS_RAR` 指定路径查找。

```powershell
# UPX 放在其他位置时，按自己的安装路径设置
$env:MACS_UPX = "C:\Tools\upx\upx.exe"
.\abuild_windows.bat 2.0.0 require

# 本地开发：找到 UPX 就压缩，找不到则明确提示未压缩
.\abuild_windows.bat 2.0.0 auto

# 保留未压缩程序
.\abuild_windows.bat 2.0.0 off

# 只需要 exe，不创建可选的 RAR 包
$env:MACS_SKIP_RAR = "1"
.\abuild_windows.bat 2.0.0 require
```

默认压缩模式为 `auto`。用于分发的 Windows exe 建议使用 `require`，避免因为机器上缺少 UPX 而误交付大体积程序。找到 UPX 后会执行 `--best --lzma` 压缩和 `-t` 完整性检查，任何一步失败都会返回非零状态；`off` 明确禁用 UPX。脚本先编译临时文件，Go 构建或 UPX 失败时保留上次成功的 exe。复制、替换和 RAR 失败也会报错，不会报告成功；可选 RAR 从新文件创建，避免旧包残留条目。

所有脚本支持 `--help`。Linux 同时兼容旧参数顺序 `build_linux.bat arm64 2.0.0`。Mac 两个架构先分别编译成功，再替换已有文件；跨平台程序仍需在目标系统验证。

### Electron 桌面壳

```powershell
# 默认 release 模式会先运行完整质量门
.\electron-app\build_desktop.bat 2.0.0 release

# 本地验证时使用 fast；仍执行后端发布凭据检查
.\electron-app\build_desktop.bat 2.0.0 fast
```

需要 Go、`go-winres`、Node.js 和 npm。脚本复用 Windows 后端构建，再进入 `electron-app/` 安装依赖并运行 `npm run build:win`，默认生成 `electron-app/dist/` 下的便携版。首次安装优先使用锁文件执行 `npm ci`；安装和打包失败均立即停止。桌面程序版本由参数注入，无需手改 `package.json`。安装包另用 `npm run build:win:installer -- --config.extraMetadata.version=2.0.0 --config.buildVersion=2.0.0`。

### 批处理回归检查

```powershell
python -m unittest discover -s scripts/build -p 'test_*.py' -v
```

此检查仅在 Windows 运行，使用临时工具替身验证参数、带空格路径、构建失败、压缩失败、旧产物保留、质量门阻断和 npm 失败的实际退出行为；另需用真实工具编译并启动程序。
