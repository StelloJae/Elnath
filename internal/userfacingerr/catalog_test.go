package userfacingerr

import "testing"

func TestLookupAndAll(t *testing.T) {
	entry, ok := Lookup(ELN001)
	if !ok {
		t.Fatal("Lookup(ELN001) did not find entry")
	}
	if entry.Code == "" || entry.Title == "" || entry.What == "" || entry.Why == "" || entry.HowToFix == "" {
		t.Fatalf("Lookup(ELN001) returned incomplete entry: %#v", entry)
	}

	if _, ok := Lookup(Code("ELN-999")); ok {
		t.Fatal("Lookup(ELN-999) unexpectedly found entry")
	}

	all := All()
	if len(all) != 14 {
		t.Fatalf("len(All()) = %d, want 14", len(all))
	}
	for _, item := range all {
		if item.Code == "" || item.Title == "" || item.What == "" || item.Why == "" || item.HowToFix == "" {
			t.Fatalf("All() returned incomplete entry: %#v", item)
		}
	}
}
