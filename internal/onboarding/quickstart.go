package onboarding

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

// QuickstartResult extends Result with quickstart-specific metadata.
type QuickstartResult struct {
	Result
	ProviderDetected string
	SmokeTestPassed  bool
}

// RunQuickstart executes the minimal onboarding path without the TUI wizard.
func RunQuickstart(cfgPath, version string) (*QuickstartResult, error) {
	_ = cfgPath
	_ = version

	res := &QuickstartResult{Result: Result{Locale: En, PermissionMode: "default"}}

	if llm.CodexOAuthAvailable() {
		res.ProviderDetected = "codex"
		fmt.Println("Codex OAuth detected - skipping API key setup.")
	} else {
		fmt.Print("Enter your Anthropic API key (press Enter to skip): ")
		res.APIKey = strings.TrimSpace(readLineOrEnv("ELNATH_ANTHROPIC_API_KEY"))
		if res.APIKey != "" {
			res.ProviderDetected = "anthropic"
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("quickstart home dir: %w", err)
	}
	base := filepath.Join(home, ".elnath")
	res.DataDir = filepath.Join(base, "data")
	res.WikiDir = filepath.Join(base, "wiki")

	if res.APIKey != "" {
		vr := ValidateAnthropicKey(context.Background(), res.APIKey)
		res.SmokeTestPassed = vr.Valid
		if vr.Valid {
			fmt.Println("Connection test passed.")
		} else {
			fmt.Printf("Connection test failed (%v) - config saved anyway.\n", vr.Error)
		}
	}

	return res, nil
}

func readLineOrEnv(envKey string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
