package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyChecksums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("new binary")
	sum := sha256.Sum256(bin)
	asset := AssetName("windows", "amd64")
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)))

	got, err := VerifyChecksums(pub, sums, sig, asset)
	if err != nil || hex.EncodeToString(got) != hex.EncodeToString(sum[:]) {
		t.Fatalf("valid signature rejected: %v", err)
	}
	tampered := append([]byte{}, sums...)
	tampered[0] ^= 1
	if _, err := VerifyChecksums(pub, tampered, sig, asset); err == nil {
		t.Fatal("tampered checksums accepted")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyChecksums(other, sums, sig, asset); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := VerifyChecksums(pub, sums, sig, "missing.exe"); err == nil {
		t.Fatal("missing asset accepted")
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.github.com/repos/x/y/releases": true,
		"https://objects.githubusercontent.com/a":   true,
		"http://github.com/x":                       false,
		"https://evil.example.com/x":                false,
		"https://github.com.evil.example/x":         false,
	} {
		u, _ := url.Parse(raw)
		if (checkURL(u) == nil) != ok {
			t.Errorf("%s: want ok=%v", raw, ok)
		}
	}
}

func TestDevNeverUpdates(t *testing.T) {
	u := New("x/y", "dev")
	r, err := u.Check(t.Context(), false)
	if r != nil || err != nil {
		t.Fatal("dev build should not check")
	}
}

// End-to-end: fake GitHub over TLS, real signature checks, real file swap.
func TestCheckAndInstall(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	newBin := []byte("#!new build v1.2.0\n")
	sum := sha256.Sum256(newBin)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)))

	var srv *httptest.Server
	tamper := false
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			fmt.Fprintf(w, `[{"tag_name":"v1.2.0","html_url":"https://github.com/o/r/releases/v1.2.0","assets":[
				{"name":%q,"browser_download_url":"%s/a"},
				{"name":"checksums.txt","browser_download_url":"%s/s"},
				{"name":"checksums.txt.sig","browser_download_url":"%s/g"}]},
				{"tag_name":"v9.0.0-beta.1","prerelease":true,"assets":[]}]`, asset, srv.URL, srv.URL, srv.URL)
		case "/a":
			if tamper {
				w.Write([]byte("evil"))
				return
			}
			w.Write(newBin)
		case "/s":
			w.Write(sums)
		case "/g":
			w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	host, _, _ := net.SplitHostPort(srv.Listener.Addr().String())
	allowedHosts[host] = true
	defer delete(allowedHosts, host)

	target := filepath.Join(t.TempDir(), "app.exe")
	os.WriteFile(target, []byte("old build"), 0o755)

	u := New("o/r", "v1.1.0")
	u.Client = srv.Client()
	u.APIBase = srv.URL
	u.Target = target
	u.pubKey = pub

	rel, err := u.Check(t.Context(), false)
	if err != nil || rel == nil || rel.Version != "v1.2.0" {
		t.Fatalf("check: %+v %v", rel, err)
	}
	if runtime.GOOS == "darwin" {
		return
	}
	if !rel.CanInstall {
		t.Fatalf("cannot install: %s", rel.Reason)
	}
	tamper = true
	if err := u.Install(t.Context(), rel); err == nil {
		t.Fatal("tampered binary installed")
	}
	if b, _ := os.ReadFile(target); string(b) != "old build" {
		t.Fatal("target modified after failed update")
	}
	tamper = false
	if err := u.Install(t.Context(), rel); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != string(newBin) {
		t.Fatal("target not replaced")
	}
	u.Current = "v1.2.0"
	if rel, _ := u.Check(t.Context(), false); rel != nil {
		t.Fatal("should be up to date")
	}
}
