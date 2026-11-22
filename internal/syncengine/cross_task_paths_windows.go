package syncengine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

// Compare current object identities, not path spellings. Only peers in the
// same role are compared, preserving target-to-source chains between tasks.
func ValidateCrossTaskPaths(ctx context.Context, current taskconfig.ID, q ManualTaskRequest, tasks []taskconfig.Task) error {
	if len(tasks) < 2 {
		return nil
	}
	cache := map[string][]string{}
	chain := func(path string) ([]string, error) {
		if keys, ok := cache[path]; ok {
			return keys, nil
		}
		var keys []string
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			d, err := winutil.OpenDirectoryAccess(path, windows.FILE_READ_ATTRIBUTES)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, nil
			}
			keys = append(keys, d.Key)
			parent := filepath.Dir(d.Path)
			done := parent == d.Path
			if err = d.Close(); err != nil {
				return nil, err
			}
			if done {
				break
			}
			path = parent
		}
		return keys, nil
	}
	compare := func(a, b string) (bool, error) {
		if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
			return false, nil
		}
		if ObviousPathOverlap(a, b) {
			return true, nil
		}
		x, xerr := chain(a)
		y, yerr := chain(b)
		if len(x) > 0 {
			cache[a] = x
		}
		if len(y) > 0 {
			cache[b] = y
		}
		// An unavailable peer cannot be active in this single-executor app.
		// A currently usable peer must be checked by its actual object.
		if xerr != nil || yerr != nil {
			if errors.Is(xerr, context.Canceled) || errors.Is(yerr, context.Canceled) {
				return false, ctx.Err()
			}
			return false, errors.Join(xerr, yerr)
		}
		if len(x) == 0 || len(y) == 0 {
			return false, nil
		}
		for _, key := range x {
			if key == y[0] {
				return true, nil
			}
		}
		for _, key := range y {
			if key == x[0] {
				return true, nil
			}
		}
		return false, nil
	}
	for _, other := range tasks {
		if other.ID == current || len(other.Destinations) == 0 {
			continue
		}
		check := func(a, b string) error {
			overlap, err := compare(a, b)
			if err != nil {
				return fmt.Errorf("无法检查与任务 %q 的目录关系：%w", other.Name, err)
			}
			if overlap {
				return fmt.Errorf("与任务 %q 的同类目录重叠，未执行文件操作", other.Name)
			}
			return nil
		}
		if err := check(q.Source, other.SourcePath); err != nil {
			return err
		}
		for _, d := range q.Destinations {
			for _, peer := range other.Destinations {
				if err := check(d.Path, peer.Target.Path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
