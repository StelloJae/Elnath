package secret

import (
	"fmt"
	"sort"
)

type Finding struct {
	RuleID string
	Match  string
	Start  int
	End    int
}

type Detector struct {
	rules []Rule
}

func NewDetector() *Detector {
	return &Detector{rules: defaultRules}
}

func (d *Detector) Scan(content string) []Finding {
	if d == nil || content == "" {
		return nil
	}

	findings := make([]Finding, 0)
	for _, rule := range d.rules {
		for _, loc := range rule.Pattern.FindAllStringIndex(content, -1) {
			findings = append(findings, Finding{
				RuleID: rule.ID,
				Match:  content[loc[0]:loc[1]],
				Start:  loc[0],
				End:    loc[1],
			})
		}
	}
	if len(findings) == 0 {
		return nil
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Start != findings[j].Start {
			return findings[i].Start < findings[j].Start
		}
		lenI := findings[i].End - findings[i].Start
		lenJ := findings[j].End - findings[j].Start
		if lenI != lenJ {
			return lenI > lenJ
		}
		if findings[i].End != findings[j].End {
			return findings[i].End < findings[j].End
		}
		return findings[i].RuleID < findings[j].RuleID
	})

	deduped := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		if len(deduped) == 0 {
			deduped = append(deduped, finding)
			continue
		}

		last := deduped[len(deduped)-1]
		if finding.Start >= last.End {
			deduped = append(deduped, finding)
			continue
		}

		if finding.Start == last.Start && finding.End == last.End {
			continue
		}

		if finding.End <= last.End {
			continue
		}
	}

	return deduped
}

func (d *Detector) Redact(content string, findings []Finding) string {
	if d == nil || len(findings) == 0 {
		return content
	}

	ordered := append([]Finding(nil), findings...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Start > ordered[j].Start
	})

	result := content
	for _, finding := range ordered {
		replacement := fmt.Sprintf("[REDACTED:%s]", finding.RuleID)
		result = result[:finding.Start] + replacement + result[finding.End:]
	}
	return result
}

func (d *Detector) ScanAndRedact(content string) (string, []Finding) {
	findings := d.Scan(content)
	return d.Redact(content, findings), findings
}

// RedactString returns content with every detected secret replaced by
// [REDACTED:rule-id]. Empty or secret-free input is returned unchanged.
// Nil-safe.
func (d *Detector) RedactString(content string) string {
	if d == nil {
		return content
	}
	redacted, _ := d.ScanAndRedact(content)
	return redacted
}
