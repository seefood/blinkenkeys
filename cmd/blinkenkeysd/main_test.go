package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnIfRootFallback_NonLinux(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("darwin", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output on non-Linux, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxNonRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 1000, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output for non-root, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if !strings.Contains(buf.String(), "running as root") {
		t.Errorf("expected a root-fallback warning, got %q", buf.String())
	}
}
