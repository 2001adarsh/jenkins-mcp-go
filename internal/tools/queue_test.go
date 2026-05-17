package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListQueue_RenderAndFilter(t *testing.T) {
	now := time.Now().UnixMilli()
	listing := apiQueueListing{Items: []apiQueueItem{
		{
			ID:           42,
			Task:         apiQueueTask{Name: "team-build", URL: "https://j/job/team/job/build/"},
			InQueueSince: now - 65_000,
			Why:          "Waiting for next available executor on linux",
			Buildable:    true,
		},
		{
			ID:           43,
			Task:         apiQueueTask{Name: "other-build", URL: "https://j/job/other/job/build/"},
			InQueueSince: now - 5_000,
			Blocked:      true,
		},
	}}
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queue/api/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(listing)
	}))
	defer srv.Close()

	res, _, err := d.ListQueue(context.Background(), nil, ListQueueInput{})
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"team-build", "other-build", "Waiting for next available executor", "buildable", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}

	filtered, _, err := d.ListQueue(context.Background(), nil, ListQueueInput{JobPathPrefix: "/job/team/"})
	if err != nil {
		t.Fatalf("ListQueue filtered: %v", err)
	}
	fout := resultText(t, filtered)
	if !strings.Contains(fout, "team-build") {
		t.Errorf("expected team-build in filtered output, got:\n%s", fout)
	}
	if strings.Contains(fout, "other-build") {
		t.Errorf("did not expect other-build in filtered output, got:\n%s", fout)
	}
}

// newPostHandler returns an httptest server that serves a crumb endpoint and
// captures the most recent /queue/cancelItem request. statusForCancel controls
// the cancel response status.
func newPostHandler(t *testing.T, statusForCancel int, cancelBody string) (Deps, *httptest.Server, func() *http.Request) {
	t.Helper()
	var last *http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"crumb":             "abc123",
				"crumbRequestField": "Jenkins-Crumb",
			})
		case "/queue/cancelItem":
			last = r
			w.WriteHeader(statusForCancel)
			_, _ = w.Write([]byte(cancelBody))
		default:
			http.NotFound(w, r)
		}
	})
	d, srv := newDepsAgainstHandler(t, h)
	return d, srv, func() *http.Request { return last }
}

func TestCancelQueueItem_Success404(t *testing.T) {
	d, srv, last := newPostHandler(t, http.StatusNotFound, "")
	defer srv.Close()

	res, _, err := d.CancelQueueItem(context.Background(), nil, CancelQueueItemInput{ItemID: 42})
	if err != nil {
		t.Fatalf("CancelQueueItem: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Canceled queue item 42") {
		t.Errorf("expected cancellation acknowledgement, got:\n%s", out)
	}
	r := last()
	if r == nil {
		t.Fatal("expected /queue/cancelItem to be hit")
	}
	if r.URL.Query().Get("id") != "42" {
		t.Errorf("expected id=42 in query, got %q", r.URL.RawQuery)
	}
	if r.Header.Get("Jenkins-Crumb") != "abc123" {
		t.Errorf("expected Jenkins-Crumb header on cancel POST, got %q", r.Header.Get("Jenkins-Crumb"))
	}
}

func TestCancelQueueItem_AlreadyGone(t *testing.T) {
	d, srv, _ := newPostHandler(t, http.StatusNotFound, "<html>No such queue item</html>")
	defer srv.Close()

	res, _, err := d.CancelQueueItem(context.Background(), nil, CancelQueueItemInput{ItemID: 99})
	if err != nil {
		t.Fatalf("CancelQueueItem: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "already left the queue") {
		t.Errorf("expected 'already left the queue' hint, got:\n%s", out)
	}
}

func TestCancelQueueItem_OtherError(t *testing.T) {
	d, srv, _ := newPostHandler(t, http.StatusInternalServerError, "boom")
	defer srv.Close()

	_, _, err := d.CancelQueueItem(context.Background(), nil, CancelQueueItemInput{ItemID: 7})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected error to mention HTTP 500, got: %v", err)
	}
}

func TestCancelQueueItem_RejectsNonPositiveID(t *testing.T) {
	d := Deps{}
	_, _, err := d.CancelQueueItem(context.Background(), nil, CancelQueueItemInput{ItemID: 0})
	if err == nil {
		t.Fatal("expected error for item_id=0")
	}
}
