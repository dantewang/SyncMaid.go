package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errOccupied = errors.New("installation or Data is already owned")

func prepareDataDirectory(path string) error { return os.MkdirAll(path, 0o755) }

// directoryObject pins the directory against rename/deletion until Close.
// Its identity is diagnostic/activation data; exclusion uses the actual file
// object, not a hash of its pathname or a Windows session's object namespace.
type directoryObject struct {
	handle windows.Handle
	path   string // Native operation path, including extended-length prefix.
	key    string
}

func openDirectory(path string) (*directoryObject, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", path, err)
	}
	ok := false
	defer func() {
		if !ok {
			windows.CloseHandle(h)
		}
	}()
	var basic windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &basic); err != nil {
		return nil, err
	}
	if basic.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return nil, fmt.Errorf("not a directory: %s", path)
	}
	var final string
	for size := uint32(512); size <= 65536; {
		buffer := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(h, &buffer[0], size, 0)
		if err != nil {
			return nil, err
		}
		if n < size {
			final = windows.UTF16ToString(buffer[:n])
			break
		}
		size = n + 1
	}
	if final == "" {
		return nil, errors.New("directory final path too long")
	}
	// Network/offline filesystem coordination is deliberately unsupported until
	// tested. A failure here never falls back to the original pathname.
	display := strings.TrimPrefix(final, `\\?\`)
	if strings.HasPrefix(strings.ToUpper(display), `UNC\`) {
		return nil, errors.New("network Data/install directories are not supported")
	}
	volume := filepath.VolumeName(display)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, errors.New("directory has no supported local DOS volume path")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, errors.New("ownership requires a local fixed volume")
	}
	var fs [256]uint16
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return nil, err
	}
	if kind := windows.UTF16ToString(fs[:]); kind != "NTFS" && kind != "ReFS" {
		return nil, fmt.Errorf("unsupported ownership filesystem %q", kind)
	}
	var id struct {
		Volume uint64
		File   [16]byte
	}
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id))); err != nil {
		return nil, fmt.Errorf("directory identity: %w", err)
	}
	if id.File == [16]byte{} {
		return nil, errors.New("filesystem returned an empty directory identity")
	}
	ok = true
	return &directoryObject{handle: h, path: final, key: fmt.Sprintf("%016x-%x", id.Volume, id.File)}, nil
}

func (d *directoryObject) Close() error { return windows.CloseHandle(d.handle) }

// A persistent, non-reparse coordination file is opened WITHOUT sharing.
// Windows enforces ownership across processes, users and sessions accessing
// this file. There is no thread-owned mutex, stale-file deletion, or timeout
// takeover. A crash closes the kernel handle; the filename remains reusable.
func claimDirectory(d *directoryObject) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(filepath.Join(d.path, ".syncmaid-owner.lock"))
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return 0, errOccupied
	}
	if err != nil {
		return 0, fmt.Errorf("claim directory ownership: %w", err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		return 0, errors.New("coordination file must be a regular, single-link file")
	}
	return h, nil
}

// ownership is a resource receipt: only a successfully returned value permits
// application writes. Acquisition rolls back all handles on every error.
type ownership struct {
	directories []*directoryObject
	claims      []windows.Handle
	data        *directoryObject
}

// dataDirectory returns the pinned Data object while this receipt is held.
func (o *ownership) dataDirectory() *directoryObject { return o.data }

func (o *ownership) Close() error {
	var result error
	for i := len(o.claims) - 1; i >= 0; i-- {
		result = errors.Join(result, windows.CloseHandle(o.claims[i]))
	}
	for i := len(o.directories) - 1; i >= 0; i-- {
		result = errors.Join(result, o.directories[i].Close())
	}
	o.claims, o.directories = nil, nil
	o.data = nil
	return result
}

func acquireOwnership(install installation) (_ *ownership, result error) {
	o := &ownership{}
	defer func() {
		if result != nil {
			result = errors.Join(result, o.Close())
		}
	}()
	for _, path := range []string{install.directory, install.data} {
		// Data creation is coordination metadata, after install exclusion,
		// before any configuration/log/UI framework write is allowed.
		if path == install.data {
			if err := prepareDataDirectory(path); err != nil {
				return nil, err
			}
		}
		d, err := openDirectory(path)
		if err != nil {
			return nil, err
		}
		o.directories = append(o.directories, d)
		if path == install.data {
			o.data = d
		}
		h, err := claimDirectory(d)
		if err != nil {
			return nil, err
		}
		o.claims = append(o.claims, h)
	}
	return o, nil
}
