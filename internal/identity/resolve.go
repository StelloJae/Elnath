package identity

import (
	"strings"

	"github.com/stello/elnath/internal/config"
)

func ResolveProjectID(cwd, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	if remote, ok := fromGitRemote(cwd); ok {
		return hashValue(remote)
	}
	return hashValue(cleanPath(cwd))
}

func ResolveCLIPrincipal(cfg *config.Config, flagValue, cwd string) Principal {
	userID := fromCLIFlag(flagValue)
	if userID == "" {
		userID = fromConfig(cfg)
	}
	if userID == "" {
		userID = fromEnv()
	}
	if userID == "" {
		userID = LegacyPrincipal().UserID
	}
	return NewPrincipal(PrincipalSource{
		UserID:          userID,
		CanonicalUserID: userID,
		ProjectID:       ResolveProjectID(cwd, ""),
		Surface:         "cli",
	})
}

func ResolveTelegramPrincipal(fromID int64, cwd string) Principal {
	userID := fromTelegram(fromID)
	if userID == "" {
		userID = LegacyPrincipal().UserID
	}
	canonicalUserID := fromEnv()
	if canonicalUserID == "" {
		canonicalUserID = userID
	}
	return NewPrincipal(PrincipalSource{
		UserID:          userID,
		CanonicalUserID: canonicalUserID,
		ProjectID:       ResolveProjectID(cwd, ""),
		Surface:         "telegram",
	})
}
