package secret

import (
	"regexp"
	"strings"
	"testing"
)

func TestDetectorScan(t *testing.T) {
	t.Parallel()

	detector := NewDetector()
	tests := []struct {
		name    string
		content string
		wantIDs []string
	}{
		{"anthropic key", "key: sk-ant-api03-" + strings.Repeat("a", 80), []string{"anthropic-api-key"}},
		{"openai key", "sk-abcdefghijklmnopqrstuvwxyz1234", []string{"openai-api-key"}},
		{"openai project", "sk-proj-abcdefghijklmnopqrstuvwxyz", []string{"openai-project-key"}},
		{"aws access", "AKIAIOSFODNN7EXAMPLE", []string{"aws-access-key"}},
		{"aws secret", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", []string{"aws-secret-key"}},
		{"gcp api", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345", []string{"gcp-api-key"}},
		{"gcp service account", `{"type": "service_account"}`, []string{"gcp-service-account"}},
		{"github token", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab", []string{"github-token"}},
		{"github fine-grained", "github_pat_ABCDEFGHIJKLMNOPQRSTUV1234", []string{"github-fine-grained"}},
		{"gitlab token", "glpat-ABCDEFGHIJKLMNOPQRSTUabcd", []string{"gitlab-token"}},
		{"slack token", "xoxb-123456789012-abcdef", []string{"slack-token"}},
		{"slack webhook", "https://hooks.slack.com/services/T12345678/B12345678/abcdefghijklmnop", []string{"slack-webhook"}},
		{"stripe key", "sk_live_abcdefghijklmnopqrstuvwxyz", []string{"stripe-key"}},
		{"telegram bot", "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi", []string{"telegram-bot-token"}},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", []string{"jwt-token"}},
		{"pem key", "-----BEGIN RSA PRIVATE KEY-----", []string{"private-key-pem"}},
		{"generic password", `password = "mysecretpass123"`, []string{"generic-password"}},
		{"generic secret", `api_key = "sk_test_very_long_secret"`, []string{"generic-secret"}},
		{"connection string", "postgres://admin:secretpass@db.example.com:5432/mydb", []string{"connection-string"}},
		{"env secret", "API_SECRET_KEY=abcdef1234567890", []string{"env-file-secret"}},
		{"no secrets", `func main() { fmt.Println("hello") }`, nil},
		{"multiple secrets", "AKIAIOSFODNN7EXAMPLE and ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab", []string{"aws-access-key", "github-token"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := detector.Scan(tc.content)
			gotIDs := findingIDs(got)
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("len(Scan()) = %d, want %d; ids=%v", len(gotIDs), len(tc.wantIDs), gotIDs)
			}
			for i, want := range tc.wantIDs {
				if gotIDs[i] != want {
					t.Fatalf("Scan()[%d] = %q, want %q", i, gotIDs[i], want)
				}
			}
		})
	}
}

func TestDetectorRedact(t *testing.T) {
	t.Parallel()

	detector := NewDetector()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "single secret",
			content: "token=sk-ant-api03-" + strings.Repeat("a", 80),
			want:    "token=[REDACTED:anthropic-api-key]",
		},
		{
			name:    "two secrets",
			content: "AKIAIOSFODNN7EXAMPLE and ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab",
			want:    "[REDACTED:aws-access-key] and [REDACTED:github-token]",
		},
		{
			name:    "no findings",
			content: "hello world",
			want:    "hello world",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			findings := detector.Scan(tc.content)
			if got := detector.Redact(tc.content, findings); got != tc.want {
				t.Fatalf("Redact() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectorScanAndRedact(t *testing.T) {
	t.Parallel()

	detector := NewDetector()
	t.Run("secret content", func(t *testing.T) {
		t.Parallel()

		content := "Authorization: Bearer ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab"
		redacted, findings := detector.ScanAndRedact(content)
		if len(findings) != 1 {
			t.Fatalf("len(findings) = %d, want 1", len(findings))
		}
		if findings[0].RuleID != "github-token" {
			t.Fatalf("findings[0].RuleID = %q, want github-token", findings[0].RuleID)
		}
		if !strings.Contains(redacted, "[REDACTED:github-token]") {
			t.Fatalf("redacted = %q, want github redaction", redacted)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		t.Parallel()

		redacted, findings := detector.ScanAndRedact("")
		if redacted != "" {
			t.Fatalf("redacted = %q, want empty string", redacted)
		}
		if findings != nil {
			t.Fatalf("findings = %#v, want nil", findings)
		}
	})
}

func TestDetectorRedactString(t *testing.T) {
	t.Parallel()

	detector := NewDetector()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		if got := detector.RedactString(""); got != "" {
			t.Fatalf("RedactString() = %q, want empty string", got)
		}
	})

	t.Run("no secret", func(t *testing.T) {
		t.Parallel()

		in := "just a normal sentence"
		if got := detector.RedactString(in); got != in {
			t.Fatalf("RedactString() = %q, want %q", got, in)
		}
	})

	t.Run("contains secret", func(t *testing.T) {
		t.Parallel()

		in := "key=AKIAIOSFODNN7EXAMPLE done"
		got := detector.RedactString(in)
		if got == in {
			t.Fatalf("RedactString() did not change %q", in)
		}
		if !strings.Contains(got, "[REDACTED:aws-access-key]") {
			t.Fatalf("RedactString() = %q, want aws-access-key marker", got)
		}
	})

	t.Run("nil receiver safe", func(t *testing.T) {
		t.Parallel()

		var detector *Detector
		in := "anything"
		if got := detector.RedactString(in); got != in {
			t.Fatalf("nil RedactString() = %q, want %q", got, in)
		}
	})
}

func TestDetectorEdgeCases(t *testing.T) {
	t.Parallel()

	detector := NewDetector()
	t.Run("stripe vs openai distinction", func(t *testing.T) {
		t.Parallel()

		stripeFindings := detector.Scan("sk_live_abcdefghijklmnopqrstuvwxyz")
		if got := findingIDs(stripeFindings); len(got) != 1 || got[0] != "stripe-key" {
			t.Fatalf("stripe findings = %v, want [stripe-key]", got)
		}

		openAIFindings := detector.Scan("sk-abcdefghijklmnopqrstuvwxyz1234")
		if got := findingIDs(openAIFindings); len(got) != 1 || got[0] != "openai-api-key" {
			t.Fatalf("openai findings = %v, want [openai-api-key]", got)
		}
	})

	t.Run("prefers longer overlapping match", func(t *testing.T) {
		t.Parallel()

		custom := &Detector{rules: []Rule{
			{ID: "short", Pattern: regexp.MustCompile(`sk-proj-[a-z]{6}`)},
			{ID: "long", Pattern: regexp.MustCompile(`sk-proj-[a-z]{26}`)},
		}}

		findings := custom.Scan("sk-proj-abcdefghijklmnopqrstuvwxyz")
		if len(findings) != 1 {
			t.Fatalf("len(findings) = %d, want 1", len(findings))
		}
		if findings[0].RuleID != "long" {
			t.Fatalf("RuleID = %q, want long", findings[0].RuleID)
		}
	})

	t.Run("openai project key stays distinct", func(t *testing.T) {
		t.Parallel()

		findings := detector.Scan("sk-proj-abcdefghijklmnopqrstuvwxyz")
		if len(findings) != 1 {
			t.Fatalf("len(findings) = %d, want 1", len(findings))
		}
		if findings[0].RuleID != "openai-project-key" {
			t.Fatalf("RuleID = %q, want openai-project-key", findings[0].RuleID)
		}
	})

	t.Run("preserves surrounding content after redaction", func(t *testing.T) {
		t.Parallel()

		content := "before AKIAIOSFODNN7EXAMPLE after"
		findings := detector.Scan(content)
		if got := detector.Redact(content, findings); got != "before [REDACTED:aws-access-key] after" {
			t.Fatalf("Redact() = %q", got)
		}
	})

	t.Run("multiline env secrets", func(t *testing.T) {
		t.Parallel()

		content := strings.Join([]string{
			"PORT=8080",
			"API_SECRET_KEY=abcdef1234567890",
			"SESSION_TOKEN=1234567890abcdef",
		}, "\n")
		findings := detector.Scan(content)
		got := findingIDs(findings)
		want := []string{"env-file-secret", "env-file-secret"}
		if len(got) != len(want) {
			t.Fatalf("len(findings) = %d, want %d; ids=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("finding[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("case-insensitive env keys", func(t *testing.T) {
		t.Parallel()

		content := strings.Join([]string{
			"api_secret_key=abcdef1234567890",
			"Aws_Token=1234567890abcdef",
		}, "\n")
		findings := detector.Scan(content)
		got := findingIDs(findings)
		want := []string{"env-file-secret", "env-file-secret"}
		if len(got) != len(want) {
			t.Fatalf("len(findings) = %d, want %d; ids=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("finding[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("keyword-only env keys", func(t *testing.T) {
		t.Parallel()

		content := strings.Join([]string{
			"TOKEN=abcdef1234567890",
			"SECRET=1234567890abcdef",
			"PASSWORD=abcdefgh12345678",
		}, "\n")
		findings := detector.Scan(content)
		got := findingIDs(findings)
		want := []string{"env-file-secret", "env-file-secret", "env-file-secret"}
		if len(got) != len(want) {
			t.Fatalf("len(findings) = %d, want %d; ids=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("finding[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("uppercase compact and quoted env keys", func(t *testing.T) {
		t.Parallel()

		content := strings.Join([]string{
			"DBPASSWORD=abcdefgh12345678",
			"APISECRET=1234567890abcdef",
			`API_SECRET="abcdef1234567890"`,
		}, "\n")
		findings := detector.Scan(content)
		got := findingIDs(findings)
		want := []string{"env-file-secret", "env-file-secret", "env-file-secret"}
		if len(got) != len(want) {
			t.Fatalf("len(findings) = %d, want %d; ids=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("finding[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("large safe content", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping large content scan in short mode")
		}

		content := strings.Repeat("safe text without secrets\n", 4096)
		if findings := detector.Scan(content); findings != nil {
			t.Fatalf("Scan() = %v, want nil", findings)
		}
	})
}

func findingIDs(findings []Finding) []string {
	ids := make([]string, len(findings))
	for i, finding := range findings {
		ids[i] = finding.RuleID
	}
	return ids
}
