// mockplatform is a stand-in for the real focusd platform, used to
// verify the daemon end-to-end before the platform is refactored.
//
//	mockplatform --version            print version, exit 0
//	mockplatform --workdir D          write D/platform_running = <version>,
//	                                  then run until SIGTERM (then remove it)
//	mockplatform --workdir D --crash  write nothing, exit 7 immediately
//	                                  (simulates a bad/crashing version)
//
// version is injected via -ldflags "-X main.version=vX".
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var version = "dev"

// crashOnStart, when "1" (set via -ldflags at build time), makes the
// binary exit non-zero immediately even on a normal run — used to e2e
// a "bad release" that the daemon must detect and roll back.
var crashOnStart = ""

func main() {
	showVer := flag.Bool("version", false, "print version and exit")
	workdir := flag.String("workdir", "", "dir to write running marker")
	crash := flag.Bool("crash", false, "exit immediately non-zero")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}
	if *crash || crashOnStart == "1" {
		fmt.Fprintf(os.Stderr, "mockplatform %s: simulated crash\n", version)
		os.Exit(7)
	}

	marker := ""
	if *workdir != "" {
		marker = filepath.Join(*workdir, "platform_running")
		_ = os.MkdirAll(*workdir, 0o755)
		_ = os.WriteFile(marker, []byte(version), 0o644)
	}
	fmt.Printf("mockplatform %s running\n", version)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGTERM, syscall.SIGINT)
	for {
		select {
		case <-sigc:
			if marker != "" {
				_ = os.Remove(marker)
			}
			fmt.Printf("mockplatform %s stopped\n", version)
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}
