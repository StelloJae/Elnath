package fault

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	envFaultProfile = "ELNATH_FAULT_PROFILE"
	guardWaitSecs   = 5
)

type GuardConfig struct {
	Enabled bool
}

func CheckGuards(cfg GuardConfig) (scenarioName string, err error) {
	profile := os.Getenv(envFaultProfile)
	if profile == "" {
		return "", nil
	}
	if !cfg.Enabled {
		return "", fmt.Errorf("fault: ELNATH_FAULT_PROFILE=%q but fault_injection.enabled=false in config - refusing to start", profile)
	}
	printDaemonWarning(profile)
	if interrupted := waitWithInterrupt(guardWaitSecs); interrupted {
		return "", fmt.Errorf("fault: startup aborted by user (SIGINT during fault warning countdown)")
	}
	return profile, nil
}

func printDaemonWarning(profile string) {
	isTTY := term.IsTerminal(int(os.Stderr.Fd()))
	if isTTY {
		_, _ = fmt.Fprint(os.Stderr, "\033[1;31m")
	}
	_, _ = fmt.Fprintf(os.Stderr,
		"\nWARNING: FAULT INJECTION ACTIVE: scenario=%q\n"+
			"  This daemon will deliberately corrupt operations.\n"+
			"  NOT for production use. Starting in %d seconds. Ctrl-C to abort.\n\n",
		profile, guardWaitSecs)
	if isTTY {
		_, _ = fmt.Fprint(os.Stderr, "\033[0m")
	}
}

func waitWithInterrupt(secs int) (interrupted bool) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	remaining := secs
	for {
		select {
		case <-sigs:
			return true
		case <-ticker.C:
			remaining--
			if remaining <= 0 {
				return false
			}
		}
	}
}
