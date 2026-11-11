package domain

import "testing"

func TestSlugs(t *testing.T) {
	for email, want := range map[string]string{
		"info@example.org": "info", "Lost.Found@example.org": "lost-found", "press_office@x": "press-office",
		"--a--@x": "a", "ä@x": "",
	} {
		if got := SlugFromEmail(email); got != want {
			t.Errorf("SlugFromEmail(%q) = %q, want %q", email, got, want)
		}
	}
	for slug, want := range map[string]bool{"info": true, "lost-found": true, "a": true, "-a": false, "a-": false, "A": false, "": false} {
		if got := ValidSlug(slug); got != want {
			t.Errorf("ValidSlug(%q) = %v", slug, got)
		}
	}
	if MailboxRoleKey("info", LevelManager) != "desk_info_manager" || MailboxGroup("lost-found", LevelDelete) != "desk-lost-found-delete" {
		t.Fatal("role naming")
	}
}
