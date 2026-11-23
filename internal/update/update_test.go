package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.3.1", "1.3.0", true},
		{"1.10.0", "1.9.9", true},
		{"1.3", "1.3.0", false},
		{"1.3.0", "1.3.0", false},
		{"v2.0.0", "1.99.0", true},
		{"1.2.9", "1.3.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	enc, err := Encrypt(key, []byte(`{"nodes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(key, enc)
	if err != nil || string(plain) != `{"nodes":[]}` {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
	enc[len(enc)-1] ^= 1
	if _, err := Decrypt(key, enc); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

type site struct {
	files map[string][]byte
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, ok := s.files[filepath.Base(r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}

func publish(t *testing.T, priv ed25519.PrivateKey, exe []byte, version string) *site {
	t.Helper()
	sum := sha256.Sum256(exe)
	m := Manifest{Version: version, File: "Chuanyun-" + version + ".exe", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(exe))}
	raw, _ := json.Marshal(m)
	return &site{files: map[string][]byte{
		ManifestName:             raw,
		ManifestName + SigSuffix: Sign(priv, raw),
		m.File:                   exe,
	}}
}

func TestManifestAndDownload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	exe := []byte("MZ new build")
	s := publish(t, priv, exe, "1.4.0")
	srv := httptest.NewServer(s)
	defer srv.Close()

	c := Client{Base: srv.URL + "/r/", Key: pub}
	m, err := c.Manifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.4.0" {
		t.Fatalf("m=%+v", m)
	}
	dst := filepath.Join(t.TempDir(), "new.exe")
	if err := c.Download(context.Background(), m, dst); err != nil {
		t.Fatal(err)
	}
	if !FileMatches(dst, m) {
		t.Fatal("downloaded file must match manifest")
	}

	s.files[m.File] = []byte("MZ evil build")
	if err := c.Download(context.Background(), m, dst+"2"); err == nil {
		t.Fatal("swapped exe must be rejected")
	}
	if _, err := os.Stat(dst + "2"); !os.IsNotExist(err) {
		t.Fatal("rejected download must not stay on disk")
	}
}

func TestManifestRejectsWrongSigner(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer(publish(t, other, []byte("MZ"), "9.9.9"))
	defer srv.Close()
	if _, err := (Client{Base: srv.URL + "/", Key: pub}).Manifest(context.Background()); err == nil {
		t.Fatal("manifest signed by another key must be rejected")
	}
}

func TestNodesSignedAndEncrypted(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	enc, _ := Encrypt(key, []byte(`{"server":"198.18.0.1"}`))
	s := &site{files: map[string][]byte{NodesName: enc, NodesName + SigSuffix: Sign(priv, enc)}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	got, err := (Client{Base: srv.URL + "/", Key: pub}).Nodes(context.Background(), key)
	if err != nil || string(got) != `{"server":"198.18.0.1"}` {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestReplaceAndRollback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Chuanyun.exe")
	newExe := filepath.Join(dir, "new.exe")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	_ = os.WriteFile(newExe, []byte("new"), 0o755)
	rollback, err := Replace(exe, newExe)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe=%q", b)
	}
	rollback()
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("after rollback exe=%q", b)
	}
}

func TestNormalizeBase(t *testing.T) {
	if b, err := NormalizeBase("http://198.18.0.1:8088/abc"); err != nil || b != "http://198.18.0.1:8088/abc/" {
		t.Fatalf("b=%q err=%v", b, err)
	}
	if _, err := NormalizeBase("ftp://x/"); err == nil {
		t.Fatal("ftp must be rejected")
	}
}
