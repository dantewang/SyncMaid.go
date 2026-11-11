package main

import (
	"crypto/sha256"
	"fmt"
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

type activationServer struct {
	listener net.Listener
	done     chan struct{}
}

func startActivation(key string, show func()) (*activationServer, error) {
	path := activationPath(key)
	// Only the exclusive Data owner may remove a stale activation endpoint.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &activationServer{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var message [1]byte
			n, err := conn.Read(message[:])
			conn.Close()
			if err == nil && n == 1 && message[0] == 1 {
				slog.Info("activation-received")
				show()
			}
		}
	}()
	return s, nil
}

func (s *activationServer) Close() error {
	err := s.listener.Close()
	<-s.done // One bounded in-flight read at most; no unbounded workers.
	return err
}

func notifyInstallation(install installation) {
	d, err := openDirectory(install.data)
	if err != nil {
		slog.Warn("activation-unavailable", "error", err)
		return
	}
	defer d.Close()
	conn, err := net.DialTimeout("unix", activationPath(d.key), time.Second)
	if err != nil {
		slog.Warn("activation-unavailable", "error", err)
		return
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte{1}); err != nil {
		slog.Warn("activation-unavailable", "error", err)
	}
}
