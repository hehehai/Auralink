// Command fakehelper is helpertest's fake auralink-helper as a program,
// to stand in for the Swift helper in a packaged app until that exists:
// scripts/stage-fake-helper.sh builds it into the app's resources. It
// takes the options of AURALINK_FAKE_HELPER (helpertest) from its
// arguments or that variable; it records a tone, not the microphone.
package main

import (
	"os"
	"strings"

	"github.com/hehehai/Auralink/internal/helper/helpertest"
)

func main() {
	opts := os.Getenv("AURALINK_FAKE_HELPER")
	if len(os.Args) > 1 {
		opts = strings.Join(os.Args[1:], ",")
	}
	helpertest.Run(opts)
}
