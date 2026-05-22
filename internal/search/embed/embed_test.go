package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLlamaCPPUsesQwenModelAndQueryInstruction(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"index": 0, "embedding": []float32{1, 0, 0, 0}},
			},
		})
	}))
	defer server.Close()

	embedder := &LlamaCPP{
		BaseURL:          server.URL,
		Model:            DefaultQwenModel,
		DimensionValue:   4,
		QueryInstruction: "Instruct: custom\nQuery: ",
		Client:           server.Client(),
	}
	vector, err := embedder.EmbedQuery(context.Background(), "vector search")
	if err != nil {
		t.Fatalf("EmbedQuery failed: %v", err)
	}
	if len(vector) != 4 {
		t.Fatalf("expected dimension 4, got %d", len(vector))
	}
	if requestBody["model"] != DefaultQwenModel {
		t.Fatalf("expected Qwen model %q, got %#v", DefaultQwenModel, requestBody["model"])
	}
	inputs := requestBody["input"].([]interface{})
	if inputs[0] != "Instruct: custom\nQuery: vector search" {
		t.Fatalf("query instruction not applied: %#v", inputs[0])
	}
	if requestBody["dimensions"].(float64) != 4 {
		t.Fatalf("expected dimensions=4, got %#v", requestBody["dimensions"])
	}
}
