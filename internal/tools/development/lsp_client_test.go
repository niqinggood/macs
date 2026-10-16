package development

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingWriteCloser struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *recordingWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(data)
}

func (w *recordingWriteCloser) Close() error { return nil }

func (w *recordingWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func newTransportTestLSPClient(writer io.WriteCloser) *LSPClient {
	return &LSPClient{
		stdin:       writer,
		pending:     make(map[int64]chan lspResponse),
		documents:   make(map[string]lspDocumentState),
		diagnostics: make(map[string][]LSPDiagnostic),
		diagNotify:  make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
}

func TestLSPDocumentSyncUsesFramingAndMonotonicVersions(t *testing.T) {
	writer := &recordingWriteCloser{}
	client := newTransportTestLSPClient(writer)
	path := filepath.Join(t.TempDir(), "hello world.go")

	if err := client.SyncDocument(path, "package main\n", false); err != nil {
		t.Fatal(err)
	}
	if err := client.SyncDocument(path, "package main\n\nfunc main() {}\n", true); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(bytes.NewReader(writer.Bytes()))
	var messages []map[string]interface{}
	for i := 0; i < 3; i++ {
		payload, err := readLSPFrame(reader)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		var message map[string]interface{}
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}

	if got := messages[0]["method"]; got != "textDocument/didOpen" {
		t.Fatalf("first method = %v", got)
	}
	openParams := messages[0]["params"].(map[string]interface{})
	openDocument := openParams["textDocument"].(map[string]interface{})
	if openDocument["version"] != float64(1) || openDocument["text"] != "package main\n" {
		t.Fatalf("unexpected didOpen document: %#v", openDocument)
	}

	if got := messages[1]["method"]; got != "textDocument/didChange" {
		t.Fatalf("second method = %v", got)
	}
	changeParams := messages[1]["params"].(map[string]interface{})
	changeDocument := changeParams["textDocument"].(map[string]interface{})
	if changeDocument["version"] != float64(2) {
		t.Fatalf("didChange version = %v", changeDocument["version"])
	}
	changes := changeParams["contentChanges"].([]interface{})
	if changes[0].(map[string]interface{})["text"] != "package main\n\nfunc main() {}\n" {
		t.Fatalf("didChange omitted full final content: %#v", changes)
	}
	if got := messages[2]["method"]; got != "textDocument/didSave" {
		t.Fatalf("third method = %v", got)
	}
}

func TestLSPDocumentSyncResolvesRelativePathFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	writer := &recordingWriteCloser{}
	client := newTransportTestLSPClient(writer)
	client.rootDir = root
	if err := client.SyncDocument(filepath.Join("pkg", "relative.go"), "package pkg\n", false); err != nil {
		t.Fatal(err)
	}
	payload, err := readLSPFrame(bufio.NewReader(bytes.NewReader(writer.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	wantSuffix := strings.ReplaceAll(filepath.ToSlash(filepath.Join(root, "pkg", "relative.go")), " ", "%20")
	if !strings.HasSuffix(message.Params.TextDocument.URI, wantSuffix) {
		t.Fatalf("relative URI = %q, want suffix %q", message.Params.TextDocument.URI, wantSuffix)
	}
}

func TestLSPIncomingRoutesPushDiagnosticsAndResponses(t *testing.T) {
	client := newTransportTestLSPClient(&recordingWriteCloser{})
	uri := "file:///tmp/example.go"
	client.handleIncoming([]byte(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///tmp/example.go","diagnostics":[{"range":{"start":{"line":2,"character":3},"end":{"line":2,"character":4}},"severity":1,"code":123,"message":"broken"}]}}`))
	diagnostics, ok := client.cachedDiagnostics(uri)
	if !ok || len(diagnostics) != 1 || diagnostics[0].Message != "broken" {
		t.Fatalf("diagnostics not cached: ok=%v diagnostics=%#v", ok, diagnostics)
	}

	responseCh := make(chan lspResponse, 1)
	client.pending[7] = responseCh
	client.handleIncoming([]byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	response := <-responseCh
	if response.Error != nil || !strings.Contains(string(response.Result), `"ok":true`) {
		t.Fatalf("response not routed: %#v", response)
	}
}

func TestLSPTransportHandlesInterleavedNotificationBeforeResponse(t *testing.T) {
	clientRequestReader, clientRequestWriter := io.Pipe()
	serverResponseReader, serverResponseWriter := io.Pipe()
	client := newTransportTestLSPClient(clientRequestWriter)
	client.reader = bufio.NewReader(serverResponseReader)
	go client.readLoop()

	serverDone := make(chan error, 1)
	go func() {
		requestPayload, err := readLSPFrame(bufio.NewReader(clientRequestReader))
		if err != nil {
			serverDone <- err
			return
		}
		var request struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(requestPayload, &request); err != nil {
			serverDone <- err
			return
		}
		publish := []byte(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///tmp/interleaved.go","diagnostics":[]}}`)
		if err := writeLSPTestFrame(serverResponseWriter, publish); err != nil {
			serverDone <- err
			return
		}
		response := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"answer":42}}`, request.ID))
		serverDone <- writeLSPTestFrame(serverResponseWriter, response)
	}()

	result, err := client.sendRequestWithTimeout("example/request", map[string]interface{}{"x": 1}, time.Second)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if !strings.Contains(string(result), `"answer":42`) {
		t.Fatalf("unexpected result: %s", result)
	}
	if _, ok := client.cachedDiagnostics("file:///tmp/interleaved.go"); !ok {
		t.Fatal("interleaved publishDiagnostics was not processed")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = clientRequestWriter.Close()
	_ = clientRequestReader.Close()
	_ = serverResponseWriter.Close()
	_ = serverResponseReader.Close()
}

func writeLSPTestFrame(writer io.Writer, payload []byte) error {
	var frame bytes.Buffer
	fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(payload))
	frame.Write(payload)
	_, err := writer.Write(frame.Bytes())
	return err
}

func TestFindProjectRootRequiresMarkerToExist(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "main.go")
	if err := os.WriteFile(file, []byte("package sub\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := findProjectRoot(file); got != root {
		t.Fatalf("root = %q, want %q", got, root)
	}

	withoutMarker := filepath.Join(t.TempDir(), "plain.go")
	if err := os.WriteFile(withoutMarker, []byte("package plain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := findProjectRoot(withoutMarker); got == filepath.Dir(withoutMarker) {
		t.Fatalf("invented the file directory as a project root without a marker: %q", got)
	}
}

func TestLSPFileURIEncodesSpaces(t *testing.T) {
	uri := lspFileURI(filepath.Join(t.TempDir(), "a b.go"))
	if !strings.HasPrefix(uri, "file://") || !strings.Contains(uri, "a%20b.go") {
		t.Fatalf("invalid file URI: %q", uri)
	}
}
