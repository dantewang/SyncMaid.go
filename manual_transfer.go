package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"time"
)

func transferNeeded(sourceSize int64, sourceTime time.Time, targetSize int64, targetTime time.Time) bool {
	return sourceSize != targetSize || sourceTime.Unix() != targetTime.Unix()
}
func copyChecked(ctx context.Context, source, temp *os.File, progress func(int64)) error {
	return copyWithVerify(ctx, source, temp, true, progress)
}

func copyUnverified(ctx context.Context, source, temp *os.File, progress func(int64)) error {
	return copyWithVerify(ctx, source, temp, false, progress)
}

func transferCopier(verifyContents bool) func(context.Context, *os.File, *os.File, func(int64)) error {
	if verifyContents {
		return copyChecked
	}
	return copyUnverified
}

func copyWithVerify(ctx context.Context, source, temp *os.File, verifyContents bool, progress func(int64)) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	buffer := make([]byte, 256<<10)
	if !verifyContents {
		written, err := copyTransfer(ctx, temp, source, buffer, progress)
		if err != nil {
			return err
		}
		if written != info.Size() {
			return errors.New("目标临时文件长度校验失败")
		}
		if err := temp.Sync(); err != nil {
			return err
		}
		return ctx.Err()
	}
	h := sha256.New()
	written, err := copyTransfer(ctx, io.MultiWriter(temp, h), source, buffer, progress)
	if err != nil {
		return err
	}
	if written != info.Size() {
		return errors.New("目标临时文件长度校验失败")
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	actual := sha256.New()
	if _, err := copyTransfer(ctx, actual, temp, buffer, nil); err != nil {
		return err
	}
	if string(h.Sum(nil)) != string(actual.Sum(nil)) {
		return errors.New("目标临时文件完整性校验失败")
	}
	return ctx.Err()
}

func copyTransfer(ctx context.Context, dst io.Writer, src io.Reader, buffer []byte, progress func(int64)) (total int64, err error) {
	for {
		if err = ctx.Err(); err != nil {
			return total, err
		}
		n, re := src.Read(buffer)
		if n > 0 {
			written, we := dst.Write(buffer[:n])
			total += int64(written)
			if progress != nil {
				progress(int64(written))
			}
			if we != nil {
				return total, we
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if re == io.EOF {
			return total, nil
		}
		if re != nil {
			return total, re
		}
	}
}
