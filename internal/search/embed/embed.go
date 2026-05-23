package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const LlamaLaunchdLabel = "com.praesidium.llama-server"

const DefaultQwenModel = "Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M"

type DenseEmbedder interface {
	ModelID() string
	Dimension() int
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
}

type LlamaCPP struct {
	BaseURL          string
	Model            string
	DimensionValue   int
	QueryInstruction string
	Client           *http.Client
}

func (e *LlamaCPP) ModelID() string {
	if e.Model != "" {
		return e.Model
	}
	return DefaultQwenModel
}

func (e *LlamaCPP) Dimension() int {
	return e.DimensionValue
}

func (e *LlamaCPP) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return e.embed(ctx, texts)
}

// EnsureLlamaReady is the package-level helper used by CLI commands before
// they kick off an indexing operation that requires dense embeddings. It
// type-asserts to *LlamaCPP and calls EnsureReady; other embedder types
// (HashEmbedder in tests, nil) are no-ops.
//
// Lance's contract: single restart attempt. If the server is still not
// reachable after the restart attempt, the caller should fail the
// indexing operation rather than degrade silently.
func EnsureLlamaReady(ctx context.Context, e DenseEmbedder, readyTimeout time.Duration) error {
	llama, ok := e.(*LlamaCPP)
	if !ok || llama == nil {
		return nil
	}
	if llama.Dimension() <= 0 {
		return nil
	}
	return llama.EnsureReady(ctx, readyTimeout)
}

// EnsureReady probes /health. If the server is reachable, it returns nil.
// If unreachable, it makes a single attempt to launchctl-kickstart the
// service labeled `com.praesidium.llama-server`, then waits up to
// readyTimeout for /health to respond. If the server still does not come
// up, EnsureReady returns the underlying error and the caller is expected
// to fail the operation that required dense embeddings. This matches the
// "single restart attempt; if it fails the index fails" contract.
//
// No-op when BaseURL is empty.
func (e *LlamaCPP) EnsureReady(ctx context.Context, readyTimeout time.Duration) error {
	baseURL := strings.TrimRight(e.BaseURL, "/")
	if baseURL == "" {
		return nil
	}
	if pingHealth(ctx, baseURL, 2*time.Second) {
		return nil
	}

	kickCtx, kickCancel := context.WithTimeout(ctx, 10*time.Second)
	defer kickCancel()
	cmd := exec.CommandContext(kickCtx, "launchctl", "kickstart", "-k",
		fmt.Sprintf("gui/%d/%s", os.Getuid(), LlamaLaunchdLabel))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("llama-server at %s is down and launchctl kickstart %s failed (%w): %s",
			baseURL, LlamaLaunchdLabel, err, strings.TrimSpace(string(out)))
	}

	if readyTimeout <= 0 {
		readyTimeout = 60 * time.Second
	}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		if pingHealth(ctx, baseURL, 2*time.Second) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("llama-server at %s did not respond on /health within %s after restart", baseURL, readyTimeout)
}

func pingHealth(ctx context.Context, baseURL string, timeout time.Duration) bool {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func (e *LlamaCPP) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	prefix := e.QueryInstruction
	if prefix == "" {
		prefix = "Instruct: Given a wrkq task search query, retrieve relevant software project tasks, specifications, comments, and implementation notes\nQuery: "
	}
	vectors, err := e.embed(ctx, []string{prefix + query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embedding response contained no vectors")
	}
	return vectors[0], nil
}

func (e *LlamaCPP) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	baseURL := strings.TrimRight(e.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	payload := map[string]interface{}{
		"model": e.ModelID(),
		"input": inputs,
	}
	if e.DimensionValue > 0 {
		payload["dimensions"] = e.DimensionValue
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(resp.Status)
		if data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2048)); readErr == nil && strings.TrimSpace(string(data)) != "" {
			message += ": " + strings.TrimSpace(string(data))
		}
		return nil, fmt.Errorf("embedding provider returned %s", message)
	}

	var decoded embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(decoded.Data), len(inputs))
	}

	out := make([][]float32, len(decoded.Data))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, fmt.Errorf("embedding provider returned invalid index %d", item.Index)
		}
		out[item.Index] = normalize(item.Embedding)
	}
	return out, nil
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

type HashEmbedder struct {
	Model string
	Dims  int
}

func (e HashEmbedder) ModelID() string {
	if e.Model != "" {
		return e.Model
	}
	return "wrkq-hash-test"
}

func (e HashEmbedder) Dimension() int {
	if e.Dims > 0 {
		return e.Dims
	}
	return 64
}

func (e HashEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = e.embedText(text)
	}
	return out, nil
}

func (e HashEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	return e.embedText(query), nil
}

func (e HashEmbedder) embedText(text string) []float32 {
	dims := e.Dimension()
	vector := make([]float32, dims)
	terms := strings.Fields(strings.ToLower(text))
	if len(terms) == 0 {
		return vector
	}
	for _, term := range terms {
		h := fnv.New64a()
		_, _ = h.Write([]byte(term))
		idx := int(h.Sum64() % uint64(dims))
		vector[idx] += 1
	}
	return normalize(vector)
}

func normalize(vector []float32) []float32 {
	var sum float64
	for _, v := range vector {
		sum += float64(v * v)
	}
	if sum == 0 {
		return vector
	}
	scale := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(vector))
	for i, v := range vector {
		out[i] = v * scale
	}
	return out
}
