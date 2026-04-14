package secret

import "regexp"

type Rule struct {
	ID      string
	Name    string
	Pattern *regexp.Regexp
}

var defaultRules = func() []Rule {
	type raw struct {
		id      string
		name    string
		pattern string
	}

	defs := []raw{
		{id: "anthropic-api-key", name: "Anthropic API Key", pattern: `sk-ant-api\d{2}-[\w-]{80,}`},
		{id: "openai-api-key", name: "OpenAI API Key", pattern: `sk-[a-zA-Z0-9]{20,}`},
		{id: "openai-project-key", name: "OpenAI Project Key", pattern: `sk-proj-[a-zA-Z0-9]{20,}`},
		{id: "aws-access-key", name: "AWS Access Key", pattern: `AKIA[0-9A-Z]{16}`},
		{id: "aws-secret-key", name: "AWS Secret Key", pattern: `(?i)aws_secret_access_key\s*[=:]\s*[A-Za-z0-9/+=]{40}`},
		{id: "gcp-api-key", name: "GCP API Key", pattern: `AIza[0-9A-Za-z\-_]{35}`},
		{id: "gcp-service-account", name: "GCP Service Account", pattern: `"type"\s*:\s*"service_account"`},
		{id: "github-token", name: "GitHub Token", pattern: `gh[pousr]_[A-Za-z0-9_]{36,}`},
		{id: "github-fine-grained", name: "GitHub Fine-Grained Token", pattern: `github_pat_[A-Za-z0-9_]{22,}`},
		{id: "gitlab-token", name: "GitLab Token", pattern: `glpat-[A-Za-z0-9\-]{20,}`},
		{id: "slack-token", name: "Slack Token", pattern: `xox[baprs]-[A-Za-z0-9\-]{10,}`},
		{id: "slack-webhook", name: "Slack Webhook", pattern: `hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]+`},
		{id: "stripe-key", name: "Stripe Key", pattern: `[sr]k_(live|test)_[A-Za-z0-9]{20,}`},
		{id: "telegram-bot-token", name: "Telegram Bot Token", pattern: `\d{8,10}:[A-Za-z0-9_-]{35}`},
		{id: "jwt-token", name: "JWT Token", pattern: `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`},
		{id: "private-key-pem", name: "PEM Private Key", pattern: `-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`},
		{id: "generic-password", name: "Generic Password Assignment", pattern: `(?i)(?:password|passwd|pwd)\s*[=:]\s*["'][^"']{8,}["']`},
		{id: "generic-secret", name: "Generic Secret Assignment", pattern: `(?i)(?:secret|token|api_key|apikey)\s*[=:]\s*["'][^"']{8,}["']`},
		{id: "connection-string", name: "Database Connection String", pattern: `(?i)(?:postgres|mysql|mongodb|redis)://[^\s]+:[^\s]+@`},
		{id: "env-file-secret", name: ".env File Secret", pattern: `(?m)^(?:[A-Z_]*(KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL)[A-Z_]*\s*=\s*\S{8,}|(?i:(?:[A-Z0-9]+_[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL)[A-Z0-9_]*|(?:KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL)[A-Z0-9_]*_[A-Z0-9_]+)\s*=\s*[^\s"']{8,}))`},
	}

	rules := make([]Rule, len(defs))
	for i, def := range defs {
		rules[i] = Rule{
			ID:      def.id,
			Name:    def.name,
			Pattern: regexp.MustCompile(def.pattern),
		}
	}
	return rules
}()
