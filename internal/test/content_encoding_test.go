package app

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestDetectAndConvertToUTF8LeavesValidUTF8ChineseUntouched(t *testing.T) {
	wantMessage := "\u0040\u79d8\u4e66 \u8bf7\u6574\u7406\u4f1a\u8bae\u7eaa\u8981"
	src, err := json.Marshal(map[string]string{
		"session_id": "utf8_session",
		"message":    wantMessage,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	original := append([]byte(nil), src...)

	got, err := detectAndConvertToUTF8(src)
	if err != nil {
		t.Fatalf("detectAndConvertToUTF8 returned error: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("valid UTF-8 body was modified:\nwant %q\n got %q", string(original), string(got))
	}

	var req MessageRequest
	if err := json.Unmarshal(got, &req); err != nil {
		t.Fatalf("unmarshal converted request: %v", err)
	}
	if req.Message != wantMessage {
		t.Fatalf("message = %q, want %q", req.Message, wantMessage)
	}
}

func TestDetectAndConvertToUTF8DecodesGBKChinese(t *testing.T) {
	src := `{"message":"贵州燃气投资价值分析"}`
	var buf bytes.Buffer
	w := transform.NewWriter(&buf, simplifiedchinese.GBK.NewEncoder())
	if _, err := io.WriteString(w, src); err != nil {
		t.Fatalf("write gbk: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close gbk writer: %v", err)
	}

	got, err := detectAndConvertToUTF8(buf.Bytes())
	if err != nil {
		t.Fatalf("detectAndConvertToUTF8 returned error: %v", err)
	}
	if string(got) != src {
		t.Fatalf("decoded mismatch:\nwant %q\n got %q", src, string(got))
	}
	if strings.Contains(string(got), "????") {
		t.Fatalf("decoded text still contains replacement question marks: %q", string(got))
	}
}
