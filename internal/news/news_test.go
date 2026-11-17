package news

import "testing"

func TestDraftPublishFeed(t *testing.T) {
	e := New()
	a, err := e.Create("t1", "PenCom approves new PSSPs", "The Commission has approved...", CatPenComNotice, "editor", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Feed("t1", "")) != 0 {
		t.Fatal("draft must not appear in feed")
	}
	if _, err := e.Publish("t1", a.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := e.Create("t1", "New micro-pension product", "...", CatProduct, "editor", false)
	_, _ = e.Publish("t1", b.ID)
	feed := e.Feed("t1", "")
	if len(feed) != 2 || !feed[0].Pinned {
		t.Fatalf("feed order: %+v", feed)
	}
	if n := len(e.Feed("t1", CatPenComNotice)); n != 1 {
		t.Fatalf("category filter: %d", n)
	}
	if _, err := e.Publish("t1", a.ID); err == nil {
		t.Fatal("double publish must fail")
	}
	if _, err := e.Archive("t1", a.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(e.Feed("t1", "")); n != 1 {
		t.Fatalf("archive: %d", n)
	}
}
