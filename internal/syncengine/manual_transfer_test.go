package syncengine

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTransferNeeded(t *testing.T) {
	for _, c := range []struct {
		name                   string
		sourceSize, targetSize int64
		sourceTime, targetTime time.Time
		want                   bool
	}{
		{"same", 3, 3, time.Unix(10, 0), time.Unix(10, 0), false},
		{"subsecond ignored", 3, 3, time.Unix(10, 1), time.Unix(10, 999999999), false},
		{"size changed", 3, 4, time.Unix(10, 0), time.Unix(10, 0), true},
		{"second changed", 3, 3, time.Unix(10, 0), time.Unix(11, 0), true},
		{"empty same", 0, 0, time.Unix(0, 0), time.Unix(0, 0), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := TransferNeeded(c.sourceSize, c.sourceTime, c.targetSize, c.targetTime); got != c.want {
				t.Fatal(got, c.want)
			}
		})
	}
}
func TestCopyTransferShortWrite(t *testing.T) {
	_, e := CopyTransfer(context.Background(), shortTransferWriter{}, strings.NewReader("input"), make([]byte, 8), nil)
	if !errors.Is(e, io.ErrShortWrite) {
		t.Fatal(e)
	}
}

type shortTransferWriter struct{}

func (shortTransferWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
