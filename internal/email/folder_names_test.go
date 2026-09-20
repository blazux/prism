package email

import (
	"github.com/emersion/go-imap/v2"
	"testing"
)

func TestResolveFolderNames(t *testing.T) {
	rows := []Folder{{Name: "Folders", Delimiter: "/"}, {Name: "Labels", Delimiter: "/"}, {Name: "Folders/Work/Old", Delimiter: "/", Selectable: true}, {Name: "Labels/Old", Delimiter: "/", Selectable: true}}
	for _, tc := range []struct{ name, source, want string }{
		{"PrismTest", "", "Folders/PrismTest"}, {"Folders/PrismTest", "", "Folders/PrismTest"}, {"Projects/Sub", "", "Folders/Projects/Sub"}, {"Labels/Tag", "", "Labels/Tag"}, {"New", "Folders/Work/Old", "Folders/Work/New"}, {"New", "Labels/Old", "Labels/New"},
	} {
		got, err := resolveFolderName(tc.name, tc.source, rows, nil)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %q %v", tc, got, err)
		}
	}
	if _, err := resolveFolderName("Folders", "", rows, nil); err == nil {
		t.Fatal("container accepted")
	}
	for _, name := range []string{"Projects", "Projects/Sub"} {
		got, err := resolveFolderName(name, "", nil, nil)
		if err != nil || got != name {
			t.Fatal("flat mailbox name changed")
		}
	}
	ns := []imap.NamespaceDescriptor{{Prefix: "INBOX.", Delim: '.'}}
	for _, name := range []string{"Projects", "INBOX.Projects"} {
		got, err := resolveFolderName(name, "", nil, ns)
		if err != nil || got != "INBOX.Projects" {
			t.Fatal(got, err)
		}
	}
}
