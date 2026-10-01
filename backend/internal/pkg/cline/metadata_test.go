package cline

import "testing"

func TestClineSubjectIdentitySeparatesActiveAccounts(t *testing.T) {
	a, err := ParseSubjectHash([]byte(`{"id":"user-a","active_account_id":"account-a","email":"not-retained@example.invalid"}`))
	if err != nil || !ValidSubjectHash(a) {
		t.Fatalf("invalid subject: %q %v", a, err)
	}
	b, err := ParseSubjectHash([]byte(`{"id":"user-b","active_account_id":"account-a"}`))
	if err != nil || a != b {
		t.Fatal("keys for one active account did not correlate")
	}
	c, err := ParseSubjectHash([]byte(`{"id":"user-a","active_account_id":"account-b"}`))
	if err != nil || a == c {
		t.Fatal("different active accounts shared a namespace")
	}
	for _, body := range []string{`{}`, `null`, `{"id":"user-a"}`, `{"id":"user-a","active_account_id":null}`, `{"id":"user-a","active_account_id":" a "}`, `{"success":true,"data":{"id":"a","active_account_id":"b"}}`} {
		if _, err := ParseSubjectHash([]byte(body)); err == nil {
			t.Fatalf("unverified identity accepted: %s", body)
		}
	}
}

func TestClineScopeValidation(t *testing.T) {
	for _, scope := range []string{ScopeThrottle, ScopePass + "weekly", ScopeFree + "vendor/model", ScopePayG} {
		if !ValidScope(scope) {
			t.Fatal(scope)
		}
	}
	for _, scope := range []string{"", "cline:anything", ScopePass + "made-up", ScopeFree + "*", ScopeFree} {
		if ValidScope(scope) {
			t.Fatal(scope)
		}
	}
}
