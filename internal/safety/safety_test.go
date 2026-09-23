package safety

import "testing"

func TestCredentialLike(t *testing.T) {
	if _, ok := CredentialLike("hello ordinary note"); ok {
		t.Fatal("false positive")
	}
	if _, ok := CredentialLike("-----BEGIN PRIVATE KEY-----\nabc"); !ok {
		t.Fatal("missed private key")
	}
	if _, ok := CredentialLike("api_key=abcdefghijklmnopqrstu"); !ok {
		t.Fatal("missed generic key")
	}
}
