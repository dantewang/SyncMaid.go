package main

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ntError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

func ordinaryDirectory(h windows.Handle) error {
	var flags uint32
	if err := windows.GetFileInformationByHandleEx(h, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&flags)), 4); err != nil {
		return err
	}
	if flags&windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0 {
		return errors.New("暂不支持区分大小写的目录")
	}
	return nil
}

func openTransferChild(parent windows.Handle, name string, dir bool, disposition, access, share uint32) (*os.File, error) {
	object, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return nil, e
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	if dir {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var h windows.Handle
	e = windows.NtCreateFile(&h, access|windows.SYNCHRONIZE, &attrs, &windows.IO_STATUS_BLOCK{}, nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition, options, 0, 0)
	if e != nil {
		return nil, fmt.Errorf("打开 %q: %w", name, ntError(e))
	}
	var info windows.ByHandleFileInformation
	e = windows.GetFileInformationByHandle(h, &info)
	if e == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		e = errors.New("不支持链接或重解析点: " + name)
	}
	if e == nil && !dir && info.NumberOfLinks > 1 {
		e = errors.New("暂不支持硬链接文件: " + name)
	}
	if e == nil && dir {
		e = ordinaryDirectory(h)
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

func publishTransfer(f *os.File, parent windows.Handle, name string, replace bool) error {
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
