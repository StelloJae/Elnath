package userfacingerr

// CatalogEntry describes an error code for CLI lookup.
type CatalogEntry struct {
	Code     Code
	Title    string
	What     string
	Why      string
	HowToFix string
}

var catalog = []CatalogEntry{
	{ELN001, "Provider not configured", "Elnath could not find an LLM provider (Anthropic API key or Codex OAuth).", "No API key is set in config.yaml and no Codex OAuth token was found.", "Run 'elnath setup --quickstart' or set ELNATH_ANTHROPIC_API_KEY."},
	{ELN002, "OAuth token expired", "The Codex OAuth access token has expired and automatic refresh failed.", "The refresh token may have been revoked or the network is unavailable.", "Re-authenticate with 'codex auth' and retry."},
	{ELN010, "Wiki not initialized", "The wiki directory does not exist or has not been initialised.", "wiki_dir in config.yaml points to a non-existent path, or setup was skipped.", "Run 'elnath setup' and confirm the wiki directory, or create it manually."},
	{ELN020, "Permission denied", "Elnath's path guard blocked access to the requested file or directory.", "The target path is outside the allowed working directories.", "Check permission.allow in config.yaml or move the file inside the project root."},
	{ELN030, "Daemon socket unreachable", "The CLI could not connect to the Elnath daemon socket.", "The daemon is not running, or the socket_path in config.yaml is stale.", "Run 'elnath daemon start' or check 'elnath daemon status'."},
	{ELN040, "LLM request timeout", "The LLM provider did not respond within the configured timeout.", "High load on the provider, large prompt, or network latency.", "Retry. Increase anthropic.timeout_seconds in config.yaml if recurring."},
	{ELN050, "Tool execution failed", "A tool (bash, write, edit, etc.) returned a non-zero exit or error.", "The command failed, a file was locked, or the path was invalid.", "Check the error detail above. Re-run with a corrected command or path."},
	{ELN060, "Config invalid", "Elnath could not parse or validate config.yaml.", "A required field is missing, has an unexpected type, or the YAML is malformed.", "Run 'elnath setup' to regenerate config, or edit ~/.elnath/config.yaml manually."},
	{ELN070, "Session file corrupted", "A session JSONL file could not be parsed.", "The file was truncated (e.g. disk full) or written by an incompatible version.", "Delete the corrupted file from ~/.elnath/data/sessions/ and start a new session."},
	{ELN080, "Rate limited (429)", "The LLM provider rejected the request due to rate limiting.", "Too many requests in a short window, or quota exhausted.", "Wait a moment and retry. Check provider dashboard for quota status."},
	{ELN090, "OAuth token missing / absent", "The OAuth access token is missing or absent from the auth file.", "Authentication was not completed or the auth file was removed.", "Run 'elnath setup' to re-authenticate with Codex / Anthropic."},
	{ELN100, "Wiki page not found", "The requested wiki page does not exist.", "The path is incorrect, the page was deleted, or the wiki dir was changed.", "Run 'elnath wiki search <term>' to find the correct path."},
	{ELN110, "Daemon task timeout", "A task submitted to the daemon exceeded its execution time limit.", "The task is too large, the LLM is slow, or the daemon is overloaded.", "Increase daemon.task_timeout_seconds in config.yaml, or split the task."},
	{ELN120, "Empty LLM response", "The LLM stream completed without producing any text content.", "The model refused the prompt, or a content filter triggered.", "Retry with a rephrased prompt. Check for content policy restrictions."},
}

func Lookup(code Code) (CatalogEntry, bool) {
	for _, entry := range catalog {
		if entry.Code == code {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}

func All() []CatalogEntry {
	return append([]CatalogEntry(nil), catalog...)
}
