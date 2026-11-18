package winutil

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

func activationPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(os.TempDir(), fmt.Sprintf("syncmaid-activate-%x.sock", sum[:12]))
}

type ActivationServer struct {
	listener net.Listener
	done     chan struct{}
}

var errInvalidActivationReceipt = errors.New("invalid activation receipt")

func StartActivation(key string, show func()) (*ActivationServer, error) {
	path := activationPath(key)
	// Only the exclusive Data owner may remove a stale activation endpoint.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &ActivationServer{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					slog.Error("activation-accept-failed", "error", err)
				}
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var message [1]byte
			n, err := conn.Read(message[:])
			if err == nil && n == 1 && message[0] == 1 {
				slog.Info("activation-received")
				show()
				_, _ = conn.Write([]byte{1}) // Receipt: activation was queued, not ownership.
			}
			conn.Close()
		}
	}()
	return s, nil
}

func (s *ActivationServer) Close() error {
	err := s.listener.Close()
	<-s.done // One bounded in-flight read at most; no unbounded workers.
	return err
}

func NotifyInstallation(install Installation) error {
	d, err := OpenDirectory(install.Data)
	if err != nil {
		slog.Warn("activation-unavailable", "error", err)
		return &LocationError{Path: install.Data, Cause: err}
	}
	defer d.Close()
	conn, err := net.DialTimeout("unix", activationPath(d.Key), time.Second)
	if err != nil {
		slog.Warn("activation-unavailable", "error", err)
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte{1}); err != nil {
		slog.Warn("activation-unavailable", "error", err)
		return err
	}
	var receipt [1]byte
	// An older owner may have queued show but sent no receipt. A failed read
	// means unconfirmed activation, never proof that the window stayed hidden.
	if _, err := io.ReadFull(conn, receipt[:]); err != nil {
		return fmt.Errorf("activation receipt: %w", err)
	}
	if receipt[0] != 1 {
		return errInvalidActivationReceipt
	}
	return nil
}
