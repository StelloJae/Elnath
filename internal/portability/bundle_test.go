package portability

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestManifestJSONRoundTrip(t *testing.T) {
	want := Manifest{
		Version:    BundleVersion,
		CreatedAt:  time.Unix(1713110400, 0).UTC(),
		SourceHost: "mbp",
		ElnathVer:  "dev",
		HasSecrets: true,
		Scope:      BundleScope{Config: true, DB: true, Wiki: true, Lessons: true, Sessions: true},
		Files: []ManifestFile{{
			RelPath: "config.yaml",
			Size:    42,
			SHA256:  "abc123",
		}},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Version != want.Version || got.SourceHost != want.SourceHost || got.ElnathVer != want.ElnathVer || !got.HasSecrets {
		t.Fatalf("manifest mismatch: %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0].RelPath != want.Files[0].RelPath {
		t.Fatalf("files = %+v, want %+v", got.Files, want.Files)
	}
}

func TestManifestVersionMismatch(t *testing.T) {
	err := validateManifestVersion(BundleVersion+1, false)
	if !errors.Is(err, ErrVersion) {
		t.Fatalf("validateManifestVersion error = %v, want ErrVersion", err)
	}

	if err := validateManifestVersion(BundleVersion+1, true); err != nil {
		t.Fatalf("validateManifestVersion(force) = %v, want nil", err)
	}
}
