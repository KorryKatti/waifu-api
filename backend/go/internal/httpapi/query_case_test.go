package httpapi

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"waifuapi/internal/config"
)

// The C# service bound [FromQuery] models, and ASP.NET matches those
// case-insensitively. PageSize worked for the life of the API and stopped the
// day the port landed, because url.Values.Get is an exact map lookup.
func TestQueryParamIsCaseInsensitive(t *testing.T) {
	query := url.Values{"PAGESIZE": {"7"}, "OrderBy": {"Random"}}

	if got := queryParam(query, "pageSize"); got != "7" {
		t.Errorf("PAGESIZE read as pageSize = %q, want 7", got)
	}
	if got := queryParam(query, "orderBy"); got != "Random" {
		t.Errorf("OrderBy read as orderBy = %q, want Random", got)
	}
}

// Values must survive untouched: reviewStatus=Accepted is an enum value, and
// reviewStatus=accepted is a different, rejected one.
func TestQueryParamKeepsValueCase(t *testing.T) {
	query := url.Values{"ReviewStatus": {"Accepted"}}

	if got := queryParam(query, "reviewStatus"); got != "Accepted" {
		t.Errorf("value became %q, want Accepted", got)
	}
}

func TestQueryParamPrefersTheExactSpelling(t *testing.T) {
	// Both present is ambiguous, but matching the caller's own spelling is the
	// least surprising resolution.
	query := url.Values{"pageSize": {"5"}, "PageSize": {"10"}}

	if got := queryParam(query, "pageSize"); got != "5" {
		t.Errorf("got %q, want 5", got)
	}
}

func TestQueryParamMissingReturnsEmpty(t *testing.T) {
	if got := queryParam(url.Values{}, "pageSize"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// IncludedTags had its filter silently dropped, so a caller asking for one tag
// received the whole catalogue. That is the failure shape worth pinning.
func TestStringListParamIsCaseInsensitive(t *testing.T) {
	query := url.Values{"IncludedTags": {"aya", "ayaka"}}

	got := stringListParam(query, "includedTags")
	if len(got) != 2 || got[0] != "aya" || got[1] != "ayaka" {
		t.Errorf("IncludedTags read as includedTags = %v, want [aya ayaka]", got)
	}
}

func TestStringListParamReturnsNilWhenAbsent(t *testing.T) {
	if got := stringListParam(url.Values{"Other": {"x"}}, "includedTags"); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// The reported bug: !gallery asked for five images and received one, because
// PageSize=5 was ignored and the default page size applied. parsePagination is
// where pageSize actually lands, so it is the one worth exercising end to end.
func TestParsePaginationAcceptsAnyCasing(t *testing.T) {
	limits := config.PageSize{Default: 30, Max: 100}

	for _, raw := range []string{"pageSize=7", "PageSize=7", "PAGESIZE=7"} {
		r := httptest.NewRequest("GET", "/images?"+raw, nil)
		page, err := parsePagination(r, limits)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if page.PageSize != 7 {
			t.Errorf("%s parsed to pageSize=%d, want 7", raw, page.PageSize)
		}
	}
}
