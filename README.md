# SyncMaid.go

SyncMaid 是面向 Windows 的单向文件同步工具，目标是支持镜像、仅添加和移动，以及多目标同步。目前 Go 版本只有 MyGo 原生窗口骨架，同步功能尚未实现。

## 编译与运行

需要 Go **1.27.3 或更高版本**。项目固定使用 MyGo **v0.8.3**，无需 Bun、WebView2 或 cgo 工具链。

在项目根目录使用 PowerShell，自行准备依赖并编译：

```powershell
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
go mod tidy
New-Item -ItemType Directory -Force build | Out-Null
go build -o build/SyncMaid.exe .
.\build\SyncMaid.exe
```

首次执行 `go mod tidy` 需要访问 Go 模块源，下载依赖并生成 `go.sum`。构建成功后运行程序，应显示 SyncMaid 启动窗口。

## 测试

```powershell
go test ./...
go vet ./...
```

现有测试使用 MyGo 的无头视图测试器，不会打开桌面窗口。已在 Windows/amd64、Go 1.27.3 下通过测试、静态检查和无 cgo 构建，并实际检查启动窗口的文字显示与关闭退出；不同 DPI 设置尚未验证。
