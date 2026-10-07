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
	personal, err := ParseSubjectHash([]byte(`{"success":true,"data":{"id":"user-a","organizations":[]}}`))
	personalLegacy, err := ParseSubjectHash([]byte(`{"id":"user-a","active_account_id":"user-a"}`))
	if err != nil || personal != personalLegacy {
		t.Fatalf("current personal profile did not use data.id: %q %q %v", personal, personalLegacy, err)
	}
	organization, err := ParseSubjectHash([]byte(`{"success":true,"data":{"id":"user-a","organizations":[{"active":true,"organizationId":"org-a"}]}}`))
	if err != nil || organization == a {
		t.Fatalf("active organization did not get its own namespace: %q %v", organization, err)
	}
	legacyOrganization, err := ParseSubjectHash([]byte(`{"id":"user-a","active_account_id":"org-a"}`))
	if err != nil || organization != legacyOrganization {
		t.Fatalf("current and legacy organization identities diverged: %q %q %v", organization, legacyOrganization, err)
	}
	for _, body := range []string{`{}`, `null`, `{"id":"user-a"}`, `{"id":"user-a","active_account_id":null}`, `{"id":"user-a","active_account_id":" a "}`, `{"success":false,"data":{"id":"a"}}`, `{"success":true,"data":{}}`, `{"success":true,"data":{"id":"a","organizations":[{"active":true,"organizationId":" "}]}}`, `{"success":true,"data":{"id":"a","organizations":[{"active":true,"organizationId":"org-a"},{"active":true,"organizationId":"org-b"}]}}`} {
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
