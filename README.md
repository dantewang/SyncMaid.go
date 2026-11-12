# SyncMaid.go

SyncMaid 是面向 Windows 的单向文件同步工具，目标是支持镜像、仅添加和移动，以及多目标同步。目前 Go 版本提供原生窗口、托盘和按安装目录区分的单实例骨架；已有独立的任务模型、旧 JSON 兼容及原子存储测试，尚未接入界面或实际 Data，同步功能尚未实现。

关闭窗口会隐藏到托盘；托盘图标点击及菜单 Show main window 的恢复操作尚待人工验收，Exit 才真正退出。同一目录再次启动会恢复已有窗口；不同目录的副本可独立运行。数据目录固定为可执行文件所在真实目录旁的 `Data`；目前不加载旧版任务或设置，关闭隐藏也尚无设置开关。

安装目录与 Data 必须可写，程序会在其中建立并保留 `.syncmaid-owner.lock`，不要在运行时修改。当前仅接受本地固定 NTFS/ReFS 卷；USB、网络位置的便携运行仍是待补齐的能力，当前会拒绝启动。窗口激活失败不会允许第二个写入实例；此机制尚不与 Rust 旧版协调。

启动失败时会显示原生提示，说明相关位置和可采取的下一步。正常再次启动只恢复已有窗口；若未能确认恢复，会提示查看已有窗口、稍后重试或从托盘恢复，不会接管 Data。

## 编译与运行

需要 Go **1.27.3**（当前过滤 profile 固定此版本）。项目固定使用 MyGo **v0.8.3**，无需 Bun、WebView2 或 cgo 工具链。

在项目根目录使用 PowerShell，自行准备依赖并编译：

```powershell
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
go mod tidy
New-Item -ItemType Directory -Force build | Out-Null
go build -ldflags=-H=windowsgui -o build/SyncMaid.exe .
.\build\SyncMaid.exe
```

首次执行 `go mod tidy` 需要访问 Go 模块源，下载依赖并生成 `go.sum`。构建成功后运行程序，应显示 SyncMaid 启动窗口。

## 测试

```powershell
go test ./...
go vet ./...
```

现有测试覆盖无头视图、目录身份、生命周期、文件过滤、四语文本目录及 Unicode 编辑。过滤器的 Unicode 17 完整大写映射有离线全字符校验；独立核心支持新 Go 正则 profile 的有限样例预览和逐条确认，尚未接入界面或配置迁移，旧非空正则仍拒绝执行。独立的只读目录扫描只用临时目录测试，遇到链接或无法完整读取时整体失败，尚未接入同步。隔离实验已确认框架支持英、日、简体和繁体中文原生文本切换，并把语言选择保存在指定 Data；正式应用尚未接入四语界面，中文输入法组合过程也未验收。

当前窗口骨架已验证无 cgo 构建、关闭隐藏、重复启动恢复、跨目录独立运行与退出。`go tool mygo build -skip-installer` 可生成不含安装器的 Windows 发布目录，当前骨架的单个 EXE 复制后可运行。新增外部资源时仍需明确嵌入方式；托盘鼠标操作、不同 DPI 和干净 Windows 环境兼容尚待验证。
