package wiki

import "testing"

func TestPageSourceNilPage(t *testing.T) {
	var p *Page
	if got := p.PageSource(); got != "" {
		t.Fatalf("want \"\", got %q", got)
	}
}

func TestPageSourceNilExtra(t *testing.T) {
	p := &Page{}
	if got := p.PageSource(); got != "" {
		t.Fatalf("want \"\", got %q", got)
	}
}

func TestPageSourceSet(t *testing.T) {
	p := &Page{Extra: map[string]any{"source": "magic-docs"}}
	if got := p.PageSource(); got != "magic-docs" {
		t.Fatalf("want %q, got %q", "magic-docs", got)
	}
}

func TestSetSourceInitializesExtra(t *testing.T) {
	p := &Page{}
	p.SetSource(SourceMagicDocs, "sess1", "evt1")
	if p.Extra == nil {
		t.Fatal("Extra must not be nil after SetSource")
	}
	if got := p.PageSource(); got != SourceMagicDocs {
		t.Fatalf("want %q, got %q", SourceMagicDocs, got)
	}
	if got := p.PageSourceSession(); got != "sess1" {
		t.Fatalf("want %q, got %q", "sess1", got)
	}
	if got := p.PageSourceEvent(); got != "evt1" {
		t.Fatalf("want %q, got %q", "evt1", got)
	}
}

func TestIsOwnedBy(t *testing.T) {
	p := &Page{Extra: map[string]any{"source": SourceUser}}
	if !p.IsOwnedBy(SourceUser) {
		t.Fatal("expected IsOwnedBy(SourceUser) to be true")
	}
	if p.IsOwnedBy(SourceMagicDocs) {
		t.Fatal("expected IsOwnedBy(SourceMagicDocs) to be false")
	}
}

func TestPageSourceSession(t *testing.T) {
	p := &Page{}
	if got := p.PageSourceSession(); got != "" {
		t.Fatalf("want \"\", got %q", got)
	}
	p.SetSource(SourceUser, "s123", "")
	if got := p.PageSourceSession(); got != "s123" {
		t.Fatalf("want %q, got %q", "s123", got)
	}
}

func TestPageSourceEvent(t *testing.T) {
	p := &Page{}
	if got := p.PageSourceEvent(); got != "" {
		t.Fatalf("want \"\", got %q", got)
	}
	p.SetSource(SourceUser, "", "boot")
	if got := p.PageSourceEvent(); got != "boot" {
		t.Fatalf("want %q, got %q", "boot", got)
	}
}
