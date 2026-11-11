# SyncMaid.go

SyncMaid 是面向 Windows 的单向文件同步工具，目标是支持镜像、仅添加和移动，以及多目标同步。目前 Go 版本提供原生窗口、托盘和按安装目录区分的单实例骨架，同步功能尚未实现。

关闭窗口会隐藏到托盘，点击托盘图标或菜单的 Show main window 可恢复，Exit 才真正退出。同一目录再次启动会恢复已有窗口；不同目录的副本可独立运行。数据目录固定为可执行文件所在真实目录旁的 `Data`；目前不加载旧版任务或设置，关闭隐藏也尚无设置开关。

## 编译与运行

需要 Go **1.27.3 或更高版本**。项目固定使用 MyGo **v0.8.3**，无需 Bun、WebView2 或 cgo 工具链。

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

现有测试覆盖无头视图、目录身份和生命周期。已在 Windows/amd64、Go 1.27.3 下通过测试、静态检查和无 cgo 构建，并检查窗口显示、关闭隐藏、重复启动恢复、跨目录独立运行与显式退出。托盘鼠标操作、不同 DPI、语言存储和干净环境单文件发布仍待验证。
