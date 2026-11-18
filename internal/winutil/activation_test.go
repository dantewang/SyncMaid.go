package winutil

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestActivationDeliveryAndServerCleanup(t *testing.T) {
	key := t.TempDir()
	shown := make(chan struct{}, 1)
	s, err := StartActivation(key, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := net.DialTimeout("unix", activationPath(key), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = c.Write([]byte{1})
	c.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-shown:
	case <-time.After(5 * time.Second):
		t.Fatal("activation did not arrive")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activationPath(key)); !os.IsNotExist(err) {
		t.Fatalf("endpoint not cleaned: %v", err)
	}
}
