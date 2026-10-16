//go:build darwin

package macx

import (
	"os/exec"
	"path/filepath"
)

func soundCmd(s Sound) *exec.Cmd {
	path := filepath.Join("/System/Library/Sounds", filepath.Base(string(s))+".aiff")
	return exec.Command("/usr/bin/afplay", "-v", soundVolume, path)
}

// playWait: afplay exits when the sound ended.
func playWait(s Sound) error { return soundCmd(s).Run() }

func play(s Sound) error {
	cmd := soundCmd(s)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it
	return nil
}
