package syncengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/windowsfs"
	"syncmaid/internal/winutil"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCompareStringOrdinal = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")

func sameFileNameOrdinal(a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	if len(ua) == 0 || len(ub) == 0 {
		return len(ua) == len(ub), nil
	}
	r1, _, err := procCompareStringOrdinal.Call(
		uintptr(unsafe.Pointer(&ua[0])),
		uintptr(len(ua)),
		uintptr(unsafe.Pointer(&ub[0])),
		uintptr(len(ub)),
		1,
	)
	if r1 == 0 {
		return false, err
	}
	return r1 == 2, nil
}

var errShortNameConflict = errors.New("目标存在 8.3 短文件名冲突，无法安全同步")

var openedFinalPath = windowsfs.FinalPathNormalized

func checkOpenedName(h windows.Handle, requested string) error {
	final, err := openedFinalPath(h)
	if err != nil {
		// Deliberately avoid wrapping (%w) so post-open query errors such as
		// ERROR_PATH_NOT_FOUND are never misclassified as a missing file.
		return fmt.Errorf("校验文件规范名称失败: %v", err)
	}
	base := strings.TrimRight(final, `\`)
	if idx := strings.LastIndexByte(base, '\\'); idx >= 0 {
		base = base[idx+1:]
	}
	ok, err := sameFileNameOrdinal(base, requested)
	if err != nil {
		return fmt.Errorf("比对文件规范名称失败: %v", err)
	}
	if !ok {
		return errShortNameConflict
	}
	return nil
}

// Retry only the initial open of an existing object. Never replay a write,
// rename or delete whose result could be ambiguous.
func retryTransferOpen(ctx context.Context, open func() (*os.File, error)) (*os.File, error) {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := open()
		if attempt == 3 || (!errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION)) {
			return f, err
		}
		timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func ntError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

func checkOrdinaryDirectory(sensitive bool, err error) error {
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return nil
		}
		return err
	}
	if sensitive {
		return errors.New("暂不支持区分大小写的目录")
	}
	return nil
}

func needsCaseCheck(d *winutil.DirectoryObject) bool {
	return d != nil && !d.Network && !d.Fat
}

func ordinaryDirectory(d *winutil.DirectoryObject) error {
	if !needsCaseCheck(d) {
		return nil
	}
	return checkOrdinaryDirectory(windowsfs.CaseSensitive(d.Handle))
}

func splitRelativePath(rel string) ([]string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return nil, errors.New("非法相对文件路径")
	}
	parts := strings.Split(rel, "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." || strings.Contains(s, ":") {
			return nil, errors.New("非法相对路径组件")
		}
	}
	return parts, nil
}

func CloseProtected(ctx context.Context, closer io.Closer) error {
	return ProtectedTransferIO(ctx, closer.Close)
}

func CloseFilesProtected(ctx context.Context, files []*os.File) error {
	return ProtectedTransferIO(ctx, func() (e error) {
		for i := len(files) - 1; i >= 0; i-- {
			e = errors.Join(e, files[i].Close())
		}
		return e
	})
}

func openTransferChild(parent windows.Handle, name string, dir bool, disposition, access, share uint32) (*os.File, error) {
	return openTransferChildChecked(parent, name, dir, true, false, disposition, access, share)
}

func openTransferChildChecked(parent windows.Handle, name string, dir, checkCase, checkName bool, disposition, access, share uint32) (*os.File, error) {
	options := uint32(windows.FILE_NON_DIRECTORY_FILE)
	if dir {
		options = windows.FILE_DIRECTORY_FILE
	}
	h, info, e := windowsfs.OpenChildNoReparse(parent, name, access, share, disposition, options)
	if errors.Is(e, windowsfs.ErrReparsePoint) {
		return nil, errors.New("不支持链接或重解析点: " + name)
	}
	if e != nil {
		return nil, fmt.Errorf("打开 %q: %w", name, e)
	}
	if !dir && info.NumberOfLinks > 1 {
		e = errors.New("暂不支持硬链接文件: " + name)
	}
	if e == nil && dir && checkCase {
		e = checkOrdinaryDirectory(windowsfs.CaseSensitive(h))
	}
	if e == nil && checkName && disposition != windows.FILE_CREATE {
		e = checkOpenedName(h, name)
	}
	if e != nil {
		windows.CloseHandle(h)
		return nil, e
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		return nil, errors.Join(errors.New("invalid file handle"), windows.CloseHandle(h))
	}
	return f, nil
}

// Windows-specific variable-length NT buffers live here, not in the UI/service.
type transferRename struct {
	Replace uint32
	Root    windows.Handle
	Bytes   uint32
	Name    [1]uint16
}

func PublishTransfer(f *os.File, parent windows.Handle, name string, replace bool) error {
	wide, e := windows.UTF16FromString(name)
	if e != nil {
		return e
	}
	wide = wide[:len(wide)-1]
	var header transferRename
	// NT also requires the fixed structure size, including alignment padding,
	// even when the counted filename itself is only one UTF-16 code unit.
	size := max(int(unsafe.Sizeof(header)), int(unsafe.Offsetof(header.Name))+len(wide)*2)
	buffer := make([]byte, size)
	info := (*transferRename)(unsafe.Pointer(&buffer[0]))
	info.Root = parent
	info.Bytes = uint32(len(wide) * 2)
	if replace {
		info.Replace = windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	copy(unsafe.Slice(&info.Name[0], len(wide)), wide)
	return ntError(windows.NtSetInformationFile(windows.Handle(f.Fd()), &windows.IO_STATUS_BLOCK{}, &buffer[0], uint32(size), windows.FileRenameInformation))
}
func discardTransfer(f *os.File) error {
	delete := byte(1)
	return ntError(windows.NtSetInformationFile(windows.Handle(f.Fd()), &windows.IO_STATUS_BLOCK{}, &delete, 1, windows.FileDispositionInformation))
}

func publishTransferChecked(ctx context.Context, temp *os.File, parent windows.Handle, name string, replace bool, publication *TransferPublication, publish func(*os.File, windows.Handle, string, bool) error) error {
	return ProtectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		*publication = TransferPublishUnknown
		if e := publish(temp, parent, name, replace); e != nil {
			return fmt.Errorf("%w (临时名称 %q): %w", ErrTransferPublicationUnknown, temp.Name(), e)
		}
		*publication = TransferPublished
		return nil
	})
}

func finalizeTempTransfer(ctx context.Context, temp *os.File, publication *TransferPublication) error {
	return ProtectedTransferIO(ctx, func() (e error) {
		if *publication == TransferStaged {
			e = discardTransfer(temp)
		}
		return errors.Join(e, temp.Close())
	})
}

func deleteVerifiedSource(ctx context.Context, source *os.File, expected MirrorStamp, fat, network bool, remove func(*os.File) error) error {
	return ProtectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		actual, e := mirrorFileStamp(source, fat, network)
		if e != nil {
			return e
		}
		if actual != expected {
			return errors.New("目标已发布，但源对象已变化，未移除源")
		}
		if remove == nil {
			remove = discardTransfer
		}
		if e := remove(source); e != nil {
			return fmt.Errorf("目标已发布，源移除未完成，请检查两处文件: %w", e)
		}
		return nil
	})
}
