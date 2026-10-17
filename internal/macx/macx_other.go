//go:build !darwin

package macx

import "time"

func activate(int, time.Duration, func(func())) (bool, time.Duration) { return false, 0 }

func frontmost() App { return App{} }

func start(Sound) (*Playing, error) {
	done := make(chan struct{})
	close(done)
	return &Playing{done: done}, nil
}

func windowNumber(uintptr) int { return 0 }
