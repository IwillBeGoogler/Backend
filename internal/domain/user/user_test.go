package user

import (
	"strings"
	"testing"
)

func TestProfileUpdateNormalize(t *testing.T) {
	got, err := ProfileUpdate{DisplayName: " D ", Bio: " b ", AvatarURL: " https://x/a.png "}.Normalize()
	if err != nil || got.DisplayName != "D" || got.Bio != "b" || got.AvatarURL != "https://x/a.png" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, av := range []string{"", "/api/uploads/a", "http://x"} {
		if _, err := (ProfileUpdate{AvatarURL: av}).Normalize(); err != nil {
			t.Errorf("%q: %v", av, err)
		}
	}
	bad := []ProfileUpdate{
		{DisplayName: strings.Repeat("a", 65)},
		{Bio: strings.Repeat("b", 301)},
		{AvatarURL: "javascript:x"},
	}
	for i, b := range bad {
		if _, err := b.Normalize(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("12345678") != nil || ValidatePassword(strings.Repeat("a", 72)) != nil {
		t.Fatal("valid rejected")
	}
	if ValidatePassword("1234567") == nil || ValidatePassword(strings.Repeat("a", 73)) == nil {
		t.Fatal("invalid accepted")
	}
}

func TestValidateEmail(t *testing.T) {
	got, err := ValidateEmail(" someone@gmail.com ")
	if err != nil || got == "" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "nope", "a@", "@b.com"} {
		if _, err := ValidateEmail(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := ValidateEmail("x@mailinator.com"); err == nil || !strings.Contains(err.Error(), "disposable") {
		t.Errorf("disposable: %v", err)
	}
}

func TestNewRegistration(t *testing.T) {
	r, err := NewRegistration(" bob_1 ", "bob@gmail.com", "password1")
	if err != nil || r.Username != "bob_1" || r.Password != "password1" || r.Email == "" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, c := range [][3]string{
		{"ab", "bob@gmail.com", "password1"},
		{"bob bob", "bob@gmail.com", "password1"},
		{"bob", "nope", "password1"},
		{"bob", "bob@gmail.com", "short"},
	} {
		if _, err := NewRegistration(c[0], c[1], c[2]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestPaginated(t *testing.T) {
	items, meta := SummaryPage{Items: []Summary{{ID: 1}}, Total: 1, Page: 1, Limit: 5, NextCursor: "c"}.Paginated()
	if len(items.([]Summary)) != 1 || meta == nil {
		t.Fatal("summary")
	}
	items, meta = Page{Items: []User{{ID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]User)) != 1 || meta == nil {
		t.Fatal("page")
	}
}
