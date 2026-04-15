package portability

import "time"

const (
	BundleVersion = 1
	ManifestName  = "bundle.json"
	MagicHeader   = "ELNBUNDLE"
)

type Manifest struct {
	Version    int            `json:"version"`
	CreatedAt  time.Time      `json:"created_at"`
	SourceHost string         `json:"source_host"`
	ElnathVer  string         `json:"elnath_version"`
	HasSecrets bool           `json:"has_secrets"`
	Files      []ManifestFile `json:"files"`
	Scope      BundleScope    `json:"scope"`
}

type ManifestFile struct {
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type BundleScope struct {
	Config   bool `json:"config"`
	DB       bool `json:"db"`
	Wiki     bool `json:"wiki"`
	Lessons  bool `json:"lessons"`
	Sessions bool `json:"sessions"`
}

type historyRecord struct {
	Timestamp time.Time   `json:"timestamp"`
	OutPath   string      `json:"out_path"`
	ByteSize  int64       `json:"byte_size"`
	Scope     BundleScope `json:"scope"`
	Manifest  Manifest    `json:"manifest"`
}

func normalizeScope(scope BundleScope) BundleScope {
	if scope.Config || scope.DB || scope.Wiki || scope.Lessons || scope.Sessions {
		return scope
	}
	return BundleScope{Config: true, DB: true, Wiki: true, Lessons: true, Sessions: true}
}

func validateManifestVersion(version int, force bool) error {
	if version == BundleVersion || force {
		return nil
	}
	return ErrVersion
}
