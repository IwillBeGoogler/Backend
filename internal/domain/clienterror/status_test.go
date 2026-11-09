package clienterror

import "testing"

func TestValidateStatus(t *testing.T) {
	for _, s := range []Status{StatusOpen, StatusResolved, StatusIgnored} {
		if ValidateStatus(s, false) != nil {
			t.Errorf("%s", s)
		}
	}
	if ValidateStatus("", true) != nil {
		t.Error("empty filter")
	}
	if ValidateStatus("", false) == nil || ValidateStatus("nope", true) == nil {
		t.Error("invalid accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []ClientError{{ID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]ClientError)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}
