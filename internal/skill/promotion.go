package skill

import "fmt"

func FormatPromotionMessage(sk *Skill, prevalence int, totalSessions int) string {
	name := ""
	if sk != nil {
		name = sk.Name
	}
	return fmt.Sprintf(
		"New skill /%s activated (%d sessions, %d independent patterns). Use /skill-list to review.",
		name,
		totalSessions,
		prevalence,
	)
}
