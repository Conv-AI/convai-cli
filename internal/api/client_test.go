package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

func TestListCharactersForwardsAPIKeyAndPagination(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/character/list" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("CONVAI-API-KEY"); got != "01234567890123456789012345678901" {
			t.Errorf("CONVAI-API-KEY = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization unexpectedly set")
		}
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "20" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"total":41,"limit":10,"offset":20}`))
	}))
	defer server.Close()

	client := newTestClient(t, server, credential.Credential{
		Type:  config.CredentialAPIKey,
		Value: "01234567890123456789012345678901",
	})
	items, total, err := client.ListCharacters(context.Background(), 10, 20)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if len(items) != 0 || total == nil || *total != 41 {
		t.Fatalf("items = %d, total = %v", len(items), total)
	}
}

func TestGetCharacterForwardsPATAsBearer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer cvpat_0123456789012345678901234567890123456789" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("CONVAI-API-KEY"); got != "" {
			t.Errorf("CONVAI-API-KEY unexpectedly set")
		}
		if r.URL.Query().Get("character_id") != "df076c58-43f5-11f1-a81f-42010a080006" {
			t.Errorf("character_id = %q", r.URL.Query().Get("character_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"character_id":"df076c58-43f5-11f1-a81f-42010a080006",
			"character_name":"Ari",
			"character_traits":{},
			"description":"",
			"is_narrative_driven":false,
			"language_code":"en",
			"memory_settings":{},
			"metadata_filter":{},
			"model_type":"model",
			"timestamp":"2026-07-30T00:00:00Z",
			"user_id":"user-1",
			"voice_type":"voice-1"
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server, credential.Credential{
		Type:  config.CredentialPAT,
		Value: "cvpat_0123456789012345678901234567890123456789",
	})
	character, err := client.GetCharacter(context.Background(), "df076c58-43f5-11f1-a81f-42010a080006")
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if character.CharacterId != "df076c58-43f5-11f1-a81f-42010a080006" {
		t.Fatalf("character ID = %q", character.CharacterId)
	}
}

func TestCreateCharacterUsesGeneratedContract(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["charName"] != "Ari" || body["voiceType"] != "voice-1" || body["backstory"] != "Backstory" {
			t.Errorf("request body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"charID":"character-1","character_id":"character-1","workspace_id":"fixture","revision_id":"fixture"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server, credential.Credential{
		Type:  config.CredentialAPIKey,
		Value: "01234567890123456789012345678901",
	})
	created, err := client.CreateCharacter(context.Background(), characterapi.CharacterCreate{
		CharName:  "Ari",
		VoiceType: new("voice-1"),
		Backstory: "Backstory",
	})
	if err != nil {
		t.Fatalf("CreateCharacter: %v", err)
	}
	if created.CharacterId != "character-1" {
		t.Fatalf("character ID = %q", created.CharacterId)
	}
}

func TestAPIErrorIsBoundedAndIncludesRequestID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "request-123")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"plan access denied"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server, credential.Credential{
		Type:  config.CredentialAPIKey,
		Value: "01234567890123456789012345678901",
	})
	_, _, err := client.ListCharacters(context.Background(), 1, 0)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.RequestID != "request-123" {
		t.Fatalf("API error = %#v", apiErr)
	}
	if strings.Contains(apiErr.Error(), "01234567890123456789012345678901") {
		t.Fatal("API error exposed credential")
	}
}

func TestDecodeCreateRequestSupportsStrictYAMLAndBoundsInput(t *testing.T) {
	t.Parallel()
	request, err := DecodeCreateRequest(
		strings.NewReader("charName: Ari\nvoiceType: voice-1\nbackstory: Backstory\n"),
		"yaml",
	)
	if err != nil {
		t.Fatalf("DecodeCreateRequest: %v", err)
	}
	if request.CharName != "Ari" || request.VoiceType == nil || *request.VoiceType != "voice-1" {
		t.Fatalf("request = %#v", request)
	}

	if _, err := DecodeCreateRequest(strings.NewReader("charName: Ari\nunknown: true\n"), "yaml"); err == nil {
		t.Fatal("unknown YAML field unexpectedly accepted")
	}
	if _, err := DecodeCreateRequest(strings.NewReader(`{"charName":"Ari","unknown":true}`), "json"); err == nil {
		t.Fatal("unknown JSON field unexpectedly accepted")
	}
	if _, err := DecodeCreateRequest(strings.NewReader(`{} {}`), "json"); err == nil {
		t.Fatal("multiple JSON documents unexpectedly accepted")
	}
	if _, err := DecodeCreateRequest(strings.NewReader(strings.Repeat("x", (1<<20)+1)), "json"); err == nil {
		t.Fatal("oversized body unexpectedly accepted")
	}
}

func newTestClient(t *testing.T, server *httptest.Server, auth credential.Credential) *Client {
	t.Helper()
	client, err := New(server.URL, auth, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}
