package machost

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ReleasePubKeyPEM is the public half of the release signing key: the
// key install.sh and the CLI carry (DECISIONS I-430), which signs every
// hostd manifest (I-1496). test/supplychain holds the copies together.
const ReleasePubKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq1BgpU75F46qQ3iMHL1E0VOqye+4
3/u1m873IxGrYRM2AZyYkK6QqN92G2qBuMvUi+qb4OYeF7KRF1MQ5oiIdw==
-----END PUBLIC KEY-----`

// ManifestFile is the Mac's manifest in a hostd-<version> release,
// beside the Linux hosts' manifest.json, which stays as I-1496 made it.
const ManifestFile = "manifest-darwin.json"

// Manifest is manifest-darwin.json: one bundle for arm64 macOS, signed by
// the release key over SignedText, which covers exactly the fields a Mac
// acts on, so it needs no JSON canonical form (I-1496's rule).
type Manifest struct {
	Version string `json:"version"`
	Darwin  struct {
		ARM64 Asset `json:"arm64"`
	} `json:"darwin"`
	Signature string `json:"signature"`
}

// Asset is one file and its lower-case hex SHA-256.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

var (
	hexSHA    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// SignedText is what the signature covers.
func (m Manifest) SignedText() string {
	return fmt.Sprintf("repose-hostd-manifest-darwin v1\nversion %s\narm64 %s %s\n", m.Version, m.Darwin.ARM64.SHA256, m.Darwin.ARM64.URL)
}

// ParseManifest reads a manifest and checks its signature against pubPEM.
// Nothing in a manifest that fails is used.
func ParseManifest(b []byte, pubPEM string) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	if !versionRe.MatchString(m.Version) {
		return Manifest{}, fmt.Errorf("manifest: version %q", m.Version)
	}
	a := m.Darwin.ARM64
	if !hexSHA.MatchString(a.SHA256) || !strings.HasPrefix(a.URL, "https://") && !strings.HasPrefix(a.URL, "http://") {
		return Manifest{}, errors.New("manifest: no arm64 bundle")
	}
	if strings.ContainsAny(a.URL, " \n") {
		return Manifest{}, errors.New("manifest: url")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return Manifest{}, errors.New("manifest: signature encoding")
	}
	pub, err := parsePub(pubPEM)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256([]byte(m.SignedText()))
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return Manifest{}, errors.New("manifest: the signature does not verify against the release key")
	}
	return m, nil
}

func parsePub(s string) (*ecdsa.PublicKey, error) {
	blk, _ := pem.Decode([]byte(s))
	if blk == nil {
		return nil, errors.New("release key: no PEM block")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("release key: %w", err)
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("release key: not ECDSA")
	}
	return pub, nil
}

// CompareVersions orders dotted numeric versions as `sort -V` does for
// them (2026.11.23 after 2026.11.22.10): -1, 0 or 1. A missing part is 0.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y uint64
		if i < len(pa) {
			x, _ = strconv.ParseUint(pa[i], 10, 64)
		}
		if i < len(pb) {
			y, _ = strconv.ParseUint(pb[i], 10, 64)
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

// ValidVersion is a version a directory may be named after.
func ValidVersion(v string) bool { return versionRe.MatchString(v) && len(v) <= 64 }
