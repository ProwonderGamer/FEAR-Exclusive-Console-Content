//go:build !windows

package install

// GameRunning is only implemented on Windows (the game runs nowhere else).
func GameRunning() bool { return false }

func processRunning(string) bool { return false }
