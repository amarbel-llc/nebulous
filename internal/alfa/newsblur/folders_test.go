package newsblur

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// recordingServer replies with okServer's fixed body but also captures the
// posted form for a test to assert exact field names/values against --
// the shape reader_test.go's okServer doesn't need but form-field
// regression tests do. captured is filled in only once the handler runs.
func recordingServer(t *testing.T) (server *httptest.Server, captured *url.Values) {
	t.Helper()
	captured = &url.Values{}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		*captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

// TestRenameFolderSendsCorrectFormFields pins the NewsBlur
// /reader/rename_folder field names (folder_to_rename, not folder_name --
// a pre-existing bug this fixes) against the API's own documented and
// server-source-verified param names.
func TestRenameFolderSendsCorrectFormFields(t *testing.T) {
	server, captured := recordingServer(t)
	c := testClientAgainstServer(t, server)

	if _, err := c.RenameFolder(context.Background(), "Old", "New", "Parent"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}

	if got := captured.Get("folder_to_rename"); got != "Old" {
		t.Errorf("folder_to_rename = %q, want %q", got, "Old")
	}
	if got := captured.Get("new_folder_name"); got != "New" {
		t.Errorf("new_folder_name = %q, want %q", got, "New")
	}
	if got := captured.Get("in_folder"); got != "Parent" {
		t.Errorf("in_folder = %q, want %q", got, "Parent")
	}
	if captured.Has("folder_name") {
		t.Error("request carries the wrong (pre-fix) folder_name field")
	}
}

// TestRenameFolderOmitsInFolderWhenTopLevel confirms in_folder is only
// sent when non-empty, matching CreateFolder/DeleteFolder's convention.
func TestRenameFolderOmitsInFolderWhenTopLevel(t *testing.T) {
	server, captured := recordingServer(t)
	c := testClientAgainstServer(t, server)

	if _, err := c.RenameFolder(context.Background(), "Old", "New", ""); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	if captured.Has("in_folder") {
		t.Error("in_folder present for a top-level rename")
	}
}

// TestDeleteFolderSendsCorrectFormFields pins the NewsBlur
// /reader/delete_folder field names (folder_to_delete, not folder_name --
// a pre-existing bug this fixes) and that in_folder is now sent when
// deleting a nested folder, needed to disambiguate a folder name that
// appears in more than one place in the tree.
func TestDeleteFolderSendsCorrectFormFields(t *testing.T) {
	server, captured := recordingServer(t)
	c := testClientAgainstServer(t, server)

	if _, err := c.DeleteFolder(context.Background(), "Photoblogs", "Blogs"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}

	if got := captured.Get("folder_to_delete"); got != "Photoblogs" {
		t.Errorf("folder_to_delete = %q, want %q", got, "Photoblogs")
	}
	if got := captured.Get("in_folder"); got != "Blogs" {
		t.Errorf("in_folder = %q, want %q", got, "Blogs")
	}
	if captured.Has("folder_name") {
		t.Error("request carries the wrong (pre-fix) folder_name field")
	}
}

// TestDeleteFolderOmitsInFolderWhenTopLevel confirms in_folder is only
// sent when non-empty.
func TestDeleteFolderOmitsInFolderWhenTopLevel(t *testing.T) {
	server, captured := recordingServer(t)
	c := testClientAgainstServer(t, server)

	if _, err := c.DeleteFolder(context.Background(), "Blogs", ""); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if captured.Has("in_folder") {
		t.Error("in_folder present for a top-level delete")
	}
}
