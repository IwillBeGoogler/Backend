package publication

import (
	"strings"
	"testing"
)

func TestRoles(t *testing.T) {
	roles := []Role{RoleOwner, RoleEditor, RoleWriter, ""}
	ranks := []int{3, 2, 1, 0}
	for i, r := range roles {
		if r.Rank() != ranks[i] {
			t.Errorf("rank %q", r)
		}
	}
	if !RoleOwner.CanWrite() || !RoleWriter.CanWrite() || Role("").CanWrite() {
		t.Error("CanWrite")
	}
	if !RoleOwner.CanEdit() || !RoleEditor.CanEdit() || RoleWriter.CanEdit() {
		t.Error("CanEdit")
	}
	manage := []struct {
		r, target Role
		want      bool
	}{
		{RoleOwner, RoleEditor, true},
		{RoleOwner, RoleWriter, true},
		{RoleOwner, RoleOwner, false},
		{RoleEditor, RoleWriter, true},
		{RoleEditor, RoleEditor, false},
		{RoleEditor, RoleOwner, false},
		{RoleWriter, RoleWriter, false},
		{"", RoleWriter, false},
	}
	for _, m := range manage {
		if m.r.CanManage(m.target) != m.want {
			t.Errorf("%q manage %q", m.r, m.target)
		}
	}
}

func TestNormalize(t *testing.T) {
	ok := PublicationInput{Slug: " My-Blog ", Name: " N ", Description: " d ", AvatarURL: " https://x/y.png "}
	got, err := ok.Normalize(true)
	if err != nil || got.Slug != "my-blog" || got.Name != "N" || got.Description != "d" || got.AvatarURL != "https://x/y.png" {
		t.Fatalf("%+v %v", got, err)
	}
	// slug ignored when not creating
	if _, err := (PublicationInput{Slug: "!!", Name: "n"}).Normalize(false); err != nil {
		t.Fatal(err)
	}
	for _, av := range []string{"", "/api/uploads/a.png", "http://x"} {
		if _, err := (PublicationInput{Name: "n", AvatarURL: av}).Normalize(false); err != nil {
			t.Errorf("avatar %q: %v", av, err)
		}
	}
	bad := []struct {
		in   PublicationInput
		slug bool
	}{
		{PublicationInput{Slug: "a", Name: "n"}, true},
		{PublicationInput{Slug: "ab", Name: "n"}, true},
		{PublicationInput{Slug: "-ab", Name: "n"}, true},
		{PublicationInput{Slug: "abc", Name: " "}, true},
		{PublicationInput{Slug: "abc", Name: strings.Repeat("n", 81)}, true},
		{PublicationInput{Slug: "abc", Name: "n", Description: strings.Repeat("d", 501)}, true},
		{PublicationInput{Slug: "abc", Name: "n", AvatarURL: "ftp://x"}, true},
	}
	for i, b := range bad {
		if _, err := b.in.Normalize(b.slug); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestValidateMemberRole(t *testing.T) {
	if ValidateMemberRole(RoleEditor) != nil || ValidateMemberRole(RoleWriter) != nil {
		t.Fatal("valid rejected")
	}
	if ValidateMemberRole(RoleOwner) == nil || ValidateMemberRole("") == nil {
		t.Fatal("invalid accepted")
	}
}
