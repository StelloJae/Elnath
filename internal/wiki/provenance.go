package wiki

const (
	SourceUser            = "user"
	SourceSelfImprovement = "self-improvement"
	SourceMagicDocs       = "magic-docs"
	SourcePromoted        = "promoted"
	SourceHint            = "hint"
	SourceResearch        = "research"
	SourceAgent           = "agent"
	SourceIngest          = "ingest"
	SourceConsolidation   = "consolidation"
)

func (p *Page) PageSource() string {
	if p == nil || p.Extra == nil {
		return ""
	}
	s, _ := p.Extra["source"].(string)
	return s
}

func (p *Page) PageSourceSession() string {
	if p == nil || p.Extra == nil {
		return ""
	}
	s, _ := p.Extra["source_session"].(string)
	return s
}

func (p *Page) PageSourceEvent() string {
	if p == nil || p.Extra == nil {
		return ""
	}
	s, _ := p.Extra["source_event"].(string)
	return s
}

func (p *Page) SetSource(source, sessionID, event string) {
	if p.Extra == nil {
		p.Extra = make(map[string]any)
	}
	p.Extra["source"] = source
	if sessionID != "" {
		p.Extra["source_session"] = sessionID
	}
	if event != "" {
		p.Extra["source_event"] = event
	}
}

func (p *Page) IsOwnedBy(source string) bool {
	return p.PageSource() == source
}
