package security

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckLicenseWithEphemeralKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	license := License{
		TenantID:  "default",
		MaxUsers:  100,
		ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
	}
	payload, err := json.Marshal(license)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	content := append(append(payload, '\n'), []byte(base64.StdEncoding.EncodeToString(signature))...)
	if err := os.WriteFile(filepath.Join(directory, "license_default.lic"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	got := checkLicenseWithPublicKey("default", directory, &privateKey.PublicKey)
	if got == nil {
		t.Fatal("ephemerally signed license was rejected")
	}
	if got.TenantID != license.TenantID || got.MaxUsers != license.MaxUsers || got.ExpiresAt != license.ExpiresAt {
		t.Fatalf("license = %#v, want %#v", got, license)
	}
}

func TestCheckLicenseRejectsMissingTenant(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkLicenseWithPublicKey("missing", t.TempDir(), &privateKey.PublicKey); got != nil {
		t.Fatalf("missing license = %#v, want nil", got)
	}
}
