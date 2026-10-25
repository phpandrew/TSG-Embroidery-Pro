// Package updater checks GitHub Releases for new versions and installs
// them in place.
//
// Trust model: every release ships checksums.txt plus checksums.txt.sig, an
// Ed25519 signature made in CI with a private key held only in GitHub
// Actions secrets. The matching public key is compiled into the app
// (release-key.pub). An update is installed only when the signature
// verifies AND the downloaded binary's SHA-256 matches the signed list, so a
// compromised release page or CDN cannot push arbitrary code.
package updater

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"
)

//go:embed release-key.pub
var embeddedKey string

const (
	AssetPrefix   = "tsg-embroidery-pro"
	ChecksumsName = "checksums.txt"
	SignatureName = "checksums.txt.sig"

	maxAPIBytes   = 2 << 20
	maxSumBytes   = 64 << 10
	maxSigBytes   = 1 << 10
	maxAssetBytes = 300 << 20
)

// allowedHosts are the only hosts the updater will talk to (incl. redirects).
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// Release describes an available update.
type Release struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	PageURL     string `json:"pageUrl"`
	PublishedAt string `json:"publishedAt"`
	AssetName   string `json:"assetName"`
	CanInstall  bool   `json:"canInstall"`
	Reason      string `json:"reason,omitempty"` // why it can't self-install

	assetURL, sumsURL, sigURL string
}

// Updater talks to one GitHub repository.
type Updater struct {
	Repo    string // owner/name
	Current string // running version
	Client  *http.Client
	APIBase string // default https://api.github.com
	Target  string // file to replace; "" = running executable
	pubKey  ed25519.PublicKey
}

func New(repo, current string) *Updater {
	u := &Updater{Repo: repo, Current: current, APIBase: "https://api.github.com"}
	u.Client = &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many redirects")
			}
			return checkURL(req.URL)
		},
	}
	if k, err := ParsePublicKey(embeddedKey); err == nil {
		u.pubKey = k
	}
	return u
}

// ParsePublicKey decodes a base64 Ed25519 public key (comments with # allowed).
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	var b64 string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			b64 = line
			break
		}
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("no valid release public key")
	}
	return ed25519.PublicKey(raw), nil
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("refusing non-https URL %s", u.Redacted())
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("refusing unexpected host %s", u.Hostname())
	}
	return nil
}

// AssetName returns the release asset for this OS/arch.
func AssetName(goos, goarch string) string {
	switch goos {
	case "windows":
		return fmt.Sprintf("%s_windows_%s.exe", AssetPrefix, goarch)
	case "darwin":
		return fmt.Sprintf("%s_darwin_universal.zip", AssetPrefix)
	default:
		return fmt.Sprintf("%s_%s_%s", AssetPrefix, goos, goarch)
	}
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (u *Updater) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := checkURL(pu); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tsg-embroidery-pro-updater/"+u.Current)
	if strings.HasPrefix(rawURL, u.APIBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	} else {
		req.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", pu.Path, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response too large", pu.Path)
	}
	return data, nil
}

// Check returns the newest release newer than Current, or nil if up to date.
// Development builds ("dev") never report updates.
func (u *Updater) Check(ctx context.Context, includePrerelease bool) (*Release, error) {
	if !semver.IsValid(u.Current) {
		return nil, nil
	}
	api := fmt.Sprintf("%s/repos/%s/releases?per_page=15", u.APIBase, u.Repo)
	data, err := u.get(ctx, api, maxAPIBytes)
	if err != nil {
		return nil, err
	}
	var rels []ghRelease
	if err := json.Unmarshal(data, &rels); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}
	var best *ghRelease
	for i := range rels {
		r := &rels[i]
		if r.Draft || !semver.IsValid(r.TagName) || (r.Prerelease && !includePrerelease) {
			continue
		}
		if semver.Prerelease(r.TagName) != "" && !includePrerelease {
			continue
		}
		if best == nil || semver.Compare(r.TagName, best.TagName) > 0 {
			best = r
		}
	}
	if best == nil || semver.Compare(best.TagName, u.Current) <= 0 {
		return nil, nil
	}
	rel := &Release{
		Version: best.TagName, Name: best.Name, Notes: best.Body, PageURL: best.HTMLURL,
		PublishedAt: best.PublishedAt, AssetName: AssetName(runtime.GOOS, runtime.GOARCH),
	}
	for _, a := range best.Assets {
		switch a.Name {
		case rel.AssetName:
			rel.assetURL = a.URL
		case ChecksumsName:
			rel.sumsURL = a.URL
		case SignatureName:
			rel.sigURL = a.URL
		}
	}
	switch {
	case runtime.GOOS == "darwin":
		rel.Reason = "Download the new version from the release page."
	case u.pubKey == nil:
		rel.Reason = "This build has no release signing key; download manually."
	case rel.assetURL == "" || rel.sumsURL == "" || rel.sigURL == "":
		rel.Reason = "Release is missing files for this platform."
	default:
		rel.CanInstall = true
	}
	return rel, nil
}

// VerifyChecksums checks sig over sums and returns the SHA-256 for asset.
func VerifyChecksums(pub ed25519.PublicKey, sums, sig []byte, asset string) ([]byte, error) {
	rawSig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(rawSig) != ed25519.SignatureSize {
		return nil, errors.New("malformed signature")
	}
	if !ed25519.Verify(pub, sums, rawSig) {
		return nil, errors.New("checksum signature is NOT valid; update refused")
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			sum, err := hex.DecodeString(f[0])
			if err != nil || len(sum) != sha256.Size {
				return nil, errors.New("malformed checksum line")
			}
			return sum, nil
		}
	}
	return nil, fmt.Errorf("%s not listed in signed checksums", asset)
}

// Install downloads, verifies and swaps in the new binary. The caller should
// then call Restart.
func (u *Updater) Install(ctx context.Context, rel *Release) error {
	if rel == nil || !rel.CanInstall {
		return errors.New("this update cannot be installed automatically")
	}
	sums, err := u.get(ctx, rel.sumsURL, maxSumBytes)
	if err != nil {
		return err
	}
	sig, err := u.get(ctx, rel.sigURL, maxSigBytes)
	if err != nil {
		return err
	}
	want, err := VerifyChecksums(u.pubKey, sums, sig, rel.AssetName)
	if err != nil {
		return err
	}
	bin, err := u.get(ctx, rel.assetURL, maxAssetBytes)
	if err != nil {
		return err
	}
	got := sha256.Sum256(bin)
	if !bytes.Equal(got[:], want) {
		return errors.New("downloaded file does not match signed checksum; update refused")
	}
	if err := selfupdate.Apply(bytes.NewReader(bin), selfupdate.Options{Checksum: want, TargetPath: u.Target}); err != nil {
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			return fmt.Errorf("update failed and rollback failed: %v (original: %w)", rerr, err)
		}
		return fmt.Errorf("update failed (previous version kept): %w", err)
	}
	return nil
}

// AfterUpdateFlag tells the relaunched app the old version is still exiting.
const AfterUpdateFlag = "--after-update"

// Restart launches the (new) executable; the caller must quit afterwards.
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var args []string
	for _, a := range os.Args[1:] {
		if a != AfterUpdateFlag {
			args = append(args, a)
		}
	}
	cmd := exec.Command(exe, append(args, AfterUpdateFlag)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Start()
}
