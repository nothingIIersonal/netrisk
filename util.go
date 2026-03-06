package main

import (
	"math"
	"os"
	"path/filepath"
	"time"
)

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func ageSec(now, last time.Time) float64 {
	if last.IsZero() {
		return 0
	}
	a := now.Sub(last).Seconds()
	if a < 0 {
		return 0
	}
	return a
}

func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var _ = math.Inf // to silence unused import if needed
