package security

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"
)

type License struct {
	TenantID  string `json:"tenant_id"`
	MaxUsers  int    `json:"max_users"`
	ExpiresAt string `json:"expires_at"`
}

// 内置公钥 — 只能验签，不能生成授权
var licensePublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAyJFAzOhasJijiFXsKGg6
3dsju3p/tQbepqE6XAWU2veDXR1Q5oQw14ZfLPdVo2riAsu7r4+qQ8j1gzn2t1CE
OSqR+dBh0d6SIGykV2+/GcS/uDiW4RcAvmieyrU0tcwmsJls2zTL4tcMHwCpCsE3
Nt9i6d32ToECYyvhhf6k3/8H3tBFsuVnmy345v4w5xuLexZmXoZQSnKLtsPPwey1
qMbG6ZEafuNhjSuNQdA3uWvidjPGKyjjKZKe1A5j+DuKP/pTE9fBlFbolkYDPE1x
EDh9DzFxpcEYfmsVV4iI1pv4MKud1ItUaZnNv1/eiPPNRdeWhP25DoDrTo7cNvId
/wIDAQAB
-----END PUBLIC KEY-----`

var licensePubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(licensePublicKeyPEM))
	if block != nil {
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err == nil {
			licensePubKey, _ = key.(*rsa.PublicKey)
		}
	}
	if licensePubKey == nil {
		println("[WARN] license public key not loaded — all tenants run as free mode")
	}
}

// LicensePublicKey returns the embedded verification key, or nil if parsing failed.
func LicensePublicKey() *rsa.PublicKey {
	return licensePubKey
}

func VerifyRSASignature(data, sigB64 string) bool {
	return verifyRSASignatureWithKey(data, sigB64, licensePubKey)
}

func verifyRSASignatureWithKey(data, sigB64 string, publicKey *rsa.PublicKey) bool {
	if publicKey == nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	h := sha256.Sum256([]byte(data))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, h[:], sig) == nil
}

func CheckLicense(tenantID, dir string) *License {
	return checkLicenseWithPublicKey(tenantID, dir, licensePubKey)
}

func checkLicenseWithPublicKey(tenantID, dir string, publicKey *rsa.PublicKey) *License {
	for _, ext := range []string{".lic", ".json"} {
		path := fmt.Sprintf("%s/license_%s%s", dir, tenantID, ext)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		// Normalize line endings (Windows \r\n → \n)
		content = strings.ReplaceAll(content, "\r\n", "\n")
		if ext == ".lic" {
			parts := strings.SplitN(strings.TrimSpace(content), "\n", 2)
			if len(parts) < 2 {
				continue
			}
			var lic License
			if json.Unmarshal([]byte(parts[0]), &lic) != nil {
				continue
			}
			if !verifyRSASignatureWithKey(parts[0], strings.TrimSpace(parts[1]), publicKey) {
				continue
			}
			if t, err := time.Parse("2006-01-02", lic.ExpiresAt); err == nil && time.Now().After(t) {
				continue
			}
			return &lic
		}
	}
	return nil
}

func GenerateLicenseKeyPair() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pubDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return priv, pub, nil
}
