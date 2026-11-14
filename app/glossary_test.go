package app_test

import (
	"fmt"
	"testing"
)

type glossaryTerm struct {
	ID         int      `json:"id"`
	Term       string   `json:"term"`
	Aliases    []string `json:"aliases"`
	Definition string   `json:"definition"`
	Lang       string   `json:"lang"`
}

func TestGlossaryAPI(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")

	var terms []glossaryTerm
	anon.do("GET", "/api/glossary", nil, 200, &terms)
	if len(terms) != 0 {
		t.Fatalf("fresh glossary = %+v", terms)
	}

	// Writes need glossary.write.
	body := map[string]any{"term": "Cache", "aliases": []string{"caching", "cached"}, "definition": "Fast store", "lang": "en"}
	anon.do("POST", "/api/glossary", body, 401, nil)
	ali.do("POST", "/api/glossary", body, 403, nil)

	var cache glossaryTerm
	admin.do("POST", "/api/glossary", body, 201, &cache)
	if cache.ID == 0 || cache.Term != "Cache" || len(cache.Aliases) != 2 || cache.Lang != "en" {
		t.Fatalf("created = %+v", cache)
	}
	admin.do("POST", "/api/glossary", map[string]any{"term": "cache", "definition": "dup", "lang": "en"}, 409, nil)
	admin.do("POST", "/api/glossary", map[string]any{"term": "Cache", "definition": "same term, other language", "lang": "uz"}, 201, nil)
	admin.do("POST", "/api/glossary", map[string]any{"term": "Index", "definition": "any language"}, 201, nil)
	admin.do("POST", "/api/glossary", map[string]any{"term": "", "definition": "x"}, 400, nil)
	admin.do("POST", "/api/glossary", map[string]any{"term": "x", "definition": ""}, 400, nil)

	// ?lang= keeps that language's terms and the language-neutral ones.
	anon.do("GET", "/api/glossary", nil, 200, &terms)
	if len(terms) != 3 {
		t.Errorf("all = %+v", terms)
	}
	anon.do("GET", "/api/glossary?lang=en", nil, 200, &terms)
	if len(terms) != 2 || terms[0].Term != "Cache" || terms[1].Term != "Index" {
		t.Errorf("en = %+v", terms)
	}
	anon.do("GET", "/api/glossary?lang=ru", nil, 200, &terms)
	if len(terms) != 1 || terms[0].Term != "Index" {
		t.Errorf("ru = %+v", terms)
	}

	// Update.
	path := fmt.Sprintf("/api/glossary/%d", cache.ID)
	ali.do("PUT", path, body, 403, nil)
	body["definition"] = "Quick store"
	admin.do("PUT", path, body, 200, &cache)
	if cache.Definition != "Quick store" {
		t.Errorf("updated = %+v", cache)
	}
	admin.do("PUT", "/api/glossary/99999", body, 404, nil)
	admin.do("PUT", path, map[string]any{"term": "Index", "definition": "clash"}, 409, nil)

	// Delete.
	ali.do("DELETE", path, nil, 403, nil)
	admin.do("DELETE", path, nil, 204, nil)
	admin.do("DELETE", path, nil, 404, nil)
	anon.do("GET", "/api/glossary?lang=en", nil, 200, &terms)
	if len(terms) != 1 || terms[0].Term != "Index" {
		t.Errorf("after delete = %+v", terms)
	}
}

func TestPostGlossary(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	type post struct {
		ID       int            `json:"id"`
		Glossary []glossaryTerm `json:"glossary"`
	}
	var p post
	// No glossary sent: [] not null.
	ali.do("POST", "/api/posts", map[string]any{"title": "Caches", "body": "all about caches"}, 201, &p)
	if p.Glossary == nil || len(p.Glossary) != 0 {
		t.Fatalf("default glossary = %#v", p.Glossary)
	}

	path := fmt.Sprintf("/api/posts/%d", p.ID)
	terms := []map[string]any{{"term": "TTL", "aliases": []string{"time to live"}, "definition": "How long an entry lives"}}
	vali.do("PUT", path, map[string]any{"title": "Caches", "body": "x", "glossary": terms}, 403, nil)
	ali.do("PUT", path, map[string]any{"title": "Caches", "body": "x", "glossary": terms}, 200, &p)
	if len(p.Glossary) != 1 || p.Glossary[0].Term != "TTL" || p.Glossary[0].Aliases[0] != "time to live" {
		t.Fatalf("saved glossary = %+v", p.Glossary)
	}

	// Readers get it with the post.
	var read post
	anon.do("GET", path, nil, 200, &read)
	if len(read.Glossary) != 1 || read.Glossary[0].Definition != "How long an entry lives" {
		t.Errorf("read glossary = %+v", read.Glossary)
	}

	// An update that leaves glossary out keeps it (the draft is the base).
	ali.do("PUT", path, map[string]any{"title": "Caches 2"}, 200, &p)
	if len(p.Glossary) != 1 {
		t.Errorf("glossary lost on partial update: %+v", p.Glossary)
	}

	// Bad entries are rejected; an empty list clears it.
	ali.do("PUT", path, map[string]any{"glossary": []map[string]any{{"term": "x"}}}, 400, nil)
	ali.do("PUT", path, map[string]any{"glossary": []map[string]any{
		{"term": "TTL", "definition": "a"}, {"term": "ttl", "definition": "b"}}}, 400, nil)
	ali.do("PUT", path, map[string]any{"glossary": []map[string]any{}}, 200, &p)
	if len(p.Glossary) != 0 {
		t.Errorf("glossary not cleared: %+v", p.Glossary)
	}
}
