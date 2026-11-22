//go:build windows

package windowsfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Identity is valid for comparing objects while their handles are held.
type Identity struct {
	Volume uint64
	File   [16]byte
	Legacy bool
}

func Volume(h windows.Handle) (string, uint32, error) {
	var name [256]uint16
	var flags uint32
	err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, &flags, &name[0], uint32(len(name)))
	return strings.ToUpper(windows.UTF16ToString(name[:])), flags, err
}

func FAT(kind string) bool {
	return kind == "FAT" || kind == "FAT32" || kind == "EXFAT"
}

func Network(path string) bool {
	volume := filepath.VolumeName(path)
	if strings.HasPrefix(strings.ToUpper(volume), "\\\\?\\UNC\\") ||
		strings.HasPrefix(volume, "\\\\") && !strings.HasPrefix(volume, "\\\\?\\") && !strings.HasPrefix(volume, "\\\\.\\") {
		return true
	}
	volume = strings.TrimPrefix(volume, "\\\\?\\")
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	root, err := windows.UTF16PtrFromString(volume + "\\")
	return err == nil && windows.GetDriveType(root) == windows.DRIVE_REMOTE
}

func isUnsupportedInfoClass(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_LEVEL)
}

func legacyFileIdentity(h windows.Handle) (Identity, error) {
	// FAT directory-entry positions and some SMB server file indices can change
	// or have narrower 64-bit scope. Use these only on local FAT-family volumes
	// or network shares whose SMB server rejects 128-bit FileIdInfo.
	var basic windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &basic); err != nil {
		return Identity{}, err
	}
	index := uint64(basic.FileIndexHigh)<<32 | uint64(basic.FileIndexLow)
	if index == 0 {
		return Identity{}, errors.New("empty legacy file index")
	}
	out := Identity{Volume: uint64(basic.VolumeSerialNumber), Legacy: true}
	binary.LittleEndian.PutUint64(out.File[:], index)
	return out, nil
}

func FileIdentity(h windows.Handle, fat, network bool) (Identity, error) {
	if fat {
		return legacyFileIdentity(h)
	}
	var id struct {
		Volume uint64
		File   [16]byte
	}
	err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	if err == nil {
		if id.File == [16]byte{} {
			return Identity{}, errors.New("empty file ID")
		}
		return Identity{Volume: id.Volume, File: id.File}, nil
	}
	if network && isUnsupportedInfoClass(err) {
		return legacyFileIdentity(h)
	}
	return Identity{}, err
}

func CaseSensitive(h windows.Handle) (bool, error) {
	var flags uint32
	err := windows.GetFileInformationByHandleEx(h, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&flags)), 4)
	if err == nil {
		return flags&windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0, nil
	}
	if isUnsupportedInfoClass(err) {
		kind, capabilities, volumeErr := Volume(h)
		// An affirmative volume report can prove case-sensitive lookups are
		// impossible. A server that can't report either capability is refused.
		if volumeErr == nil && (FAT(kind) || capabilities&windows.FILE_CASE_SENSITIVE_SEARCH == 0) {
			return false, nil
		}
		return false, fmt.Errorf("%w: %w", errors.ErrUnsupported, errors.Join(err, volumeErr))
	}
	return false, err
}
