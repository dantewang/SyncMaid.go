package winutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"syncmaid/internal/windowsfs"

	"golang.org/x/sys/windows"
)

var ErrOccupied = errors.New("installation or Data is already owned")

func PrepareDataDirectory(path string) error { return os.MkdirAll(path, 0o755) }

// DirectoryObject pins the directory against rename/deletion until Close.
// Its identity is diagnostic/activation data; exclusion uses the actual file
// object, not a hash of its pathname or a Windows session's object namespace.
type DirectoryObject struct {
	Handle    windows.Handle
	Path      string // Native operation path, including extended-length prefix.
	Key       string
	Network   bool
	Fat       bool
	Removable bool
}

func OpenDirectory(path string) (*DirectoryObject, error) {
	d, err := OpenDirectoryAccess(path, windows.FILE_READ_ATTRIBUTES)
	if err == nil && d.Network {
		err = errors.Join(fmt.Errorf("%w: installation and Data must be local", ErrUnsupportedLocation), d.Close())
		return nil, err
	}
	return d, err
}

func OpenDirectoryAccess(path string, access uint32) (*DirectoryObject, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, access,
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
	final, err := windowsfs.FinalPath(h)
	if err != nil {
		return nil, err
	}
	network := windowsfs.Network(final) || windowsfs.Network(path)
	removable := false
	if !network {
		volume := filepath.VolumeName(PathLabel(final))
		if len(volume) != 2 || volume[1] != ':' {
			return nil, fmt.Errorf("%w: directory has no local DOS volume path", ErrUnsupportedLocation)
		}
		root, err := windows.UTF16PtrFromString(volume + "\\")
		if err != nil {
			return nil, err
		}
		switch windows.GetDriveType(root) {
		case windows.DRIVE_FIXED:
		case windows.DRIVE_REMOVABLE:
			removable = true
		default:
			return nil, fmt.Errorf("%w: directory drive type", ErrUnsupportedLocation)
		}
	}
	kind, _, err := windowsfs.Volume(h)
	if err != nil {
		return nil, err
	}
	fat := !network && windowsfs.FAT(kind)
	if !network && !fat && kind != "NTFS" && kind != "REFS" {
		return nil, fmt.Errorf("%w: filesystem %q", ErrUnsupportedLocation, kind)
	}
	id, err := windowsfs.FileIdentity(h, fat, network)
	if err != nil {
		return nil, fmt.Errorf("%w (fs=%s, network=%t, path=%s): %w", ErrUnreliableIdentity, kind, network, PathLabel(final), err)
	}
	key := fmt.Sprintf("%016x-%x", id.Volume, id.File)
	if id.Legacy {
		key = "legacy-" + key
	}
	ok = true
	return &DirectoryObject{Handle: h, Path: final, Key: key,
		Network: network, Fat: fat, Removable: removable}, nil
}

func (d *DirectoryObject) Close() error { return windows.CloseHandle(d.Handle) }

// A persistent, non-reparse coordination file is opened WITHOUT sharing.
// Windows enforces ownership across processes, users and sessions accessing
// this file. There is no thread-owned mutex, stale-file deletion, or timeout
// takeover. A crash closes the kernel handle; the filename remains reusable.
func claimDirectory(d *DirectoryObject) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(filepath.Join(d.Path, ".syncmaid-owner.lock"))
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return 0, ErrOccupied
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

// Ownership is a resource receipt: only a successfully returned value permits
// application writes. Acquisition rolls back all handles on every error.
type Ownership struct {
	directories []*DirectoryObject
	claims      []windows.Handle
	data        *DirectoryObject
}

// DataDirectory returns the pinned Data object while this receipt is held.
func (o *Ownership) DataDirectory() *DirectoryObject { return o.data }

func (o *Ownership) Close() error {
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

func AcquireOwnership(install Installation) (_ *Ownership, result error) {
	o := &Ownership{}
	defer func() {
		if result != nil {
			result = errors.Join(result, o.Close())
		}
	}()
	for _, path := range []string{install.Directory, install.Data} {
		// Data creation is coordination metadata, after install exclusion,
		// before any configuration/log/UI framework write is allowed.
		if path == install.Data {
			if err := PrepareDataDirectory(path); err != nil {
				return nil, &LocationError{Path: path, Cause: err}
			}
		}
		d, err := OpenDirectory(path)
		if err != nil {
			return nil, &LocationError{Path: path, Cause: err}
		}
		o.directories = append(o.directories, d)
		if path == install.Data {
			o.data = d
		}
		h, err := claimDirectory(d)
		if err != nil {
			return nil, &LocationError{Path: path, Cause: err}
		}
		o.claims = append(o.claims, h)
	}
	if err := o.claimLegacyPaths(install.Data, o.data.Path, install.LegacyData); err != nil {
		return nil, &LocationError{Path: install.Data, Cause: err}
	}
	return o, nil
}
