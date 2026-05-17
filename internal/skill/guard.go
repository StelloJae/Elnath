package skill

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type SkillRiskScan struct {
	SkillName  string             `json:"skill_name"`
	Verdict    string             `json:"verdict"`
	TrustLevel string             `json:"trust_level,omitempty"`
	External   bool               `json:"external"`
	Findings   []SkillRiskFinding `json:"findings"`
}

type SkillRiskFinding struct {
	PatternID   string `json:"pattern_id"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Line        int    `json:"line"`
	Description string `json:"description"`
}

type skillRiskPattern struct {
	id          string
	severity    string
	category    string
	description string
	re          *regexp.Regexp
}

var skillRiskPatterns = []skillRiskPattern{
	{
		id:          "prompt_injection_ignore",
		severity:    "critical",
		category:    "injection",
		description: "attempts to override previous instructions",
		re:          regexp.MustCompile(`(?i)\b(ignore|disregard)\s+(previous|all|above|prior)\s+instructions\b`),
	},
	{
		id:          "secret_exfil_curl",
		severity:    "critical",
		category:    "exfiltration",
		description: "network command appears to interpolate a secret variable",
		re:          regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n$]*(\$\{?[A-Za-z0-9_]*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API))`),
	},
	{
		id:          "read_secret_file",
		severity:    "high",
		category:    "exfiltration",
		description: "references known credential or secret file",
		re:          regexp.MustCompile(`(?i)(\.env|\.netrc|\.npmrc|credentials|id_rsa|authorized_keys)`),
	},
	{
		id:          "destructive_remove",
		severity:    "critical",
		category:    "destructive",
		description: "contains broad destructive remove command",
		re:          regexp.MustCompile(`(?i)\brm\s+-rf\s+(/|\$HOME|~)`),
	},
	{
		id:          "hidden_instruction",
		severity:    "high",
		category:    "injection",
		description: "contains hidden or deceptive instruction marker",
		re:          regexp.MustCompile(`(?i)(<!--[^>]*(ignore|override|secret|hidden)[^>]*-->|do\s+not\s+tell\s+the\s+user)`),
	},
}

func ScanSkillRisk(sk *Skill) SkillRiskScan {
	scan := SkillRiskScan{
		Verdict: "safe",
	}
	if sk == nil {
		return scan
	}
	scan.SkillName = sk.Name
	scan.TrustLevel = sk.TrustLevel()
	scan.External = sk.External()
	text := strings.Join([]string{
		sk.Description,
		sk.Trigger,
		strings.Join(sk.RequiredTools, "\n"),
		sk.Prompt,
	}, "\n")
	scan.Findings = scanSkillRiskText(text)
	scan.Verdict = skillRiskVerdict(scan.Findings)
	return scan
}

func scanSkillRiskText(text string) []SkillRiskFinding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	var findings []SkillRiskFinding
	seen := make(map[string]struct{})
	for idx, line := range lines {
		for _, pattern := range skillRiskPatterns {
			if !pattern.re.MatchString(line) {
				continue
			}
			key := pattern.id + ":" + strconv.Itoa(idx+1)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			findings = append(findings, SkillRiskFinding{
				PatternID:   pattern.id,
				Severity:    pattern.severity,
				Category:    pattern.category,
				Line:        idx + 1,
				Description: pattern.description,
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].PatternID < findings[j].PatternID
	})
	return findings
}

func skillRiskVerdict(findings []SkillRiskFinding) string {
	verdict := "safe"
	for _, finding := range findings {
		switch finding.Severity {
		case "critical", "high":
			return "dangerous"
		case "medium", "low":
			verdict = "caution"
		}
	}
	return verdict
}
