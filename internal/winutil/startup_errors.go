package winutil

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/windows"
)

var (
	ErrUnsupportedLocation = errors.New("unsupported installation or Data location")
	ErrUnreliableIdentity  = errors.New("directory identity cannot be verified")
)

type LocationError struct {
	Path  string
	Cause error
}

func (e *LocationError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Cause) }
func (e *LocationError) Unwrap() error { return e.Cause }

type StartupKind uint8

const (
	StartupOwnership StartupKind = iota
	StartupUnsupported
	StartupPermission
	StartupOccupied
	StartupFramework
)

type StartupFailure struct {
	Kind  StartupKind
	Path  string
	Cause error
}

func (e *StartupFailure) Error() string {
	return fmt.Sprintf("startup (%d) %s: %v", e.Kind, e.Path, e.Cause)
}
func (e *StartupFailure) Unwrap() error { return e.Cause }

func StartupProblem(err error, fallback StartupKind, path string) error {
	var location *LocationError
	if errors.As(err, &location) {
		path = location.Path
	} else {
		var fileError *os.PathError
		if errors.As(err, &fileError) {
			path = fileError.Path
		}
	}
	kind := fallback
	// A directory probe can prove a location/permission problem even during
	// activation. Socket transport errors cannot prove Data access is denied.
	if fallback != StartupFramework && (fallback != StartupOccupied || location != nil) {
		switch {
		case errors.Is(err, ErrUnsupportedLocation):
			kind = StartupUnsupported
		case errors.Is(err, os.ErrPermission), errors.Is(err, windows.ERROR_ACCESS_DENIED), errors.Is(err, windows.ERROR_WRITE_PROTECT):
			kind = StartupPermission
		case errors.Is(err, ErrUnreliableIdentity):
			kind = StartupOwnership
		}
	}
	return &StartupFailure{Kind: kind, Path: path, Cause: err}
}

type StartupNotice struct{ Title, Message string }

func NoticeForStartup(err error) StartupNotice {
	var failure *StartupFailure
	if !errors.As(err, &failure) {
		failure = &StartupFailure{Kind: StartupFramework, Cause: err}
	}
	var message string
	switch failure.Kind {
	case StartupUnsupported:
		message = "当前位置暂不受当前版本支持。\n请将程序和 Data 一起放到可写的本地 NTFS、ReFS 或 FAT/exFAT 卷后重试。共享目录只能作为同步端点。"
	case StartupPermission:
		message = "无法访问或写入安装目录或 Data。\n请关闭可能占用文件的程序，并将 SyncMaid 和 Data 一起放到您有读写权限的目录后重试。"
	case StartupOccupied:
		message = "已有 SyncMaid 实例可能正在运行或启动，但本次未能确认窗口已恢复。\n请查看已有窗口，或稍后重试、使用托盘图标恢复。不要删除锁文件；本次启动不会接管或写入 Data。"
	case StartupOwnership:
		if errors.Is(err, ErrLegacyInstance) {
			message = "此目录的旧版 SyncMaid 正在运行。请先退出旧版，再启动新版。"
		} else {
			message = "无法确认安装目录或 Data 的安全访问状态，程序已停止启动以保护数据。\n请确认 Data 可访问，并将程序和 Data 放在可写的本地磁盘。不要删除正在使用的锁文件。"
		}
	default:
		message = "原生界面未能启动或正常结束。\n请关闭其他 SyncMaid 窗口后重新启动；若仍失败，请保留现有 Data 并反馈此问题。"
	}
	if failure.Path != "" {
		path := []rune(PathLabel(failure.Path))
		if len(path) > 200 {
			path = append(append(path[:80], []rune(" … ")...), path[len(path)-100:]...)
		}
		message += "\n\n位置：" + string(path)
	}
	return StartupNotice{Title: "SyncMaid 启动提示", Message: message}
}

// RunStartup displays at most one native notice AFTER run and all its resource
// defers finish. Neither classification nor the presenter initializes MyGo.
func RunStartup(run func() error, present func(StartupNotice) error) int {
	if err := run(); err != nil {
		slog.Error("startup-failed", "error", err)
		if displayErr := present(NoticeForStartup(err)); displayErr != nil {
			slog.Error("startup-notice-failed", "error", displayErr)
		}
		return 1
	}
	return 0
}
