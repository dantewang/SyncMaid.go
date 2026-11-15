package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Read the actual handle's current filesystem name, never os.File.Name's
// cached spelling. The temp is private and its parent remains pinned.
func transferObjectName(f *os.File) ([]byte, error) {
	for size := 512; size <= 131072; size *= 2 {
		b := make([]byte, size)
		e := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileNameInfo, &b[0], uint32(size))
		if errors.Is(e, windows.ERROR_MORE_DATA) || errors.Is(e, windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if e != nil {
			return nil, e
		}
		n := int(binary.LittleEndian.Uint32(b[:4]))
		if n <= 0 || n > size-4 || n%2 != 0 {
			return nil, errors.New("无法确认临时文件实际名称")
		}
		return b[4 : 4+n], nil
	}
	return nil, errors.New("临时文件实际名称超出支持长度")
}

func publishMoveSuffix(ctx context.Context, temp *os.File, parent windows.Handle, name string, publish func(*os.File, windows.Handle, string, bool) error) (transferPublication, error) {
	staged, e := transferObjectName(temp)
	if e != nil {
		return transferStaged, e
	}
	return tryMoveNames(ctx, name, func(candidate string) (transferPublication, error) {
		state := transferStaged
		e := protectedTransferIO(ctx, func() error {
			if e := ctx.Err(); e != nil {
				return e
			}
			state = transferPublishUnknown
			e := publish(temp, parent, candidate, false)
			if e == nil {
				state = transferPublished
				return nil
			}
			if errors.Is(e, windows.ERROR_ALREADY_EXISTS) || errors.Is(e, windows.ERROR_FILE_EXISTS) {
				actual, queryErr := transferObjectName(temp)
				if queryErr == nil && bytes.Equal(staged, actual) {
					state = transferStaged
					return fmt.Errorf("%w: %w", errMoveNameOccupied, e)
				}
				e = errors.Join(e, queryErr)
			}
			return fmt.Errorf("%w: %w", errTransferPublicationUnknown, e)
		})
		return state, e
	})
}
