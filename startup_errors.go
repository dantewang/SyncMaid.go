package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/windows"
)

var (
	errUnsupportedLocation = errors.New("unsupported installation or Data location")
	errUnreliableIdentity  = errors.New("directory identity cannot be verified")
)

type locationError struct {
	path  string
	cause error
}

func (e *locationError) Error() string { return fmt.Sprintf("%s: %v", e.path, e.cause) }
func (e *locationError) Unwrap() error { return e.cause }

type startupKind uint8

const (
	startupOwnership startupKind = iota
	startupUnsupported
	startupPermission
	startupOccupied
	startupFramework
)

type startupFailure struct {
	kind  startupKind
	path  string
	cause error
}

func (e *startupFailure) Error() string {
	return fmt.Sprintf("startup (%d) %s: %v", e.kind, e.path, e.cause)
}
func (e *startupFailure) Unwrap() error { return e.cause }

func startupProblem(err error, fallback startupKind, path string) error {
	var location *locationError
	if errors.As(err, &location) {
		path = location.path
	} else {
		var fileError *os.PathError
		if errors.As(err, &fileError) {
			path = fileError.Path
		}
	}
	kind := fallback
	// A directory probe can prove a location/permission problem even during
	// activation. Socket transport errors cannot prove Data access is denied.
	if fallback != startupFramework && (fallback != startupOccupied || location != nil) {
		switch {
		case errors.Is(err, errUnsupportedLocation):
			kind = startupUnsupported
		case errors.Is(err, os.ErrPermission), errors.Is(err, windows.ERROR_ACCESS_DENIED), errors.Is(err, windows.ERROR_WRITE_PROTECT):
			kind = startupPermission
		case errors.Is(err, errUnreliableIdentity):
			kind = startupOwnership
		}
	}
	return &startupFailure{kind: kind, path: path, cause: err}
}

type startupNotice struct{ title, message string }

func noticeForStartup(err error) startupNotice {
	var failure *startupFailure
	if !errors.As(err, &failure) {
		failure = &startupFailure{kind: startupFramework, cause: err}
	}
	var message string
	switch failure.kind {
	case startupUnsupported:
		message = "当前位置暂不受当前版本支持。\n请将程序和 Data 一起放到可写的本地固定 NTFS/ReFS 磁盘目录后重试。USB 和网络位置的便携运行仍待支持。"
	case startupPermission:
		message = "无法访问或写入安装目录或 Data。\n请关闭可能占用文件的程序，并将 SyncMaid 和 Data 一起放到您有读写权限的目录后重试。"
	case startupOccupied:
		message = "已有 SyncMaid 实例可能正在运行或启动，但本次未能确认窗口已恢复。\n请查看已有窗口，或稍后重试、使用托盘图标恢复。不要删除锁文件；本次启动不会接管或写入 Data。"
	case startupOwnership:
		message = "无法确认安装目录或 Data 的安全访问状态，程序已停止启动以保护数据。\n请确认 Data 是可访问的目录，并将程序和 Data 一起放到可写的本地固定 NTFS/ReFS 磁盘目录后重试。不要删除正在使用的锁文件。"
	default:
		message = "原生界面未能启动或正常结束。\n请关闭其他 SyncMaid 窗口后重新启动；若仍失败，请保留现有 Data 并反馈此问题。"
	}
	if failure.path != "" {
		path := []rune(pathLabel(failure.path))
		if len(path) > 200 {
			path = append(append(path[:80], []rune(" … ")...), path[len(path)-100:]...)
		}
		message += "\n\n位置：" + string(path)
	}
	return startupNotice{title: "SyncMaid 启动提示", message: message}
}

// runStartup displays at most one native notice AFTER run and all its resource
// defers finish. Neither classification nor the presenter initializes MyGo.
func runStartup(run func() error, present func(startupNotice) error) int {
	if err := run(); err != nil {
		slog.Error("startup-failed", "error", err)
		if displayErr := present(noticeForStartup(err)); displayErr != nil {
			slog.Error("startup-notice-failed", "error", displayErr)
		}
		return 1
	}
	return 0
}
