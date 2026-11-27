<div align="center">

<img src="assets/syncmaid.png" alt="SyncMaid" width="128" />

# SyncMaid

**为 Windows 设计的单向文件同步工具，简单可靠、开箱即用。**

[English](README.md) | 简体中文

SyncMaid 监视一个源文件夹，并将文件同步到一个或多个目标文件夹——每个目标都可以独立设置过滤规则和同步策略（Mirror 镜像、Add-only 仅添加或 Move 移动），支持手动触发、Cron 定时执行，或监视源目录变化自动触发，并可驻留系统托盘在后台持续运行。

</div>

SyncMaid 基于 Go 与 [MyGo](https://github.com/egoist/mygo) 原生 Windows 界面构建，采用**纯便携运行方式**：所有任务配置、设置与运行记录都保存在可执行文件旁边的 `Data` 文件夹内，整套程序可以随目录整体迁移，不在系统中散布额外配置。

## 文档索引

- **使用指南（简体中文）** — [`docs/zh-CN/`](docs/zh-CN/README.md)
- **User Guide (English)** — [`docs/`](docs/README.md)
- **开发与 Agent 协作规范** — [AGENT.md](AGENT.md)

## 环境要求

- Windows 10 或更高版本（本应用仅支持 Windows）
- Go 1.27.4 或更高版本（[`go.mod`](go.mod) 已声明 `tool github.com/egoist/mygo/cmd/mygo` v0.10.0）
- `CGO_ENABLED=0`（纯 Go 原生构建，无需安装 C 编译器或 MSVC）

## 构建与运行

开发模式直接运行：

```powershell
$env:CGO_ENABLED = "0"
go run .
```

打包独立可执行文件（输出到 `build/windows-amd64/SyncMaid.exe`）：

```powershell
$env:CGO_ENABLED = "0"
go tool mygo build -skip-installer
```

## 测试

运行静态检查与全量测试：

```powershell
go vet ./...
go test -count=1 ./...
```

仅运行核心传输与过滤引擎测试（`internal/syncengine`）：

```powershell
go test ./internal/syncengine
```

测试覆盖临时文件写盘校验与原子替换、目录句柄校验、路径重叠与符号链接/联接点拒绝、取消清理、Mirror 删前确认门禁、Move 首匹配路由与冲突编号后缀、运行记录持久化保护以及界面会话状态流转。

## 分发运行

通过 `go tool mygo build -skip-installer` 生成的 `build/windows-amd64/SyncMaid.exe` 已内嵌应用图标、SVG 图标资源、四语界面文案以及 [`mygo.json`](mygo.json) 版本元数据。将其复制到任意可写的本地磁盘目录即可直接运行，首次启动会在同级目录下使用/创建 `Data` 文件夹。
