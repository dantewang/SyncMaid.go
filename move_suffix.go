package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const moveNameAttempts = 4096

var errMoveNameOccupied = errors.New("候选名称已占用，临时对象仍在原位置")

// Name selection is bounded and separate from the Windows publication attempt.
func tryMoveNames(ctx context.Context, name string, attempt func(string) (transferPublication, error)) (transferPublication, error) {
	for number := 1; number <= moveNameAttempts; number++ {
		if e := ctx.Err(); e != nil {
			return transferStaged, e
		}
		state, e := attempt(moveSuffixName(name, number))
		if state == transferPublished {
			return state, nil
		}
		if state == transferPublishUnknown {
			return state, e
		}
		if ctx.Err() != nil {
			return transferStaged, errors.Join(e, ctx.Err())
		}
		if !errors.Is(e, errMoveNameOccupied) {
			return state, e
		}
	}
	return transferStaged, fmt.Errorf("寻找空闲名称达到%d次上限，已保留源文件", moveNameAttempts)
}

// The last dot divides stem/extension, matching the existing filename shape:
// report.tar.gz -> report.tar (2).gz; a name without a dot keeps no extension.
func moveSuffixName(name string, number int) string {
	if number == 1 {
		return name
	}
	stem, extension := name, ""
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		stem, extension = name[:dot], name[dot:]
	}
	return fmt.Sprintf("%s (%d)%s", stem, number, extension)
}
