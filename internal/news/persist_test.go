package news

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New()
	a.AttachSink(st)
	art, err := a.Create("t1", "PenCom circular", "body", CatPenComNotice, "editor", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish("t1", art.ID); err != nil {
		t.Fatal(err)
	}
	draft, _ := a.Create("t1", "draft", "body", CatIndustry, "editor", false)

	b := New()
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if feed := b.Feed("t1", ""); len(feed) != 1 || feed[0].Title != "PenCom circular" {
		t.Fatalf("feed: %+v", feed)
	}
	if _, err := b.Publish("t1", draft.ID); err != nil {
		t.Fatalf("restored draft should be publishable: %v", err)
	}
	// Seq continuation.
	art2, _ := b.Create("t1", "x", "y", CatProduct, "e", false)
	if art2.ID == art.ID || art2.ID == draft.ID {
		t.Fatalf("ID collision: %s", art2.ID)
	}
}
